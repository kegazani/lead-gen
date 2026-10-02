package inproc_test

import (
	"context"
	"testing"
	"time"

	"lead-gen/contracts/engagement-observation/engagementevent"
	"lead-gen/contracts/engagement-observation/engagementevent/inproc"
)

type countingPublisher struct {
	calls int
	err   error
}

func (p *countingPublisher) Publish(context.Context, engagementevent.EngagementEvent) error {
	p.calls++
	return p.err
}

func validEvent() engagementevent.EngagementEvent {
	event := engagementevent.NewEngagementEvent(engagementevent.Envelope{
		EventID:       "event-1",
		OccurredAt:    time.Now().UTC(),
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
	})
	event.EngagementEventID = "evt-1"
	event.CompanyID = "company-1"
	event.Kind = engagementevent.KindDelivery
	event.Confidence = engagementevent.ConfidenceHigh
	event.Outcome = "delivered"
	return event
}

func TestBindRequiresImpl(t *testing.T) {
	if _, err := inproc.Bind(nil); err == nil {
		t.Fatalf("Bind без реализации должен падать")
	}
}

func TestPublishValidatesAndDeduplicates(t *testing.T) {
	impl := &countingPublisher{}
	adapter, err := inproc.Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	event := validEvent()
	if err := adapter.Publish(context.Background(), event); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := adapter.Publish(context.Background(), event); err != nil {
		t.Fatalf("повторный Publish: %v", err)
	}
	if impl.calls != 1 {
		t.Fatalf("вызовов владельца = %d, ожидался 1 (идемпотентность)", impl.calls)
	}

	invalid := event
	invalid.Kind = "open"
	if err := adapter.Publish(context.Background(), invalid); err == nil {
		t.Fatalf("невалидный контракт должен быть отвергнут")
	}
}

func TestPublishRespectsCancelledContext(t *testing.T) {
	adapter, err := inproc.Bind(&countingPublisher{})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := adapter.Publish(ctx, validEvent()); err == nil {
		t.Fatalf("отменённый контекст должен прервать вызов")
	}
}
