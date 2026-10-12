// Package command — прикладные сценарии записи контекста agent-orchestration:
// публикация пайплайна, запуск и остановка агента, решение оператора и прогон шага.
//
// Сценарии соединяют домен и порты: правил домена здесь нет, транзакцию и outbox
// обеспечивает unit-of-work. Прогон шага исполняет блок по контракту владельца через
// реестр исполнителей и фиксирует событие жизненного цикла шага (design.md, Реш. 12).
package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/app/registry"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// RunStep — вход прогона текущего шага запуска.
type RunStep struct {
	RunID         string
	TenantID      string
	CorrelationID string
	CausationID   string
}

// RunStepResult — наблюдаемый исход прогона шага.
type RunStepResult struct {
	RunID            string
	StepRunID        string
	StepID           string
	BlockRef         string
	Outcome          domain.BlockOutcome
	RunStatus        domain.RunStatus
	Repeated         bool
	AwaitingApproval bool
}

// RunStepHandler исполняет текущий шаг запуска по блоку и фиксирует событие шага.
//
// Идемпотентность: повтор прогона уже завершённого шага не даёт второго эффекта
// (домен сообщает alreadyDone). Отсутствие исполнителя блока или явная недоступность
// провайдера дают исход «недоступно» и не подменяются успехом (AGENTS.md §3).
type RunStepHandler struct {
	uow      ports.UnitOfWork
	registry *registry.Registry
	clock    ports.Clock
	ids      ports.IDGenerator
}

// NewRunStepHandler собирает сценарий прогона шага.
func NewRunStepHandler(uow ports.UnitOfWork, executors *registry.Registry, clock ports.Clock, ids ports.IDGenerator) *RunStepHandler {
	return &RunStepHandler{uow: uow, registry: executors, clock: clock, ids: ids}
}

// Handle выполняет текущий шаг запуска в одной транзакции и пишет его события в
// outbox.
func (h *RunStepHandler) Handle(ctx context.Context, in RunStep) (RunStepResult, error) {
	var result RunStepResult
	result.RunID = in.RunID
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		run, err := tx.Runs().FindByID(ctx, domain.RunID(in.RunID), domain.TenantID(in.TenantID))
		if err != nil {
			return err
		}
		revision := run.Revision()
		if run.Status() != domain.StatusRunning {
			result.RunStatus = run.Status()
			return nil
		}
		step, ok := run.CurrentStep()
		if !ok {
			result.RunStatus = run.Status()
			return nil
		}
		result.StepID = string(step.StepID())
		result.BlockRef = string(step.Ref())

		now := h.clock.Now()
		hdr := header(h.ids, in.CorrelationID, in.CausationID)
		begun, alreadyDone, err := run.BeginStep(step.StepID(), now)
		if err != nil {
			return err
		}
		result.StepRunID = string(begun.ID())
		if alreadyDone {
			result.Repeated = true
			result.Outcome = begun.Outcome()
			result.RunStatus = run.Status()
			return nil
		}

		if err := h.executeStep(ctx, run, begun, now, hdr); err != nil {
			return err
		}
		result.Outcome = begun.Outcome()
		result.RunStatus = run.Status()
		result.AwaitingApproval = result.RunStatus == domain.StatusAwaitingApproval

		if err := tx.Runs().Save(ctx, run, revision); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, run.PullDomainEvents()...)
	})
	return result, err
}

// executeStep выполняет блок шага и фиксирует исход в агрегате. Недоступность
// провайдера и отказ различаются: первая держит обязательный шаг в blocked, второй
// повторяется в пределах попыток.
func (h *RunStepHandler) executeStep(ctx context.Context, run *domain.AgentRun, step *domain.AgentStepRun, now time.Time, hdr domain.EventHeader) error {
	stepID := step.StepID()
	executor, ok := h.registry.Lookup(step.Ref())
	if !ok {
		// Зарегистрированный блок без подключённого исполнителя — честная
		// недоступность, а не выдуманный результат.
		_, err := run.MarkStepUnavailable(stepID, fmt.Sprintf("блок %s: исполнитель не подключён", step.Ref()), now, hdr)
		return err
	}

	outcome, err := executor.Execute(ctx, ports.StepInput{
		RunID:       string(run.ID()),
		StepRunID:   string(step.ID()),
		StepID:      string(stepID),
		TenantID:    string(run.TenantID()),
		MissionID:   string(run.Mission().ID()),
		CompanyID:   string(run.Mission().CompanyID()),
		Criteria:    run.Mission().Criteria(),
		BlockRef:    step.Ref(),
		Attempt:     step.Attempts(),
		ArtifactRef: previousArtifact(run, step),
	})
	if err != nil {
		_, failErr := run.FailStep(stepID, normalizeFailure(step.Ref(), err), now, hdr)
		return failErr
	}

	switch {
	case outcome.Outcome.IsUnavailable():
		_, err := run.MarkStepUnavailable(stepID, outcome.Outcome.Reason(), now, hdr)
		return err
	case outcome.Outcome.Kind() == domain.OutcomeFailed:
		_, err := run.FailStep(stepID, outcome.Outcome.Reason(), now, hdr)
		return err
	default:
		if _, err := run.CompleteStep(stepID, domain.Succeeded(), outcome.ArtifactRef, outcome.DraftRef, outcome.DraftVersion, now, hdr); err != nil {
			return err
		}
		if outcome.AwaitApproval {
			return run.RequestApproval(outcome.DraftRef, outcome.DraftVersion, domain.ActorID("agent"), now, hdr)
		}
		return nil
	}
}

// previousArtifact возвращает непрозрачную ссылку на артефакт предыдущего шага
// (например, найденную компанию). Содержания артефакта здесь нет.
func previousArtifact(run *domain.AgentRun, step *domain.AgentStepRun) string {
	index := step.Order()
	if index <= 0 {
		return ""
	}
	steps := run.Steps()
	if index-1 < len(steps) {
		return steps[index-1].ArtifactRef()
	}
	return ""
}

// normalizeFailure возвращает нормализованную причину отказа без содержимого ошибки:
// имя блока и грубая категория. Секреты, токены и персональные данные сюда не
// попадают (Реш. 12).
func normalizeFailure(ref domain.BlockRef, err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("блок %s: превышен срок исполнения", ref)
	case errors.Is(err, context.Canceled):
		return fmt.Sprintf("блок %s: исполнение прервано", ref)
	default:
		return fmt.Sprintf("блок %s: исполнение не выполнено", ref)
	}
}
