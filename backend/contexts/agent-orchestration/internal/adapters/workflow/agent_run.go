package workflowadapter

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Имена workflow, activities, query и сигналов. Явные имена предпочтительны:
// workflow-код ссылается на имя, а не на указатель функции, поэтому бегущие
// исполнения не ломаются при рефакторинге.
const (
	WorkflowName = "agent-run"

	ActivityExecuteStep = "agent-run-execute-step"
	ActivityRunState    = "agent-run-read-state"

	QueryStatus = "status"

	SignalApprovalDecision = "agent-run-approval-decision"
	SignalStopRun          = "agent-run-stop"
)

// Значения таймаутов и попыток по умолчанию.
const (
	defaultApprovalTTL        = 72 * time.Hour
	defaultActivityStartClose = time.Minute
	defaultActivityAttempts   = 3
	defaultExecutionTimeout   = 7 * 24 * time.Hour
)

// withDefaults заполняет нулевые поля политики значениями по умолчанию.
func (p ExecutionPolicy) withDefaults() ExecutionPolicy {
	if p.ApprovalTTL <= 0 {
		p.ApprovalTTL = defaultApprovalTTL
	}
	if p.ActivityStartToClose <= 0 {
		p.ActivityStartToClose = defaultActivityStartClose
	}
	if p.ActivityMaxAttempts <= 0 {
		p.ActivityMaxAttempts = defaultActivityAttempts
	}
	if p.ExecutionTimeout <= 0 {
		p.ExecutionTimeout = defaultExecutionTimeout
	}
	return p
}

// AgentRun — workflow исполнения запуска агента: двигает одну компанию по версии
// пайплайна, вызывая activities. Workflow детерминирован: время, случайность и
// внешние вызовы — только внутри activities.
//
// Граф:
//
//	чтение состояния → [ожидание решения] → прогон шага → … → терминальный исход
//
// Состояние запуска живёт в реляционном хранилище контекста, а не только в истории
// Temporal: после перезапуска процесса workflow восстанавливает историю и продолжает
// обработку с шага, зафиксированного в агрегате.
func AgentRun(ctx workflow.Context, params Params) (Status, error) {
	logger := workflow.GetLogger(ctx)
	policy := params.Policy.withDefaults()
	actCtx := workflow.WithActivityOptions(ctx, activityOptions(policy))

	status := Status{RunID: params.RunID}
	if err := workflow.SetQueryHandler(ctx, QueryStatus, func() (Status, error) { return status, nil }); err != nil {
		return status, err
	}

	for {
		var current runState
		if err := workflow.ExecuteActivity(actCtx, ActivityRunState, runInput{
			RunID: params.RunID, TenantID: params.TenantID,
		}).Get(actCtx, &current); err != nil {
			return status, err
		}
		status.Phase = current.Status
		status.CurrentStep = current.CurrentStep
		status.Cursor = current.Cursor

		if current.Terminal {
			logger.Info("запуск агента завершён", "run_id", params.RunID, "status", current.Status)
			return status, nil
		}
		if current.AwaitingApproval {
			// Человеческая пауза необратимого шага: ждём решение оператора в пределах
			// срока. Истёк срок — workflow завершается, состояние сохраняется, решение
			// можно подать командой позже.
			if _, decided := awaitApproval(ctx, policy.ApprovalTTL); !decided {
				logger.Info("ожидание решения оператора истекло", "run_id", params.RunID)
				return status, nil
			}
			continue
		}
		if current.Blocked {
			// Обязательный шаг недоступен: обработка не завершена и требует
			// вмешательства (устранения недоступности). Workflow завершается, запуск
			// остаётся незавершённым.
			logger.Info("запуск заблокирован недоступностью", "run_id", params.RunID)
			return status, nil
		}

		var outcome stepOutcome
		if err := workflow.ExecuteActivity(actCtx, ActivityExecuteStep, stepInput{
			RunID: params.RunID, TenantID: params.TenantID, CorrelationID: params.CorrelationID,
		}).Get(actCtx, &outcome); err != nil {
			return status, err
		}
		status.Phase = outcome.RunStatus
		status.CurrentStep = outcome.StepID
	}
}

// awaitApproval ждёт решение оператора не дольше срока паузы. Возвращает решение и
// признак его получения: false — срок истёк, решение не пришло.
func awaitApproval(ctx workflow.Context, ttl time.Duration) (ApprovalDecision, bool) {
	var decision ApprovalDecision
	decided := false
	signal := workflow.GetSignalChannel(ctx, SignalApprovalDecision)
	selector := workflow.NewSelector(ctx)
	selector.AddReceive(signal, func(channel workflow.ReceiveChannel, _ bool) {
		channel.Receive(ctx, &decision)
		decided = true
	})
	selector.AddFuture(workflow.NewTimer(ctx, ttl), func(workflow.Future) {})
	selector.Select(ctx)
	return decision, decided
}

// activityOptions собирает опции activities шага: StartToCloseTimeout и ограниченную
// политику повтора. Ретраи Temporal покрывают сбой транспорта/хранилища; число
// попыток шага на доменном уровне ограничивает сам агрегат.
func activityOptions(policy ExecutionPolicy) workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: policy.ActivityStartToClose,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: int32(policy.ActivityMaxAttempts)},
	}
}
