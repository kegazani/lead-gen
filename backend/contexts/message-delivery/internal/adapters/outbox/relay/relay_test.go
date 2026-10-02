package relay

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/contracts/message-delivery/deliveryevent"
)

var relayInstant = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

type fakeSource struct {
	messages  []Message
	published []string
	err       error
}

func (s *fakeSource) Pending(context.Context) ([]Message, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make([]Message, len(s.messages))
	copy(out, s.messages)
	return out, nil
}

func (s *fakeSource) MarkPublished(_ context.Context, ids ...string) error {
	s.published = append(s.published, ids...)
	return nil
}

type captureSink struct {
	events []deliveryevent.DeliveryEvent
	err    error
}

func (s *captureSink) Publish(_ context.Context, event deliveryevent.DeliveryEvent) error {
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, event)
	return nil
}

func testSenderDomain(t *testing.T) domain.SenderDomain {
	t.Helper()
	sender, err := domain.ParseSenderDomain("mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	return sender
}

func deliveredEvent(t *testing.T) domain.MessageDelivered {
	t.Helper()
	return domain.MessageDelivered{
		MessageID:        domain.MessageID("msg-1"),
		TenantID:         domain.TenantID("tenant-1"),
		ApprovedActionID: domain.ApprovedActionID("act-1"),
		RecipientRef:     domain.RecipientRef("ref-1"),
		SenderDomain:     testSenderDomain(t),
		AttemptNumber:    1,
		ProviderCode:     "250",
		At:               relayInstant,
	}
}

func TestRelay_PublishesDeliveryEvent(t *testing.T) {
	source := &fakeSource{messages: []Message{{ID: "outbox-1", Event: deliveredEvent(t)}}}
	sink := &captureSink{}

	published, err := New(source, sink).PublishPending(context.Background())
	if err != nil {
		t.Fatalf("PublishPending: %v", err)
	}
	if published != 1 || len(sink.events) != 1 {
		t.Fatalf("published=%d events=%d", published, len(sink.events))
	}
	if len(source.published) != 1 || source.published[0] != "outbox-1" {
		t.Fatalf("запись не подтверждена: %+v", source.published)
	}
	got := sink.events[0]
	if got.EventID != "outbox-1" {
		t.Fatalf("event_id не проставлен из outbox: %q", got.EventID)
	}
	if got.Outcome != deliveryevent.OutcomeDelivered || got.MessageID != "msg-1" {
		t.Fatalf("контракт: %+v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("опубликованный контракт невалиден: %v", err)
	}
}

func TestRelay_PublishesFailedEvent(t *testing.T) {
	failed := domain.MessageDeliveryFailed{
		MessageID:        domain.MessageID("msg-2"),
		TenantID:         domain.TenantID("tenant-1"),
		ApprovedActionID: domain.ApprovedActionID("act-2"),
		RecipientRef:     domain.RecipientRef("ref-2"),
		SenderDomain:     testSenderDomain(t),
		AttemptNumber:    1,
		Outcome:          domain.OutcomeHardBounce,
		ProviderCode:     "550",
		At:               relayInstant,
	}
	source := &fakeSource{messages: []Message{{ID: "outbox-1", Event: failed}}}
	sink := &captureSink{}

	if _, err := New(source, sink).PublishPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 || sink.events[0].Outcome != deliveryevent.OutcomeFailed {
		t.Fatalf("контракт отказа: %+v", sink.events)
	}
}

func TestRelay_UnknownEventConfirmedWithoutPublish(t *testing.T) {
	source := &fakeSource{messages: []Message{{ID: "outbox-1", Event: unknownEvent{}}}}
	sink := &captureSink{}

	published, err := New(source, sink).PublishPending(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if published != 0 || len(sink.events) != 0 {
		t.Fatalf("событие не должно публиковаться: published=%d", published)
	}
	if len(source.published) != 1 {
		t.Fatalf("событие должно быть подтверждено: %+v", source.published)
	}
}

func TestRelay_PublishErrorStopsAndDoesNotMark(t *testing.T) {
	source := &fakeSource{messages: []Message{{ID: "outbox-1", Event: deliveredEvent(t)}}}
	sink := &captureSink{err: errors.New("брокер недоступен")}

	published, err := New(source, sink).PublishPending(context.Background())
	if err == nil {
		t.Fatal("ожидалась ошибка публикации")
	}
	if published != 0 {
		t.Fatalf("published=%d", published)
	}
	if len(source.published) != 0 {
		t.Fatalf("запись не должна подтверждаться при ошибке: %+v", source.published)
	}
}

type unknownEvent struct{}

func (unknownEvent) Name() domain.EventName { return domain.EventName("something-else") }
func (unknownEvent) OccurredAt() time.Time  { return relayInstant }
