package domain

import (
	"fmt"
	"time"
)

// ParticipationLimits — ограничения участия лида в цепочках. Это конфигурация
// домена, а не правило одного сценария: предел касаний считается суммарно по
// всем цепочкам одного лида, а объём черновиков — по всем активным цепочкам.
type ParticipationLimits struct {
	// MaxTouchesPerLead — предел числа касаний по одному лиду за период.
	MaxTouchesPerLead int

	// TouchPeriod — период, на котором считается предел касаний.
	TouchPeriod time.Duration

	// MaxPendingDrafts — предел суммарного объёма неутверждённых черновиков по
	// всем активным цепочкам.
	MaxPendingDrafts int
}

// DefaultParticipationLimits возвращает ограничения по умолчанию. Значения —
// конфигурация среза: требования задают наличие пределов, не их значения
// (design.md, «Open Questions»).
func DefaultParticipationLimits() ParticipationLimits {
	return ParticipationLimits{
		MaxTouchesPerLead: 3,
		TouchPeriod:       30 * 24 * time.Hour,
		MaxPendingDrafts:  100,
	}
}

// Validate проверяет ограничения. Нулевые или отрицательные значения
// отвергаются: предел без значения означал бы отсутствие контроля.
func (l ParticipationLimits) Validate() error {
	if l.MaxTouchesPerLead <= 0 {
		return fmt.Errorf("%w: предел касаний по лиду %d", ErrInvalidParticipationLimits, l.MaxTouchesPerLead)
	}
	if l.TouchPeriod <= 0 {
		return fmt.Errorf("%w: период касаний %s", ErrInvalidParticipationLimits, l.TouchPeriod)
	}
	if l.MaxPendingDrafts <= 0 {
		return fmt.Errorf("%w: предел объёма черновиков %d", ErrInvalidParticipationLimits, l.MaxPendingDrafts)
	}
	return nil
}

// IsZero сообщает, что ограничения не заданы.
func (l ParticipationLimits) IsZero() bool {
	return l.MaxTouchesPerLead == 0 && l.TouchPeriod == 0 && l.MaxPendingDrafts == 0
}
