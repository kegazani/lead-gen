package workflowadapter

import (
	"strings"
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

	// ActivitySearchLeads — activity шага поиска лидов.
	ActivitySearchLeads = "SearchLeads"

	// ActivityDraftEmails — activity шага генерации черновиков писем под найденных
	// лидов.
	ActivityDraftEmails = "DraftEmails"

	// SignalApprovalDecision — сигнал утверждения результата.
	SignalApprovalDecision = "approval-decision"

	// QueryStatus — запрос наблюдаемого состояния workflow.
	QueryStatus = "status"
)

// Фазы workflow — наблюдаемые состояния для query и API.
const (
	PhaseStartingProcessing = "starting-processing"
	PhaseSearching          = "searching"
	PhaseDrafting           = "drafting"
	// PhaseNoLeads — исход «0 уникальных лидов»: заявка не может стать fulfilled
	// молча, без явного одобрения оператора.
	PhaseNoLeads          = "no-leads"
	PhaseAwaitingApproval = "awaiting-approval"
	PhaseFulfilling       = "fulfilling"
	PhaseFulfilled        = "fulfilled"
	PhaseCancelled        = "cancelled"
)

// Идентификаторы версионирования: по ним workflow отличает структуру графа при
// деплое, чтобы бегущие заявки не ломались.
const (
	changeTopUpLoop     = "top-up-loop"
	changeApprovalGate  = "approval-gate"
	changeApprovalTimer = "approval-timer"
	changeEmailDrafts   = "email-drafts"
	// changeShortfallGate — маркер гейта «недобор → письма»: при включённом гейте
	// черновики генерируются только при наблюдаемом недоборе. Отдельный маркер
	// нужен, чтобы прежние бегущие исполнения воспроизводились без гейта.
	changeShortfallGate = "shortfall-gate"
)

// Коды причин терминальных исходов человеческой паузы.
const (
	approvalRefusedReason = "утверждение: отказ"
	approvalExpiredReason = "утверждение: срок истёк"
)

// Параметры графа фулфилмента (значения по умолчанию; конкретное исполнение
// может задать свои через Params.Policy).
const (
	// defaultLeadTarget — целевое число лидов, если заявка его не задала.
	defaultLeadTarget = 1

	// maxTopUpRounds — предел добора лидов: цикл обязан завершаться.
	maxTopUpRounds = 3

	// defaultTransitionStartToClose — таймаут коротких activities-переходов.
	defaultTransitionStartToClose = 30 * time.Second

	// defaultTransitionMaxAttempts — число попыток коротких переходов.
	defaultTransitionMaxAttempts = 3

	// defaultLongStartToClose — таймаут длинных activities (поиск, генерация
	// писем): минуты, а не секунды.
	defaultLongStartToClose = 10 * time.Minute

	// defaultLongHeartbeat — период контроля жизни длинной activity.
	defaultLongHeartbeat = time.Minute

	// defaultLongMaxAttempts — число попыток длинных внешних шагов. Ограничено,
	// чтобы таймаут не приводил к каскаду повторных краулов и LLM-вызовов.
	defaultLongMaxAttempts = 2

	// defaultApprovalTTL — срок паузы утверждения (7 дней).
	defaultApprovalTTL = 7 * 24 * time.Hour

	// defaultExecutionTimeout — предельное время исполнения заявки; покрывает
	// срок паузы, чтобы таймер успел сработать.
	defaultExecutionTimeout = 30 * 24 * time.Hour
)

// Fulfillment — workflow исполнения заявки: двигает её по статусной машине,
// вызывая activities-адаптеры к application services. Workflow детерминирован:
// время, случайность и внешние вызовы — только внутри activities.
//
// Граф:
//
//	validated → processing → поиск (уникальные лиды, добор) →
//	черновики писем → пауза утверждения (сигнал или таймер) → fulfilled
//
// Валидация выполняется вне workflow (экран валидации), поэтому исполнение
// стартует сразу с перехода validated → processing. Отказ или истечение срока на
// паузе утверждения переводит заявку в cancelled. Нуль уникальных лидов наблюдаем
// отдельным признаком и не завершает заявку успехом без решения оператора.
func Fulfillment(ctx workflow.Context, params Params) (Status, error) {
	logger := workflow.GetLogger(ctx)
	policy := params.Policy.withDefaults()
	state := Status{RequestID: params.RequestID, Phase: PhaseStartingProcessing}

	if err := workflow.SetQueryHandler(ctx, QueryStatus, func() (Status, error) { return state, nil }); err != nil {
		return state, err
	}

	// Раздельные опции activities: короткие переходы — малый таймаут без
	// heartbeat; длинные поиск/письма — таймаут в минутах и HeartbeatTimeout.
	transitionOpts, longOpts := activityOptions(policy)
	transitionCtx := workflow.WithActivityOptions(ctx, transitionOpts)
	longCtx := workflow.WithActivityOptions(ctx, longOpts)

	base := transitionInput{
		RequestID:     params.RequestID,
		TenantID:      params.TenantID,
		CorrelationID: params.CorrelationID,
	}

	// 1. Запуск исполнения: validated → processing. Актор перехода —
	// автоматический (workflow), так как решение валидации принято вне workflow.
	if err := workflow.ExecuteActivity(transitionCtx, ActivityStartProcessing, base).Get(transitionCtx, nil); err != nil {
		return state, err
	}

	// 2. Поиск с ветвлением «достаточно лидов / добор». Считаются уникальные лиды
	// (дедуп по идентификатору), а не сумма сырых счётчиков по раундам. Цикл
	// версионируется: прежние бегущие исполнения без добора воспроизводятся
	// детерминированно.
	target := params.LeadTarget
	// explicitTarget отличает явно заданную цель от значения по умолчанию: при
	// отсутствии цели недобор не рассчитывается (см. шаг 2.0).
	explicitTarget := target > 0
	if target <= 0 {
		target = defaultLeadTarget
	}
	state.Phase = PhaseSearching
	topUpVersion := workflow.GetVersion(ctx, changeTopUpLoop, workflow.DefaultVersion, 1)
	seen := make(map[string]struct{})
	leadIDs := make([]string, 0, target)
	for round := 0; ; round++ {
		var result searchResult
		if err := workflow.ExecuteActivity(longCtx, ActivitySearchLeads, searchInput{
			RequestID: params.RequestID,
			TenantID:  params.TenantID,
			Target:    target,
			Round:     round,
		}).Get(longCtx, &result); err != nil {
			return state, err
		}
		for _, id := range result.LeadIDs {
			if strings.TrimSpace(id) == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			leadIDs = append(leadIDs, id)
		}
		state.Found = len(seen)
		if state.Found >= target {
			break
		}
		if topUpVersion < 1 || round+1 >= maxTopUpRounds {
			break
		}
	}

	// 2.0. Недобор — производное наблюдаемое значение: сколько компаний не
	// хватает до цели заявки. Считается только при явно заданной цели: при её
	// отсутствии недобор не рассчитывается, чтобы не показывать выдуманный ноль.
	// Лиды не выдумываются — недобор честно отражает остаток (AGENTS.md §3).
	if explicitTarget {
		shortfall := target - state.Found
		if shortfall < 0 {
			shortfall = 0
		}
		state.Shortfall = &shortfall
	}

	// 2.1. Нуль уникальных лидов — наблюдаемый исход: заявка не может быть
	// завершена успехом молча, без явного одобрения оператора.
	if topUpVersion >= 1 && state.Found == 0 {
		state.NoLeads = true
		state.Phase = PhaseNoLeads
	}

	// 2.5. Черновики писем под найденных лидов. Генерация идёт до человеческой
	// паузы: оператор утверждает результат вместе с готовыми письмами. Шаг
	// версионируется, чтобы бегущие исполнения без него воспроизводились
	// детерминированно. Гейт «недобор → письма» версионируется отдельным маркером
	// (`shortfall-gate`): письма нужны, чтобы добрать недостающее, поэтому при
	// достигнутой цели шаг пропускается, а прежние исполнения без гейта
	// воспроизводят безусловную генерацию. Текст писем в payload не поднимается —
	// он оседает в хранилище черновиков, а workflow получает только счётчик.
	draftsVersion := workflow.GetVersion(ctx, changeEmailDrafts, workflow.DefaultVersion, 1)
	shortfallGate := workflow.GetVersion(ctx, changeShortfallGate, workflow.DefaultVersion, 1)
	if draftsVersion >= 1 && len(leadIDs) > 0 && draftsAllowed(shortfallGate, state.Shortfall) {
		state.Phase = PhaseDrafting
		var drafted draftResult
		if err := workflow.ExecuteActivity(longCtx, ActivityDraftEmails, draftInput{
			RequestID: params.RequestID,
			TenantID:  params.TenantID,
			LeadIDs:   leadIDs,
		}).Get(longCtx, &drafted); err != nil {
			return state, err
		}
		state.Drafted = drafted.Drafted
	}

	// 3. Человеческая пауза: утверждение результата перед выпуском. Гейт
	// версионируется, чтобы бегущие исполнения без паузы не ломались. Новая ветка
	// с таймером версионируется отдельно: старые исполнения воспроизводят прежнее
	// бесконечное ожидание сигнала.
	if workflow.GetVersion(ctx, changeApprovalGate, workflow.DefaultVersion, 1) >= 1 {
		state.Phase = PhaseAwaitingApproval
		if workflow.GetVersion(ctx, changeApprovalTimer, workflow.DefaultVersion, 1) >= 1 {
			approval, decided := awaitApproval(ctx, policy.ApprovalTTL)
			if !decided {
				return cancelRequest(transitionCtx, base, string(defaultActor), approvalExpiredReason, &state)
			}
			if !approval.Approved {
				return cancelRequest(transitionCtx, base, approval.Actor, approvalRefusedReason, &state)
			}
		} else {
			var approval ApprovalDecision
			workflow.GetSignalChannel(ctx, SignalApprovalDecision).Receive(ctx, &approval)
			if !approval.Approved {
				return cancelRequest(transitionCtx, base, approval.Actor, approvalRefusedReason, &state)
			}
		}
	}

	// 4. Завершение: processing → fulfilled.
	state.Phase = PhaseFulfilling
	if err := workflow.ExecuteActivity(transitionCtx, ActivityFulfillRequest, base).Get(transitionCtx, nil); err != nil {
		return state, err
	}
	state.Phase = PhaseFulfilled
	logger.Info("фулфилмент заявки завершён", "request_id", params.RequestID)
	return state, nil
}

// awaitApproval ждёт решение оператора не дольше срока паузы. Возвращает решение
// и признак его получения: false — срок истёк, решение не пришло. Реализовано
// selector'ом с таймером, поэтому ожидание ограничено сроком, а не бесконечно.
func awaitApproval(ctx workflow.Context, ttl time.Duration) (ApprovalDecision, bool) {
	var approval ApprovalDecision
	decided := false
	signal := workflow.GetSignalChannel(ctx, SignalApprovalDecision)
	selector := workflow.NewSelector(ctx)
	selector.AddReceive(signal, func(channel workflow.ReceiveChannel, _ bool) {
		channel.Receive(ctx, &approval)
		decided = true
	})
	selector.AddFuture(workflow.NewTimer(ctx, ttl), func(workflow.Future) {})
	selector.Select(ctx)
	return approval, decided
}

// cancelRequest переводит заявку в cancelled с указанной причиной и завершает
// workflow нормальным исходом (без ошибки): терминальный статус достигнут.
//
// state принимается указателем: фазу видит не только результат workflow, но и
// query-обработчик, замкнутый на ту же переменную. Иначе после отмены query
// отдавал бы прежнюю фазу (awaiting-approval), пока исполнение уже завершено.
func cancelRequest(
	activityCtx workflow.Context,
	base transitionInput,
	actor string,
	reason string,
	state *Status,
) (Status, error) {
	state.Phase = PhaseCancelled
	in := base
	in.Actor = actor
	in.ReasonCode = reason
	if err := workflow.ExecuteActivity(activityCtx, ActivityCancelRequest, in).Get(activityCtx, nil); err != nil {
		return *state, err
	}
	return *state, nil
}

// draftsAllowed решает, запускать ли генерацию черновиков при найденных лидах.
// При включённом гейте (`shortfall-gate`) письма нужны только при наблюдаемом
// недоборе: цель достигнута — outreach не нужен. Прежние исполнения без гейта
// (версия DefaultVersion) воспроизводят безусловную генерацию под найденных
// лидов, чтобы реплей не менял их структуру. Недобор nil (цель не задана)
// трактуется как отсутствие недобора — выдуманный ноль не подставляется.
func draftsAllowed(shortfallGate workflow.Version, shortfall *int) bool {
	if shortfallGate < 1 {
		return true
	}
	return shortfall != nil && *shortfall > 0
}

// activityOptions строит раздельные опции activities: короткие переходы статуса —
// малый StartToCloseTimeout без heartbeat; длинные внешние шаги (поиск, письма) —
// StartToCloseTimeout в минутах и HeartbeatTimeout. Чистая функция от политики:
// тот же вход — те же опции, что делает их воспроизводимыми при реплее и
// проверяемыми без живого сервера.
func activityOptions(policy ExecutionPolicy) (workflow.ActivityOptions, workflow.ActivityOptions) {
	transition := workflow.ActivityOptions{
		StartToCloseTimeout: policy.TransitionStartToClose,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: int32(policy.TransitionMaxAttempts)},
	}
	long := workflow.ActivityOptions{
		StartToCloseTimeout: policy.LongStartToClose,
		HeartbeatTimeout:    policy.LongHeartbeat,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts:    int32(policy.LongMaxAttempts),
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    policy.LongStartToClose,
		},
	}
	return transition, long
}

// withDefaults заполняет нулевые поля политики осмысленными значениями и
// согласует предел исполнения со сроком паузы: предел не меньше срока, чтобы
// таймер паузы успевал сработать.
func (p ExecutionPolicy) withDefaults() ExecutionPolicy {
	if p.ApprovalTTL <= 0 {
		p.ApprovalTTL = defaultApprovalTTL
	}
	if p.ExecutionTimeout <= 0 {
		p.ExecutionTimeout = defaultExecutionTimeout
	}
	if p.TransitionStartToClose <= 0 {
		p.TransitionStartToClose = defaultTransitionStartToClose
	}
	if p.TransitionMaxAttempts <= 0 {
		p.TransitionMaxAttempts = defaultTransitionMaxAttempts
	}
	if p.LongStartToClose <= 0 {
		p.LongStartToClose = defaultLongStartToClose
	}
	if p.LongHeartbeat <= 0 {
		p.LongHeartbeat = defaultLongHeartbeat
	}
	if p.LongMaxAttempts <= 0 {
		p.LongMaxAttempts = defaultLongMaxAttempts
	}
	if p.ExecutionTimeout < p.ApprovalTTL {
		p.ExecutionTimeout = p.ApprovalTTL
	}
	return p
}
