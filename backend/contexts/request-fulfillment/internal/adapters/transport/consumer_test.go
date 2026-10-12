package transport

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/messaging/inbox"
	amqptransport "lead-gen/backend/platform/transport/amqp"
)

// stubRepo — минимальный репозиторий заявок: отдаёт заявку по кампании и
// тенанту, чужое — ErrRequestNotFound.
type stubRepo struct{ request *domain.Request }

func (stubRepo) Save(context.Context, *domain.Request, int) error { return nil }
func (stubRepo) FindByID(context.Context, domain.RequestID) (*domain.Request, error) {
	return nil, ports.ErrRequestNotFound
}
func (stubRepo) ListByStatus(context.Context, domain.RequestStatus) ([]*domain.Request, error) {
	return nil, nil
}
func (stubRepo) CountByTenant(context.Context, domain.TenantID) (int, error) { return 0, nil }
func (stubRepo) CountInWork(context.Context, domain.TenantID) (int, error)   { return 0, nil }
func (stubRepo) ListByTenant(context.Context, domain.TenantID) ([]*domain.Request, error) {
	return nil, nil
}
func (r stubRepo) FindByCampaign(_ context.Context, tenantID domain.TenantID, campaignID domain.CampaignID) (*domain.Request, error) {
	if r.request != nil && r.request.TenantID() == tenantID && r.request.CampaignID() == campaignID {
		return r.request, nil
	}
	return nil, ports.ErrRequestNotFound
}

// stubSignaler — сигналер, запоминающий исходы доставки. Метод
// SignalDeliveryOutcome вызывается из горутины Subscriber.Run, а поля читаются из
// основной горутины теста, поэтому доступ защищён мьютексом.
type stubSignaler struct {
	mu    sync.Mutex
	calls []ports.DeliveryOutcome
	err   error
}

func (s *stubSignaler) SignalApprovalDecision(context.Context, ports.WorkflowDecision) error {
	return nil
}
func (s *stubSignaler) SignalDeliveryOutcome(_ context.Context, in ports.DeliveryOutcome) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, in)
	return s.err
}

// snapshotCalls возвращает потокобезопасную копию зафиксированных исходов.
func (s *stubSignaler) snapshotCalls() []ports.DeliveryOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ports.DeliveryOutcome, len(s.calls))
	copy(out, s.calls)
	return out
}

// mustCampaignRequest строит заявку с зафиксированной кампанией.
func mustCampaignRequest(t *testing.T, campaign domain.CampaignID) *domain.Request {
	t.Helper()
	request, err := domain.NewRequest(domain.NewRequestParams{
		ID:       "req-1",
		OfferID:  "offer-1",
		TenantID: "tenant-1",
		Content:  "Предлагаем автоматизацию",
		Target:   "IT",
		Actor:    "actor-1",
		At:       time.Now(),
		EventID:  "evt-1",
	})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := request.RecordCampaign(campaign); err != nil {
		t.Fatalf("RecordCampaign: %v", err)
	}
	return request
}

// TestConcludeDeliveryHandlerIgnoresForeignCampaign проверяет, что событие чужой
// кампании игнорируется, а не привязывается к произвольной заявке.
func TestConcludeDeliveryHandlerIgnoresForeignCampaign(t *testing.T) {
	signaler := &stubSignaler{}
	handler := command.NewConcludeDeliveryHandler(stubRepo{request: mustCampaignRequest(t, "handoff-req-1")}, signaler, nil)
	result, err := handler.Handle(context.Background(), command.ConcludeDelivery{
		TenantID: "tenant-1", CampaignID: "handoff-other", MessageID: "msg-1", Delivered: true,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if calls := signaler.snapshotCalls(); result.Matched || len(calls) != 0 {
		t.Fatalf("чужое событие не должно сопоставляться: %+v, сигналов %d", result, len(calls))
	}
}

// TestConcludeDeliveryHandlerUnavailableRuntime проверяет, что при недоступном
// рантайме обработчик честно сообщает недоступность, а не выдаёт завершение.
func TestConcludeDeliveryHandlerUnavailableRuntime(t *testing.T) {
	handler := command.NewConcludeDeliveryHandler(stubRepo{request: mustCampaignRequest(t, "handoff-req-1")}, nil, nil)
	_, err := handler.Handle(context.Background(), command.ConcludeDelivery{
		TenantID: "tenant-1", CampaignID: "handoff-req-1", MessageID: "msg-1", Delivered: true,
	})
	if !errors.Is(err, ports.ErrWorkflowUnavailable) {
		t.Fatalf("недоступность рантайма не распознана: %v", err)
	}
}

// TestSubscriberRoutesDeliveryToWorkflow проверяет подписчика: доставленное событие
// подаёт сигнал, повтор по event_id дедуплицируется, исход отказа переносится как
// недоставка, а чужое событие не сигналит.
func TestSubscriberRoutesDeliveryToWorkflow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	broker := amqptransport.NewInMemoryBroker(3)
	signaler := &stubSignaler{}
	handler := command.NewConcludeDeliveryHandler(stubRepo{request: mustCampaignRequest(t, "handoff-req-1")}, signaler, nil)
	sub := NewSubscriber(broker, inbox.NewMemoryDeduper(), "", handler)

	publish := func(eventID, campaign, messageID, outcome string) {
		body, err := json.Marshal(map[string]string{
			"event_id":    eventID,
			"tenant_id":   "tenant-1",
			"campaign_id": campaign,
			"message_id":  messageID,
			"outcome":     outcome,
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := broker.Publish(ctx, "message-delivery", "delivery-event.v1", body, map[string]string{"event_id": eventID}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}
	publish("evt-1", "handoff-req-1", "msg-1", "delivered")
	publish("evt-1", "handoff-req-1", "msg-1", "delivered") // повтор того же event_id
	publish("evt-2", "handoff-req-1", "msg-2", "failed")
	publish("evt-3", "handoff-foreign", "msg-3", "delivered")

	done := make(chan error, 1)
	go func() { done <- sub.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for len(signaler.snapshotCalls()) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("сигналы не поданы за отведённое время: %+v", signaler.snapshotCalls())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Чужое событие и повтор не должны добавить сигналов.
	time.Sleep(100 * time.Millisecond)
	calls := signaler.snapshotCalls()
	if len(calls) != 2 {
		t.Fatalf("сигналов = %d, ожидалось 2: %+v", len(calls), calls)
	}
	if !calls[0].Delivered || calls[0].MessageID != "msg-1" {
		t.Fatalf("первый сигнал = %+v, ожидалась доставка msg-1", calls[0])
	}
	if calls[1].Delivered || calls[1].MessageID != "msg-2" {
		t.Fatalf("второй сигнал = %+v, ожидался отказ msg-2", calls[1])
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run не завершился по отмене контекста")
	}
}

// TestNormalizeOutcomeRejectsUnknown проверяет закрытый набор исходов доставки.
func TestNormalizeOutcomeRejectsUnknown(t *testing.T) {
	if delivered, err := normalizeOutcome("delivered"); err != nil || !delivered {
		t.Fatalf("delivered: got %v/%v", delivered, err)
	}
	if delivered, err := normalizeOutcome("failed"); err != nil || delivered {
		t.Fatalf("failed: got %v/%v", delivered, err)
	}
	if _, err := normalizeOutcome("bogus"); err == nil {
		t.Fatal("неизвестный исход должен отвергаться")
	}
}
