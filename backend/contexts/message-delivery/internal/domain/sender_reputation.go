package domain

import (
	"fmt"
	"time"
)

// ReputationConstraint — активное ограничение отправляющего домена. Значения
// совпадают с контрактом deliverability-policy.
type ReputationConstraint string

const (
	// ConstraintPaused — новые отправки домена приостановлены (много жёстких
	// отказов).
	ConstraintPaused ReputationConstraint = "paused"

	// ConstraintThrottled — отправки домена ограничены по интенсивности (много
	// мягких отказов).
	ConstraintThrottled ReputationConstraint = "throttled"
)

// Пороги вычисления ограничений. Константы домена: правило не конфигурируется
// вызывающим, чтобы ограничение нельзя было обойти настройкой.
const (
	hardBouncePauseLimit    = 3
	softBounceThrottleLimit = 10
)

// SenderReputationSnapshot — сохранённое состояние репутации домена.
type SenderReputationSnapshot struct {
	Domain      SenderDomain
	Delivered   int
	HardBounces int
	SoftBounces int
	UpdatedAt   time.Time
}

// SenderReputation — aggregate root: репутация отправляющего домена.
//
// Ограничения вычисляются ТОЛЬКО по нормализованным ответам провайдера
// (инвариант sender-reputation): метод ApplyNormalizedOutcome принимает
// NormalizedOutcome, который не может нести сбой транспорта или стоп-лист.
// Активные ограничения приостанавливают новые отправки домена.
type SenderReputation struct {
	domain      SenderDomain
	delivered   int
	hardBounces int
	softBounces int
	updatedAt   time.Time
}

// NewSenderReputation создаёт репутацию домена без ограничений.
func NewSenderReputation(domain SenderDomain, at time.Time) (*SenderReputation, error) {
	if err := domain.validate(); err != nil {
		return nil, err
	}
	return &SenderReputation{domain: domain, updatedAt: at}, nil
}

// Domain возвращает отправляющий домен.
func (r *SenderReputation) Domain() SenderDomain { return r.domain }

// DeliveredCount возвращает число принятых сообщений.
func (r *SenderReputation) DeliveredCount() int { return r.delivered }

// HardBounceCount возвращает число постоянных отказов.
func (r *SenderReputation) HardBounceCount() int { return r.hardBounces }

// SoftBounceCount возвращает число временных отказов.
func (r *SenderReputation) SoftBounceCount() int { return r.softBounces }

// UpdatedAt возвращает момент последнего учтённого ответа.
func (r *SenderReputation) UpdatedAt() time.Time { return r.updatedAt }

// ApplyNormalizedOutcome учитывает нормализованный ответ провайдера и
// пересчитывает ограничения домена.
//
// Нулевой исход (не объявленный доменом) отвергается, а не «примеряется» к
// счётчикам: репутация не должна меняться от нераспознанного ответа.
func (r *SenderReputation) ApplyNormalizedOutcome(outcome NormalizedOutcome, at time.Time) error {
	if at.IsZero() {
		return ErrAttemptTimeRequired
	}
	switch outcome.Outcome {
	case OutcomeDelivered:
		r.delivered++
	case OutcomeHardBounce:
		r.hardBounces++
	case OutcomeSoftBounce:
		r.softBounces++
	default:
		return fmt.Errorf("%w: %q", ErrUnknownAttemptOutcome, outcome.Outcome)
	}
	r.updatedAt = at
	return nil
}

// Constraints возвращает активные ограничения домена в устойчивом порядке:
// сначала paused, затем throttled.
func (r *SenderReputation) Constraints() []ReputationConstraint {
	var out []ReputationConstraint
	if r.hardBounces >= hardBouncePauseLimit {
		out = append(out, ConstraintPaused)
	}
	if r.softBounces >= softBounceThrottleLimit {
		out = append(out, ConstraintThrottled)
	}
	return out
}

// Suspended сообщает, приостановлены ли новые отправки домена. Любое активное
// ограничение приостанавливает отправку.
func (r *SenderReputation) Suspended() bool { return len(r.Constraints()) > 0 }

// Snapshot возвращает состояние репутации для сохранения.
func (r *SenderReputation) Snapshot() SenderReputationSnapshot {
	return SenderReputationSnapshot{
		Domain:      r.domain,
		Delivered:   r.delivered,
		HardBounces: r.hardBounces,
		SoftBounces: r.softBounces,
		UpdatedAt:   r.updatedAt,
	}
}

// RehydrateSenderReputation восстанавливает репутацию из сохранённого состояния.
// События не эмитятся: восстановление не является новым фактом домена.
func RehydrateSenderReputation(s SenderReputationSnapshot) (*SenderReputation, error) {
	if err := s.Domain.validate(); err != nil {
		return nil, err
	}
	if s.Delivered < 0 || s.HardBounces < 0 || s.SoftBounces < 0 {
		return nil, fmt.Errorf("%w: отрицательные счётчики репутации домена %s", ErrInvalidSnapshot, s.Domain)
	}
	return &SenderReputation{
		domain:      s.Domain,
		delivered:   s.Delivered,
		hardBounces: s.HardBounces,
		softBounces: s.SoftBounces,
		updatedAt:   s.UpdatedAt,
	}, nil
}
