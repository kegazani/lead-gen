package reference

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/message-delivery/internal/ports"
	"lead-gen/contracts/lead-management/deliverytarget"
)

// RecipientResolver реализует ports.RecipientResolver поверх контракта
// delivery-target владельца lead-management. Состояния не хранит, безопасен для
// конкурентного использования.
type RecipientResolver struct {
	impl deliverytarget.Resolver
}

// RecipientResolver реализует порт разрешения получателя.
var _ ports.RecipientResolver = (*RecipientResolver)(nil)

// NewRecipientResolver связывает реализацию владельца с портом контекста.
// Отсутствие реализации — ошибка конфигурации, а не молчаливая недоступность на
// пути доставки.
func NewRecipientResolver(impl deliverytarget.Resolver) (*RecipientResolver, error) {
	if impl == nil {
		return nil, errors.New("reference: delivery-target: не задан резолвер владельца")
	}
	return &RecipientResolver{impl: impl}, nil
}

// ResolveRecipient реализует порт: разрешает ссылку получателя в проверенный
// адрес назначения. Исход контракта переводится в исход порта (см.
// mapRecipientError); пустой адрес без ошибки означает неразрешимую ссылку и
// отвергается вызывающим, а не подменяется.
func (r *RecipientResolver) ResolveRecipient(ctx context.Context, tenantID, recipientRef string) (string, error) {
	if r == nil || r.impl == nil {
		return "", fmt.Errorf("%w: адаптер delivery-target не собран", ports.ErrResolverUnavailable)
	}
	recipient, err := r.impl.ResolveRecipient(ctx, tenantID, recipientRef)
	if err != nil {
		return "", mapRecipientError(err)
	}
	return recipient.Address, nil
}

// mapRecipientError переводит ошибку контракта delivery-target в исход порта.
// Отмена и дедлайн пробрасываются как есть (их различает вызывающий); доменный
// отказ владельца — неразрешимая ссылка (ports.ErrReferenceUnresolved); всё
// прочее, включая явную недоступность, — недоступность резолвера
// (ports.ErrResolverUnavailable). Неизвестная ошибка не выдаётся за разрешённый
// адрес.
func mapRecipientError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, deliverytarget.ErrRejected):
		return fmt.Errorf("%w: %v", ports.ErrReferenceUnresolved, err)
	default:
		return fmt.Errorf("%w: %v", ports.ErrResolverUnavailable, err)
	}
}
