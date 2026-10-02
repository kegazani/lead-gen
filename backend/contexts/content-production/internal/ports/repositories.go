package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/content-production/internal/domain"
)

// Ошибки портов хранилища.
//
// «Не найдено» — свойство хранилища, а не домена, поэтому ошибки объявлены здесь;
// вызывающий различает их через errors.Is.
var (
	// ErrContentPackageNotFound — контент-пакет отсутствует в хранилище.
	ErrContentPackageNotFound = errors.New("порты: контент-пакет не найден")

	// ErrRenderJobNotFound — задание рендеринга отсутствует в хранилище.
	ErrRenderJobNotFound = errors.New("порты: задание рендеринга не найдено")

	// ErrVersionConflict — сохранённая версия агрегата не совпала с ожидаемой:
	// кто-то изменил агрегат параллельно. Ошибка оптимистичной блокировки.
	ErrVersionConflict = errors.New("порты: конфликт версии агрегата")
)

// ContentPackageRepository — порт сохранения и загрузки aggregate root
// content-package.
//
// Репозиторий существует только для aggregate root: сущности content-version,
// landing-content и document входят в состав пакета и записываются вместе с ним.
// Портовый метод не знает о драйвере БД и схеме таблиц.
type ContentPackageRepository interface {
	// Save сохраняет агрегат целиком с оптимистичной блокировкой.
	//
	// expectedVersion — версия, которую вызывающий прочитал при загрузке. Если
	// сохранённая версия ей не равна, изменение отвергается ошибкой, для которой
	// истинно errors.Is(err, ErrVersionConflict), и состояние не меняется. При
	// создании нового пакета expectedVersion равен нулю.
	Save(ctx context.Context, pkg *domain.ContentPackage, expectedVersion int) error

	// FindByID загружает пакет по идентификатору. Если пакета нет, возвращается
	// ошибка, для которой истинно errors.Is(err, ErrContentPackageNotFound);
	// загруженный агрегат в этом случае равен nil.
	FindByID(ctx context.Context, id domain.ContentAssetID) (*domain.ContentPackage, error)
}

// RenderJobRepository — порт сохранения и загрузки aggregate root render-job.
type RenderJobRepository interface {
	// Save сохраняет агрегат целиком с оптимистичной блокировкой.
	Save(ctx context.Context, job *domain.RenderJob, expectedVersion int) error

	// FindByID загружает задание по идентификатору. Если задания нет, возвращается
	// ошибка, для которой истинно errors.Is(err, ErrRenderJobNotFound).
	FindByID(ctx context.Context, id domain.RenderJobID) (*domain.RenderJob, error)

	// ListPending возвращает задания, ожидающие исполнения (requested или
	// rendering), в порядке идентификатора, чтобы выдача была воспроизводимой.
	// Метод обслуживает воркер рендеринга, а не доменное правило. limit больше
	// нуля ограничивает размер порции.
	ListPending(ctx context.Context, limit int) ([]*domain.RenderJob, error)
}
