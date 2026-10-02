package analytics

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// recordingPublisher — заглушка порта публикации брокера: записывает вызовы.
type recordingPublisher struct {
	calls []publishCall
	err   error
}

type publishCall struct {
	exchange   string
	routingKey string
	body       []byte
	headers    map[string]string
}

func (p *recordingPublisher) Publish(_ context.Context, exchange, routingKey string, body []byte, headers map[string]string) error {
	if p.err != nil {
		return p.err
	}
	p.calls = append(p.calls, publishCall{exchange: exchange, routingKey: routingKey, body: body, headers: headers})
	return nil
}

func TestPublisherSendsAnalyticsEventContract(t *testing.T) {
	recorder := &recordingPublisher{}
	publisher := NewPublisher(recorder, "tenant-default")

	occurred := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	err := publisher.Publish(context.Background(), ports.AnalyticsEvent{
		EventID:    "evt-1",
		TenantID:   "t1",
		Kind:       ports.AnalyticsKindMessageSent,
		CompanyID:  "c1",
		Status:     "sent",
		Outcome:    "sent",
		OccurredAt: occurred,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(recorder.calls) != 1 {
		t.Fatalf("вызовов публикации = %d, ожидался 1", len(recorder.calls))
	}
	call := recorder.calls[0]
	if call.exchange != "lead-management" {
		t.Fatalf("exchange = %q", call.exchange)
	}
	// Контракт analytics-event, MAJOR = 1: очередь analytics.analytics-event
	// получает событие по привязке analytics-event.*.
	if call.routingKey != "analytics-event.1" {
		t.Fatalf("routingKey = %q, ожидалось analytics-event.1", call.routingKey)
	}
	if call.headers["event_id"] != "evt-1" || call.headers["tenant_id"] != "t1" {
		t.Fatalf("заголовки = %+v", call.headers)
	}

	var dto eventDTO
	if err := json.Unmarshal(call.body, &dto); err != nil {
		t.Fatalf("разбор тела: %v", err)
	}
	if dto.EventID != "evt-1" || dto.TenantID != "t1" || dto.Kind != "message-sent" ||
		dto.CompanyID != "c1" || dto.Status != "sent" || dto.OccurredAt != occurred.Format(time.RFC3339) {
		t.Fatalf("тело события = %+v", dto)
	}
}

func TestPublisherFallsBackToDefaultTenant(t *testing.T) {
	recorder := &recordingPublisher{}
	publisher := NewPublisher(recorder, "tenant-default")

	if err := publisher.Publish(context.Background(), ports.AnalyticsEvent{
		EventID:   "evt-2",
		Kind:      ports.AnalyticsKindLeadWorkflow,
		CompanyID: "c2",
		Status:    "warming",
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := recorder.calls[0].headers["tenant_id"]; got != "tenant-default" {
		t.Fatalf("tenant_id = %q, ожидался tenant-default", got)
	}
}

func TestPublisherRejectsEventWithoutID(t *testing.T) {
	publisher := NewPublisher(&recordingPublisher{}, "t")
	if err := publisher.Publish(context.Background(), ports.AnalyticsEvent{CompanyID: "c"}); err == nil {
		t.Fatal("событие без event_id должно быть отвергнуто")
	}
}

func TestPublisherWithoutBrokerIsNotAssembled(t *testing.T) {
	var publisher *Publisher
	if err := publisher.Publish(context.Background(), ports.AnalyticsEvent{EventID: "e"}); err == nil {
		t.Fatal("издатель без брокера должен отвергать публикацию")
	}
}

var _ ports.AnalyticsPublisher = (*Publisher)(nil)
