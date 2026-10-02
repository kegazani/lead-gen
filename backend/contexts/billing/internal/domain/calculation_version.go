package domain

import "strings"

// CalculationVersion — версия правила расчёта вознаграждения за результат (VO
// calculation-version, architecture/domain-catalog.yaml). Непуста и неизменяема:
// расчёт без версии правила невоспроизводим.
type CalculationVersion string

// NewCalculationVersion нормализует версию правила (обрезка пробелов). Пустая
// отвергается.
func NewCalculationVersion(version string) (CalculationVersion, error) {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return "", ErrEmptyCalculationVersion
	}
	return CalculationVersion(trimmed), nil
}

// String возвращает версию правила.
func (v CalculationVersion) String() string { return string(v) }

// IsZero сообщает, что версия не задана.
func (v CalculationVersion) IsZero() bool { return strings.TrimSpace(string(v)) == "" }
