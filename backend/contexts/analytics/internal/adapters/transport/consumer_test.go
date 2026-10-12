package transport_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"lead-gen/backend/contexts/analytics/internal/adapters/transport"
	"lead-gen/backend/contexts/analytics/internal/app/command"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
	"lead-gen/backend/platform/messaging/inbox"
	amqptransport "lead-gen/backend/platform/transport/amqp"
)

// contractQueue — очередь потребителя аналитики по конвенции
// <downstream>.<contract>; последний сегмент — имя контракта-события.
const contractQueue = "analytics.analytics-event"

// TestSubscriberAppliesEventsToProjection проверяет, что подписчик читает очередь
// брокера и наполняет проекцию дашборда: опубликованные события доходят до
// read-модели через command.ApplyEvent.
func TestSubscriberAppliesEventsToProjection(t *testing.T) {
	broker := amqptransport.NewInMemoryBroker(3)
	model := readmodel.New()
	handler := command.NewApplyEventHandler(model, nil, nil)

	startSubscriber(t, broker, inbox.NewMemoryDeduper(), func(_ context.Context, event command.ApplyEvent) error {
		return handler.Handle(context.Background(), event)
	})

	occurred := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	publishEvent(t, broker, "evt-1", map[string]any{
		"tenant_id": "t1", "company_id": "c1", "kind": "lead-workflow",
		"status": "warming", "occurred_at": occurred.Format(time.RFC3339),
	})
	publishEvent(t, broker, "evt-2", map[string]any{
		"tenant_id": "t1", "company_id": "c2", "kind": "lead-workflow",
		"status": "cold", "occurred_at": occurred.Format(time.RFC3339),
	})
	publishEvent(t, broker, "evt-3", map[string]any{
		"tenant_id": "t1", "company_id": "c2", "kind": "message-sent",
		"occurred_at": occurred.Format(time.RFC3339),
	})

	var snapshot readmodel.Snapshot
	waitFor(t, func() bool {
		snapshot = model.Snapshot("t1", readmodel.Scope{}, occurred)
		return snapshot.Visible && snapshot.Found == 2 && snapshot.Warmed == 1 && snapshot.Cold == 1 && snapshot.Communicated == 1
	}, "проекция не наполнилась событиями из очереди")

	if snapshot.Found != 2 {
		t.Fatalf("Found = %d, ожидалось 2", snapshot.Found)
	}
	if snapshot.Warmed != 1 || snapshot.Cold != 1 {
		t.Fatalf("Warmed/Cold = %d/%d, ожидалось 1/1", snapshot.Warmed, snapshot.Cold)
	}
	if snapshot.Communicated != 1 {
		t.Fatalf("Communicated = %d, ожидалось 1", snapshot.Communicated)
	}
}

// TestSubscriberDeduplicatesByEventID проверяет, что повтор сообщения с тем же
// event_id подтверждается без второго эффекта: обработчик вызывается один раз.
func TestSubscriberDeduplicatesByEventID(t *testing.T) {
	broker := amqptransport.NewInMemoryBroker(3)

	var applied int32
	startSubscriber(t, broker, inbox.NewMemoryDeduper(), func(_ context.Context, _ command.ApplyEvent) error {
		atomic.AddInt32(&applied, 1)
		return nil
	})

	event := map[string]any{
		"event_id": "evt-dup", "tenant_id": "t1", "company_id": "c1",
		"kind": "lead-workflow", "status": "warming",
	}
	// Владелец публикует факт дважды с тем же event_id: доставка at-least-once.
	publishBody(t, broker, mustJSON(t, event))
	publishBody(t, broker, mustJSON(t, event))

	waitFor(t, func() bool { return broker.Pending(contractQueue) == 0 }, "очередь не опустошена после обработки")

	if got := atomic.LoadInt32(&applied); got != 1 {
		t.Fatalf("обработчик вызван %d раз, ожидалось 1: повтор по event_id не должен давать второй эффект", got)
	}
}

// startSubscriber запускает подписчика в отдельной горутине: Run блокирующий.
// Остановка выполняется при завершении теста: контекст отменяется, работа
// подписчика дожидается — обработка не пересекается со следующими проверками.
func startSubscriber(t *testing.T, broker amqptransport.Consumer, deduper inbox.Deduper, handle func(context.Context, command.ApplyEvent) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	subscriber := transport.NewSubscriber(broker, deduper, contractQueue)
	go func() {
		done <- subscriber.Run(ctx, handle)
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
}

// publishEvent публикует нормализованное событие в очередь потребителя с
// заданным event_id в конверте и в теле.
func publishEvent(t *testing.T, broker amqptransport.Publisher, eventID string, event map[string]any) {
	t.Helper()
	event["event_id"] = eventID
	publishBody(t, broker, mustJSON(t, event))
}

// publishBody публикует готовое тело в очередь потребителя. Ключ маршрутизации
// <contract>.<major> направляет сообщение в очередь аналитики.
func publishBody(t *testing.T, broker amqptransport.Publisher, body []byte) {
	t.Helper()
	if err := broker.Publish(context.Background(), "analytics", "analytics-event.1", body, nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

// mustJSON сериализует значение или валит тест.
func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("сериализация события: %v", err)
	}
	return body
}

// waitFor ждёт выполнения условия, чтобы не зависеть от точного момента события.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(msg)
}
