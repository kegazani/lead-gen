package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/platform/migrate"
	"lead-gen/backend/platform/transport/amqp"
)

const (
	relayPostgresEnv = "LEADGEN_POSTGRES_TEST_DSN"
	relayRabbitEnv   = "LEADGEN_RABBITMQ_TEST_URL"
)

type rejectingPublisher struct{}

func (rejectingPublisher) Publish(context.Context, string, string, []byte, map[string]string) error {
	return errors.New("test publish rejected")
}

// TestRelayPostgresRabbitMQAcceptance проверяет durable retry/no-mark после
// отказа, повтор relay после его пересоздания, стабильную дедупликацию и реальный
// publisher confirm/consume. Оба подключения обязаны вести на disposable ресурсы.
func TestRelayPostgresRabbitMQAcceptance(t *testing.T) {
	dsn, rabbitURL := os.Getenv(relayPostgresEnv), os.Getenv(relayRabbitEnv)
	if dsn == "" || rabbitURL == "" {
		t.Skipf("требуются disposable %s и %s", relayPostgresEnv, relayRabbitEnv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect disposable postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool, filepath.Join("..", "..", "..", "..", "migrations"), migrate.OwnSchema("platform-governance")); err != nil {
		t.Fatalf("apply context migrations: %v", err)
	}
	eventID := fmt.Sprintf("relay-test-%d", time.Now().UnixNano())
	tenantID := "relay-test-tenant"
	payload, _ := json.Marshal(map[string]any{
		"AuditRecordID": eventID, "Actor": "relay-test-operator", "Action": "tenant.disabled",
		"Object": "tenant:relay-test-tenant", "Outcome": "success",
		"EventHeader": map[string]any{"EventID": eventID, "TenantID": tenantID, "CorrelationID": eventID,
			"OccurredAt": time.Now().UTC()},
	})
	if _, err := pool.Exec(ctx, `INSERT INTO "platform-governance".outbox_message
	(message_id, contract, schema_version, event_id, tenant_id, occurred_at, correlation_id, payload)
	VALUES ($1, 'audit-recorded', '1.0.0', $1, $2, $3, $1, $4)`, eventID, tenantID, time.Now().UTC(), payload); err != nil {
		t.Fatalf("insert durable test record: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM "platform-governance".outbox_message WHERE message_id = $1`, eventID)
	})

	rejected, err := NewRelay(pool, rejectingPublisher{}, nil, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rejected.publishBatch(ctx); err == nil {
		t.Fatal("rejected publish reported success")
	}
	var published bool
	if err := pool.QueryRow(ctx, `SELECT published FROM "platform-governance".outbox_message WHERE message_id = $1`, eventID).Scan(&published); err != nil {
		t.Fatal(err)
	}
	if published {
		t.Fatal("rejected publish was marked published")
	}

	broker, err := amqp.NewRabbitMQBroker(rabbitURL, 3)
	if err != nil {
		t.Fatalf("connect disposable RabbitMQ: %v", err)
	}
	defer broker.Close()
	consumeCtx, stopConsumer := context.WithCancel(ctx)
	delivered := make(chan amqp.Delivery, 2)
	consumeErr := make(chan error, 1)
	go func() {
		consumeErr <- broker.Consume(consumeCtx, "platform-governance.audit-recorded", func(_ context.Context, d amqp.Delivery) error {
			delivered <- d
			return nil
		})
	}()
	// Дать consumer объявить durable очередь до первой публикации.
	time.Sleep(100 * time.Millisecond)

	// Создание нового relay моделирует перезапуск worker: строка остаётся durable,
	// получает тот же event_id и подтверждается после реального publisher confirm.
	restarted, err := NewRelay(pool, broker, nil, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := restarted.publishBatch(ctx); err != nil || count != 1 {
		t.Fatalf("publish after restart count=%d err=%v", count, err)
	}
	select {
	case delivery := <-delivered:
		if delivery.EventID != eventID {
			t.Fatalf("delivered event_id=%q want %q", delivery.EventID, eventID)
		}
		var body map[string]any
		if err := json.Unmarshal(delivery.Body, &body); err != nil {
			t.Fatalf("invalid contract JSON: %v", err)
		}
		if body["event_id"] != eventID {
			t.Fatalf("wire event_id changed: %#v", body)
		}
		for _, forbidden := range []string{"password", "secret", "login", "email"} {
			if _, exists := body[forbidden]; exists {
				t.Fatalf("wire contains prohibited field %q", forbidden)
			}
		}
	case <-ctx.Done():
		t.Fatal("RabbitMQ did not deliver confirmed event")
	}
	if count, err := restarted.publishBatch(ctx); err != nil || count != 0 {
		t.Fatalf("duplicate relay pass count=%d err=%v", count, err)
	}
	if err := pool.QueryRow(ctx, `SELECT published FROM "platform-governance".outbox_message WHERE message_id = $1`, eventID).Scan(&published); err != nil {
		t.Fatal(err)
	}
	if !published {
		t.Fatal("confirmed message was not marked published")
	}
	stopConsumer()
	select {
	case err := <-consumeErr:
		if err != nil {
			t.Errorf("consumer exit: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("consumer did not stop")
	}
}
