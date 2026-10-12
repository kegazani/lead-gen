package deliveryevent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

var testInstant = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func baseEnvelope() Envelope {
	return Envelope{
		EventID:       "evt-1",
		OccurredAt:    testInstant,
		TenantID:      "tenant-1",
		CorrelationID: "act-1",
	}
}

func validEvent() DeliveryEvent {
	event := NewDeliveryEvent(baseEnvelope())
	event.MessageID = "msg-1"
	event.ApprovedActionID = "act-1"
	event.RecipientRef = "ref-1"
	event.SenderDomain = "mail.example.com"
	event.Outcome = OutcomeDelivered
	event.AttemptNumber = 1
	event.ProviderCode = "250"
	return event
}

func TestDeliveryEvent_RoundTrip(t *testing.T) {
	original := validEvent()
	if err := original.Validate(); err != nil {
		t.Fatalf("исходный контракт должен быть валиден: %v", err)
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored DeliveryEvent
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, restored) {
		t.Fatalf("round-trip не совпал:\n%+v\n%+v", original, restored)
	}
}

func TestDeliveryEvent_RequiredFields(t *testing.T) {
	if err := validEvent().Validate(); err != nil {
		t.Fatalf("полный контракт должен быть валиден: %v", err)
	}
	err := NewDeliveryEvent(baseEnvelope()).Validate()
	if err == nil {
		t.Fatal("ожидалась ошибка об обязательных полях")
	}
	for _, field := range []string{"message_id", "approved_action_id", "recipient_ref", "sender_domain", "outcome", "attempt_number"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
	}
}

func TestDeliveryEvent_CampaignOptional(t *testing.T) {
	// Ссылка на кампанию (MINOR 1.1.0) необязательна: событие без неё валидно.
	if err := validEvent().Validate(); err != nil {
		t.Fatalf("событие без кампании должно быть валидно: %v", err)
	}
	withCampaign := validEvent()
	withCampaign.CampaignID = "handoff-req-1"
	raw, err := json.Marshal(withCampaign)
	if err != nil {
		t.Fatal(err)
	}
	var restored DeliveryEvent
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.CampaignID != "handoff-req-1" {
		t.Fatalf("ссылка на кампанию не пережила round-trip: %+v", restored)
	}
	if SchemaVersion != "1.1.0" {
		t.Fatalf("ожидалась версия схемы 1.1.0, got %q", SchemaVersion)
	}
}

func TestDeliveryEvent_RejectsUnknownOutcome(t *testing.T) {
	unknown := validEvent()
	unknown.Outcome = "forwarded"
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), "outcome") {
		t.Fatalf("неизвестный исход должен отвергаться, got %v", err)
	}
}

func TestDeliveryEvent_IdempotencyKey(t *testing.T) {
	event := validEvent()
	if got, want := event.IdempotencyKey(), "tenant-1|msg-1|1"; got != want {
		t.Fatalf("ключ идемпотентности: got %q, want %q", got, want)
	}
	other := event
	other.AttemptNumber = 2
	if other.IdempotencyKey() == event.IdempotencyKey() {
		t.Fatal("другая попытка должна давать другой ключ")
	}
}

func TestNormalizeOutcome(t *testing.T) {
	if _, err := NormalizeOutcome("DELIVERED"); err != nil {
		t.Fatalf("регистр не значим: %v", err)
	}
	if _, err := NormalizeOutcome("failed"); err != nil {
		t.Fatalf("failed должен быть валиден: %v", err)
	}
	if _, err := NormalizeOutcome("bounced"); err == nil {
		t.Fatal("неизвестный исход должен отвергаться")
	}
}
