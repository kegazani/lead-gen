package domain

import (
	"errors"
	"testing"
	"time"
)

func TestSenderReputation_ConstraintsFromNormalizedResponses(t *testing.T) {
	reputation, err := NewSenderReputation(mustSender(t, "mail.example.com"), testTime)
	if err != nil {
		t.Fatal(err)
	}
	if reputation.Suspended() {
		t.Fatal("новая репутация не должна быть приостановлена")
	}

	// Сбой транспорта и стоп-лист не являются ответами провайдера и в репутацию
	// не входят (инвариант sender-reputation).
	for _, outcome := range []AttemptOutcome{OutcomeTemporary, OutcomeSuppressed} {
		if _, ok := NormalizeProviderResponse(outcome, "x"); ok {
			t.Fatalf("%q не должен попадать в репутацию", outcome)
		}
	}

	if err := reputation.ApplyNormalizedOutcome(NormalizedOutcome{Outcome: OutcomeDelivered}, testTime); err != nil {
		t.Fatal(err)
	}
	if reputation.DeliveredCount() != 1 {
		t.Fatalf("счётчик доставок: %d", reputation.DeliveredCount())
	}

	// Достижение порога жёстких отказов приостанавливает отправки.
	for i := 0; i < hardBouncePauseLimit; i++ {
		if err := reputation.ApplyNormalizedOutcome(NormalizedOutcome{Outcome: OutcomeHardBounce}, testTime); err != nil {
			t.Fatal(err)
		}
	}
	if !reputation.Suspended() {
		t.Fatal("по достижении порога отправки должны приостанавливаться")
	}
	if len(reputation.Constraints()) == 0 || reputation.Constraints()[0] != ConstraintPaused {
		t.Fatalf("ожидалось ограничение paused: %+v", reputation.Constraints())
	}
}

func TestSenderReputation_ThrottleConstraint(t *testing.T) {
	reputation, err := NewSenderReputation(mustSender(t, "mail.example.com"), testTime)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < softBounceThrottleLimit; i++ {
		if err := reputation.ApplyNormalizedOutcome(NormalizedOutcome{Outcome: OutcomeSoftBounce}, testTime); err != nil {
			t.Fatal(err)
		}
	}
	found := false
	for _, c := range reputation.Constraints() {
		if c == ConstraintThrottled {
			found = true
		}
	}
	if !found {
		t.Fatalf("ожидалось ограничение throttled: %+v", reputation.Constraints())
	}
}

func TestSenderReputation_RejectsNonProviderOutcome(t *testing.T) {
	reputation, err := NewSenderReputation(mustSender(t, "mail.example.com"), testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := reputation.ApplyNormalizedOutcome(NormalizedOutcome{Outcome: OutcomeTemporary}, testTime); !errors.Is(err, ErrUnknownAttemptOutcome) {
		t.Fatalf("несерверный исход должен отвергаться, got %v", err)
	}
	if err := reputation.ApplyNormalizedOutcome(NormalizedOutcome{Outcome: OutcomeDelivered}, time.Time{}); !errors.Is(err, ErrAttemptTimeRequired) {
		t.Fatalf("отсутствие времени должно отвергаться, got %v", err)
	}
}

func TestSenderReputation_SnapshotRoundTrip(t *testing.T) {
	reputation, err := NewSenderReputation(mustSender(t, "mail.example.com"), testTime)
	if err != nil {
		t.Fatal(err)
	}
	_ = reputation.ApplyNormalizedOutcome(NormalizedOutcome{Outcome: OutcomeHardBounce}, testTime)
	restored, err := RehydrateSenderReputation(reputation.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if restored.HardBounceCount() != 1 || !restored.Domain().Equals(reputation.Domain()) {
		t.Fatalf("репутация восстановлена неверно: %+v", restored.Snapshot())
	}
}

func TestRehydrateSenderReputation_RejectsNegativeCounters(t *testing.T) {
	snapshot := SenderReputationSnapshot{
		Domain:      mustSender(t, "mail.example.com"),
		HardBounces: -1,
		UpdatedAt:   testTime,
	}
	if _, err := RehydrateSenderReputation(snapshot); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("отрицательные счётчики: got %v", err)
	}
}

func TestSenderReputation_UpdatedAt(t *testing.T) {
	reputation, err := NewSenderReputation(mustSender(t, "mail.example.com"), testTime)
	if err != nil {
		t.Fatal(err)
	}
	later := testTime.Add(time.Hour)
	if err := reputation.ApplyNormalizedOutcome(NormalizedOutcome{Outcome: OutcomeDelivered}, later); err != nil {
		t.Fatal(err)
	}
	if !reputation.UpdatedAt().Equal(later) {
		t.Fatalf("UpdatedAt: got %v", reputation.UpdatedAt())
	}
}
