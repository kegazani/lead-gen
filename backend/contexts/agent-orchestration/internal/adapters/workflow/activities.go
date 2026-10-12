package workflowadapter

import (
	"context"
	"errors"

	"go.temporal.io/sdk/temporal"

	"lead-gen/backend/contexts/agent-orchestration/internal/app/command"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/backend/platform/observability/observ"
)

// Activities — тонкие адаптеры к application services контекста. Доменной логики
// здесь нет: идемпотентность и допустимость перехода решает aggregate root agent-run,
// а инварианты — прикладной слой.
type Activities struct {
	// Steps — прогон шага запуска по блоку.
	Steps *command.RunStepHandler

	// Runs — репозиторий запусков: activity чтения состояния.
	Runs ports.RunRepository

	// Logger — логгер сервиса. nil — без логирования исходов activity.
	Logger observ.Logger
}

// ExecuteStep выполняет текущий шаг запуска и возвращает наблюдаемое состояние
// после шага. Повторный прогон уже завершённого шага идемпотентен (решает домен).
func (a *Activities) ExecuteStep(ctx context.Context, in stepInput) (stepOutcome, error) {
	if a.Steps == nil {
		return stepOutcome{}, temporal.NewNonRetryableApplicationError(
			"шаг не собран", "not-configured", errors.New("activity: сценарий прогона шага не собран"))
	}
	result, err := a.Steps.Handle(ctx, command.RunStep{
		RunID:         in.RunID,
		TenantID:      in.TenantID,
		CorrelationID: in.CorrelationID,
		CausationID:   in.CausationID,
	})
	if err != nil {
		if errors.Is(err, ports.ErrRunNotFound) {
			return stepOutcome{}, temporal.NewNonRetryableApplicationError(
				"запуск не найден", "run-not-found", err)
		}
		return stepOutcome{}, err
	}
	return stepOutcome{
		StepID:           result.StepID,
		BlockRef:         result.BlockRef,
		RunStatus:        string(result.RunStatus),
		Terminal:         result.RunStatus.IsTerminal(),
		AwaitingApproval: result.AwaitingApproval,
		Blocked:          result.RunStatus == domain.StatusBlocked,
		Outcome:          string(result.Outcome.Kind()),
	}, nil
}

// RunState читает наблюдаемое состояние запуска из хранилища контекста.
func (a *Activities) RunState(ctx context.Context, in runInput) (runState, error) {
	if a.Runs == nil {
		return runState{}, temporal.NewNonRetryableApplicationError(
			"хранилище не подключено", "not-configured", errors.New("activity: репозиторий запусков не собран"))
	}
	run, err := a.Runs.FindByID(ctx, domain.RunID(in.RunID), domain.TenantID(in.TenantID))
	if err != nil {
		if errors.Is(err, ports.ErrRunNotFound) {
			return runState{}, temporal.NewNonRetryableApplicationError(
				"запуск не найден", "run-not-found", err)
		}
		return runState{}, err
	}
	state := runState{
		Status:           string(run.Status()),
		Cursor:           run.Cursor(),
		Terminal:         run.Status().IsTerminal(),
		AwaitingApproval: run.Status() == domain.StatusAwaitingApproval,
		Blocked:          run.Status() == domain.StatusBlocked,
	}
	if step, ok := run.CurrentStep(); ok {
		state.CurrentStep = string(step.StepID())
	}
	return state, nil
}
