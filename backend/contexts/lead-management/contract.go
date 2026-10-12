// Контракты контекста lead-management, публикуемые composition root'ом.
//
// Публичная поверхность модуля — корневой пакет сборки сервиса (README, «Карта
// репозитория»): cmd/lead-gen не импортирует internal-пакеты напрямую, а получает
// адаптеры контрактов через эти фабрики, как интеграционная платформа публикует
// NewFirecrawl и NewOpenAI.
package leadmanagement

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/adapters/verification/dns"
	"lead-gen/backend/contexts/lead-management/internal/adapters/verification/remote"
	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/lead-management/addressverification"
	"lead-gen/contracts/lead-management/deliverytarget"
	"lead-gen/contracts/lead-management/emailgeneration"
	"lead-gen/contracts/lead-management/leadsearch"
)

// LeadSearchDeps — зависимости фабрики in-proc адаптера контракта lead-search.
// Репозитории пула обязательны; Search и Crawl необязательны: без них поиск
// отвечает только по существующему пулу.
type LeadSearchDeps struct {
	// Repositories — адаптеры хранилища (карточки и контакты).
	Repositories Repositories
	// Budget — общий Firecrawl Guard factory. Без него дозабор честно недоступен.
	Budget budget.Factory
	// MaxCredits — настроенный per-task cap; 0 не отключает account admission.
	MaxCredits int
	// Clock — часы; nil — системное время.
	Clock ports.Clock
	// Limits — пределы обхода одного сайта.
	Limits CrawlLimits
	// MaxResults — верхняя граница результатов поиска (топ-N сайтов); 0 — значение
	// по умолчанию владельца.
	MaxResults int
	// Deadline — предельное время фазы краулинга; 0 — значение по умолчанию.
	Deadline time.Duration
	// Logger / Tracer — наблюдаемость; могут быть nil.
	Logger observ.Logger
	Tracer observ.Tracer
}

// NewLeadSearch собирает опубликованный контракт lead-search поверх use case
// поиска лидов под заявку: сначала пул, при нехватке — краулинг Firecrawl.
//
// Это публичная фабрика владельца поиска: точка сборки (cmd/lead-gen) не имеет
// доступа к внутренним пакетам контекста, поэтому прямой адаптер контракта
// объявлен здесь. При co-location он вызывается в процессе; при разделении
// сервисов та же форма даётся сетевым клиентом (leadsearch/httpport).
func NewLeadSearch(deps LeadSearchDeps) leadsearch.Port {
	if deps.Repositories.Profiles == nil {
		// Владелец не собран: честно сообщаем о недоступности, а не выдумываем
		// пустой успех.
		return leadsearch.InProc(leadsearch.InProcHandler{})
	}
	useCase := &app.SearchLeadsForRequest{
		Profiles:    deps.Repositories.Profiles,
		Contacts:    deps.Repositories.Contacts,
		Budget:      deps.Budget,
		MaxCredits:  deps.MaxCredits,
		Clock:       deps.Clock,
		Limits:      deps.Limits,
		MaxResults:  deps.MaxResults,
		Deadline:    deps.Deadline,
		Logger:      deps.Logger,
		Tracer:      deps.Tracer,
		ServiceName: ServiceID,
	}
	return leadsearch.InProc(leadsearch.InProcHandler{
		SearchFunc: func(ctx context.Context, query leadsearch.Query) (leadsearch.Result, error) {
			result, err := useCase.Run(ctx, app.SearchLeadsInput{
				RequestID: query.RequestID,
				TenantID:  query.TenantID,
				Criteria:  query.Criteria,
				Limit:     query.Limit,
			})
			if err != nil {
				return leadsearch.Result{}, leadSearchError(err)
			}
			leads := make([]leadsearch.LeadRef, 0, len(result.Leads))
			for _, lead := range result.Leads {
				leads = append(leads, leadsearch.LeadRef{ID: lead.CompanyID, Origin: string(lead.Origin)})
			}
			return leadsearch.Result{
				Found:         result.Found,
				FromPool:      result.FromPool,
				Crawled:       result.Crawled,
				Leads:         leads,
				PartialReason: result.PartialReason,
			}, nil
		},
	})
}

// leadSearchError переводит ошибку сценария поиска в исход опубликованного
// контракта: некорректный критерий — ErrRejected, прочее — ErrUnavailable. Ошибка
// не выдаётся за пустой успешный результат.
func leadSearchError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, app.ErrInvalidSearchCriteria):
		return fmt.Errorf("%w: %v", leadsearch.ErrRejected, err)
	default:
		return fmt.Errorf("%w: %v", leadsearch.ErrUnavailable, err)
	}
}

// EmailGenerationDeps — зависимости фабрики in-proc адаптера контракта
// email-generation. Репозитории владельца обязательны; LLM необязателен: без него
// генерация честно отвечает недоступностью.
type EmailGenerationDeps struct {
	// Repositories — адаптеры хранилища (карточки и контакты).
	Repositories Repositories
	// LLM — существующий порт LLM; nil — генерация недоступна.
	LLM llm.Port
}

// NewEmailGeneration собирает опубликованный контракт email-generation поверх
// use case генерации письма под лида и оффер: читает карточку и контакт, строит
// промпт и вызывает порт LLM.
//
// Это публичная фабрика владельца генерации: точка сборки (cmd/lead-gen) не имеет
// доступа к внутренним пакетам контекста, поэтому прямой адаптер контракта
// объявлен здесь. При co-location он вызывается в процессе; при разделении
// сервисов та же форма даётся сетевым клиентом (emailgeneration/httpport).
func NewEmailGeneration(deps EmailGenerationDeps) emailgeneration.Port {
	if deps.Repositories.Profiles == nil {
		// Владелец не собран: честно сообщаем о недоступности, а не выдумываем
		// письмо.
		return emailgeneration.InProc(emailgeneration.InProcHandler{})
	}
	useCase := &app.DraftEmailForLead{
		Profiles: deps.Repositories.Profiles,
		Contacts: deps.Repositories.Contacts,
		LLM:      deps.LLM,
	}
	return emailgeneration.InProc(emailgeneration.InProcHandler{
		GenerateFunc: func(ctx context.Context, request emailgeneration.Request) (emailgeneration.Email, error) {
			draft, err := useCase.Run(ctx, app.DraftEmailInput{
				RequestID:     request.RequestID,
				TenantID:      request.TenantID,
				LeadID:        request.LeadID,
				Criteria:      request.Criteria,
				Language:      request.Language,
				Tone:          request.Tone,
				SenderCompany: request.SenderCompany,
			})
			if err != nil {
				return emailgeneration.Email{}, emailGenerationError(err)
			}
			return emailgeneration.Email{
				Subject:      draft.Subject,
				Body:         draft.Body,
				Language:     draft.Language,
				RecipientRef: draft.RecipientRef,
			}, nil
		},
	})
}

// emailGenerationError переводит ошибку сценария генерации в исход
// опубликованного контракта: некорректный запрос (пустой оффер, несуществующий
// лид) — ErrRejected, прочее (в том числе отсутствие порта LLM) — ErrUnavailable.
// Ошибка не выдаётся за готовое письмо.
func emailGenerationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, app.ErrInvalidEmailRequest):
		return fmt.Errorf("%w: %v", emailgeneration.ErrRejected, err)
	default:
		return fmt.Errorf("%w: %v", emailgeneration.ErrUnavailable, err)
	}
}

// DeliveryTargetDeps — зависимости фабрики in-proc адаптера контракта
// delivery-target. Контакты обязательны; TenantID — тенант, которому принадлежит
// хранилище контактов (красная зона — контур клиента).
type DeliveryTargetDeps struct {
	// Repositories — адаптеры хранилища (контакты).
	Repositories Repositories
	// TenantID — тенант-владелец ссылки; пусто — резолвер честно недоступен.
	TenantID string
}

// NewDeliveryTarget собирает опубликованный контракт delivery-target поверх use
// case разрешения ссылки получателя в адрес по контактам.
func NewDeliveryTarget(deps DeliveryTargetDeps) deliverytarget.Resolver {
	if deps.Repositories.Contacts == nil {
		// Владелец не собран: честно сообщаем о недоступности, а не выдумываем
		// адрес.
		return deliverytarget.InProc(deliverytarget.InProcHandler{})
	}
	useCase := &app.ResolveDeliveryTarget{
		Contacts: deps.Repositories.Contacts,
		TenantID: deps.TenantID,
	}
	return deliverytarget.InProc(deliverytarget.InProcHandler{
		ResolveFunc: func(ctx context.Context, tenantID, recipientRef string) (deliverytarget.Recipient, error) {
			recipient, err := useCase.Run(ctx, app.ResolveRecipientInput{
				TenantID:     tenantID,
				RecipientRef: recipientRef,
			})
			if err != nil {
				return deliverytarget.Recipient{}, deliveryTargetError(err)
			}
			return deliverytarget.Recipient{Address: recipient.Address}, nil
		},
	})
}

// deliveryTargetError переводит ошибку сценария разрешения в исход опубликованного
// контракта: неразрешимая ссылка — ErrRejected, прочее — ErrUnavailable. Ошибка не
// выдаётся за разрешённый адрес.
func deliveryTargetError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, app.ErrInvalidDeliveryTarget):
		return fmt.Errorf("%w: %v", deliverytarget.ErrRejected, err)
	default:
		return fmt.Errorf("%w: %v", deliverytarget.ErrUnavailable, err)
	}
}

// AddressVerificationDeps — зависимости фабрики in-proc адаптера контракта
// address-verification. Контакты обязательны; проверка адреса без внешнего
// сервиса и DNS честно недоступна, а не выдаётся за подтверждённый адрес.
type AddressVerificationDeps struct {
	// Repositories — адаптеры хранилища (контакты).
	Repositories Repositories
	// TenantID — тенант-владелец ссылки; пусто — владелец честно недоступен.
	TenantID string
	// Verifier — внешний сервис проверки адресов. nil — собирается по
	// VerifierEndpoint; при пустом адресе проверка честно недоступна.
	Verifier ports.AddressVerifier
	// VerifierEndpoint — адрес внешнего сервиса проверки (когда Verifier не задан).
	VerifierEndpoint string
	// VerifierAPIKey — ключ доступа к сервису проверки; пусто — не отправляется.
	VerifierAPIKey string
	// DNS — резолвер домена; nil — системный резолвер Go.
	DNS ports.DNSResolver
	// Clock — часы; nil — системное время.
	Clock ports.Clock
}

// NewAddressVerification собирает опубликованный контракт address-verification
// поверх use case разрешения получателя и верификации адреса владельца.
//
// Это публичная фабрика владельца верификации: точка сборки (cmd/lead-gen) не
// имеет доступа к внутренним пакетам контекста, поэтому прямой адаптер контракта
// объявлен здесь. При co-location он вызывается в процессе; сетевой транспорт —
// точка расширения (design.md, «Отложено»).
//
// Снимок несёт статус и вердикт владельца «адрес подтверждён»: вердикт выводится
// из политики владельца по статусу (DecisionAllow), а не выдумывается
// потребителем. Неразрешимая ссылка — ErrRejected, недоступность владельца или
// сервиса проверки — ErrUnavailable; выдуманный «подтверждён» не возвращается.
func NewAddressVerification(deps AddressVerificationDeps) addressverification.Port {
	if deps.Repositories.Contacts == nil {
		// Владелец не собран: честно сообщаем о недоступности, а не выдумываем
		// статус верификации.
		return addressverification.InProc(addressverification.InProcHandler{})
	}
	verifier := deps.Verifier
	if verifier == nil {
		verifier = remote.New(remote.Config{Endpoint: deps.VerifierEndpoint, APIKey: deps.VerifierAPIKey})
	}
	resolverDNS := deps.DNS
	if resolverDNS == nil {
		resolverDNS = dns.New()
	}
	resolve := &app.ResolveDeliveryTarget{Contacts: deps.Repositories.Contacts, TenantID: deps.TenantID}
	verify := &app.VerifyAddress{DNS: resolverDNS, Verifier: verifier, Clock: deps.Clock}
	policy := domain.DefaultVerificationPolicy()
	return addressverification.InProc(addressverification.InProcHandler{
		VerificationFunc: func(ctx context.Context, q addressverification.Query) (addressverification.Snapshot, error) {
			if err := q.Validate(); err != nil {
				return addressverification.Snapshot{}, err
			}
			recipient, err := resolve.Run(ctx, app.ResolveRecipientInput{TenantID: q.TenantID, RecipientRef: q.RecipientRef})
			if err != nil {
				return addressverification.Snapshot{}, addressVerificationError(err)
			}
			checked, err := verify.Run(ctx, app.VerifyAddressInput{Address: recipient.Address})
			if err != nil {
				return addressverification.Snapshot{}, addressVerificationError(err)
			}
			decision, _ := policy.Decision(checked.Status)
			return addressverification.Snapshot{
				RecipientRef: q.RecipientRef,
				Status:       addressverification.Status(checked.Status),
				Verified:     decision == domain.DecisionAllow,
				Reason:       checked.Reason,
				CheckedAt:    checked.CheckedAt,
			}, nil
		},
	})
}

// addressVerificationError переводит ошибку сценария в исход опубликованного
// контракта: неразрешимая ссылка — ErrRejected, прочее — ErrUnavailable. Ошибка не
// выдаётся за подтверждённый адрес.
func addressVerificationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, app.ErrInvalidDeliveryTarget):
		return fmt.Errorf("%w: %v", addressverification.ErrRejected, err)
	default:
		return fmt.Errorf("%w: %v", addressverification.ErrUnavailable, err)
	}
}
