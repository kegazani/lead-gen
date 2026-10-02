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
}
