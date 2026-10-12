package agentevent

import (
	"errors"
	"testing"
	"time"
)

func TestStepEventValidate(t *testing.T) {
	envelope := Envelope{EventID: "evt-1", Name: NameStepCompleted, OccurredAt: time.Now(), TenantID: "tenant-1"}
	if err := (StepEvent{Envelope: envelope, RunID: "run-1", BlockRef: "search", Outcome: OutcomeSucceeded}).Validate(); err != nil {
		t.Fatalf("корректное событие отвергнуто: %v", err)
	}
	bad := StepEvent{Envelope: envelope, RunID: "run-1", BlockRef: "search", Outcome: "выдумка"}
	if err := bad.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalid", err)
	}
}

func TestEnvelopeValidate(t *testing.T) {
	if err := (Envelope{}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("пустой конверт: ошибка = %v, ожидалась ErrInvalid", err)
	}
}
