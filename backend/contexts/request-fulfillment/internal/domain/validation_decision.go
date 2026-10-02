package domain

import (
	"fmt"
	"strings"
	"time"
)

// ValidationOutcome — исход валидации заявки админом.
type ValidationOutcome string

const (
	// ValidationApproved — заявка одобрена (validating → validated).
	ValidationApproved ValidationOutcome = "approved"

	// ValidationRejected — заявка отклонена (validating → rejected).
	ValidationRejected ValidationOutcome = "rejected"
)

// isValid сообщает, что исход объявлен доменом.
func (o ValidationOutcome) isValid() bool {
	return o == ValidationApproved || o == ValidationRejected
}

// String возвращает строковое значение исхода.
func (o ValidationOutcome) String() string { return string(o) }

// ValidationDecision — VO решения валидации: решение, актор, время и (при
// отклонении) причина.
//
// Решение живёт внутри aggregate root request, а не отдельным агрегатом: предмет
// решения — сама заявка, а не черновик действия (тот аудируется через
// operator-decision контекста outreach-orchestration). Неизменяемо.
type ValidationDecision struct {
	outcome ValidationOutcome
	actor   ActorID
	at      time.Time
	reason  string
}

// newValidationDecision собирает решение, проверяя обязательные части: актор и
// время обязательны всегда, причина — при отклонении.
func newValidationDecision(outcome ValidationOutcome, actor ActorID, at time.Time, reason string) (ValidationDecision, error) {
	if !outcome.isValid() {
		return ValidationDecision{}, fmt.Errorf("%w: %q", ErrUnknownValidationOutcome, outcome)
	}
	if err := actor.validate(); err != nil {
		return ValidationDecision{}, err
	}
	if at.IsZero() {
		return ValidationDecision{}, ErrMissingTime
	}
	trimmed := strings.TrimSpace(reason)
	if outcome == ValidationRejected && trimmed == "" {
		return ValidationDecision{}, ErrReasonRequired
	}
	return ValidationDecision{outcome: outcome, actor: actor, at: at, reason: trimmed}, nil
}

// Outcome возвращает исход решения.
func (d ValidationDecision) Outcome() ValidationOutcome { return d.outcome }

// Actor возвращает актора решения.
func (d ValidationDecision) Actor() ActorID { return d.actor }

// At возвращает время решения.
func (d ValidationDecision) At() time.Time { return d.at }

// Reason возвращает причину решения (пустая при одобрении).
func (d ValidationDecision) Reason() string { return d.reason }

// Approved сообщает, что заявка одобрена.
func (d ValidationDecision) Approved() bool { return d.outcome == ValidationApproved }

// ValidationDecisionSnapshot — сохранённое состояние решения валидации.
type ValidationDecisionSnapshot struct {
	Outcome ValidationOutcome
	Actor   ActorID
	At      time.Time
	Reason  string
}

// Snapshot возвращает состояние решения для сохранения.
func (d ValidationDecision) Snapshot() ValidationDecisionSnapshot {
	return ValidationDecisionSnapshot{Outcome: d.outcome, Actor: d.actor, At: d.at, Reason: d.reason}
}

// rehydrateValidationDecision восстанавливает решение из снимка, проверяя
// инварианты. События не эмитятся: восстановление — не новый факт домена.
func rehydrateValidationDecision(s ValidationDecisionSnapshot) (ValidationDecision, error) {
	return newValidationDecision(s.Outcome, s.Actor, s.At, s.Reason)
}
