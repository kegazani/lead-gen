package workflowadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/content-production/contentrequest"
	"lead-gen/contracts/lead-management/leadsearch"
	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
)

// defaultActor — автор перехода, если сигнал не назвал человека (автоматический
// шаг workflow).
const defaultActor = domain.ActorID("workflow")

// ErrSearchContractMissing — шаг поиска не может выполниться из-за конфигурации:
// контракт lead-search (или репозиторий заявок) не подключён. Это постоянный
// отказ: повтор не исправит отсутствующую конфигурацию, поэтому activity помечает
// его non-retryable, не смешивая с транзиентной недоступностью владельца
// (leadsearch.ErrUnavailable).
var ErrSearchContractMissing = errors.New("activity: контракт lead-search не подключён")

// SearchInput — вход шага поиска лидов. Только идентификаторы.
type SearchInput struct {
	RequestID string
	TenantID  string
	Target    int
	Round     int
}

// SearchOutcome — результат шага поиска: сколько лидов найдено и ссылки на них.
// Несёт только идентификаторы компаний: карточек и персональных данных контракт
// не отдаёт.
type SearchOutcome struct {
	// Found — всего найдено лидов.
	Found int
	// Leads — ссылки на найденные компании (идентификатор и происхождение).
	Leads []leadsearch.LeadRef
	// PartialReason — безопасная причина, по которой поиск не смог завершить
	// дозабор. Пусто означает, что известной причины неполноты нет.
	PartialReason string
}

// SearchStub — источник шага поиска лидов: либо заглушка (детерминированно
// сообщает, сколько лидов «нашлось», не ходя в сеть), либо адаптер опубликованного
// контракта lead-search (ContractSearch). Заглушка не выдаёт себя за настоящий
// поиск: она названа явно и заменяется адаптером контракта.
type SearchStub interface {
	Search(ctx context.Context, in SearchInput) (SearchOutcome, error)
}

// FixedSearch — заглушка, возвращающая заданное число найденных лидов. Заглушка
// выдаёт детерминированные ссылки на лидов (stub-N), чтобы цикл добора считал
// уникальные лиды так же, как с настоящим контрактом, а не суммировал сырые
// счётчики.
type FixedSearch struct{ Leads int }

// Search реализует SearchStub.
func (s FixedSearch) Search(context.Context, SearchInput) (SearchOutcome, error) {
	if s.Leads <= 0 {
		return SearchOutcome{}, nil
	}
	leads := make([]leadsearch.LeadRef, 0, s.Leads)
	for i := 1; i <= s.Leads; i++ {
		leads = append(leads, leadsearch.LeadRef{ID: fmt.Sprintf("stub-%d", i), Origin: leadsearch.OriginPool})
	}
	return SearchOutcome{Found: s.Leads, Leads: leads}, nil
}

// NoSearch — поиск не подключён: шаг честно сообщает ноль найденных лидов.
type NoSearch struct{}

// Search реализует SearchStub.
func (NoSearch) Search(context.Context, SearchInput) (SearchOutcome, error) {
	return SearchOutcome{}, nil
}

// ContractSearch — SearchStub поверх опубликованного контракта lead-search
// (владелец — lead-management). Заявку читает из репозитория контекста, критерий
// собирает из оффера, а у владельца просит лиды под заявку. Отказ владельца и сбой
// транспорта сохраняются в цепочке ошибок (errors.Is): activity не подменяет их
// успехом и различает исход при выборе ретраев.
type ContractSearch struct {
	// Requests — репозиторий заявок: источник оффера по RequestID.
	Requests ports.RequestRepository
	// Contract — порт опубликованного контракта lead-search.
	Contract leadsearch.Port
}

// Search реализует SearchStub: читает заявку, собирает критерий из оффера и
// вызывает контракт. Число найденных и ссылки на лидов берутся из Result владельца.
func (s ContractSearch) Search(ctx context.Context, in SearchInput) (SearchOutcome, error) {
	if s.Requests == nil {
		return SearchOutcome{}, ErrSearchContractMissing
	}
	if s.Contract == nil {
		return SearchOutcome{}, fmt.Errorf("%w: контракт lead-search не подключён", ErrSearchContractMissing)
	}
	request, err := s.Requests.FindByID(ctx, domain.RequestID(in.RequestID))
	if err != nil {
		return SearchOutcome{}, fmt.Errorf("чтение заявки %s: %w", in.RequestID, err)
	}
	result, err := s.Contract.Search(ctx, leadsearch.Query{
		RequestID: in.RequestID,
		TenantID:  in.TenantID,
		Criteria:  criteriaForRound(request.Offer(), in.Round),
		Limit:     in.Target,
	})
	if err != nil {
		return SearchOutcome{}, err
	}
	return SearchOutcome{Found: result.Found, Leads: result.Leads, PartialReason: result.PartialReason}, nil
}

// criteriaFromOffer берёт описание желаемых лидов; оффер нужен письму, но его
// услуги в поиске приводят к агентствам-конкурентам вместо клиентов.
func criteriaFromOffer(offer *domain.Offer) string {
	if offer == nil {
		return ""
	}
	if goal := strings.TrimSpace(offer.Target().Description()); goal != "" {
		return goal
	}
	return strings.TrimSpace(offer.Content().Text())
}

// criteriaForRound уточняет существующий тематический поиск при доборе. Разные
// строки дают отдельные budget scopes у владельца и не повторяют топ первого раунда.
func criteriaForRound(offer *domain.Offer, round int) string {
	criteria := criteriaFromOffer(offer)
	switch round {
	case 1:
		return criteria + " official company websites"
	case 2:
		return criteria + " companies official websites -reddit -linkedin -facebook -youtube"
	default:
		return criteria
	}
}

// Activities — тонкие адаптеры к application services контекста. Доменной
// логики здесь нет: допустимость перехода решает aggregate root request, а
// инварианты — прикладной слой. Повторный переход из терминального состояния
// вернёт доменную ошибку — activity не подменяет её успехом.
type Activities struct {
	StartProcessing *command.StartProcessingHandler
	Fulfill         *command.FulfillRequestHandler
	Cancel          *command.CancelRequestHandler
	// Fail переводит заявку processing → failed при необратимом сбое шага
	// исполнения. nil — не собран: компенсация сбоя не выполняется, о чём activity
	// честно предупреждает.
	Fail *command.FailRequestHandler
	// RecordCampaign фиксирует кампанию заявки после передачи писем — чтобы
	// события доставки можно было сопоставить с заявкой. nil — не собран: передача
	// не сможет зафиксировать кампанию, о чём activity честно предупреждает.
	RecordCampaign *command.RecordCampaignHandler
	Search         SearchStub
	Drafts         DraftGenerator
	Handoff        HandoffSender
	Clock          ports.Clock
	IDs            ports.IDGenerator

	// HeartbeatInterval — период отправки heartbeat длинной activity. Ноль —
	// значение по умолчанию. Как правило выводится из настроенного HeartbeatTimeout
	// (heartbeatInterval), чтобы heartbeat не опаздывал при коротком таймауте.
	HeartbeatInterval time.Duration

	// Logger — логгер сервиса. nil — без логирования исходов activity.
	Logger observ.Logger
}

// HandleStartProcessing переводит заявку validated → processing. Повторная
// доставка, когда заявка уже в processing, возвращает успех (no-op решает домен).
func (a *Activities) HandleStartProcessing(ctx context.Context, in transitionInput) error {
	if a.StartProcessing == nil {
		return notConfigured("activity: сценарий запуска исполнения не собран")
	}
	_, err := a.StartProcessing.Handle(ctx, command.StartProcessing{LifecycleFields: a.fields(in)})
	return a.wrapTransition(ActivityStartProcessing, in, err)
}

// HandleFulfillRequest переводит заявку processing → fulfilled. Повторная
// доставка, когда заявка уже fulfilled, — успех (no-op решает домен).
func (a *Activities) HandleFulfillRequest(ctx context.Context, in transitionInput) error {
	if a.Fulfill == nil {
		return notConfigured("activity: сценарий завершения исполнения не собран")
	}
	_, err := a.Fulfill.Handle(ctx, command.FulfillRequest{LifecycleFields: a.fields(in)})
	return a.wrapTransition(ActivityFulfillRequest, in, err)
}

// HandleCancelRequest переводит заявку из активного статуса в cancelled.
// Повторная доставка, когда заявка уже cancelled, — успех (no-op решает домен).
func (a *Activities) HandleCancelRequest(ctx context.Context, in transitionInput) error {
	if a.Cancel == nil {
		return notConfigured("activity: сценарий отмены не собран")
	}
	_, err := a.Cancel.Handle(ctx, command.CancelRequest{
		LifecycleFields: a.fields(in),
		Reason:          reasonCode(in.ReasonCode, "утверждение: отказ"),
	})
	return a.wrapTransition(ActivityCancelRequest, in, err)
}

// HandleFailRequest переводит заявку processing → failed с кодом и текстом
// причины сбоя. Повторная доставка, когда заявка уже failed, — успех (no-op
// решает домен); из другого терминального статуса — доменная ошибка.
func (a *Activities) HandleFailRequest(ctx context.Context, in transitionInput) error {
	if a.Fail == nil {
		return notConfigured("activity: сценарий сбоя исполнения не собран")
	}
	_, err := a.Fail.Handle(ctx, command.FailRequest{
		LifecycleFields: a.fields(in),
		ReasonCode:      in.ReasonCode,
		Reason:          reasonCode(in.Reason, reasonCode(in.ReasonCode, "исполнение прервано")),
	})
	return a.wrapTransition(ActivityFailRequest, in, err)
}

// SearchLeads выполняет шаг поиска лидов. Источник — подключённый контракт
// lead-search (ContractSearch) либо прежняя заглушка (SearchStub). Доменный отказ
// владельца не ретраится, а сбой транспорта — ретраится по политике activity.
// Наружу activity отдаёт число найденных и идентификаторы лидов: карточек и PII в
// payload нет.
func (a *Activities) SearchLeads(ctx context.Context, in searchInput) (searchResult, error) {
	if a.Search == nil {
		return searchResult{}, notConfigured("activity: шаг поиска не собран")
	}
	stop := startHeartbeat(ctx, heartbeatInterval(a.HeartbeatInterval))
	defer stop()
	outcome, err := a.Search.Search(ctx, SearchInput{
		RequestID: in.RequestID,
		TenantID:  in.TenantID,
		Target:    in.Target,
		Round:     in.Round,
	})
	if err != nil {
		switch {
		case errors.Is(err, leadsearch.ErrRejected):
			return searchResult{}, nonRetryable(fmt.Sprintf("activity: поиск лидов: %v", err), "LeadSearchRejected", err)
		case errors.Is(err, ports.ErrRequestNotFound):
			// Заявка не найдена — постоянный отказ: повтор не восстановит её.
			return searchResult{}, nonRetryable(fmt.Sprintf("activity: поиск лидов: %v", err), "RequestNotFound", err)
		case errors.Is(err, ErrSearchContractMissing):
			return searchResult{}, nonRetryable(fmt.Sprintf("activity: поиск лидов: %v", err), "SearchContractMissing", err)
		default:
			// Транзиентный сбой транспорта — retryable: повтор осмыслен.
			return searchResult{}, fmt.Errorf("activity: поиск лидов: %w", err)
		}
	}
	result := searchResult{Found: outcome.Found, LeadIDs: make([]string, 0, len(outcome.Leads)), PartialReason: outcome.PartialReason}
	for _, lead := range outcome.Leads {
		result.LeadIDs = append(result.LeadIDs, lead.ID)
	}
	return result, nil
}

// DraftEmails выполняет шаг генерации черновиков писем под найденных лидов.
// Источник — подключённый контракт email-generation (ContractDrafts); если он не
// подключён, шаг штатно пропускается и логируется, а заявка идёт дальше. Ошибки
// генерации по отдельному лиду не роняют шаг: они собираются частично и видны
// счётчиком Failed.
func (a *Activities) DraftEmails(ctx context.Context, in draftInput) (draftResult, error) {
	if a.Drafts == nil {
		if in.Editorial {
			return draftResult{}, notConfigured("редакторская генерация: контракт email-generation не подключён")
		}
		if a.Logger != nil {
			a.Logger.Info("activity: шаг генерации писем пропущен: контракт email-generation не подключён",
				observ.Fields{}, "activity", ActivityDraftEmails, "request_id", in.RequestID)
		}
		return draftResult{}, nil
	}
	stop := startHeartbeat(ctx, heartbeatInterval(a.HeartbeatInterval))
	defer stop()
	result, err := a.Drafts.Draft(ctx, DraftInput{
		RequestID: in.RequestID,
		TenantID:  in.TenantID,
		LeadIDs:   in.LeadIDs,
		Editorial: in.Editorial,
	})
	if err != nil {
		return draftResult{}, fmt.Errorf("activity: генерация черновиков писем: %w", err)
	}
	if a.Logger != nil {
		a.Logger.Info("activity: черновики писем сгенерированы",
			observ.Fields{}, "activity", ActivityDraftEmails, "request_id", in.RequestID,
			"drafted", result.Drafted, "failed", result.Failed)
	}
	return draftResult{Drafted: result.Drafted, Failed: result.Failed, LeadIDs: result.LeadIDs}, nil
}

// HandoffDrafts выполняет шаг передачи черновиков писем заявки в
// outreach-orchestration. Источник — подключённые контракты content-request и
// outreach-handoff (ContractHandoff); если они не подключены, шаг штатно
// пропускается и логируется, а заявка идёт дальше — как шаг генерации писем без
// контракта. Доменный отказ контракта помечается non-retryable, а сбой транспорта
// остаётся retryable, чтобы недоступность не подменялась успехом.
func (a *Activities) HandoffDrafts(ctx context.Context, in handoffInput) (handoffOutcome, error) {
	if a.Handoff == nil {
		if a.Logger != nil {
			a.Logger.Info("activity: шаг передачи писем пропущен: контракты материала/передачи не подключены",
				observ.Fields{}, "activity", ActivityHandoffDrafts, "request_id", in.RequestID)
		}
		return handoffOutcome{}, nil
	}
	stop := startHeartbeat(ctx, heartbeatInterval(a.HeartbeatInterval))
	defer stop()
	result, err := a.Handoff.Handoff(ctx, HandoffInput{
		RequestID: in.RequestID,
		TenantID:  in.TenantID,
		Actor:     in.Actor,
		Name:      in.Name,
	})
	if err != nil {
		switch {
		case errors.Is(err, outreachhandoff.ErrRejected), errors.Is(err, contentrequest.ErrRejected):
			return handoffOutcome{}, nonRetryable(fmt.Sprintf("activity: передача писем: %v", err), "HandoffRejected", err)
		default:
			// Транзиентный сбой транспорта — retryable: повтор осмыслен.
			return handoffOutcome{}, fmt.Errorf("activity: передача писем: %w", err)
		}
	}
	if a.Logger != nil {
		a.Logger.Info("activity: черновики писем переданы",
			observ.Fields{}, "activity", ActivityHandoffDrafts, "request_id", in.RequestID,
			"handed", result.Handed, "skipped", result.Skipped, "repeated", result.Repeated)
	}
	// Фиксация кампании заявки: без неё событие доставки нельзя сопоставить с
	// заявкой (request-delivery-conclusion). Повтор идемпотентен (RecordCampaign
	// — no-op на тот же идентификатор), поэтому повтор activity безопасен.
	if result.CampaignID != "" {
		if a.RecordCampaign == nil {
			if a.Logger != nil {
				a.Logger.Warn("activity: кампания заявки не зафиксирована: сценарий не собран",
					observ.Fields{}, "activity", ActivityHandoffDrafts, "request_id", in.RequestID,
					"campaign_id", result.CampaignID)
			}
		} else {
			if _, err := a.RecordCampaign.Handle(ctx, command.RecordCampaign{
				RequestID:  domain.RequestID(in.RequestID),
				TenantID:   domain.TenantID(in.TenantID),
				CampaignID: domain.CampaignID(result.CampaignID),
			}); err != nil {
				return handoffOutcome{}, fmt.Errorf("activity: фиксация кампании заявки %s: %w", in.RequestID, err)
			}
		}
	}
	return handoffOutcome{
		Handed:     result.Handed,
		Skipped:    result.Skipped,
		Repeated:   result.Repeated,
		CampaignID: result.CampaignID,
		Expected:   result.Expected,
	}, nil
}

// fields собирает поля перехода из входа activity, подставляя часы контекста.
func (a *Activities) fields(in transitionInput) command.LifecycleFields {
	at := time.Now()
	if a.Clock != nil {
		at = a.Clock.Now()
	}
	actor := domain.ActorID(in.Actor)
	if actor == "" {
		actor = defaultActor
	}
	return command.LifecycleFields{
		RequestID:     domain.RequestID(in.RequestID),
		TenantID:      domain.TenantID(in.TenantID),
		Actor:         actor,
		At:            at,
		CorrelationID: domain.CorrelationID(in.CorrelationID),
		CausationID:   domain.CausationID(in.CausationID),
	}
}

// wrapTransition дополняет ошибку контекстом activity и логирует исход. Успех —
// nil. Постоянные отказы (доменный запрет перехода, терминальное состояние,
// отсутствие причины, заявка не найдена, чужой тенант) помечаются non-retryable:
// повтор их не исправит, а лишние попытки задерживают исход. Транзиентные сбои
// (конфликт версии, недоступность хранилища) остаются retryable.
func (a *Activities) wrapTransition(name string, in transitionInput, err error) error {
	if err == nil {
		if a.Logger != nil {
			a.Logger.Info("переход заявки выполнен", observ.Fields{
				TenantID:      in.TenantID,
				CorrelationID: in.CorrelationID,
				CausationID:   in.CausationID,
			}, "activity", name, "request_id", in.RequestID)
		}
		return nil
	}
	cause := fmt.Errorf("activity %s: переход заявки %s: %w", name, in.RequestID, err)
	if isPermanentTransitionFailure(err) {
		return nonRetryable(cause.Error(), "RequestTransitionRejected", cause)
	}
	return cause
}

// isPermanentTransitionFailure сообщает, что отказ перехода постоянен: повтор той
// же activity при том же состоянии не изменит исход.
func isPermanentTransitionFailure(err error) bool {
	return errors.Is(err, domain.ErrInvalidTransition) ||
		errors.Is(err, domain.ErrTerminalState) ||
		errors.Is(err, domain.ErrReasonRequired) ||
		errors.Is(err, domain.ErrInvalidReasonCode) ||
		errors.Is(err, ports.ErrRequestNotFound) ||
		errors.Is(err, command.ErrTenantMismatch)
}

// nonRetryable оборачивает постоянный отказ в ApplicationError, который Temporal
// не повторяет. Причина сохраняется в цепочке (errors.Is), чтобы вызывающий
// различал доменный исход.
func nonRetryable(message, kind string, cause error) error {
	return temporal.NewNonRetryableApplicationError(message, kind, cause)
}

// notConfigured сообщает о несобранной зависимости activity. Это постоянный
// отказ: повтор не соберёт отсутствующий сценарий.
func notConfigured(message string) error {
	return temporal.NewNonRetryableApplicationError(message, "ActivityNotConfigured", nil)
}

// reasonCode возвращает код причины, подставляя значение по умолчанию.
func reasonCode(code, fallback string) string {
	if code == "" {
		return fallback
	}
	return code
}
