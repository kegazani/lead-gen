package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/billing/internal/domain"
)

// ErrSuccessFeeNotFound — вознаграждение отсутствует в хранилище.
var ErrSuccessFeeNotFound = errors.New("порты: вознаграждение не найдено")

// SuccessFeeRepository — порт сохранения и загрузки aggregate root success-fee.
type SuccessFeeRepository interface {
	// Save сохраняет агрегат целиком с оптимистичной блокировкой. expectedVersion
	// равен нулю при создании.
	Save(ctx context.Context, fee *domain.SuccessFee, expectedVersion int) error

	// FindByID загружает вознаграждение по идентификатору. Отсутствие даёт ошибку,
	// для которой истинно errors.Is(err, ErrSuccessFeeNotFound).
	FindByID(ctx context.Context, id domain.SuccessFeeID) (*domain.SuccessFee, error)

	// FindByResultRef находит вознаграждение тенанта по ссылке на подтверждённый
	// результат. Отсутствие — ErrSuccessFeeNotFound. Обслуживает идемпотентность
	// расчёта: повторный результат не создаёт второго начисления.
	FindByResultRef(ctx context.Context, tenantID domain.TenantID, ref domain.ResultRef) (*domain.SuccessFee, error)

	// ListByTenant возвращает вознаграждения тенанта, упорядоченные по
	// идентификатору.
	ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.SuccessFee, error)
}
