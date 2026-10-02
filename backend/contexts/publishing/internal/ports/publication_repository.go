package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/publishing/internal/domain"
)

// ErrPublicationNotFound — публикация отсутствует в хранилище. Ошибка порта, а не
// домена: «не найдено» — свойство хранилища.
var ErrPublicationNotFound = errors.New("порты: публикация не найдена")

// ErrVersionConflict — сохранённая версия агрегата не совпала с ожидаемой:
// кто-то изменил публикацию параллельно. Ошибка оптимистичной блокировки.
var ErrVersionConflict = errors.New("порты: конфликт версии публикации")

// PublicationRepository — порт сохранения и загрузки aggregate root publication.
//
// Репозиторий существует только для aggregate root: material и цель входят в
// состав публикации и записываются вместе с ней. FindByIdempotencyKey обслуживает
// инвариант «повтор с тем же ключом не создаёт вторую публикацию».
type PublicationRepository interface {
	// Save сохраняет агрегат целиком с оптимистичной блокировкой.
	//
	// expectedVersion — версия, которую вызывающий прочитал при загрузке. Если
	// сохранённая версия ей не равна, изменение отвергается ошибкой, для которой
	// истинно errors.Is(err, ErrVersionConflict), и состояние не меняется. При
	// создании новой публикации expectedVersion равен нулю.
	Save(ctx context.Context, p *domain.Publication, expectedVersion int) error

	// FindByID загружает публикацию по идентификатору. Если её нет, возвращается
	// ошибка, для которой истинно errors.Is(err, ErrPublicationNotFound).
	FindByID(ctx context.Context, id domain.PublicationID) (*domain.Publication, error)

	// FindByIdempotencyKey загружает публикацию по ключу идемпотентности. Если
	// такой нет, возвращается ошибка, для которой истинно
	// errors.Is(err, ErrPublicationNotFound).
	FindByIdempotencyKey(ctx context.Context, key domain.IdempotencyKey) (*domain.Publication, error)

	// ListByStatus возвращает публикации в заданном статусе, упорядоченные по
	// идентификатору, чтобы выдача была воспроизводимой.
	ListByStatus(ctx context.Context, status domain.PublicationStatus) ([]*domain.Publication, error)
}
