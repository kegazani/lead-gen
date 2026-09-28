// Package integrationerror — опубликованный (published language) контракт
// integration-error контекста integration-platform.
//
// Контракт описывает нормализованную ошибку внешней интеграции: категорию из
// закрытого набора, имя адаптера и имя контракта, к которым относится ошибка, и
// диагностическую причину. Ошибку формирует владелец порта (политика
// error-normalization) и передаёт потребителю вместо ошибки SDK поставщика:
// потребитель не импортирует SDK и различает только категории контракта
// (design.md, Решение 3).
//
// # Секреты
//
// Полей секретов здесь нет — контракт их не переносит. Диагностическая причина
// (Reason) предназначена только для журналов и по инварианту о секретах не
// попадает в доменные события; вызывающий обязан не помещать в неё API-ключи,
// токены и адреса.
//
// Зависимости — исключительно стандартная библиотека.
package integrationerror

import (
	"fmt"
	"strings"
)

// Category — категория нормализованной ошибки. Набор закрыт: значение вне
// перечня ниже не проходит валидацию.
type Category string

// Категории контракта integration-error. Значения совпадают с набором §3.4
// изменения migrate-platform-to-go и с решением error-normalization домена
// владельца.
const (
	// CategoryTemporarilyUnavailable — внешняя сторона временно недоступна.
	CategoryTemporarilyUnavailable Category = "temporarily-unavailable"
	// CategoryRateLimited — превышен лимит частоты обращений.
	CategoryRateLimited Category = "rate-limited"
	// CategoryAuthorizationDenied — отказ авторизации (недействительные данные
	// доступа).
	CategoryAuthorizationDenied Category = "authorization-denied"
	// CategoryInvalidRequest — запрос отвергнут как некорректный.
	CategoryInvalidRequest Category = "invalid-request"
	// CategoryConflict — конфликт состояния (например, повтор с иным телом).
	CategoryConflict Category = "conflict"
	// CategoryPermanentFailure — окончательный отказ, повтор бессмыслен.
	CategoryPermanentFailure Category = "permanent-failure"
)

// AllCategories — полный набор категорий в порядке объявления. Возвращённый
// срез — копия: вызывающий не может испортить закрытый набор.
func AllCategories() []Category {
	return []Category{
		CategoryTemporarilyUnavailable,
		CategoryRateLimited,
		CategoryAuthorizationDenied,
		CategoryInvalidRequest,
		CategoryConflict,
		CategoryPermanentFailure,
	}
}

var allowedCategories = func() map[Category]struct{} {
	set := make(map[Category]struct{}, len(AllCategories()))
	for _, category := range AllCategories() {
		set[category] = struct{}{}
	}
	return set
}()

// IsValidCategory сообщает, входит ли категория в закрытый набор.
func IsValidCategory(category Category) bool {
	_, ok := allowedCategories[category]
	return ok
}

// ParseCategory разбирает имя категории: регистр и окружающие пробелы не
// значимы, неизвестное имя отвергается. Возвращается каноническое (нижний
// регистр) значение.
func ParseCategory(name string) (Category, error) {
	normalized := Category(strings.ToLower(strings.TrimSpace(name)))
	if !IsValidCategory(normalized) {
		return "", fmt.Errorf("integrationerror: неизвестная категория %q", name)
	}
	return normalized, nil
}

// Error — нормализованная ошибка интеграции. Реализует стандартный интерфейс
// error, поэтому её можно передавать и оборачивать как обычную ошибку Go, не
// протекая типом SDK.
type Error struct {
	// Category — категория из закрытого набора. Обязательна.
	Category Category `json:"category"`
	// AdapterName — имя адаптера, чей вызов завершился ошибкой. Обязательно.
	AdapterName string `json:"adapter_name"`
	// ContractName — имя контракта (например, provider-port), к которому
	// относится вызов. Обязательно.
	ContractName string `json:"contract_name"`
	// Reason — диагностическая причина для журналов. Необязательна. Секретов
	// содержать не должна.
	Reason string `json:"reason,omitempty"`
}

// Утверждение, что контракт можно использовать как обычную ошибку Go.
var _ error = Error{}

// Validate проверяет обязательные поля и собирает все некорректные или
// отсутствующие одним сообщением, а не возвращает ошибку по первому.
func (e Error) Validate() error {
	var invalid []string
	if !IsValidCategory(e.Category) {
		invalid = append(invalid, "category")
	}
	if strings.TrimSpace(e.AdapterName) == "" {
		invalid = append(invalid, "adapter_name")
	}
	if strings.TrimSpace(e.ContractName) == "" {
		invalid = append(invalid, "contract_name")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("integrationerror: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Error реализует интерфейс error. Категория, адаптер и контракт дают
// машиночитаемую часть, Reason добавляется как человекочитаемая причина.
func (e Error) Error() string {
	if reason := strings.TrimSpace(e.Reason); reason != "" {
		return fmt.Sprintf("integrationerror: %s: адаптер %q, контракт %q: %s", e.Category, e.AdapterName, e.ContractName, reason)
	}
	return fmt.Sprintf("integrationerror: %s: адаптер %q, контракт %q", e.Category, e.AdapterName, e.ContractName)
}
