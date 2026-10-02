package workflowadapter

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Имена workflow, activities, сигналов и запросов. Имена заданы явно: workflow
// ссылается на строку, а не на указатель функции, поэтому бегущие исполнения не
// ломаются при рефакторинге кода.
const (
	// WorkflowName — имя workflow фулфилмента заявки.
	WorkflowName = "request-fulfillment"

	// ActivityStartProcessing — activity перехода validated → processing.
	ActivityStartProcessing = "StartProcessing"

	// ActivityFulfillRequest — activity перехода processing → fulfilled.
	ActivityFulfillRequest = "FulfillRequest"

	// ActivityCancelRequest — activity перехода в cancelled.
	ActivityCancelRequest = "CancelRequest"

	// ActivitySearchLeads — activity шага поиска лидов (заглушка).
	ActivitySearchLeads = "SearchLeads"

	// SignalApprovalDecision — сигнал утверждения результата.
	SignalApprovalDecision = "approval-decision"

	// QueryStatus — запрос наблюдаемого состояния workflow.
	QueryStatus = "status"
)

// Фазы workflow — наблюдаемые состояния для query и API.
const (
	PhaseStartingProcessing = "starting-processing"
	PhaseSearching          = "searching"
	PhaseAwaitingApproval   = "awaiting-approval"
	PhaseFulfilling         = "fulfilling"
	PhaseFulfilled          = "fulfilled"
	PhaseCancelled          = "cancelled"
)

// Идентификаторы версионирования: по ним workflow отличает структуру графа при
// деплое, чтобы бегущие заявки не ломались (design.md, Решение 5).
const (
	changeTopUpLoop    = "top-up-loop"
	changeApprovalGate = "approval-gate"
)

// Параметры графа фулфилмента.
const (
	// defaultLeadTarget — целевое число лидов, если заявка его не задала.
	defaultLeadTarget = 1

	// maxTopUpRounds — предел добора лидов: цикл обязан завершаться.
	maxTopUpRounds = 3

	// activityStartToCloseTimeout — предельное время одной activity.
	activityStartToCloseTimeout = 30 * time.Second

	// activityMaxAttempts — число попыток activity до отказа.
	activityMaxAttempts = 3
)

// Fulfillment — workflow исполнения заявки: двигает её по статусной машине,
// вызывая activities-адаптеры к application services. Workflow детерминирован:
// время, случайность и внешние вызовы — только внутри activities.
//
// Граф:
//
//	validated → processing → поиск → (добор) →
//	пауза утверждения → fulfilled
//
// Валидация выполняется вне workflow (экран валидации), поэтому исполнение
// стартует сразу с перехода validated → processing. Отказ на паузе утверждения
// переводит заявку в cancelled.
func Fulfillment(ctx workflow.Context, params Params) (Status, error) {
	logger := workflow.GetLogger(ctx)
	state := Status{RequestID: params.RequestID, Phase: PhaseStartingProcessing}

	if err := workflow.SetQueryHandler(ctx, QueryStatus, func() (Status, error) { return state, nil }); err != nil {
		return state, err
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: activityStartToCloseTimeout,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: activityMaxAttempts},
	}
	actx := workflow.WithActivityOptions(ctx, ao)

	base := transitionInput{
		RequestID:     params.RequestID,
		TenantID:      params.TenantID,
		CorrelationID: params.CorrelationID,
	}

	// 1. Запуск исполнения: validated → processing. Актор перехода —
	// автоматический (workflow), так как решение валидации принято вне workflow.
	if err := workflow.ExecuteActivity(actx, ActivityStartProcessing, base).Get(actx, nil); err != nil {
		return state, err
	}

	// 2. Поиск с ветвлением «достаточно лидов / добор». Цикл версионируется:
	// прежние бегущие исполнения без добора воспроизводятся детерминированно.
	target := params.LeadTarget
	if target <= 0 {
		target = defaultLeadTarget
	}
	state.Phase = PhaseSearching
	topUpVersion := workflow.GetVersion(ctx, changeTopUpLoop, workflow.DefaultVersion, 1)
	for round := 0; ; round++ {
		var result searchResult
		if err := workflow.ExecuteActivity(actx, ActivitySearchLeads, searchInput{
			RequestID: params.RequestID,
			TenantID:  params.TenantID,
			Target:    target,
			Round:     round,
		}).Get(actx, &result); err != nil {
			return state, err
		}
		state.Found += result.Found
		if state.Found >= target {
			break
		}
		if topUpVersion < 1 || round+1 >= maxTopUpRounds {
			break
		}
	}

	// 3. Человеческая пауза: утверждение результата перед выпуском. Гейт
	// версионируется, чтобы бегущие исполнения без паузы не ломались.
	if workflow.GetVersion(ctx, changeApprovalGate, workflow.DefaultVersion, 1) >= 1 {
		state.Phase = PhaseAwaitingApproval
		var approval ApprovalDecision
		workflow.GetSignalChannel(ctx, SignalApprovalDecision).Receive(ctx, &approval)
		if !approval.Approved {
			state.Phase = PhaseCancelled
			in := base
			in.Actor = approval.Actor
			in.ReasonCode = "утверждение: отказ"
			if err := workflow.ExecuteActivity(actx, ActivityCancelRequest, in).Get(actx, nil); err != nil {
				return state, err
			}
			return state, nil
		}
	}

	// 4. Завершение: processing → fulfilled.
	state.Phase = PhaseFulfilling
	if err := workflow.ExecuteActivity(actx, ActivityFulfillRequest, base).Get(actx, nil); err != nil {
		return state, err
	}
	state.Phase = PhaseFulfilled
	logger.Info("фулфилмент заявки завершён", "request_id", params.RequestID)
	return state, nil
}
