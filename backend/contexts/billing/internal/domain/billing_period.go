package domain

import (
	"fmt"
	"time"
)

// BillingPeriod — интервал начисления подписки и потребления лимитов (VO
// billing-period, architecture/domain-catalog.yaml). Неизменяем; полуинтервал
// [start, end).
type BillingPeriod struct {
	start time.Time
	end   time.Time
}

// NewBillingPeriod собирает VO из начала и конца периода (UTC). Обе границы
// обязательны, начало строго раньше конца.
func NewBillingPeriod(start, end time.Time) (BillingPeriod, error) {
	if start.IsZero() || end.IsZero() {
		return BillingPeriod{}, fmt.Errorf("%w: границы периода не заданы", ErrInvalidPeriod)
	}
	start = start.UTC()
	end = end.UTC()
	if !end.After(start) {
		return BillingPeriod{}, fmt.Errorf("%w: конец %s не позже начала %s", ErrInvalidPeriod, end, start)
	}
	return BillingPeriod{start: start, end: end}, nil
}

// Start возвращает начало периода.
func (p BillingPeriod) Start() time.Time { return p.start }

// End возвращает конец периода.
func (p BillingPeriod) End() time.Time { return p.end }

// Duration возвращает длительность периода.
func (p BillingPeriod) Duration() time.Duration { return p.end.Sub(p.start) }

// IsZero сообщает, что период не задан.
func (p BillingPeriod) IsZero() bool { return p.start.IsZero() && p.end.IsZero() }

// Contains сообщает, что момент входит в период [start, end).
func (p BillingPeriod) Contains(at time.Time) bool {
	if p.IsZero() {
		return false
	}
	t := at.UTC()
	return !t.Before(p.start) && t.Before(p.end)
}

// Key возвращает канонический ключ периода. Ключ участвует в ключе
// идемпотентности записи потребления и счёта, поэтому детерминирован и не зависит
// от локали.
func (p BillingPeriod) Key() string {
	return fmt.Sprintf("%s_%s", p.start.Format(time.RFC3339), p.end.Format(time.RFC3339))
}

// Equal сообщает равенство периодов по границам.
func (p BillingPeriod) Equal(other BillingPeriod) bool {
	return p.start.Equal(other.start) && p.end.Equal(other.end)
}

// String возвращает ключ периода.
func (p BillingPeriod) String() string { return p.Key() }
