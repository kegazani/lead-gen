package domain

import (
	"errors"
	"testing"
)

func TestParseDeliveryAddress(t *testing.T) {
	valid := map[string]string{
		"lead@example.com":        "lead@example.com",
		"  Lead@Example.COM":      "Lead@example.com",
		"a.b+tag@sub.example.org": "a.b+tag@sub.example.org",
	}
	for raw, want := range valid {
		got, err := ParseDeliveryAddress(raw)
		if err != nil {
			t.Fatalf("%q: ожидался успех, got %v", raw, err)
		}
		if got.String() != want {
			t.Errorf("%q: got %q, want %q", raw, got.String(), want)
		}
	}

	invalid := []string{"", "   ", "lead", "lead@", "@example.com", "lead@example", "a@b@c.com", "lead @example.com", "lead@.com", "lead@example."}
	for _, raw := range invalid {
		if _, err := ParseDeliveryAddress(raw); !errors.Is(err, ErrInvalidDeliveryAddress) && !errors.Is(err, ErrEmptyDeliveryAddress) {
			t.Errorf("%q: ожидался отказ, got %v", raw, err)
		}
	}
}

func TestParseSenderDomain(t *testing.T) {
	got, err := ParseSenderDomain("  Mail.Example.COM ")
	if err != nil {
		t.Fatalf("ожидался успех, got %v", err)
	}
	if got.String() != "mail.example.com" {
		t.Fatalf("нормализация домена: got %q", got.String())
	}

	invalid := []string{"", "   ", "example", ".example.com", "example.com.", "exa mple.com"}
	for _, raw := range invalid {
		if _, err := ParseSenderDomain(raw); !errors.Is(err, ErrInvalidSenderDomain) && !errors.Is(err, ErrEmptySenderDomain) {
			t.Errorf("%q: ожидался отказ, got %v", raw, err)
		}
	}
}

func TestNormalizeProviderResponse(t *testing.T) {
	for _, outcome := range []AttemptOutcome{OutcomeDelivered, OutcomeHardBounce, OutcomeSoftBounce} {
		if _, ok := NormalizeProviderResponse(outcome, "code"); !ok {
			t.Errorf("%q должен считаться ответом провайдера", outcome)
		}
	}
	for _, outcome := range []AttemptOutcome{OutcomeTemporary, OutcomeSuppressed, AttemptOutcome("")} {
		if _, ok := NormalizeProviderResponse(outcome, "code"); ok {
			t.Errorf("%q не должен считаться ответом провайдера", outcome)
		}
	}
}

func TestDeliveryAttemptValidate(t *testing.T) {
	valid := DeliveryAttempt{Number: 1, Outcome: OutcomeDelivered, At: testTime}
	if err := valid.validate(); err != nil {
		t.Fatalf("корректная попытка отвергнута: %v", err)
	}
	if err := (DeliveryAttempt{Number: 0, Outcome: OutcomeDelivered, At: testTime}).validate(); !errors.Is(err, ErrInvalidAttemptNumber) {
		t.Fatalf("нулевой номер должен отвергаться, got %v", err)
	}
	if err := (DeliveryAttempt{Number: 1, Outcome: AttemptOutcome("nope"), At: testTime}).validate(); !errors.Is(err, ErrUnknownAttemptOutcome) {
		t.Fatalf("неизвестный исход должен отвергаться, got %v", err)
	}
	if err := (DeliveryAttempt{Number: 1, Outcome: OutcomeDelivered}).validate(); !errors.Is(err, ErrAttemptTimeRequired) {
		t.Fatalf("отсутствие времени должно отвергаться, got %v", err)
	}
}
