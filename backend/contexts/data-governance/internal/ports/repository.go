package ports

import (
	"context"
	"errors"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
)

// ErrBasisNotFound — основание обработки отсутствует в хранилище. Ошибка порта, а
// не домена: «не найдено» — свойство хранилища.
var ErrBasisNotFound = errors.New("порты: основание обработки не найдено")

// ErrVersionConflict — сохранённая версия агрегата не совпала с ожидаемой:
// кто-то изменил основание параллельно. Ошибка оптимистичной блокировки.
var ErrVersionConflict = errors.New("порты: конфликт версии основания обработки")

// ProcessingBasisRepository — порт сохранения и загрузки aggregate root
// processing-basis.
//
// Репозиторий существует только для aggregate root: entity suppression-entry
// входит в состав основания и записывается вместе с ним. Портовый метод не знает
// о драйвере БД и схеме таблиц.
type ProcessingBasisRepository interface {
	// Save сохраняет агрегат целиком с оптимистичной блокировкой.
	//
	// expectedVersion — версия, которую вызывающий прочитал при загрузке. Если
	// сохранённая версия ей не равна, изменение отвергается ошибкой, для которой
	// истинно errors.Is(err, ErrVersionConflict), и состояние не меняется. При
	// создании нового основания expectedVersion равен нулю.
	Save(ctx context.Context, basis *domain.ProcessingBasis, expectedVersion int) error

	// FindByID загружает основание по идентификатору. Если основания нет,
	// возвращается ошибка, для которой истинно errors.Is(err, ErrBasisNotFound);
	// загруженный агрегат в этом случае равен nil.
	FindByID(ctx context.Context, id domain.BasisID) (*domain.ProcessingBasis, error)

	// FindByTenant возвращает все основания тенанта, упорядоченные по
	// идентификатору. Метод обслуживает проверку подавления по всем основаниям
	// тенанта и чтение его состояния.
	FindByTenant(ctx context.Context, tenant domain.TenantID) ([]*domain.ProcessingBasis, error)

	// ListExpired возвращает действующие основания, окно действия которых истекло
	// к моменту at. Метод обслуживает фоновое истечение полномочий роли worker.
	ListExpired(ctx context.Context, at time.Time) ([]*domain.ProcessingBasis, error)
}
