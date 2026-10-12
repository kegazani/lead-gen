package analytics

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"lead-gen/backend/contexts/analytics/internal/readmodel"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
)

// TestWorkerAppliesBrokerEventsToProjection проверяет проводку роли worker:
// сервис, собранный с потребителем брокера, читает очередь и наполняет проекцию
// дашборда доменными событиями без dev-приёма.
func TestWorkerAppliesBrokerEventsToProjection(t *testing.T) {
	broker := amqp.NewInMemoryBroker(3)
	service, err := New(Config{
		Consumer: broker,
		Logger:   observ.NewLoggerWithWriter(io.Discard),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer(ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка сервиса: %v", err)
	}

	runner, ok := service.RoleRunner(composition.RoleWorker)
	if !ok {
		t.Fatal("роль worker не зарегистрирована")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	occurred := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	publishAnalyticsEvent(t, broker, "evt-a", map[string]any{
		"tenant_id": "t1", "company_id": "c1", "kind": "lead-workflow",
		"status": "warming", "occurred_at": occurred.Format(time.RFC3339),
	})
	publishAnalyticsEvent(t, broker, "evt-b", map[string]any{
		"tenant_id": "t1", "company_id": "c2", "kind": "lead-workflow",
		"status": "cold", "occurred_at": occurred.Format(time.RFC3339),
	})

	waitForSnapshot(t, service.slice.ReadModel, occurred)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("worker: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker не завершился после отмены контекста")
	}
}

// TestWorkerAppliesDeliveryEventsToProjection проверяет проводку роли worker с
// двумя подписками: сервис, собранный с потребителем брокера, читает и очередь
// исходов доставки (delivery-event), поэтому витрина клиента отражает реальный
// ход цикла (найдено → прокоммуницировано) без dev-приёма.
func TestWorkerAppliesDeliveryEventsToProjection(t *testing.T) {
	broker := amqp.NewInMemoryBroker(3)
	service, err := New(Config{
		Consumer: broker,
		Logger:   observ.NewLoggerWithWriter(io.Discard),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer(ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка сервиса: %v", err)
	}

	runner, ok := service.RoleRunner(composition.RoleWorker)
	if !ok {
		t.Fatal("роль worker не зарегистрирована")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	occurred := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	for _, event := range []map[string]any{
		{"event_id": "wire-d1", "tenant_id": "t1", "recipient_ref": "reach-a",
			"campaign_id": "camp-1", "outcome": "delivered", "occurred_at": occurred.Format(time.RFC3339)},
		{"event_id": "wire-d2", "tenant_id": "t1", "recipient_ref": "reach-b",
			"campaign_id": "camp-1", "outcome": "delivered", "occurred_at": occurred.Format(time.RFC3339)},
	} {
		body, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("сериализация события доставки: %v", err)
		}
		if err := broker.Publish(context.Background(), "message-delivery", "delivery-event.1", body, nil); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := service.slice.ReadModel.Snapshot("t1", readmodel.Scope{}, occurred)
		if snapshot.Visible && snapshot.Communicated == 2 {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("worker: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("worker не завершился после отмены контекста")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("проекция не наполнилась исходами доставки из очереди")
}

// publishAnalyticsEvent публикует нормализованное событие в очередь аналитики.
func publishAnalyticsEvent(t *testing.T, broker amqp.Publisher, eventID string, event map[string]any) {
	t.Helper()
	event["event_id"] = eventID
	body, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("сериализация события: %v", err)
	}
	if err := broker.Publish(context.Background(), "analytics", "analytics-event.1", body, nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

// waitForSnapshot ждёт, пока проекция отразит оба опубликованных события.
func waitForSnapshot(t *testing.T, model *readmodel.ReadModel, now time.Time) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot := model.Snapshot("t1", readmodel.Scope{}, now)
		if snapshot.Visible && snapshot.Found == 2 && snapshot.Warmed == 1 && snapshot.Cold == 1 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("проекция не наполнилась событиями из очереди")
}
