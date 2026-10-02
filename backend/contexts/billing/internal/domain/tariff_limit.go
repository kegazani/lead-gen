package domain

import (
	"fmt"
	"strings"
)

// Feature — возможность тарифа (VO-подобное значение): измерение, по которому
// тарификация ведёт потребление. Непусто и неизменяемо.
type Feature string

// NewFeature нормализует имя возможности (обрезка пробелов). Пустое отвергается.
func NewFeature(name string) (Feature, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", ErrUnknownFeature
	}
	return Feature(trimmed), nil
}

// String возвращает имя возможности.
func (f Feature) String() string { return string(f) }

// IsZero сообщает, что возможность не задана.
func (f Feature) IsZero() bool { return strings.TrimSpace(string(f)) == "" }

// TariffID — идентификатор тарифа (VO-подобное значение).
type TariffID string

// NewTariffID нормализует идентификатор тарифа. Пустой отвергается.
func NewTariffID(id string) (TariffID, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "", fmt.Errorf("%w: tariff_id", ErrEmptyID)
	}
	return TariffID(trimmed), nil
}

// String возвращает идентификатор тарифа.
func (t TariffID) String() string { return string(t) }

// TariffLimit — допустимый объём использования возможности в тарифном периоде (VO
// tariff-limit, architecture/domain-catalog.yaml): возможность, предел и
// потреблённый объём. Неизменяем: изменение потребления возвращает новое значение.
type TariffLimit struct {
	feature Feature
	limit   int64
	used    int64
}

// NewTariffLimit собирает VO лимита. Предел не может быть отрицательным;
// потребление начинается с нуля.
func NewTariffLimit(feature Feature, limit int64) (TariffLimit, error) {
	if feature.IsZero() {
		return TariffLimit{}, ErrUnknownFeature
	}
	if limit < 0 {
		return TariffLimit{}, fmt.Errorf("%w: %d", ErrInvalidLimit, limit)
	}
	return TariffLimit{feature: feature, limit: limit}, nil
}

// Feature возвращает возможность лимита.
func (l TariffLimit) Feature() Feature { return l.feature }

// Limit возвращает допустимый объём.
func (l TariffLimit) Limit() int64 { return l.limit }

// Used возвращает потреблённый объём.
func (l TariffLimit) Used() int64 { return l.used }

// Remaining возвращает остаток лимита (не меньше нуля).
func (l TariffLimit) Remaining() int64 {
	if l.used >= l.limit {
		return 0
	}
	return l.limit - l.used
}

// Exceeds сообщает, что дополнительный объём превысит лимит.
func (l TariffLimit) Exceeds(additional int64) bool {
	if additional < 0 {
		return true
	}
	if l.used >= l.limit {
		return additional > 0
	}
	return additional > l.limit-l.used
}

// IsApproaching сообщает, что остаток лимита не превышает заданной доли от
// предела: повод уведомить клиента о приближении к лимиту (BRD, лимиты
// потребления). Доля вне диапазона (0, 100] отвергается.
func (l TariffLimit) IsApproaching(thresholdPercent int) bool {
	if thresholdPercent <= 0 || thresholdPercent > 100 {
		return false
	}
	if l.limit == 0 {
		return false
	}
	return l.Remaining()*100 <= l.limit*int64(thresholdPercent)
}

// withUsage возвращает копию лимита с увеличенным потреблением.
func (l TariffLimit) withUsage(quantity int64) TariffLimit {
	l.used += quantity
	return l
}

// resetUsage возвращает копию лимита с обнулённым потреблением: новый тарифный
// период начинается с нуля.
func (l TariffLimit) resetUsage() TariffLimit {
	l.used = 0
	return l
}

// Tariff — тариф подписки: идентификатор, цена периода и лимиты по возможностям.
// Неизменяем; набор возможностей уникален.
type Tariff struct {
	id     TariffID
	price  Money
	limits []TariffLimit
}

// NewTariff собирает тариф из идентификатора, цены периода и лимитов. Возможности
// не дублируются; цена задаётся корректной суммой.
func NewTariff(id TariffID, price Money, limits []TariffLimit) (Tariff, error) {
	if strings.TrimSpace(string(id)) == "" {
		return Tariff{}, fmt.Errorf("%w: tariff_id", ErrEmptyID)
	}
	if price.Currency() == "" {
		return Tariff{}, fmt.Errorf("%w: цена тарифа", ErrInvalidCurrency)
	}
	seen := make(map[Feature]struct{}, len(limits))
	copied := make([]TariffLimit, 0, len(limits))
	for _, limit := range limits {
		if limit.Feature().IsZero() {
			return Tariff{}, ErrUnknownFeature
		}
		if _, ok := seen[limit.Feature()]; ok {
			return Tariff{}, fmt.Errorf("%w: %s", ErrDuplicateFeature, limit.Feature())
		}
		seen[limit.Feature()] = struct{}{}
		copied = append(copied, limit)
	}
	return Tariff{id: id, price: price, limits: copied}, nil
}

// ID возвращает идентификатор тарифа.
func (t Tariff) ID() TariffID { return t.id }

// Price возвращает цену периода.
func (t Tariff) Price() Money { return t.price }

// Limits возвращает копию лимитов тарифа.
func (t Tariff) Limits() []TariffLimit {
	return append([]TariffLimit(nil), t.limits...)
}

// Allows сообщает, что возможность входит в тариф.
func (t Tariff) Allows(feature Feature) bool {
	_, ok := t.limit(feature)
	return ok
}

// limit возвращает лимит возможности.
func (t Tariff) limit(feature Feature) (TariffLimit, bool) {
	for _, limit := range t.limits {
		if limit.feature == feature {
			return limit, true
		}
	}
	return TariffLimit{}, false
}

// withLimits возвращает тариф с заменёнными лимитами (используется агрегатом при
// изменении потребления).
func (t Tariff) withLimits(limits []TariffLimit) Tariff {
	t.limits = limits
	return t
}
