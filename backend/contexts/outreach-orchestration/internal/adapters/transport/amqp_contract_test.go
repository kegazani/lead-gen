package transport

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
	"lead-gen/backend/platform/messaging/inbox"
	amqptransport "lead-gen/backend/platform/transport/amqp"
)

// publishContract публикует контракт владельца в брокер: exchange назван по
// контексту-владельцу, ключ маршрутизации — <contract>.<major>, event_id кладётся
// в метаданные (design.md, Решение 3).
func publishContract(t *testing.T, broker *amqptransport.InMemoryBroker, exchange, contract, body string, eventID string) {
	t.Helper()
	headers := map[string]string{
		"event_id":       eventID,
		"name":           contract,
		"schema_version": "1.0.0",
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := broker.Publish(context.Background(), exchange, contract+".1", []byte(body), headers); err != nil {
			t.Fatalf("публикация %s (попытка %d): %v", contract, attempt+1, err)
		}
	}
}

// expectNoSecondEffect проверяет, что в канал не пришёл второй эффект.
func expectNoSecondEffect[T any](t *testing.T, checked string, handled <-chan T) {
	t.Helper()
	select {
	case second := <-handled:
		t.Fatalf("%s: повторная доставка создала второй эффект: %+v", checked, second)
	default:
	}
}

// waitQueueEmpty ждёт, пока очередь опустеет: все доставленные сообщения
// подтверждены (дедуплицированный повтор подтверждается без эффекта).
func waitQueueEmpty(t *testing.T, broker *amqptransport.InMemoryBroker, queue string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if broker.Pending(queue) == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("очередь %q не опустела", queue)
}

// stopSubscriber отменяет контекст и дожидается штатного завершения Run.
func stopSubscriber(t *testing.T, cancel context.CancelFunc, runErr <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("потребитель завершился с ошибкой: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("потребитель не завершился после отмены контекста")
	}
}

// TestObservationToOutreachEngagementEventOverAMQP — контрактный тест связи
// observation-to-outreach (architecture/domain-catalog.yaml): интеграция event,
// транспорт async/amqp.
//
// Владелец контракта — engagement-observation, потребитель —
// outreach-orchestration (этот контекст). Обе стороны смоделированы in-memory:
// издатель владельца пишет в InMemoryBroker, потребитель
// (EngagementEventSubscriber) читает очередь и дедуплицирует по event_id через
// inbox.Deduper. Внешняя сеть не используется.
func TestObservationToOutreachEngagementEventOverAMQP(t *testing.T) {
	const queue = "outreach-orchestration.engagement-event"
	broker := amqptransport.NewInMemoryBroker(3)
	subscriber := NewEngagementEventSubscriber(broker, inbox.NewMemoryDeduper(), queue)

	ctx, cancel := context.WithCancel(context.Background())
	handled := make(chan contracts.EngagementEvent, 4)
	runErr := make(chan error, 1)
	go func() {
		runErr <- subscriber.Run(ctx, func(_ context.Context, e contracts.EngagementEvent) error {
			handled <- e
			return nil
		})
	}()

	event := validEngagementEvent()
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("сериализация engagement-event: %v", err)
	}
	// Владелец публикует факт дважды с тем же event_id: доставка at-least-once.
	publishContract(t, broker, "engagement-observation", contracts.ContractEngagementEvent, string(body), event.EventID)

	select {
	case got := <-handled:
		if got.EventID != event.EventID || got.CampaignID != event.CampaignID {
			t.Fatalf("потребитель получил не тот факт: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("событие не доставлено потребителю")
	}
	waitQueueEmpty(t, broker, queue)
	expectNoSecondEffect(t, "engagement-event", handled)

	stopSubscriber(t, cancel, runErr)
}

// TestGovernancePolicyForOutreachProcessingAuthorityOverAMQP — контрактный тест
// связи governance-policy-for-outreach (architecture/domain-catalog.yaml):
// интеграция policy, транспорт async/amqp.
//
// Владелец контракта — data-governance, потребитель — outreach-orchestration
// (этот контекст). Policy доставляется подпиской, а не вызовом: потребитель
// (ProcessingAuthoritySubscriber) хранит контрактный снимок и дедуплицирует
// доставку по event_id. Обе стороны смоделированы in-memory, внешняя сеть не
// используется.
func TestGovernancePolicyForOutreachProcessingAuthorityOverAMQP(t *testing.T) {
	const queue = "outreach-orchestration.processing-authority"
	broker := amqptransport.NewInMemoryBroker(3)
	subscriber := NewProcessingAuthoritySubscriber(broker, inbox.NewMemoryDeduper(), queue)

	ctx, cancel := context.WithCancel(context.Background())
	handled := make(chan contracts.ProcessingAuthority, 4)
	runErr := make(chan error, 1)
	go func() {
		runErr <- subscriber.Run(ctx, func(_ context.Context, p contracts.ProcessingAuthority) error {
			handled <- p
			return nil
		})
	}()

	authority := validProcessingAuthority()
	body, err := json.Marshal(authority)
	if err != nil {
		t.Fatalf("сериализация processing-authority: %v", err)
	}
	publishContract(t, broker, "data-governance", contracts.ContractProcessingAuthority, string(body), authority.EventID)

	select {
	case got := <-handled:
		if got.PolicyID != authority.PolicyID || got.Scope != authority.Scope {
			t.Fatalf("потребитель получил не ту policy: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("policy не доставлена потребителю")
	}
	waitQueueEmpty(t, broker, queue)
	expectNoSecondEffect(t, "processing-authority", handled)

	stopSubscriber(t, cancel, runErr)
}
