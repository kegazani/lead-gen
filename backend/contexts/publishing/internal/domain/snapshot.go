package domain

import "fmt"

// Сохранённое состояние агрегата publication.
//
// Адаптер репозитория не имеет доступа к закрытым полям агрегата, поэтому он
// читает и пишет PublicationSnapshot с экспортированными полями. Доменный объект
// из снимка собирается только через RehydratePublication, который проверяет
// инварианты и не эмитит события: восстановление не является новым фактом домена.

// PublicationSnapshot — полное сохранённое состояние aggregate root publication.
type PublicationSnapshot struct {
	ID             PublicationID
	TenantID       TenantID
	Digest         DigestRef
	Target         PublicationTargetSnapshot
	Material       MaterialSnapshot
	Status         PublicationStatus
	IdempotencyKey IdempotencyKey
	Approval       *Approval
	Outcome        *Outcome
	Transitions    []Transition
	Version        int
}

// Snapshot возвращает состояние агрегата для сохранения.
func (p *Publication) Snapshot() PublicationSnapshot {
	transitions := make([]Transition, len(p.transitions))
	copy(transitions, p.transitions)
	return PublicationSnapshot{
		ID:             p.id,
		TenantID:       p.tenantID,
		Digest:         p.digest,
		Target:         p.target.Snapshot(),
		Material:       p.material.Snapshot(),
		Status:         p.status,
		IdempotencyKey: p.idempotencyKey,
		Approval:       copyApproval(p.approval),
		Outcome:        copyOutcome(p.outcome),
		Transitions:    transitions,
		Version:        p.version,
	}
}

// copyApproval копирует решение, сохраняя отсутствие как nil.
func copyApproval(a *Approval) *Approval {
	if a == nil {
		return nil
	}
	out := *a
	return &out
}

// copyOutcome копирует результат, сохраняя отсутствие как nil.
func copyOutcome(o *Outcome) *Outcome {
	if o == nil {
		return nil
	}
	out := *o
	return &out
}

// RehydratePublication восстанавливает агрегат из сохранённого состояния,
// проверяя инварианты: известность статуса, положительную версию, непустой и
// согласованный журнал переходов, наличие утверждения там, где оно обязательно.
// События не эмитятся.
func RehydratePublication(s PublicationSnapshot) (*Publication, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := s.Digest.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	target, err := rehydrateTarget(s.Target)
	if err != nil {
		return nil, fmt.Errorf("%w: публикация %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	material, err := rehydrateMaterial(target, s.Material)
	if err != nil {
		return nil, fmt.Errorf("%w: публикация %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	if !s.Status.Valid() {
		return nil, fmt.Errorf("%w: публикация %s: статус %q", ErrUnknownStatus, s.ID, s.Status)
	}
	if err := s.IdempotencyKey.validate(); err != nil {
		return nil, fmt.Errorf("%w: публикация %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	if s.Version <= 0 {
		return nil, fmt.Errorf("%w: публикация %s версии %d", ErrInvalidSnapshot, s.ID, s.Version)
	}
	if len(s.Transitions) == 0 {
		return nil, fmt.Errorf("%w: публикация %s без журнала переходов", ErrInvalidSnapshot, s.ID)
	}
	var last PublicationStatus
	for _, t := range s.Transitions {
		if !t.To.Valid() {
			return nil, fmt.Errorf("%w: публикация %s: неизвестный статус перехода %q", ErrInvalidSnapshot, s.ID, t.To)
		}
		if t.At.IsZero() {
			return nil, fmt.Errorf("%w: публикация %s: переход без времени", ErrInvalidSnapshot, s.ID)
		}
		last = t.To
	}
	if last != s.Status {
		return nil, fmt.Errorf(
			"%w: публикация %s: последний переход %q не совпадает со статусом %q",
			ErrInvalidSnapshot, s.ID, last, s.Status,
		)
	}
	if err := validateOutcomeForStatus(s); err != nil {
		return nil, err
	}

	return &Publication{
		id:             s.ID,
		tenantID:       s.TenantID,
		digest:         s.Digest,
		target:         target,
		material:       material,
		status:         s.Status,
		idempotencyKey: s.IdempotencyKey,
		approval:       copyApproval(s.Approval),
		outcome:        copyOutcome(s.Outcome),
		transitions:    append([]Transition(nil), s.Transitions...),
		version:        s.Version,
	}, nil
}

// validateOutcomeForStatus сверяет результат и утверждение со статусом: публикация
// невозможна без утверждения и адреса, отказ — без причины.
func validateOutcomeForStatus(s PublicationSnapshot) error {
	switch s.Status {
	case StatusPublished:
		if s.Approval == nil {
			return fmt.Errorf("%w: публикация %s в статусе published без утверждения", ErrInvalidSnapshot, s.ID)
		}
		if s.Outcome == nil || s.Outcome.Address == "" {
			return fmt.Errorf("%w: публикация %s в статусе published без адреса", ErrInvalidSnapshot, s.ID)
		}
	case StatusFailed:
		if s.Outcome == nil || s.Outcome.FailureReason == "" {
			return fmt.Errorf("%w: публикация %s в статусе failed без причины", ErrInvalidSnapshot, s.ID)
		}
	}
	return nil
}
