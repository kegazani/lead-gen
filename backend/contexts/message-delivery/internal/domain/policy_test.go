package domain

import (
	"errors"
	"testing"
)

func validCommand(t *testing.T) DeliveryCommand {
	t.Helper()
	return DeliveryCommand{
		CommandID:        CommandID("cmd-1"),
		TenantID:         TenantID("tenant-1"),
		ApprovedActionID: ApprovedActionID("act-1"),
		RecipientRef:     RecipientRef("recipient-ref-1"),
		Address:          mustAddress(t, "lead@example.com"),
		SenderDomain:     mustSender(t, "mail.example.com"),
		CorrelationID:    CorrelationID("act-1"),
	}
}

func TestApprovedActionOnly(t *testing.T) {
	if err := ApprovedActionOnly(validCommand(t)); err != nil {
		t.Fatalf("команда с утверждённым действием отвергнута: %v", err)
	}
	without := validCommand(t)
	without.ApprovedActionID = ""
	if err := ApprovedActionOnly(without); !errors.Is(err, ErrApprovedActionRequired) {
		t.Fatalf("отсутствие утверждённого действия: got %v", err)
	}
}

func TestDeliverabilityPolicy(t *testing.T) {
	reputation, err := NewSenderReputation(mustSender(t, "mail.example.com"), testTime)
	if err != nil {
		t.Fatal(err)
	}

	decision, err := DeliverabilityPolicy(validCommand(t), reputation, false)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed {
		t.Fatalf("отправка должна быть допустима: %+v", decision)
	}

	suppressed, err := DeliverabilityPolicy(validCommand(t), reputation, true)
	if err != nil {
		t.Fatal(err)
	}
	if suppressed.Allowed || !suppressed.Suppressed {
		t.Fatalf("стоп-лист должен блокировать: %+v", suppressed)
	}

	// Приостановленная репутация блокирует отправку.
	for i := 0; i < hardBouncePauseLimit; i++ {
		_ = reputation.ApplyNormalizedOutcome(NormalizedOutcome{Outcome: OutcomeHardBounce}, testTime)
	}
	suspended, err := DeliverabilityPolicy(validCommand(t), reputation, false)
	if err != nil {
		t.Fatal(err)
	}
	if suspended.Allowed || !suspended.Suspended {
		t.Fatalf("приостановленная репутация должна блокировать: %+v", suspended)
	}

	if _, err := DeliverabilityPolicy(validCommand(t), reputation, false); err != nil {
		t.Fatal(err)
	}
	// Стоп-лист имеет приоритет над репутацией.
	both, err := DeliverabilityPolicy(validCommand(t), reputation, true)
	if err != nil {
		t.Fatal(err)
	}
	if !both.Suppressed {
		t.Fatalf("стоп-лист приоритетнее: %+v", both)
	}
}

func TestDeliverabilityPolicy_RejectsUnapprovedCommand(t *testing.T) {
	without := validCommand(t)
	without.ApprovedActionID = ""
	if _, err := DeliverabilityPolicy(without, nil, false); !errors.Is(err, ErrApprovedActionRequired) {
		t.Fatalf("политика должна требовать утверждённое действие: got %v", err)
	}
}

func TestDeliveryCommand_IdempotencyKey(t *testing.T) {
	cmd := validCommand(t)
	if got, want := cmd.IdempotencyKey(), "tenant-1|act-1"; got != want {
		t.Fatalf("ключ идемпотентности: got %q, want %q", got, want)
	}
	other := cmd
	other.ApprovedActionID = "act-2"
	if other.IdempotencyKey() == cmd.IdempotencyKey() {
		t.Fatal("другое действие должно давать другой ключ")
	}
}
