package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
)

// ErrPipelineNotFound — версия пайплайна отсутствует в хранилище.
var ErrPipelineNotFound = errors.New("порты: версия пайплайна не найдена")

// ErrVersionConflict — сохранённая ревизия агрегата не совпала с ожидаемой:
// кто-то изменил агрегат параллельно. Ошибка оптимистичной блокировки.
var ErrVersionConflict = errors.New("порты: конфликт ревизии агрегата")

// PipelineRepository — порт сохранения и загрузки aggregate root agent-pipeline.
//
// Хранятся только версии пайплайна. Черновик (неопубликованная версия) также
// может быть сохранён, но выборки отдают опубликованные версии: запуск
// привязывается только к опубликованной версии.
type PipelineRepository interface {
	// Save сохраняет версию пайплайна с оптимистичной блокировкой по ревизии.
	// При создании expectedRevision равен нулю.
	Save(ctx context.Context, pipeline *domain.AgentPipeline, expectedRevision int) error

	// FindByID загружает версию пайплайна тенанта. Отсутствие даёт ошибку, для
	// которой истинно errors.Is(err, ErrPipelineNotFound).
	FindByID(ctx context.Context, id domain.PipelineID, version int, tenant domain.TenantID) (*domain.AgentPipeline, error)

	// LatestPublished возвращает номер последней опубликованной версии пайплайна
	// тенанта. Если опубликованных версий нет — ErrPipelineNotFound.
	LatestPublished(ctx context.Context, id domain.PipelineID, tenant domain.TenantID) (int, error)
}
