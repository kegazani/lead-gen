package contracts

import (
	"fmt"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/contracts/integration-platform/integrationerror"
)

// Реэкспорт опубликованного контракта integration-error.
//
// Категории контракта — константы его модуля; здесь они не переобъявляются,
// чтобы не появилось второе определение набора. Доступны через
// integrationerror.Category*; сверку с доменным VO выполняет ParseErrorCategory.

// Реэкспорт типов integration-error.
type (
	// ErrorCategory — категория нормализованной ошибки.
	ErrorCategory = integrationerror.Category
	// IntegrationError — нормализованная ошибка интеграции.
	IntegrationError = integrationerror.Error
)

// AllCategories возвращает закрытый набор категорий контракта в порядке
// объявления.
func AllCategories() []integrationerror.Category {
	return integrationerror.AllCategories()
}

// ParseErrorCategory сверяет категорию контракта с доменным VO: значение,
// недопустимое для домена, отвергается.
func ParseErrorCategory(category integrationerror.Category) (domain.ErrorCategory, error) {
	parsed, err := domain.ParseErrorCategory(string(category))
	if err != nil {
		return "", fmt.Errorf("contracts: integration-error: category: %w", err)
	}
	return parsed, nil
}
