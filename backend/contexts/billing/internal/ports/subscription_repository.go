package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/billing/internal/domain"
)

// ErrSubscriptionNotFound — подписка отсутствует в хранилище. Ошибка порта, а не
// домена: «не найдено» — свойство хранилища.
var ErrSubscriptionNotFound = errors.New("порты: подписка не найдена")

// ErrVersionConflict — сохранённая версия агрегата не совпала с ожидаемой:
// параллельное изменение. Ошибка оптимистичной блокировки.
var ErrVersionConflict = errors.New("порты: конфликт версии агрегата")

// SubscriptionRepository — порт сохранения и загрузки aggregate root
// subscription-invoice. Репозиторий существует только для aggregate root:
// сущности, входящие в его состав, записываются вместе с ним.
type SubscriptionRepository interface {
	// Save сохраняет агрегат целиком с оптимистичной блокировкой.
	//
	// expectedVersion — версия, прочитанная при загрузке. Если сохранённая версия
	// ей не равна, изменение отвергается ошибкой, для которой истинно
	// errors.Is(err, ErrVersionConflict), и состояние не меняется. При создании
	// новой подписки expectedVersion равен нулю.
	Save(ctx context.Context, subscription *domain.SubscriptionInvoice, expectedVersion int) error

	// FindByID загружает подписку по идентификатору. Отсутствие даёт ошибку, для
	// которой истинно errors.Is(err, ErrSubscriptionNotFound).
	FindByID(ctx context.Context, id domain.SubscriptionID) (*domain.SubscriptionInvoice, error)

	// FindByTenant загружает подписку тенанта. Если у тенанта несколько подписок,
	// возвращается с наименьшим идентификатором (воспроизводимо).
	FindByTenant(ctx context.Context, tenantID domain.TenantID) (*domain.SubscriptionInvoice, error)

	// List возвращает все подписки, упорядоченные по идентификатору. Обслуживает
	// фоновый расчётный контур роли worker.
	List(ctx context.Context) ([]*domain.SubscriptionInvoice, error)
}
