// Package policy реализует ports.SuppressionList поверх контракта
// suppression-policy (lead-gen/contracts/data-governance/suppressionpolicy):
// проверку выполняет владелец data-governance, а контекст message-delivery
// держит только форму порта.
//
// Недоступность источника возвращает ошибку, а не «не в стоп-листе»: молча
// считать получателя свободным при неуверенности запрещено — отправка
// необратима.
package policy

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
	"lead-gen/contracts/data-governance/suppressionpolicy"
)

// List — реализация ports.SuppressionList поверх контракта suppression-policy.
type List struct {
	port suppressionpolicy.Port
}

// List реализует порт стоп-листа.
var _ ports.SuppressionList = (*List)(nil)

// New собирает адаптер. Отсутствие порта — ошибка конфигурации.
func New(port suppressionpolicy.Port) (*List, error) {
	if port == nil {
		return nil, errors.New("policy: порт suppression-policy обязателен")
	}
	return &List{port: port}, nil
}

// IsSuppressed реализует ports.SuppressionList. Запрос привязан к тенанту;
// недоступность владельца возвращается как ошибка.
func (l *List) IsSuppressed(ctx context.Context, tenant domain.TenantID, address domain.DeliveryAddress) (bool, error) {
	decision, err := l.port.Check(ctx, suppressionpolicy.SuppressionQuery{
		TenantID: tenant.String(),
		Contact:  address.String(),
	})
	if err != nil {
		return false, fmt.Errorf("suppression-policy: проверка %s: %w", address, err)
	}
	return decision.Suppressed, nil
}
