package transport_test

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/contexts/analytics/internal/adapters/transport"
	"lead-gen/backend/contexts/analytics/internal/app/command"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
	"lead-gen/backend/platform/messaging/inbox"
	amqptransport "lead-gen/backend/platform/transport/amqp"
)

// TestDeliveryEventSubscriberProjectsCycleOutcomes проверяет, что подписчик на
// исходы доставки (контракт delivery-event владельца message-delivery) наполняет
// проекцию дашборда: доставка учитывается как коммуникация, отказ — как отбой.
func TestDeliveryEventSubscriberProjectsCycleOutcomes(t *testing.T) {
	broker := amqptransport.NewInMemoryBroker(3)
	model := readmodel.New()
	handler := command.NewApplyEventHandler(model, nil, nil)

	subscriber := transport.NewDeliveryEventSubscriber(broker, inbox.NewMemoryDeduper(), "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- subscriber.Run(ctx, func(_ context.Context, event command.ApplyEvent) error {
			return handler.Handle(context.Background(), event)
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Run не завершился после отмены контекста")
		}
	})

	occurred := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	publishDeliveryEvent(t, broker, mustJSON(t, map[string]any{
		"event_id": "evt-d1", "tenant_id": "t1", "recipient_ref": "reach-a",
		"campaign_id": "camp-1", "outcome": "delivered",
		"occurred_at": occurred.Format(time.RFC3339),
	}))
	publishDeliveryEvent(t, broker, mustJSON(t, map[string]any{
		"event_id": "evt-d2", "tenant_id": "t1", "recipient_ref": "reach-b",
		"campaign_id": "camp-1", "outcome": "failed",
		"occurred_at": occurred.Format(time.RFC3339),
	}))

	var snapshot readmodel.Snapshot
	waitFor(t, func() bool {
		snapshot = model.Snapshot("t1", readmodel.Scope{}, occurred)
		return snapshot.Visible && snapshot.Found == 2 && snapshot.Communicated == 1 && snapshot.Cold == 1
	}, "проекция не наполнилась исходами доставки")

	if snapshot.Communicated != 1 {
		t.Fatalf("Communicated = %d, ожидалось 1 (доставка = коммуникация)", snapshot.Communicated)
	}
	if snapshot.Cold != 1 {
		t.Fatalf("Cold = %d, ожидалось 1 (отказ доставки)", snapshot.Cold)
	}
}

// TestDeliveryEventSubscriberIgnoresUnattributed проверяет, что исход доставки без
// принадлежности (нет тенанта или получателя) не создаёт запись: проекция не
// выдумывает срез.
func TestDeliveryEventSubscriberIgnoresUnattributed(t *testing.T) {
	broker := amqptransport.NewInMemoryBroker(3)
	model := readmodel.New()
	handler := command.NewApplyEventHandler(model, nil, nil)

	subscriber := transport.NewDeliveryEventSubscriber(broker, inbox.NewMemoryDeduper(), "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = subscriber.Run(ctx, func(_ context.Context, event command.ApplyEvent) error {
			return handler.Handle(context.Background(), event)
		})
	}()

	// Нет тенанта: событие игнорируется проекцией.
	publishDeliveryEvent(t, broker, mustJSON(t, map[string]any{
		"event_id": "evt-x1", "recipient_ref": "reach-a", "outcome": "delivered",
	}))
	// Нет ни получателя, ни идентификатора сообщения: запись не создаётся.
	publishDeliveryEvent(t, broker, mustJSON(t, map[string]any{
		"event_id": "evt-x2", "tenant_id": "t1", "outcome": "delivered",
	}))

	waitFor(t, func() bool {
		return broker.Pending(transport.DefaultDeliveryQueue) == 0
	}, "очередь не опустошена после обработки")

	if snapshot := model.Snapshot("t1", readmodel.Scope{}, time.Now()); snapshot.Visible {
		t.Fatalf("событие без принадлежности создало срез: %+v", snapshot)
	}
}

// publishDeliveryEvent публикует исход доставки в очередь потребителя аналитики.
// Ключ маршрутизации <contract>.<major> направляет его в analytics.delivery-event.
func publishDeliveryEvent(t *testing.T, broker amqptransport.Publisher, body []byte) {
	t.Helper()
	if err := broker.Publish(context.Background(), "message-delivery", "delivery-event.1", body, nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}
