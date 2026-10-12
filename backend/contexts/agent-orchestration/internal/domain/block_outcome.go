package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

// BlockOutcomeKind — исход шага по блок обработки. Различает успех, отказ с
// нормализованной причиной и явную недоступность внешней системы: недоступность
// SHALL NOT выдаваться за успех (спека company-agent-orchestration, AGENTS.md §3).
type BlockOutcomeKind string

const (
	// OutcomeSucceeded — блок выполнен и дал результат.
	OutcomeSucceeded BlockOutcomeKind = "succeeded"

	// OutcomeFailed — блок отказал; причина нормализована.
	OutcomeFailed BlockOutcomeKind = "failed"

	// OutcomeUnavailable — внешняя система блока не выбрана или недоступна.
	OutcomeUnavailable BlockOutcomeKind = "unavailable"
)

// BlockOutcome — value object block-outcome: исход выполнения шага.
//
// Причина обязательна для отказа и недоступности и должна быть нормализована —
// без ключей, токенов и персональных данных (Реш. 12). Пустая причина не
// принимается: исход без разбираемой причины бесполезен для оператора.
type BlockOutcome struct {
	kind   BlockOutcomeKind
	reason string
}

// Succeeded возвращает исход «успех».
func Succeeded() BlockOutcome {
	return BlockOutcome{kind: OutcomeSucceeded}
}

// Failed возвращает исход «отказ» с нормализованной причиной.
func Failed(reason string) BlockOutcome {
	return BlockOutcome{kind: OutcomeFailed, reason: strings.TrimSpace(reason)}
}

// Unavailable возвращает исход «недоступно» с нормализованной причиной.
func Unavailable(reason string) BlockOutcome {
	return BlockOutcome{kind: OutcomeUnavailable, reason: strings.TrimSpace(reason)}
}

// Kind возвращает вид исхода.
func (o BlockOutcome) Kind() BlockOutcomeKind { return o.kind }

// Reason возвращает нормализованную причину (пусто для успеха).
func (o BlockOutcome) Reason() string { return o.reason }

// IsSuccess сообщает, что исход — успех.
func (o BlockOutcome) IsSuccess() bool { return o.kind == OutcomeSucceeded }

// IsUnavailable сообщает, что исход — явная недоступность.
func (o BlockOutcome) IsUnavailable() bool { return o.kind == OutcomeUnavailable }

// Validate проверяет согласованность исхода: известный вид и непустая причина у
// отказа и недоступности.
func (o BlockOutcome) Validate() error {
	switch o.kind {
	case OutcomeSucceeded:
		return nil
	case OutcomeFailed, OutcomeUnavailable:
		if o.reason == "" {
			return fmt.Errorf("%w: %s", ErrMissingOutcomeReason, o.kind)
		}
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrMissingOutcomeReason, o.kind)
	}
}

// MarshalJSON сериализует исход явной формой с видом и нормализованной причиной:
// поле исхода домена непустое в published-событии шага, а не пустой объект.
func (o BlockOutcome) MarshalJSON() ([]byte, error) {
	return json.Marshal(outcomeJSON{Kind: o.kind, Reason: o.reason})
}

// outcomeJSON — wire-форма исхода шага.
type outcomeJSON struct {
	Kind   BlockOutcomeKind `json:"kind"`
	Reason string           `json:"reason,omitempty"`
}
