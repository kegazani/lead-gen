// Package postgres публикует durable outbox-сообщения platform-governance.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
)

const relayBatchSize = 64

// Relay читает записи из PostgreSQL и подтверждает их только после publisher
// confirm. Повторная публикация после падения между confirm и UPDATE допустима:
// event_id сохраняется, поэтому потребители дедуплицируют at-least-once доставку.
type Relay struct {
	pool      *pgxpool.Pool
	publisher amqp.Publisher
	logger    observ.Logger
	interval  time.Duration
}

// NewRelay собирает релей только с реальным хранилищем и publisher.
func NewRelay(pool *pgxpool.Pool, publisher amqp.Publisher, logger observ.Logger, interval time.Duration) (*Relay, error) {
	if pool == nil {
		return nil, errors.New("platform-governance: для outbox relay требуется PostgreSQL pool")
	}
	if publisher == nil {
		return nil, errors.New("platform-governance: для outbox relay требуется AMQP publisher")
	}
	if interval <= 0 {
		interval = time.Second
	}
	return &Relay{pool: pool, publisher: publisher, logger: logger, interval: interval}, nil
}

// Run обслуживает outbox до отмены контекста. Ошибка прохода видна в
// структурированном журнале, а неподтверждённое сообщение остаётся в БД.
func (r *Relay) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	backoff := r.interval
	for {
		published, err := r.publishBatch(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			r.logFailure(err)
			if !sleep(ctx, backoff) {
				return nil
			}
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			continue
		}
		backoff = r.interval
		if published == 0 && !sleep(ctx, r.interval) {
			return nil
		}
	}
}

// publishBatch обрабатывает записи по возрастанию id. Строка удерживается под
// FOR UPDATE до confirm и отметки published, исключая параллельную отправку
// несколькими worker-процессами. Ошибка/отказ брокера откатывает транзакцию.
func (r *Relay) publishBatch(ctx context.Context) (int, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("open_transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	rows, err := tx.Query(ctx, `
SELECT id, contract, schema_version, event_id, tenant_id, occurred_at,
       snapshot_version, correlation_id, causation_id, payload
FROM "platform-governance".outbox_message
WHERE published = false
ORDER BY id
LIMIT $1
FOR UPDATE SKIP LOCKED`, relayBatchSize)
	if err != nil {
		return 0, fmt.Errorf("read_pending: %w", err)
	}
	type message struct {
		id                                   int64
		contract, version, eventID, tenantID string
		occurred                             time.Time
		snapshot                             int64
		correlation, causation               string
		payload                              []byte
	}
	items := make([]message, 0, relayBatchSize)
	for rows.Next() {
		var item message
		if err := rows.Scan(&item.id, &item.contract, &item.version, &item.eventID, &item.tenantID,
			&item.occurred, &item.snapshot, &item.correlation, &item.causation, &item.payload); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan_pending: %w", err)
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read_pending: %w", err)
	}
	for _, item := range items {
		body, err := wireEnvelope(item.contract, item.version, item.eventID, item.tenantID,
			item.occurred, item.correlation, item.causation, item.payload)
		if err != nil {
			return 0, fmt.Errorf("invalid_envelope: %w", err)
		}
		routingKey := item.contract + ".1"
		if err := r.publisher.Publish(ctx, "platform-governance", routingKey, body, map[string]string{
			"event_id": item.eventID, "tenant_id": item.tenantID,
			"correlation_id": item.correlation, "schema_version": item.version,
		}); err != nil {
			return 0, fmt.Errorf("publish_rejected: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE "platform-governance".outbox_message SET published = true WHERE id = $1 AND published = false`, item.id); err != nil {
			return 0, fmt.Errorf("mark_published: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit_published: %w", err)
	}
	return len(items), nil
}

// wireEnvelope выдаёт стабильную опубликованную форму. Чувствительные данные
// не выводятся в ошибки/логи; тело ограничено текущими структурами контрактов.
func wireEnvelope(contract, version, eventID, tenantID string, occurred time.Time, correlation, causation string, payload []byte) ([]byte, error) {
	var values map[string]any
	if err := json.Unmarshal(payload, &values); err != nil {
		return nil, errors.New("payload is not valid JSON")
	}
	flatten(values)
	// Доменное событие содержит заголовок как вложенный EventHeader; его поля
	// поднимаются на уровень контракта, не меняя идентификатор события.
	if h, ok := values["event_header"].(map[string]any); ok {
		flatten(h)
		for key, value := range h {
			if _, exists := values[key]; !exists {
				values[key] = value
			}
		}
		delete(values, "event_header")
	}
	allowed, required := contractFields(contract)
	if allowed == nil {
		return nil, errors.New("unsupported published contract")
	}
	for key := range values {
		if !allowed[key] && !envelopeField(key) {
			return nil, fmt.Errorf("payload contains field outside the published contract: %s", key)
		}
	}
	for _, key := range required {
		if value, ok := values[key]; !ok || value == nil || value == "" {
			return nil, errors.New("payload is missing required published fields")
		}
	}
	metadata := map[string]any{
		"event_id": eventID, "occurred_at": occurred.UTC(), "tenant_id": tenantID,
		"correlation_id": correlation, "name": contract, "schema_version": version,
	}
	if causation != "" {
		metadata["causation_id"] = causation
	}
	for key, expected := range metadata {
		if actual, exists := values[key]; exists {
			if key == "occurred_at" {
				delete(values, key)
				continue
			}
			if actual == "" && expected != "" {
				// Пустой необязательный correlation/causation в исходном
				// domain event заменяется стабильным значением envelope из outbox.
				delete(values, key)
				continue
			}
			matches := fmt.Sprint(actual) == fmt.Sprint(expected)
			if !matches {
				return nil, fmt.Errorf("payload envelope mismatch: %s", key)
			}
		}
		delete(values, key)
	}
	body := map[string]any{
		"event_id": eventID, "occurred_at": occurred.UTC(), "tenant_id": tenantID,
		"correlation_id": correlation, "name": contract, "schema_version": version,
	}
	if causation != "" {
		body["causation_id"] = causation
	}
	if contract == "access-decision" {
		if v, ok := values["version"]; ok {
			values["snapshot_version"] = v
			delete(values, "version")
		}
	}
	for key, value := range values {
		body[key] = value
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("payload cannot be encoded")
	}
	return encoded, nil
}

func envelopeField(key string) bool {
	switch key {
	case "event_id", "occurred_at", "tenant_id", "correlation_id", "causation_id", "name", "schema_version":
		return true
	default:
		return false
	}
}

func flatten(values map[string]any) {
	for key, value := range values {
		normalized := snake(key)
		if normalized != key {
			delete(values, key)
			values[normalized] = value
		}
		if nested, ok := value.(map[string]any); ok {
			flatten(nested)
		}
		if list, ok := value.([]any); ok {
			for _, entry := range list {
				if nested, ok := entry.(map[string]any); ok {
					flatten(nested)
				}
			}
		}
	}
}

func snake(value string) string {
	if strings.HasSuffix(value, "ID") {
		value = strings.TrimSuffix(value, "ID") + "_id"
	}
	var out strings.Builder
	for i, r := range value {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				out.WriteByte('_')
			}
			out.WriteRune(r + ('a' - 'A'))
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func contractFields(contract string) (map[string]bool, []string) {
	switch contract {
	case "audit-recorded":
		return fields("audit_record_id", "actor", "action", "object", "outcome", "correction_of"),
			[]string{"audit_record_id", "actor", "action", "object", "outcome"}
	case "access-decision":
		return fields("state", "version", "assignments", "occurred_at", "tenant_id"),
			[]string{"state", "version", "assignments"}
	case "tenant-access-changed":
		return fields("user_id", "roles", "change"),
			[]string{"change"}
	default:
		return nil, nil
	}
}

func fields(names ...string) map[string]bool {
	result := make(map[string]bool, len(names))
	for _, name := range names {
		result[name] = true
	}
	return result
}

func (r *Relay) logFailure(err error) {
	if r.logger == nil {
		return
	}
	category := "outbox_failure"
	text := err.Error()
	switch {
	case strings.Contains(text, "publish_rejected"):
		category = "publish_rejected"
	case strings.Contains(text, "mark_published"), strings.Contains(text, "commit_published"):
		category = "mark_published"
	case strings.Contains(text, "invalid_envelope"):
		category = "invalid_envelope"
	case strings.Contains(text, "read_pending"), strings.Contains(text, "open_transaction"):
		category = "journal_read"
	}
	r.logger.Error("platform-governance: outbox relay pass failed", nil, observ.Fields{}, "failure_category", category)
}

func sleep(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
