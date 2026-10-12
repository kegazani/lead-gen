package workflowadapter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// Имена workflow, activities, сигналов и запросов. Имена заданы явно: workflow
// ссылается на строку, а не на указатель функции, поэтому бегущие исполнения не
// ломаются при рефакторинге кода.
const (
	// WorkflowName — имя workflow фулфилмента заявки.
	WorkflowName = "request-fulfillment"
	// LegacyWorkflowName — историческое Temporal имя до явной регистрации.
	// Существующие executions с этим типом продолжают replay/работу.
	LegacyWorkflowName = "Fulfillment"

	// ActivityStartProcessing — activity перехода validated → processing.
	ActivityStartProcessing = "StartProcessing"

	// ActivityFulfillRequest — activity перехода processing → fulfilled.
	ActivityFulfillRequest = "FulfillRequest"

	// ActivityCancelRequest — activity перехода в cancelled.
	ActivityCancelRequest = "CancelRequest"

	// ActivityFailRequest — activity перехода в failed (терминальный сбой
	// исполнения).
	ActivityFailRequest = "FailRequest"

	// ActivitySearchLeads — activity шага поиска лидов.
	ActivitySearchLeads = "SearchLeads"

	// ActivityDraftEmails — activity шага генерации черновиков писем под найденных
	// лидов.
	ActivityDraftEmails = "DraftEmails"

	// ActivityHandoffDrafts — activity шага передачи черновиков писем заявки в
	// outreach-orchestration на утверждение.
	ActivityHandoffDrafts = "HandoffDrafts"

	// SignalApprovalDecision — сигнал утверждения результата.
	SignalApprovalDecision = "approval-decision"

	// SignalDeliveryOutcome — сигнал исхода доставки письма заявки: подаётся
	// подписчиком delivery-event после сопоставления события с заявкой по кампании
	// (capability request-delivery-conclusion). Может снять паузу завершением
	// доставки, не ожидая сигнала консоли заявок.
	SignalDeliveryOutcome = "delivery-outcome"

	// QueryStatus — запрос наблюдаемого состояния workflow.
	QueryStatus = "status"
)

// Фазы workflow — наблюдаемые состояния для query и API.
const (
	PhaseStartingProcessing = "starting-processing"
	PhaseSearching          = "searching"
	PhaseDrafting           = "drafting"
	// PhaseHandoff — фаза передачи черновиков писем в очередь утверждения
	// outreach-orchestration.
	PhaseHandoff = "handoff"
	// PhaseNoLeads — исход «0 уникальных лидов»: заявка не может стать fulfilled
	// молча, без явного одобрения оператора.
	PhaseNoLeads          = "no-leads"
	PhaseAwaitingApproval = "awaiting-approval"
	PhaseFulfilling       = "fulfilling"
	PhaseFulfilled        = "fulfilled"
	PhaseCancelled        = "cancelled"
	// PhasePending — terminal intent записан, но переход владельца не подтверждён.
	PhasePending = "pending"
	// PhaseFailed — терминальный исход необратимого сбоя шага исполнения: заявка
	// переведена в доменный статус failed.
	PhaseFailed            = "failed"
	PhasePreparedForReview = "prepared-for-review"
	PhaseDraftsGenerated   = "drafts-generated"
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
	changeShortfallGate      = "shortfall-gate"
	changeEditorialExecution = "editorial-execution-v1"
	// changeEditorialDraftCompletion separates new portal executions from the
	// legacy editorial handoff path while preserving replay of existing histories.
	changeEditorialDraftCompletion = "editorial-draft-completion-v1"
	// Новые editorial executions фиксируют терминальный статус заявки после
	// сохранения черновиков; старые истории повторяют прежнее завершение.
	changeEditorialTerminalCompletion = "editorial-terminal-completion-v1"
	// changeOutreachHandoff — маркер звена передачи «черновики заявки → очередь
	// утверждения outreach»: при включённом звене пригодные черновики передаются в
	// outreach-orchestration. Отдельный маркер нужен, чтобы прежние бегущие
	// исполнения воспроизводились без передачи.
	changeOutreachHandoff = "outreach-handoff"
	// changeDeliveryConclusion — маркер завершения заявки по доставке писем: пауза
	// снимается не только сигналом утверждения, но и наблюдаемым завершением
	// доставки всех переданных писем. Отдельный маркер нужен, чтобы прежние бегущие
	// исполнения воспроизводились без ожидания доставки.
	changeDeliveryConclusion = "delivery-conclusion"
	// changeFailureCompensation — маркер компенсации необратимого сбоя: при
	// включённом маркере сбой шага (после исчерпания ретраев или non-retryable) и
	// истечение внутреннего дедлайна переводят заявку в failed (failRequest), а не
	// роняют workflow ошибкой. Отдельный маркер нужен, чтобы прежние бегущие
	// исполнения воспроизводились без компенсации и без проверок дедлайна.
	changeFailureCompensation = "failure-compensation"
	// changeTerminalIntent сохраняет replay старых executions без нового marker.
	changeTerminalIntent   = "terminal-intent-bounded-compensation"
	changeTerminalChecksum = "terminal-intent-checksum-v1"
)

// Коды причин терминальных исходов человеческой паузы.
const (
	approvalRefusedReason = "утверждение: отказ"
	approvalExpiredReason = "утверждение: срок истёк"
	// deliveryFailedReason — терминальный исход, когда письма заявки не доставлены.
	deliveryFailedReason = "доставка: письма не доставлены"
)

// Машинные коды причин сбоя исполнения (согласованный набор; маппинг «отказ шага
// → код» задаётся здесь, в адаптере workflow). Код сохраняется в агрегате и в
// событии request-execution-failed.
const (
	failCodeSearch             = "search_failed"
	failCodeDraft              = "draft_failed"
	failCodeHandoff            = "handoff_failed"
	failCodeTransitionRejected = "transition_rejected"
	failCodeWorkflowTimedOut   = "workflow_timed_out"
)

// failReason возвращает сообщение оператору по машинному коду сбоя. Текст живёт в
// адаптере: домен сохраняет и код, и текст, но формулировку задаёт источник сбоя.
func failReason(code string) string {
	switch code {
	case failCodeSearch:
		return "исполнение прервано: поиск лидов завершился ошибкой"
	case failCodeDraft:
		return "исполнение прервано: генерация черновиков писем завершилась ошибкой"
	case failCodeHandoff:
		return "исполнение прервано: передача писем на утверждение завершилась ошибкой"
	case failCodeTransitionRejected:
		return "исполнение прервано: переход заявки отклонён"
	case failCodeWorkflowTimedOut:
		return "исполнение прервано: истёк предельный срок исполнения"
	default:
		return "исполнение прервано"
	}
}

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

	// workflowTimeoutReserve — запас времени на компенсацию сбоя: внутренний
	// дедлайн workflow наступает на этот срок раньше Temporal ExecutionTimeout.
	// Temporal ExecutionTimeout терминально убивает execution, не давая workflow
	// выполнить failRequest, поэтому workflow наблюдает собственный дедлайн
	// раньше и завершается штатно с кодом workflow_timed_out.
	workflowTimeoutReserve             = time.Hour
	defaultCompensationMaxAttempts     = 3
	defaultCompensationScheduleToClose = 2 * time.Minute
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
	state := Status{RequestID: params.RequestID, Phase: PhaseStartingProcessing, Target: params.LeadTarget}

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

	// Внутренний дедлайн исполнения: наступает раньше Temporal ExecutionTimeout,
	// оставляя запас на компенсацию сбоя штатным завершением (workflow_timed_out),
	// а не терминальным kill execution.
	deadline := executionDeadline(ctx, policy)
	// Компенсация сбоя и проверки дедлайна версионируются отдельным маркером:
	// прежние бегущие исполнения (DefaultVersion) воспроизводят прежнюю структуру
	// — сбой шага роняет workflow ошибкой, дедлайн не наблюдается.
	failureCompensation := workflow.GetVersion(ctx, changeFailureCompensation, workflow.DefaultVersion, 1)
	terminalIntentVersion := workflow.GetVersion(ctx, changeTerminalIntent, workflow.DefaultVersion, 1)
	terminalChecksumVersion := workflow.GetVersion(ctx, changeTerminalChecksum, workflow.DefaultVersion, 1)
	editorialVersion := workflow.GetVersion(ctx, changeEditorialExecution, workflow.DefaultVersion, 1)
	editorial := editorialVersion >= 1 && params.ExecutionPurpose == domain.ExecutionPurposeEditorial
	// Существующие v1 histories сохраняют старые activity options; только новые
	// executions используют ограниченный бюджет терминальной компенсации.
	compensationCtx := workflow.WithActivityOptions(ctx, compensationOpts(policy, terminalIntentVersion >= 1))

	// 1. Запуск исполнения: validated → processing. Актор перехода —
	// автоматический (workflow), так как решение валидации принято вне workflow.
	//
	// Дедлайн workflow здесь не проверяется: до перехода заявка ещё в validated, а
	// компенсация failRequest (validated → failed) недопустима — она дала бы
	// ErrInvalidTransition. Сбой самого перехода StartProcessing остаётся прежним
	// исходом: workflow возвращает ошибку, заявка не в processing (этот край вне
	// scope терминального failed).
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
		if failureCompensation >= 1 && workflowTimedOut(ctx, deadline) {
			return failRequest(ctx, terminalIntentVersion, terminalChecksumVersion, compensationCtx, params, base, failCodeWorkflowTimedOut, &state)
		}
		var result searchResult
		if err := workflow.ExecuteActivity(longCtx, ActivitySearchLeads, searchInput{
			RequestID: params.RequestID,
			TenantID:  params.TenantID,
			Target:    target,
			Round:     round,
		}).Get(longCtx, &result); err != nil {
			return failStep(ctx, terminalIntentVersion, terminalChecksumVersion, failureCompensation, compensationCtx, params, base, failCodeSearch, &state, err)
		}
		if result.PartialReason != "" {
			state.PartialReason = result.PartialReason
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
			state.PartialReason = ""
			break
		}
		if topUpVersion < 1 || round+1 >= maxTopUpRounds {
			break
		}
	}

	// 2.0. Недобор — производное наблюдаемое значение: сколько компаний не
	// хватает до цели заявки. Считается только при явно заданной цели: при её
	// отсутствии недобор не рассчитывается, чтобы не показывать выдуманный ноль.
	// Вместе с недобором публикуется и сама цель: экран показывает «цель / найдено /
	// недобор» одним срезом. Лиды не выдумываются — недобор честно отражает
	// остаток (AGENTS.md §3).
	if explicitTarget {
		state.Target = target
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
	if editorial && state.Found == 0 {
		cause := errors.New("редакторское исполнение: тематический поиск не нашёл уникальных лидов")
		return failStep(ctx, terminalIntentVersion, terminalChecksumVersion, failureCompensation, compensationCtx, params, base, failCodeSearch, &state, cause)
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
	// drafted живёт вне блока: результат шага черновиков решает, есть ли что
	// передавать (шаг передачи ниже). Нулевое значение — шаг черновиков не
	// запускался (прежнее исполнение без него или нет лидов/недобора).
	var drafted draftResult
	if draftsVersion >= 1 && len(leadIDs) > 0 && (editorial || draftsAllowed(shortfallGate, state.Shortfall)) {
		state.Phase = PhaseDrafting
		if failureCompensation >= 1 && workflowTimedOut(ctx, deadline) {
			return failRequest(ctx, terminalIntentVersion, terminalChecksumVersion, compensationCtx, params, base, failCodeWorkflowTimedOut, &state)
		}
		if err := workflow.ExecuteActivity(longCtx, ActivityDraftEmails, draftInput{
			RequestID: params.RequestID,
			TenantID:  params.TenantID,
			LeadIDs:   leadIDs,
			Editorial: editorial,
		}).Get(longCtx, &drafted); err != nil {
			return failStep(ctx, terminalIntentVersion, terminalChecksumVersion, failureCompensation, compensationCtx, params, base, failCodeDraft, &state, err)
		}
		state.Drafted = drafted.Drafted
		if editorial && (drafted.Drafted == 0 || drafted.Failed > 0) {
			cause := fmt.Errorf("редакторская генерация неполная: создано %d, ошибок %d", drafted.Drafted, drafted.Failed)
			return failStep(ctx, terminalIntentVersion, terminalChecksumVersion, failureCompensation, compensationCtx, params, base, failCodeDraft, &state, cause)
		}
	}
	// Новые portal executions завершаются после сохранения черновиков в request
	// store. Маркер сохраняет прежний handoff при replay уже начатых исполнений.
	if editorial && workflow.GetVersion(ctx, changeEditorialDraftCompletion, workflow.DefaultVersion, 1) >= 1 {
		state.Phase = PhaseDraftsGenerated
		if workflow.GetVersion(ctx, changeEditorialTerminalCompletion, workflow.DefaultVersion, 1) >= 1 {
			if err := workflow.ExecuteActivity(transitionCtx, ActivityFulfillRequest, base).Get(transitionCtx, nil); err != nil {
				return failStep(ctx, terminalIntentVersion, terminalChecksumVersion, failureCompensation, compensationCtx, params, base, failCodeTransitionRejected, &state, err)
			}
			state.Phase = PhaseFulfilled
		}
		return state, nil
	}

	// 2.6. Передача черновиков писем заявки в очередь утверждения
	// outreach-orchestration — только когда черновики действительно есть. Шаг
	// версионируется отдельным маркером: прежние бегущие исполнения (маркер
	// DefaultVersion) воспроизводятся без передачи. Материал писем производится и
	// ссылки на получателя/материал разрешаются внутри activity; в payload workflow
	// поднимаются только идентификаторы и счётчики.
	handoffVersion := workflow.GetVersion(ctx, changeOutreachHandoff, workflow.DefaultVersion, 1)
	// handed объявлен вне блока: результат передачи решает, ожидать ли доставку писем
	// перед завершением (capability request-delivery-conclusion).
	var handed handoffOutcome
	if editorial && drafted.Drafted > 0 {
		if handoffVersion < 1 {
			return state, errors.New("редакторский handoff недоступен для этой версии workflow")
		}
		state.Phase = PhaseHandoff
		var handedResult handoffOutcome
		if err := workflow.ExecuteActivity(longCtx, ActivityHandoffDrafts, handoffInput{
			RequestID: params.RequestID, TenantID: params.TenantID, Actor: string(defaultActor),
		}).Get(longCtx, &handedResult); err != nil {
			return failStep(ctx, terminalIntentVersion, terminalChecksumVersion, failureCompensation, compensationCtx, params, base, failCodeHandoff, &state, err)
		}
		handed = handedResult
		state.Handed = handed.Handed
		if handed.Expected == 0 {
			return failStep(ctx, terminalIntentVersion, terminalChecksumVersion, failureCompensation, compensationCtx, params, base, failCodeHandoff, &state, errors.New("редакторский handoff не подтвердил сохранённые материалы"))
		}
		state.Phase = PhasePreparedForReview
		return state, nil
	}
	if handoffVersion >= 1 && drafted.Drafted > 0 {
		state.Phase = PhaseHandoff
		if failureCompensation >= 1 && workflowTimedOut(ctx, deadline) {
			return failRequest(ctx, terminalIntentVersion, terminalChecksumVersion, compensationCtx, params, base, failCodeWorkflowTimedOut, &state)
		}
		var handedResult handoffOutcome
		// Actor — автор материала: content-request требует непустого
		// идентификатора актора (происхождение). Передача outreach-handoff идёт без
		// актора (машинная межсервисная команда): приёмник при пустом акторе право
		// оператора не проверяет, а актор `workflow` не является участником тенанта.
		if err := workflow.ExecuteActivity(longCtx, ActivityHandoffDrafts, handoffInput{
			RequestID: params.RequestID,
			TenantID:  params.TenantID,
			Actor:     string(defaultActor),
		}).Get(longCtx, &handedResult); err != nil {
			return failStep(ctx, terminalIntentVersion, terminalChecksumVersion, failureCompensation, compensationCtx, params, base, failCodeHandoff, &state, err)
		}
		handed = handedResult
		state.Handed = handed.Handed
		logger.Info("черновики заявки переданы на утверждение",
			"request_id", params.RequestID, "handed", handed.Handed, "skipped", handed.Skipped, "repeated", handed.Repeated)
	}

	// 2.7. Ожидание доставки (capability request-delivery-conclusion): для заявки,
	// передавшей письма, пауза завершается наблюдаемым покрытием доставкой, а не
	// только сигналом консоли заявок — решение оператора в этом контуре уже принято
	// в очереди утверждения outreach. Маркер версионирования сохраняет прежние
	// бегущие исполнения без ожидания доставки.
	deliveryConclusion := workflow.GetVersion(ctx, changeDeliveryConclusion, workflow.DefaultVersion, 1)
	expectDelivery := deliveryConclusion >= 1 && handed.Expected > 0

	// 3. Человеческая пауза: утверждение результата перед выпуском. Гейт
	// версионируется, чтобы бегущие исполнения без паузы не ломались. Новая ветка
	// с таймером версионируется отдельно: старые исполнения воспроизводят прежнее
	// бесконечное ожидание сигнала.
	if workflow.GetVersion(ctx, changeApprovalGate, workflow.DefaultVersion, 1) >= 1 {
		state.Phase = PhaseAwaitingApproval
		if failureCompensation >= 1 && workflowTimedOut(ctx, deadline) {
			return failRequest(ctx, terminalIntentVersion, terminalChecksumVersion, compensationCtx, params, base, failCodeWorkflowTimedOut, &state)
		}
		if workflow.GetVersion(ctx, changeApprovalTimer, workflow.DefaultVersion, 1) >= 1 {
			// Срок паузы ограничен внутренним дедлайном: пауза не должна пережить
			// дедлайн, иначе истёкший дедлайн workflow был бы неотличим от истёкшего
			// срока утверждения. Признак deadlineReached различает эти исходы.
			pauseTTL, deadlineReached := policy.ApprovalTTL, false
			if failureCompensation >= 1 {
				pauseTTL, deadlineReached = approvalPause(ctx, policy.ApprovalTTL, deadline)
			}
			if expectDelivery {
				// Пауза с ожиданием доставки: снимается сигналом решения оператора,
				// полным покрытием доставкой (→ fulfilled) либо истечением срока
				// (→ cancelled с причиной).
				decision := awaitConclusion(ctx, pauseTTL, handed.Expected)
				if failureCompensation >= 1 && workflowTimedOut(ctx, deadline) {
					return failRequest(ctx, terminalIntentVersion, terminalChecksumVersion, compensationCtx, params, base, failCodeWorkflowTimedOut, &state)
				}
				switch {
				case decision.approved:
					// Решение оператора принято — идём к завершению.
				case decision.delivered:
					logger.Info("все письма заявки доставлены — завершение по доставке",
						"request_id", params.RequestID, "expected", handed.Expected)
				case decision.failed:
					return cancelRequest(compensationCtx, base, string(defaultActor), deliveryFailedReason, &state)
				case decision.refused:
					return cancelRequest(compensationCtx, base, decision.actor, approvalRefusedReason, &state)
				default:
					// Срок паузы истёк без полного покрытия доставкой — недоставка.
					return cancelRequest(compensationCtx, base, string(defaultActor), deliveryFailedReason, &state)
				}
			} else {
				approval, decided := awaitApproval(ctx, pauseTTL)
				if !decided {
					// Пауза завершилась по таймеру. Дедлайн workflow наступает раньше
					// срока утверждения (запас), поэтому исход различается по тому,
					// что ограничило паузу: дедлайн → failed, срок утверждения → cancelled.
					if deadlineReached {
						return failRequest(ctx, terminalIntentVersion, terminalChecksumVersion, compensationCtx, params, base, failCodeWorkflowTimedOut, &state)
					}
					return cancelRequest(compensationCtx, base, string(defaultActor), approvalExpiredReason, &state)
				}
				if failureCompensation >= 1 && workflowTimedOut(ctx, deadline) {
					return failRequest(ctx, terminalIntentVersion, terminalChecksumVersion, compensationCtx, params, base, failCodeWorkflowTimedOut, &state)
				}
				if !approval.Approved {
					return cancelRequest(compensationCtx, base, approval.Actor, approvalRefusedReason, &state)
				}
			}
		} else {
			var approval ApprovalDecision
			workflow.GetSignalChannel(ctx, SignalApprovalDecision).Receive(ctx, &approval)
			if !approval.Approved {
				return cancelRequest(compensationCtx, base, approval.Actor, approvalRefusedReason, &state)
			}
		}
	}

	// 4. Завершение: processing → fulfilled.
	if failureCompensation >= 1 && workflowTimedOut(ctx, deadline) {
		return failRequest(ctx, terminalIntentVersion, terminalChecksumVersion, compensationCtx, params, base, failCodeWorkflowTimedOut, &state)
	}
	state.Phase = PhaseFulfilling
	if err := workflow.ExecuteActivity(transitionCtx, ActivityFulfillRequest, base).Get(transitionCtx, nil); err != nil {
		return failStep(ctx, terminalIntentVersion, terminalChecksumVersion, failureCompensation, compensationCtx, params, base, failCodeTransitionRejected, &state, err)
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

// conclusionDecision — исход человеческой паузы с учётом доставки.
type conclusionDecision struct {
	approved  bool // пришло положительное решение оператора
	refused   bool // пришло отрицательное решение оператора
	actor     string
	delivered bool // все переданные письма доставлены
	failed    bool // терминальное покрытие доставкой с недоставкой
}

// awaitConclusion ждёт завершения человеческой паузы не дольше срока: возвращает
// решение оператора, полное покрытие доставкой или (по умолчанию) истечение срока.
// Сигналы доставки дедуплицируются по идентификатору сообщения, поэтому повторная
// доставка события (at-least-once) не искажает счёт. Срок отсчитывается от момента
// входа (абсолютный дедлайн), а не перезапускается каждым сигналом.
func awaitConclusion(ctx workflow.Context, ttl time.Duration, expected int) conclusionDecision {
	deadline := workflow.Now(ctx).Add(ttl)
	approvalCh := workflow.GetSignalChannel(ctx, SignalApprovalDecision)
	deliveryCh := workflow.GetSignalChannel(ctx, SignalDeliveryOutcome)
	delivered := make(map[string]struct{})
	failed := make(map[string]struct{})

	var decision conclusionDecision
	for {
		if !workflow.Now(ctx).Before(deadline) {
			return decision
		}
		var approval ApprovalDecision
		gotApproval := false
		timerFired := false
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(approvalCh, func(channel workflow.ReceiveChannel, _ bool) {
			channel.Receive(ctx, &approval)
			gotApproval = true
		})
		selector.AddReceive(deliveryCh, func(channel workflow.ReceiveChannel, _ bool) {
			var outcome DeliveryOutcome
			channel.Receive(ctx, &outcome)
			id := strings.TrimSpace(outcome.MessageID)
			if id == "" {
				return
			}
			if outcome.Delivered {
				delivered[id] = struct{}{}
			} else {
				failed[id] = struct{}{}
			}
		})
		selector.AddFuture(workflow.NewTimer(ctx, deadline.Sub(workflow.Now(ctx))), func(workflow.Future) {
			timerFired = true
		})
		selector.Select(ctx)

		switch {
		case gotApproval:
			decision.approved = approval.Approved
			decision.refused = !approval.Approved
			decision.actor = approval.Actor
			return decision
		case len(delivered) >= expected:
			decision.delivered = true
			return decision
		case len(delivered)+len(failed) >= expected:
			// Покрытие доставкой терминально, но есть недоставка — успехом не завершаем.
			decision.failed = true
			return decision
		case timerFired:
			return decision
		}
	}
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

// failRequest переводит заявку в failed с кодом и текстом причины. Доменная
// компенсация (activity FailRequest) завершается успешно, но workflow возвращает
// non-retryable ошибку: Temporal close-статус становится Failed (а не Completed),
// чтобы политика WorkflowIDReusePolicy = ALLOW_DUPLICATE_FAILED_ONLY разрешала
// переиспользование того же WorkflowID при повторном запуске (re-run). Доменный
// статус уже failed — компенсация выполнена, это корректно. Симметрична
// cancelRequest, которая остаётся штатным завершением (Completed): отменённая
// заявка не перезапускается.
//
// state принимается указателем: фазу видит не только результат workflow, но и
// query-обработчик, замкнутый на ту же переменную. Иначе после сбоя query
// отдавал бы прежнюю фазу, пока исполнение уже завершено.
func failRequest(
	workflowCtx workflow.Context,
	terminalIntentVersion workflow.Version,
	terminalChecksumVersion workflow.Version,
	activityCtx workflow.Context,
	params Params,
	base transitionInput,
	code string,
	state *Status,
) (Status, error) {
	in := base
	in.Actor = string(defaultActor)
	in.ReasonCode = code
	in.Reason = failReason(code)
	if terminalIntentVersion >= 1 {
		info := workflow.GetInfo(workflowCtx)
		intent := TerminalIntent{
			Version:        1,
			WorkflowID:     info.WorkflowExecution.ID,
			RunID:          info.WorkflowExecution.RunID,
			RequestID:      params.RequestID,
			TenantID:       params.TenantID,
			AttemptID:      params.AttemptID,
			Generation:     params.Generation,
			CommandID:      params.CommandID,
			ReasonCode:     code,
			OriginalReason: in.Reason,
		}
		if terminalChecksumVersion >= 1 {
			intent.Checksum = terminalIntentChecksum(intent)
		}
		// SideEffect записывает сериализованный TerminalIntent в history до
		// schedule compensation activity. Без обязательной identity marker
		// остаётся пригодным для аудита, но не пройдёт verifier и не снимет gate.
		var recorded TerminalIntent
		if err := workflow.SideEffect(workflowCtx, func(workflow.Context) interface{} { return intent }).Get(&recorded); err != nil {
			state.Phase = PhasePending
			return *state, fmt.Errorf("записать terminal intent в историю: %w", err)
		}
	}
	if err := workflow.ExecuteActivity(activityCtx, ActivityFailRequest, in).Get(activityCtx, nil); err != nil {
		if terminalIntentVersion >= 1 {
			state.Phase = PhasePending
			return *state, fmt.Errorf("терминальная компенсация осталась pending: %w", err)
		}
		state.Phase = PhaseFailed
		return *state, err
	}
	state.Phase = PhaseFailed
	return *state, temporal.NewNonRetryableApplicationError(
		"исполнение заявки завершилось сбоем: "+code,
		"RequestExecutionFailed",
		nil,
	)
}

// failStep обрабатывает необратимый сбой шага исполнения. При включённом маркере
// компенсации (новое исполнение) переводит заявку в failed с кодом причины;
// прежнее исполнение (DefaultVersion) воспроизводит прежнее поведение —
// возвращает исходную ошибку, роняя workflow. Детерминизм реплея сохранён
// маркером changeFailureCompensation.
func failStep(
	workflowCtx workflow.Context,
	terminalIntentVersion workflow.Version,
	terminalChecksumVersion workflow.Version,
	version workflow.Version,
	activityCtx workflow.Context,
	params Params,
	base transitionInput,
	code string,
	state *Status,
	cause error,
) (Status, error) {
	if version < 1 {
		return *state, cause
	}
	return failRequest(workflowCtx, terminalIntentVersion, terminalChecksumVersion, activityCtx, params, base, code, state)
}

func terminalIntentChecksum(intent TerminalIntent) string {
	intent.Checksum = ""
	encoded, _ := json.Marshal(intent)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// executionDeadline возвращает абсолютный внутренний дедлайн исполнения:
// предельное время минус запас на компенсацию. Детерминирован: время — только
// workflow.Now, запас — из политики исполнения (часть payload). Если предел
// меньше запаса, дедлайн наступает немедленно — компенсация не может уложиться,
// и workflow сразу завершается с кодом workflow_timed_out, а не ждёт
// терминального kill Temporal.
func executionDeadline(ctx workflow.Context, policy ExecutionPolicy) time.Time {
	budget := policy.ExecutionTimeout - workflowTimeoutReserve
	if budget < 0 {
		budget = 0
	}
	return workflow.Now(ctx).Add(budget)
}

// workflowTimedOut сообщает, что внутренний дедлайн исполнения наступил.
func workflowTimedOut(ctx workflow.Context, deadline time.Time) bool {
	return !workflow.Now(ctx).Before(deadline)
}

// approvalPause возвращает срок паузы утверждения, ограниченный внутренним
// дедлайном исполнения: пауза не должна пережить дедлайн, иначе workflow не
// успеет выполнить компенсацию до терминального kill Temporal. Второй результат —
// признак того, что паузу ограничил именно дедлайн (а не срок утверждения): по
// нему различаются исходы «срок утверждения истёк» (cancelled) и «дедлайн
// workflow истёк» (failed). Детерминирован: время — только workflow.Now.
func approvalPause(ctx workflow.Context, approvalTTL time.Duration, deadline time.Time) (time.Duration, bool) {
	remaining := deadline.Sub(workflow.Now(ctx))
	if remaining < approvalTTL {
		if remaining < 0 {
			remaining = 0
		}
		return remaining, true
	}
	return approvalTTL, false
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

// compensationOpts строит опции activities-компенсаций (FailRequest/CancelRequest):
// повтор до успеха, потому что терминальный переход не должен теряться при
// транзиентном сбое хранилища или конфликте версии. Идемпотентность перехода
// решает домен (повтор — no-op), поэтому неограниченный повтор безопасен; доменные
// отказы (терминальное состояние, недопустимый переход) остаются non-retryable и
// не зацикливаются. Чистая функция от политики: тот же вход — те же опции.
func compensationOpts(policy ExecutionPolicy, bounded bool) workflow.ActivityOptions {
	retry := &temporal.RetryPolicy{
		MaximumAttempts:    0, // совместимость с уже записанными v1 histories
		InitialInterval:    time.Second,
		BackoffCoefficient: 2,
		MaximumInterval:    time.Minute,
	}
	options := workflow.ActivityOptions{StartToCloseTimeout: policy.TransitionStartToClose, RetryPolicy: retry}
	if bounded {
		options.ScheduleToCloseTimeout = policy.CompensationScheduleToClose
		options.RetryPolicy = &temporal.RetryPolicy{
			MaximumAttempts:    int32(policy.CompensationMaxAttempts),
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    time.Minute,
		}
	}
	return options
}

// withDefaults заполняет нулевые поля политики осмысленными значениями и
// согласует предел исполнения со сроком паузы: предел не меньше срока плюс запас
// на компенсацию (workflowTimeoutReserve), чтобы внутренний дедлайн не съедал
// срок утверждения, а резерв на компенсацию был гарантирован всегда.
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
	if p.CompensationMaxAttempts <= 0 {
		p.CompensationMaxAttempts = defaultCompensationMaxAttempts
	}
	if p.CompensationScheduleToClose <= 0 {
		p.CompensationScheduleToClose = defaultCompensationScheduleToClose
	}
	if p.ExecutionTimeout < p.ApprovalTTL {
		p.ExecutionTimeout = p.ApprovalTTL + workflowTimeoutReserve
	}
	return p
}
