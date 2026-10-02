package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/sales-workflow/internal/domain"
)

// ErrCRMSyncNotFound — запись синхронизации для сделки отсутствует.
var ErrCRMSyncNotFound = errors.New("порты: синхронизация не найдена")

// CRMSyncRepository — порт сохранения и загрузки aggregate root crm-sync.
//
// Синхронизация принадлежит контексту (зона green, storage relational) и
// хранится локально. Репозиторий существует только для aggregate root: события
// синхронизации записываются вместе с ней.
type CRMSyncRepository interface {
	// Save сохраняет запись синхронизации (upsert по идентификатору).
	Save(ctx context.Context, sync *domain.CRMSync) error

	// FindByDeal загружает синхронизацию по сделке. Если записи нет,
	// возвращается ошибка, для которой истинно errors.Is(err, ErrCRMSyncNotFound).
	FindByDeal(ctx context.Context, dealID domain.DealID) (*domain.CRMSync, error)
}
