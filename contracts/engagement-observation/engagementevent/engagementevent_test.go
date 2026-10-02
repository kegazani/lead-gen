package engagementevent

import (
	"testing"
	"time"
)

func validEvent() EngagementEvent {
	event := NewEngagementEvent(Envelope{
		EventID:       "event-1",
		OccurredAt:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
	})
	event.EngagementEventID = "evt-1"
	event.CompanyID = "company-1"
	event.Kind = KindDelivery
	event.Confidence = ConfidenceHigh
	event.Outcome = "delivered"
	return event
}

func TestValidateAcceptsDelivery(t *testing.T) {
	event := validEvent()
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if event.Name != ContractName || event.SchemaVersion != SchemaVersion {
		t.Fatalf("имя/версия = %q/%q", event.Name, event.SchemaVersion)
	}
}

func TestValidateRejectsPixelKind(t *testing.T) {
	event := validEvent()
	event.Kind = "open"
	event.Outcome = "opened"
	if err := event.Validate(); err == nil {
		t.Fatalf("пиксельный вид должен быть отвергнут")
	}
}

func TestValidateRejectsMissingFields(t *testing.T) {
	event := validEvent()
	event.CompanyID = ""
	event.Confidence = ""
	if err := event.Validate(); err == nil {
		t.Fatalf("отсутствие обязательных полей должно быть отвергнуто")
	}
}

func TestValidateTransitionRequiresLink(t *testing.T) {
	event := validEvent()
	event.Kind = KindTransition
	event.Outcome = "transitioned"
	if err := event.Validate(); err == nil {
		t.Fatalf("переход без ссылки должен быть отвергнут")
	}
	event.LinkRef = "lnk-1"
	event.Target = "https://example.com"
	if err := event.Validate(); err != nil {
		t.Fatalf("корректный переход отвергнут: %v", err)
	}
}

func TestIdempotencyKeyDistinct(t *testing.T) {
	first := validEvent()
	second := validEvent()
	second.EventID = "event-2"
	if first.IdempotencyKey() == second.IdempotencyKey() {
		t.Fatalf("разные события дали одинаковый ключ идемпотентности")
	}
}

func TestIsValidSemver(t *testing.T) {
	if !IsValidSemver("1.0.0") {
		t.Fatalf("1.0.0 должен быть корректной версией")
	}
	if IsValidSemver("v1") {
		t.Fatalf("v1 не semver")
	}
}
