package command

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// PipelineStepInput — вход описания шага публикуемой версии.
type PipelineStepInput struct {
	// ID — идентификатор шага. Обязателен и уникален.
	ID string
	// BlockRef — ссылка на зарегистрированный блок.
	BlockRef string
	// Params — параметры блока.
	Params map[string]string
	// MaxAttempts — предел попыток шага; 0 — значение по умолчанию.
	MaxAttempts int
}

// PublishPipeline — вход публикации версии пайплайна.
type PublishPipeline struct {
	PipelineID string
	TenantID   string
	// Version — номер версии. 0 означает «следующая после последней
	// опубликованной».
	Version       int
	Steps         []PipelineStepInput
	Actor         string
	CorrelationID string
	CausationID   string
}

// PublishPipelineResult — наблюдаемый исход публикации.
type PublishPipelineResult struct {
	PipelineID string
	Version    int
}

// PublishPipelineHandler публикует версию пайплайна: состав шагов проверяется
// против реестра блоков, неизвестный блок отвергается, а событие публикации
// пишется в outbox вместе с агрегатом.
type PublishPipelineHandler struct {
	uow       ports.UnitOfWork
	blocks    *domain.BlockRegistry
	pipelines ports.PipelineRepository
	clock     ports.Clock
	ids       ports.IDGenerator
}

// NewPublishPipelineHandler собирает сценарий публикации пайплайна.
func NewPublishPipelineHandler(uow ports.UnitOfWork, blocks *domain.BlockRegistry, pipelines ports.PipelineRepository, clock ports.Clock, ids ports.IDGenerator) *PublishPipelineHandler {
	return &PublishPipelineHandler{uow: uow, blocks: blocks, pipelines: pipelines, clock: clock, ids: ids}
}

// Handle собирает, проверяет и публикует версию пайплайна.
func (h *PublishPipelineHandler) Handle(ctx context.Context, in PublishPipeline) (PublishPipelineResult, error) {
	version := in.Version
	if version <= 0 {
		latest, err := h.pipelines.LatestPublished(ctx, domain.PipelineID(in.PipelineID), domain.TenantID(in.TenantID))
		switch {
		case err == nil:
			version = latest + 1
		case errors.Is(err, ports.ErrPipelineNotFound):
			version = 1
		default:
			return PublishPipelineResult{}, err
		}
	}
	specs := make([]domain.StepSpec, 0, len(in.Steps))
	for _, step := range in.Steps {
		config, err := domain.NewStepConfig(step.Params, step.MaxAttempts)
		if err != nil {
			return PublishPipelineResult{}, err
		}
		specs = append(specs, domain.StepSpec{
			ID:     domain.PipelineStepID(step.ID),
			Ref:    domain.BlockRef(step.BlockRef),
			Config: config,
		})
	}
	pipeline, err := domain.NewPipeline(domain.PipelineID(in.PipelineID), domain.TenantID(in.TenantID), version, specs, h.blocks)
	if err != nil {
		return PublishPipelineResult{}, err
	}
	if err := pipeline.Publish(h.clock.Now(), header(h.ids, in.CorrelationID, in.CausationID)); err != nil {
		return PublishPipelineResult{}, err
	}
	err = h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		// Публикация создаёт новую версию: сохранённой версии ещё нет, ожидаемая
		// ревизия — нулевая. Повторная публикация уже выпущенной версии отвергается
		// доменом (неизменяемость).
		if err := tx.Pipelines().Save(ctx, pipeline, 0); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, pipeline.PullDomainEvents()...)
	})
	if err != nil {
		return PublishPipelineResult{}, fmt.Errorf("публикация пайплайна: %w", err)
	}
	return PublishPipelineResult{PipelineID: string(pipeline.ID()), Version: pipeline.Version()}, nil
}
