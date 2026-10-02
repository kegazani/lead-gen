package domain

import (
	"fmt"
	"strings"
	"time"
)

// UsageEntry — запись потребления возможности тарифа за период (entity внутри
// aggregate root subscription-invoice).
//
// Инвариант: запись идемпотентна по ключу периода — пара (periodKey, usageKey)
// уникальна в подписке, поэтому повторная доставка той же записи не расходует
// лимит дважды.
type UsageEntry struct {
	id        UsageID
	feature   Feature
	quantity  int64
	periodKey string
	usageKey  string
	at        time.Time
}

// UsageInput — вход фиксации потребления.
type UsageInput struct {
	UsageID  UsageID
	Feature  Feature
	Quantity int64
	UsageKey string
	At       time.Time
}

// validate проверяет вход фиксации потребления.
func (in UsageInput) validate() error {
	if err := in.UsageID.validate(); err != nil {
		return err
	}
	if in.Feature.IsZero() {
		return ErrUnknownFeature
	}
	if in.Quantity <= 0 {
		return fmt.Errorf("%w: %d", ErrQuantityRequired, in.Quantity)
	}
	if strings.TrimSpace(in.UsageKey) == "" {
		return ErrUsageKeyRequired
	}
	if in.At.IsZero() {
		return ErrMissingTime
	}
	return nil
}

// ID возвращает идентификатор записи.
func (u UsageEntry) ID() UsageID { return u.id }

// Feature возвращает возможность потребления.
func (u UsageEntry) Feature() Feature { return u.feature }

// Quantity возвращает объём потребления.
func (u UsageEntry) Quantity() int64 { return u.quantity }

// PeriodKey возвращает ключ периода записи.
func (u UsageEntry) PeriodKey() string { return u.periodKey }

// UsageKey возвращает ключ записи внутри периода.
func (u UsageEntry) UsageKey() string { return u.usageKey }

// At возвращает время фиксации.
func (u UsageEntry) At() time.Time { return u.at }

// IdempotencyKey возвращает ключ идемпотентности записи: период и ключ записи.
func (u UsageEntry) IdempotencyKey() string {
	return usageIdempotencyKey(u.periodKey, u.usageKey)
}

// usageIdempotencyKey канонизирует ключ идемпотентности записи потребления.
func usageIdempotencyKey(periodKey, usageKey string) string {
	return periodKey + "\x1f" + usageKey
}

// UsageResult — результат фиксации потребления.
type UsageResult struct {
	// Duplicate — запись распознана как повторная; состояние не менялось.
	Duplicate bool
	// Feature — возможность потребления.
	Feature Feature
	// Quantity — учтённый объём.
	Quantity int64
	// Limit — предел возможности в периоде.
	Limit int64
	// Remaining — остаток лимита после учёта.
	Remaining int64
}
