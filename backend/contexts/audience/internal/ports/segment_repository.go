package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/audience/internal/domain"
)

// ErrSegmentNotFound — сегмент отсутствует в хранилище. Ошибка порта, а не
// домена: «не найдено» — свойство хранилища.
var ErrSegmentNotFound = errors.New("порты: сегмент не найден")

// ErrVersionConflict — сохранённая версия агрегата не совпала с ожидаемой:
// кто-то изменил сегмент параллельно. Ошибка оптимистичной блокировки.
var ErrVersionConflict = errors.New("порты: конфликт версии сегмента")

// SegmentRepository — порт сохранения и загрузки aggregate root segment.
//
// Репозиторий существует только для aggregate root: сущности lead-score входят в
// состав сегмента и записываются вместе с ним. Портовый метод не знает о драйвере
// БД и схеме таблиц.
type SegmentRepository interface {
	// Save сохраняет агрегат целиком с оптимистичной блокировкой.
	//
	// expectedVersion — версия, прочитанная вызывающим при загрузке. Если
	// сохранённая версия ей не равна, изменение отвергается ошибкой, для которой
	// истинно errors.Is(err, ErrVersionConflict), и состояние не меняется. При
	// создании нового сегмента expectedVersion равен нулю.
	Save(ctx context.Context, segment *domain.Segment, expectedVersion int) error

	// FindByID загружает сегмент по идентификатору. Если сегмента нет, возвращается
	// ошибка, для которой истинно errors.Is(err, ErrSegmentNotFound).
	FindByID(ctx context.Context, id domain.SegmentID) (*domain.Segment, error)

	// ListByTenant возвращает сегменты тенанта, упорядоченные по идентификатору.
	ListByTenant(ctx context.Context, tenant domain.TenantID) ([]*domain.Segment, error)

	// List возвращает все сегменты, упорядоченные по идентификатору. Обслуживает
	// регулярный пересчёт роли schedule, охватывающий все тенанты.
	List(ctx context.Context) ([]*domain.Segment, error)
}
