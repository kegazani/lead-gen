package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// ErrRequestNotFound — заявка отсутствует в хранилище. Ошибка порта, а не
// домена: «не найдено» — свойство хранилища.
var ErrRequestNotFound = errors.New("порты: заявка не найдена")

// ErrVersionConflict — сохранённая версия агрегата не совпала с ожидаемой:
// кто-то изменил заявку параллельно. Ошибка оптимистичной блокировки.
var ErrVersionConflict = errors.New("порты: конфликт версии заявки")

// RequestRepository — порт сохранения и загрузки aggregate root request.
//
// Репозиторий существует только для aggregate root: entity offer входит в состав
// заявки и записывается вместе с ней. Портовый метод не знает о драйвере БД и
// схеме таблиц.
type RequestRepository interface {
	// Save сохраняет агрегат целиком с оптимистичной блокировкой.
	//
	// expectedVersion — версия, которую вызывающий прочитал при загрузке. Если
	// сохранённая версия ей не равна, изменение отвергается ошибкой, для которой
	// истинно errors.Is(err, ErrVersionConflict), и состояние не меняется. При
	// создании новой заявки expectedVersion равен нулю.
	Save(ctx context.Context, request *domain.Request, expectedVersion int) error

	// FindByID загружает заявку по идентификатору. Если заявки нет, возвращается
	// ошибка, для которой истинно errors.Is(err, ErrRequestNotFound); загруженный
	// агрегат в этом случае равен nil.
	FindByID(ctx context.Context, id domain.RequestID) (*domain.Request, error)

	// ListByStatus возвращает заявки в заданном статусе (например, очередь
	// валидации — submitted). Порядок — по идентификатору, чтобы выдача была
	// воспроизводимой. Метод обслуживает чтение экрана, а не доменное правило.
	ListByStatus(ctx context.Context, status domain.RequestStatus) ([]*domain.Request, error)

	// ListByTenant возвращает все заявки тенанта в порядке по идентификатору.
	// Обслуживает историю заявок клиента (портал) и список заявок компании
	// (админка).
	ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.Request, error)

	// FindByCampaign загружает заявку по идентификатору кампании в границах
	// тенанта (capability request-delivery-conclusion): событие доставки
	// сопоставляется с заявкой по её кампании. Если заявки нет, возвращается
	// ошибка, для которой истинно errors.Is(err, ErrRequestNotFound).
	FindByCampaign(ctx context.Context, tenantID domain.TenantID, campaignID domain.CampaignID) (*domain.Request, error)

	// CountByTenant возвращает число заявок тенанта в нетерминальных статусах
	// (submitted, validating, validated, processing, needs-revision). Обслуживает
	// лимит одновременных заявок клиента.
	CountByTenant(ctx context.Context, tenantID domain.TenantID) (int, error)

	// CountInWork возвращает число заявок тенанта в статусе processing.
	// Обслуживает лимит «не более одной заявки в работе».
	CountInWork(ctx context.Context, tenantID domain.TenantID) (int, error)
}
