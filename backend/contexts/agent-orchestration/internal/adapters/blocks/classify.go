package blocks

import (
	"context"
	"errors"
	"strings"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// ClassifyBlockRef — ссылка блока оценки релевантности.
const ClassifyBlockRef domain.BlockRef = "classify"

// ClassifyExecutor — исполнитель блока classify поверх read-контракта
// lead-relevance (владелец — lead-management, design.md, Реш. 6). Блок получает
// вердикт «подходит/не подходит» для компании в контексте задания и не переносит
// его между заданиями: оценку даёт владелец по запросу, а не агент.
//
// Компания с вердиктом «не подходит» завершает шаг отказом, а не успехом: черновик
// письма для неё не создаётся (спека company-agent-orchestration, «Оценка
// релевантности как вход решения»).
type ClassifyExecutor struct {
	contract ports.RelevanceEvaluator
}

// NewClassifyExecutor собирает исполнитель блока классификации. nil-контракт
// допустим: шаг честно сообщает недоступность, а не выдумывает вердикт.
func NewClassifyExecutor(contract ports.RelevanceEvaluator) *ClassifyExecutor {
	return &ClassifyExecutor{contract: contract}
}

// Ref реализует ports.BlockExecutor.
func (e *ClassifyExecutor) Ref() domain.BlockRef { return ClassifyBlockRef }

// Execute реализует ports.BlockExecutor.
func (e *ClassifyExecutor) Execute(ctx context.Context, in ports.StepInput) (ports.StepResult, error) {
	if e.contract == nil {
		return ports.StepResult{Outcome: domain.Unavailable("lead-relevance: контракт оценки не подключён")}, nil
	}
	if strings.TrimSpace(in.CompanyID) == "" {
		return ports.StepResult{Outcome: domain.Unavailable("lead-relevance: компания не задана")}, nil
	}
	evaluation, err := e.contract.Evaluate(ctx, ports.RelevanceQuery{
		TenantID:  in.TenantID,
		MissionID: in.MissionID,
		CompanyID: in.CompanyID,
		Criteria:  in.Criteria,
	})
	if err != nil {
		switch {
		case errors.Is(err, ports.ErrRelevanceRejected):
			return ports.StepResult{Outcome: domain.Failed("lead-relevance: оценка отклонена владельцем")}, nil
		case errors.Is(err, ports.ErrRelevanceUnavailable):
			return ports.StepResult{Outcome: domain.Unavailable("lead-relevance: владелец оценки недоступен")}, nil
		default:
			return ports.StepResult{}, err
		}
	}
	switch evaluation.Verdict {
	case ports.RelevanceSuitable:
		return ports.StepResult{Outcome: domain.Succeeded(), ArtifactRef: in.CompanyID}, nil
	case ports.RelevanceUnsuitable:
		return ports.StepResult{Outcome: domain.Failed("lead-relevance: компания не подходит в контексте задания")}, nil
	default:
		return ports.StepResult{Outcome: domain.Unavailable("lead-relevance: вердикт оценки не распознан")}, nil
	}
}

// Проверка соответствия порту исполнителя блока.
var _ ports.BlockExecutor = (*ClassifyExecutor)(nil)
