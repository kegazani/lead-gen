package domain

import (
	"fmt"
	"time"
)

// DigestPeriod — value object «интервал сообщений, включённых в одну выжимку»
// (architecture/domain-catalog.yaml, value_objects: digest-period).
//
// Границы полуинтервала [from, to). Период неизменяем: правка периода создаёт
// новую выжимку, а не переписывает прошлую (spec, «Источник удалён»).
type DigestPeriod struct {
	from time.Time
	to   time.Time
}

// NewDigestPeriod создаёт период. Обе границы обязательны, конец не раньше
// начала.
func NewDigestPeriod(from, to time.Time) (DigestPeriod, error) {
	if from.IsZero() || to.IsZero() {
		return DigestPeriod{}, fmt.Errorf("%w: границы периода обязательны", ErrInvalidPeriod)
	}
	if to.Before(from) {
		return DigestPeriod{}, fmt.Errorf("%w: конец %s раньше начала %s", ErrInvalidPeriod, to, from)
	}
	return DigestPeriod{from: from, to: to}, nil
}

// From возвращает начало периода.
func (p DigestPeriod) From() time.Time { return p.from }

// To возвращает конец периода.
func (p DigestPeriod) To() time.Time { return p.to }

// Contains сообщает, попадает ли момент в полуинтервал [from, to).
func (p DigestPeriod) Contains(t time.Time) bool {
	return !t.Before(p.from) && t.Before(p.to)
}

// Snapshot возвращает состояние периода для сохранения.
func (p DigestPeriod) Snapshot() DigestPeriodSnapshot {
	return DigestPeriodSnapshot{From: p.from, To: p.to}
}

// DigestPeriodSnapshot — сохранённое состояние периода.
type DigestPeriodSnapshot struct {
	From time.Time
	To   time.Time
}

// RehydrateDigestPeriod восстанавливает период из снимка, повторно проверяя
// инварианты.
func RehydrateDigestPeriod(s DigestPeriodSnapshot) (DigestPeriod, error) {
	period, err := NewDigestPeriod(s.From, s.To)
	if err != nil {
		return DigestPeriod{}, fmt.Errorf("%w: период: %v", ErrInvalidSnapshot, err)
	}
	return period, nil
}
