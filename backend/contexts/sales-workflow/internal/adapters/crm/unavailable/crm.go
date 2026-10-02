// Package unavailable — честный недоступный адаптер порта внешней CRM.
//
// Адаптер ничего не выдумывает: каждый вызов отвечает отказом, для которого
// истинно errors.Is(err, ports.ErrCRMUnavailable). Он используется, когда внешняя
// CRM не сконфигурирована: срез честно сообщает о недоступности системы-источника
// сделок, а не притворяется, что сделок нет.
package unavailable

import (
	"context"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
)

// CRM — недоступная внешняя CRM.
type CRM struct{}

// CRM реализует порт внешней CRM.
var _ ports.DealRepository = CRM{}

// New возвращает недоступный адаптер CRM.
func New() CRM { return CRM{} }

// Save отвергает запись: внешняя CRM недоступна.
func (CRM) Save(context.Context, *domain.Deal, int) error {
	return ports.ErrCRMUnavailable
}

// FindByID отвергает чтение: внешняя CRM недоступна. Ошибка отличается от
// «сделка не найдена» — потребитель не должен принять недоступность за отсутствие.
func (CRM) FindByID(context.Context, domain.DealID) (*domain.Deal, error) {
	return nil, ports.ErrCRMUnavailable
}

// ListByStage отвергает выборку: внешняя CRM недоступна.
func (CRM) ListByStage(context.Context, domain.DealStage) ([]*domain.Deal, error) {
	return nil, ports.ErrCRMUnavailable
}
