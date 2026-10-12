package outreachorchestration

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/access"
	httpapi "lead-gen/backend/contexts/outreach-orchestration/internal/adapters/http"
	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/idempotency"
	outboxinmemory "lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/relay"
	persistence "lead-gen/backend/contexts/outreach-orchestration/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/riskfacts"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/batchlimits"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	appeditorial "lead-gen/backend/contexts/outreach-orchestration/internal/app/editorialreview"
	publicationapp "lead-gen/backend/contexts/outreach-orchestration/internal/app/publication"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/query"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/scheduling"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/readiness"
	"lead-gen/contracts/content-production/contentversion"
	"lead-gen/contracts/data-governance/suppressionpolicy"
	"lead-gen/contracts/lead-management/addressverification"
	"lead-gen/contracts/lead-management/deliverytarget"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
	"lead-gen/contracts/platform-governance/accessdecision"
	"lead-gen/frontend/components"
)

// Сборка сервиса outreach-orchestration для его ролей (api, worker и др.).
//
// Здесь только соединение адаптеров, портов и use cases: ни одного бизнес-правила
// и ни одного решения о допустимости. Инварианты обеспечивает aggregate root
// campaign, транзакцию и доступ — прикладной слой, а сборка лишь выбирает
// реализации и передаёт зависимости. Каждый сервис собирает только свой контекст
// (design.md, Решение 6), поэтому пакет вправе импортировать adapters своего
// контекста, а другие контексты — только через транспорт и опубликованные
// контракты владельца.
//
// Хранилище подключается через Params.Persistence: по умолчанию — прежние
// in-memory адаптеры (тесты и dev), а при заданном DSN точка входа передаёт
// PostgreSQL-адаптеры (NewPostgresPersistence). Движок последовательностей пишет
// агрегат и события атомарно через порт ports.UnitOfWork в обоих случаях.

// Grant — выданное право актора действовать в тенанте. Права задаёт вызывающий:
// composition не выдумывает стартовую политику доступа.
type Grant struct {
	Actor  domain.ActorID
	Tenant domain.TenantID
}

// SystemClock — часы процесса, источник времени по умолчанию. Не адаптер
// внешней системы: используется, когда вызывающий не передал свой Clock.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// SinkFunc — функция, реализующая relay.Sink. Позволяет собрать публикацию без
// отдельного адаптера: процесс и тест передают своё назначение доставки, а
// composition остаётся свободным от конкретной внешней системы.
type SinkFunc func(ctx context.Context, action contracts.ApprovedAction) error

// Publish реализует relay.Sink. Нулевая функция ничего не доставляет и не
// считается ошибкой.
func (f SinkFunc) Publish(ctx context.Context, action contracts.ApprovedAction) error {
	if f == nil {
		return nil
	}
	return f(ctx, action)
}

// RiskFacts — внешний предикат признаков риска черновика (псевдоним доменного
// типа). Введён, чтобы точка входа (cmd/lead-gen) задавала поле без импорта
// внутренних пакетов контекста.
type RiskFacts = domain.CanBeDecidedInPackage

// RiskFactsPorts — владельческие порты, из которых сборщик берёт факты риска
// черновика. Каждый порт — опубликованный контракт владельца: адрес получателя
// (`delivery-target`, lead-management), стоп-лист (`suppression-policy`,
// data-governance) и статус верификации адреса (`address-verification`,
// lead-management).
//
// Незаданный порт не подменяется успехом: сборщик честно отказывает при
// обращении, поэтому пакетное утверждение становится недоступным, а не идёт «без
// проверки» (AGENTS.md §3).
type RiskFactsPorts struct {
	// Recipients — разрешение непрозрачной ссылки получателя в адрес владельца
	// lead-management (контракт delivery-target).
	Recipients deliverytarget.Resolver
	// Suppression — проверка стоп-листа у владельца data-governance (контракт
	// suppression-policy).
	Suppression suppressionpolicy.Port
	// Verification — статус верификации адреса получателя у владельца
	// lead-management (контракт address-verification).
	Verification addressverification.Port
}

// NewRiskFacts собирает внешний предикат признаков риска пакетного пути поверх
// портов владельцев. Сборщик живёт вне домена: он разрешает адрес получателя,
// проверяет стоп-лист и статус верификации и передаёт домену только признаки.
//
// Незаданные порты честно недоступны: предикат возвращает ошибку, а не «рисков
// нет». Значение передаётся в Params.Risk — предикат риска обязателен и явен.
func NewRiskFacts(ports RiskFactsPorts) RiskFacts {
	return riskfacts.New(ports.Recipients, ports.Suppression, ports.Verification).RiskReasons
}

// Params — параметры сборки среза. Хранилище задаётся через Persistence:
// нулевое значение означает in-memory адаптеры (прежнее поведение), заданное —
// адаптеры PostgreSQL. Сборка принимает порты, а не конкретные реализации,
// поэтому точка входа подключает нужное хранилище без правки среза.
type Params struct {
	// Clock — источник времени. nil → SystemClock.
	Clock ports.Clock

	// Grants — права доступа dev/test-двойника. По умолчанию доступ закрыт.
	// Используются только когда не задан Access (dev-сборка без владельца).
	Grants []Grant

	// Access — решение о доступе из контракта access-decision владельца
	// platform-governance. Продуктовый путь задаёт его явно: точка сборки
	// подключает решение владельца (реальный или честно недоступный). nil —
	// dev-сборка на правах Grants (тесты/dev); молчаливое «разрешено» не ставится.
	Access accessdecision.Decider

	// Persistence — адаптеры хранилища среза. Нулевое значение означает
	// in-memory-сборку (тесты и dev); заполненное — PostgreSQL-адаптеры,
	// полученные из NewPostgresPersistence. Частично заданная конфигурация
	// отвергается: собрать половину на in-memory, половину на PostgreSQL нельзя.
	Persistence Persistence
	// ContentVersions — точный read source владельца content-production; не
	// извлекается из campaign latest и не зависит от risk screening.
	ContentVersions contentversion.Source

	// Sink — назначение публикации approved-action. nil означает, что relay не
	// собирается: процесс не публикует контракт (например, api).
	Sink relay.Sink

	// ApprovedActionSink — явное назначение релиза approved-action в форме
	// функции (SinkFunc): позволяет подменить транспортный Sink и направить
	// утверждённые действия внешнему получателю. Приоритет выбора назначения:
	// Sink → ApprovedActionSink → ApprovedActionConsumer. nil — не задан.
	ApprovedActionSink SinkFunc

	// ApprovedActionTransport — способ связи синхронного контракта
	// approved-action с потребителем. Пустое значение — TransportInProcess:
	// совмещённый деплой, где сервисы co-located. Значение приходит из топологии
	// (механизм группы процессов deploy/services.yaml), а не выбирается кодом
	// потребителя.
	ApprovedActionTransport Transport

	// ApprovedActionConsumer — внешний потребитель утверждённых действий
	// (например, мост в message-delivery), реализующий потребительский интерфейс
	// контракта approvedaction.Publisher. Задан — сборка связывает с контрактом
	// именно его выбранным транспортом, и за Slice.ApprovedActionPublisher стоит
	// реальный потребитель; не задан — прежнее поведение: владелец собирает свой
	// in-memory publication-сервис (Slice.Publication).
	//
	// Кроме того, потребитель может служить назначением релиза: если не заданы
	// ни Sink, ни ApprovedActionSink, relay доставляет утверждённые действия ему,
	// а не транспортному Sink (см. ApprovedActionSink).
	ApprovedActionConsumer approvedaction.Publisher

	// ApprovedActionCanceller — владелец доставки, которому после commit owner
	// tombstone синхронно передаётся campaign cancellation fence. Отсутствие
	// допустимо только для dev/test локального fence; production wiring остаётся
	// blocker, пока реальный Canceller не подключён composition root.
	ApprovedActionCanceller approvedaction.Canceller

	// Risk — внешний предикат признаков риска пакетного пути. ОБЯЗАТЕЛЕН:
	// Build возвращает ошибку, если предикат не задан. Тихий nil-дефолт «рисков
	// нет» недопустим — домен при nil трактует факты как недоступные, поэтому
	// незаданный предикат молча отключил бы исключение рисковых черновиков.
	//
	// Значение инъецируется вызывающим: composition не вычисляет признаки сам
	// и не выводит политику. Один и тот же предикат получают все пакетные
	// сценарии, чтобы решение отвергало то, что исключил состав. Реальный
	// предикат собирает NewRiskFacts поверх портов владельцев.
	Risk domain.CanBeDecidedInPackage

	// SequenceLimits — ограничения участия лида в цепочках (предел касаний за
	// период и объём неутверждённых черновиков). Нулевое значение означает
	// ограничения по умолчанию; частично заданная конфигурация отвергается.
	SequenceLimits domain.ParticipationLimits

	// ConsoleAuth — учётные данные оператора консольных экранов рассылок.
	// Пустое значение означает, что консоль не настроена: экраны честно
	// отвечают 503, а вход закрыт. Значение задаёт точка входа.
	ConsoleAuth consoleauth.Config

	// Console — базовые адреса сервисов консоли для общей навигации по разделам.
	// Пустая карта даёт относительные пути.
	Console components.ConsoleBases

	// BasePath — внешний префикс пути консоли под единым origin (admin-proxy).
	// Пусто — корень origin.
	BasePath string

	// ExternalPrefixes — префиксы других консолей единого origin: ссылки на них
	// в навигации не переписываются под собственный префикс.
	ExternalPrefixes []string
}

// Slice — собранный контекст среза: порты, use cases, HTTP-обработчики и
// публикация. Поля экспортированы, чтобы процесс мог запустить нужную часть.
type Slice struct {
	// Repository — хранилище кампаний (in-memory или PostgreSQL). Срез зависит
	// от портов, поэтому реализация выбирается сборкой.
	Repository CampaignStore
	// Journal — журнал outbox со стороны публикации (in-memory или PostgreSQL).
	Journal relay.Source
	// UnitOfWork — транзакция записи агрегата и его событий (in-memory или
	// PostgreSQL).
	UnitOfWork  ports.UnitOfWork
	Access      ports.AccessDecision
	Idempotency *idempotency.Store

	// Relay не равен nil только если при сборке задан Sink.
	Relay *relay.Relay

	// Server — HTTP-обработчики команд и запросов среза. Реализует http.Handler.
	Server *httpapi.Server

	CreateCampaign *command.CreateCampaignHandler
	DraftAction    *command.DraftActionHandler
	ApproveDraft   *command.ApproveDraftHandler
	RejectDraft    *command.RejectDraftHandler
	ReviseDraft    *command.ReviseDraftHandler
	ExpireApproval *command.ExpireApprovalHandler
	ApprovalQueue  *query.ApprovalQueueHandler
	DraftDecision  *query.DraftDecisionHandler
	BatchQueue     *query.BatchQueueHandler
	BuildBatch     *command.BuildBatchHandler
	ApproveBatch   *command.ApproveBatchHandler

	// Консоль очереди утверждения рассылок (срез 4).
	PostponeDraft *command.PostponeDraftHandler
	DraftQueue    *query.DraftQueueHandler
	StalledDrafts *query.StalledDraftsHandler

	DefineSequence  *command.DefineSequenceHandler
	EnrollLead      *command.EnrollLeadHandler
	RemoveLead      *command.RemoveLeadHandler
	RecordOutcome   *command.RecordOutcomeHandler
	ResumeExecution *command.ResumeExecutionHandler
	SequenceHistory *query.SequenceHistoryHandler
	SequenceState   *query.SequenceStateHandler

	// Handoff — приём передачи писем заявки из request-fulfillment (capability
	// outreach-handoff): создаёт кампанию заявки с черновиками в очереди
	// утверждения.
	Handoff         *command.SubmitHandoffHandler
	EditorialReview *appeditorial.Service

	// SubmitApproval — подача черновика агента в очередь утверждения (контракт
	// campaign-command, capability company-agent-orchestration): создаёт кампанию
	// подачи с черновиком в очереди утверждения.
	SubmitApproval *command.SubmitForApprovalHandler

	// CancelRequest — owner command для durable tombstone отмены заявки.
	CancelRequest *command.CancelRequestHandler

	// ApprovedAction — чтение утверждённого действия по черновику для шага
	// отправки (read-контракт draftdecision).
	ApprovedAction *query.ApprovedActionHandler

	// SequenceEngine — движок последовательностей, который запускает роль
	// chains: он формирует черновики шагов и продвигает цепочки.
	SequenceEngine *scheduling.Engine

	// SequenceLimits — валидированные ограничения участия, переданные сценариям
	// и движку.
	SequenceLimits domain.ParticipationLimits

	// BatchLimits — валидированные лимиты пакета. Источник — batchlimits;
	// composition их только передаёт сценариям и HTTP-адаптеру, не решая по ним.
	BatchLimits domain.BatchLimits

	// ApprovedActionPublisher — синхронный контракт approved-action, связанный с
	// реализацией владельца адаптером выбранного транспорта. Потребитель держит
	// этот интерфейс и не импортирует пакеты владельца.
	ApprovedActionPublisher approvedaction.Publisher

	// Publication — прикладной сервис владельца, реализующий контракт (та сторона,
	// которую потребитель получает через ApprovedActionPublisher). Равен nil, если
	// задан внешний потребитель (Params.ApprovedActionConsumer): тогда встроенной
	// реализации владельца на пути нет.
	Publication *publicationapp.Service
	// Readiness — оценка владельческих зависимостей ролей.
	Readiness *ReadinessReport
}

// Build собирает контекст среза из переданного хранилища (in-memory по
// умолчанию, PostgreSQL — при заданном Params.Persistence) и переданных
// зависимостей.
func Build(params Params) (*Slice, error) {
	// Предикат риска обязателен: nil здесь означал бы молчаливое отключение
	// исключения рисковых черновиков (домен при nil трактует факты как
	// недоступные). Ошибка сборки заставляет вызывающий собрать предикат явно —
	// NewRiskFacts поверх портов владельцев.
	if params.Risk == nil {
		return nil, fmt.Errorf(
			"сборка: не задан предикат признаков риска (Params.Risk): " +
				"соберите его через NewRiskFacts поверх портов владельцев",
		)
	}

	clock := params.Clock
	if clock == nil {
		clock = SystemClock{}
	}

	// Хранилище: PostgreSQL-адаптеры при заданном Persistence, иначе in-memory
	// (прежнее поведение тестов и dev). Частичная конфигурация — ошибка сборки:
	// половина среза на одном хранилище, половина на другом — недопустимое
	// состояние, а не молчаливый выбор.
	var (
		repository CampaignStore
		journal    relay.Source
		unitOfWork ports.UnitOfWork
	)
	if params.Persistence.configured() {
		if params.Persistence.Campaigns == nil || params.Persistence.Journal == nil || params.Persistence.UnitOfWork == nil {
			return nil, fmt.Errorf(
				"сборка: неполная конфигурация хранилища (Params.Persistence): " +
					"нужны Campaigns, Journal и UnitOfWork",
			)
		}
		repository = params.Persistence.Campaigns
		journal = params.Persistence.Journal
		unitOfWork = params.Persistence.UnitOfWork
	} else {
		memoryRepository := persistence.New()
		memoryJournal := outboxinmemory.NewJournal()
		repository = memoryRepository
		journal = memoryJournal
		unitOfWork = outboxinmemory.NewUnitOfWorkWith(memoryRepository, memoryJournal)
	}
	var editorialStore appeditorial.Store = params.Persistence.Editorial
	if editorialStore == nil {
		if params.Persistence.configured() {
			return nil, fmt.Errorf("сборка: persistence editorial review не подключён")
		}
		editorialStore = persistence.NewEditorialStore()
	}
	editorialReview := appeditorial.New(editorialStore, params.ContentVersions, clock.Now)

	// Решение о доступе: при заданном Access — тонкий перевод к контракту
	// владельца (продуктовый путь); иначе dev/test-двойник на правах Grants.
	// Сборка не выдумывает стартовую политику и не выдаёт двойник за авторизацию.
	var accessDecision ports.AccessDecision
	if params.Access != nil {
		contract, err := access.NewContract(params.Access)
		if err != nil {
			return nil, fmt.Errorf("сборка: решение о доступе: %w", err)
		}
		accessDecision = contract
	} else {
		dev := access.NewDecider()
		for _, grant := range params.Grants {
			if err := dev.Grant(grant.Actor, grant.Tenant); err != nil {
				return nil, fmt.Errorf("сборка: выдача права доступа: %w", err)
			}
		}
		accessDecision = dev
	}

	// Назначение релиза approved-action. Приоритет: явный Sink, затем
	// ApprovedActionSink (функция), затем внешний потребитель
	// (ApprovedActionConsumer). Ни одно не задано — relay не собирается, и
	// действия не публикуются (например, сборка только под api).
	//
	// Проверка ApprovedActionSink != nil до присваивания интерфейсу обязательна:
	// nil-функция, положенная в relay.Sink, дала бы непустой интерфейс с
	// no-op-реализацией и молча съедала бы действия.
	sink := params.Sink
	if sink == nil && params.ApprovedActionSink != nil {
		sink = params.ApprovedActionSink
	}
	if sink == nil && params.ApprovedActionConsumer != nil {
		sink = params.ApprovedActionConsumer
	}

	var publication *relay.Relay
	if sink != nil {
		publication = relay.New(journal, sink)
	}

	keys := idempotency.NewStore()

	// Лимиты пакета — конфигурация: берём значения по умолчанию и валидируем их
	// при сборке (design.md, Решение 7). Невалидная конфигурация — ошибка сборки,
	// а не паника: вызывающий решает, как поступить.
	limits, err := batchlimits.Load(batchlimits.Default())
	if err != nil {
		return nil, fmt.Errorf("сборка: лимиты пакета: %w", err)
	}

	createCampaign := command.NewCreateCampaignHandler(accessDecision, unitOfWork)
	draftAction := command.NewDraftActionHandler(accessDecision, clock, unitOfWork)
	approveDraft := command.NewApproveDraftHandler(accessDecision, clock, unitOfWork)
	rejectDraft := command.NewRejectDraftHandler(accessDecision, clock, unitOfWork)
	reviseDraft := command.NewReviseDraftHandler(accessDecision, clock, unitOfWork)
	expireApproval := command.NewExpireApprovalHandler(accessDecision, clock, unitOfWork)
	approvalQueue := query.NewApprovalQueueHandler(repository, accessDecision)
	draftDecision := query.NewDraftDecisionHandler(repository, accessDecision)

	// Пакетный путь того же среза: формирование состава окном черновиков,
	// пакетное утверждение и пакетная проекция очереди. Все три получают один
	// инъецированный предикат риска; утверждение идёт через тот же UnitOfWork,
	// поэтому action-approved попадают в outbox в одной транзакции с агрегатом.
	buildBatch := command.NewBuildBatchHandler(repository, accessDecision, params.Risk)
	approveBatch := command.NewApproveBatchHandler(accessDecision, clock, unitOfWork, params.Risk)
	batchQueue := query.NewBatchQueueHandler(repository, params.Risk, accessDecision)

	// Консоль очереди утверждения рассылок (web-frontend-architecture, срез 4):
	// чтение очереди с проверками риска, срез зависших и отложение решения.
	// Читающие запросы используют тот же репозиторий и как перечислитель
	// кампаний, и тот же предикат риска, что пакетный путь, поэтому решение
	// отвергает то, что исключил состав.
	postponeDraft := command.NewPostponeDraftHandler(accessDecision, unitOfWork)
	draftQueue := query.NewDraftQueueHandler(repository, repository, params.Risk, accessDecision)
	stalledDrafts := query.NewStalledDraftsHandler(repository, repository, accessDecision)

	// Ограничения участия — конфигурация сборки. Нулевое значение означает
	// ограничения по умолчанию; частично заданная конфигурация — ошибка сборки.
	sequenceLimits := params.SequenceLimits
	if sequenceLimits.IsZero() {
		sequenceLimits = domain.DefaultParticipationLimits()
	}
	if err := sequenceLimits.Validate(); err != nil {
		return nil, fmt.Errorf("сборка: ограничения участия: %w", err)
	}

	// Движок последовательностей и его сценарии. Движок читает кампании через
	// репозиторий-листер и пишет изменения в той же транзакции, что и события.
	defineSequence := command.NewDefineSequenceHandler(accessDecision, unitOfWork)
	enrollLead := command.NewEnrollLeadHandler(accessDecision, clock, unitOfWork, sequenceLimits)
	removeLead := command.NewRemoveLeadHandler(accessDecision, clock, unitOfWork)
	recordOutcome := command.NewRecordOutcomeHandler(accessDecision, clock, unitOfWork)
	resumeExecution := command.NewResumeExecutionHandler(accessDecision, clock, unitOfWork)
	sequenceHistory := query.NewSequenceHistoryHandler(repository, accessDecision)
	sequenceState := query.NewSequenceStateHandler(repository, accessDecision)
	sequenceEngine, err := scheduling.NewEngine(repository, unitOfWork, clock, sequenceLimits)
	if err != nil {
		return nil, fmt.Errorf("сборка: движок последовательностей: %w", err)
	}

	// Синхронный контракт approved-action: прикладной сервис владельца реализует
	// интерфейс контракта, а связывается он с потребителем адаптером транспорта,
	// выбранного топологией (design.md, Решение 2). Потребитель получает
	// интерфейс контракта, а не пакет владельца.
	//
	// Если задан внешний потребитель (Params.ApprovedActionConsumer, например мост
	// в message-delivery), связываем с контрактом именно его: за
	// Slice.ApprovedActionPublisher стоит реальный потребитель, а встроенный
	// in-memory сервис владельца не собирается. Иначе сохраняется прежнее
	// поведение — владелец собирает свой publication-сервис, и Slice.Publication
	// остаётся доступен.
	var (
		approvedActionPublisher approvedaction.Publisher
		publicationService      *publicationapp.Service
	)
	if params.ApprovedActionConsumer != nil {
		approvedActionPublisher, err = LinkApprovedActionPublisher(params.ApprovedActionTransport, params.ApprovedActionConsumer)
		if err != nil {
			return nil, fmt.Errorf("сборка: синхронный контракт approved-action: %w", err)
		}
	} else {
		approvedActionPublisher, publicationService, err = newApprovedActionPublisher(params.ApprovedActionTransport)
		if err != nil {
			return nil, fmt.Errorf("сборка: синхронный контракт approved-action: %w", err)
		}
	}

	handoff := command.NewSubmitHandoffHandler(accessDecision, clock, unitOfWork)
	submitApproval := command.NewSubmitForApprovalHandler(clock, unitOfWork)
	cancelRequest := command.NewCancelRequestHandler(unitOfWork, params.ApprovedActionCanceller)
	approvedAction := query.NewApprovedActionHandler(repository)

	server := httpapi.NewServer(httpapi.Dependencies{
		CreateCampaign: createCampaign,
		DraftAction:    draftAction,
		ApproveDraft:   approveDraft,
		RejectDraft:    rejectDraft,
		ReviseDraft:    reviseDraft,
		ApprovalQueue:  approvalQueue,
		DraftDecision:  draftDecision,
		BatchQueue:     batchQueue,
		BuildBatch:     buildBatch,
		ApproveBatch:   approveBatch,
		CommandKeys:    keys,
		BatchLimits:    limits,

		PostponeDraft:    postponeDraft,
		DraftQueue:       draftQueue,
		StalledDrafts:    stalledDrafts,
		Auth:             params.ConsoleAuth,
		Console:          params.Console,
		BasePath:         params.BasePath,
		ExternalPrefixes: params.ExternalPrefixes,

		DefineSequence:  defineSequence,
		EnrollLead:      enrollLead,
		RemoveLead:      removeLead,
		RecordOutcome:   recordOutcome,
		ResumeExecution: resumeExecution,
		SequenceHistory: sequenceHistory,
		SequenceState:   sequenceState,

		Handoff:          handoff,
		EditorialHandoff: editorialReview,
		EditorialReview:  editorialReview,
		CancelRequest:    cancelRequest,

		SubmitApproval: submitApproval,
		ApprovedAction: approvedAction,
	})
	readinessReport := NewReadinessReport(params.Persistence, nil)
	server.SetReadiness(func(ctx context.Context) readiness.Snapshot {
		return readinessReport.RoleReadiness(ctx, composition.RoleAPI)
	})

	return &Slice{
		Repository:     repository,
		Journal:        journal,
		UnitOfWork:     unitOfWork,
		Access:         accessDecision,
		Idempotency:    keys,
		Relay:          publication,
		Server:         server,
		CreateCampaign: createCampaign,
		DraftAction:    draftAction,
		ApproveDraft:   approveDraft,
		RejectDraft:    rejectDraft,
		ReviseDraft:    reviseDraft,
		ExpireApproval: expireApproval,
		ApprovalQueue:  approvalQueue,
		DraftDecision:  draftDecision,
		BatchQueue:     batchQueue,
		BuildBatch:     buildBatch,
		ApproveBatch:   approveBatch,
		BatchLimits:    limits,

		PostponeDraft: postponeDraft,
		DraftQueue:    draftQueue,
		StalledDrafts: stalledDrafts,

		DefineSequence:  defineSequence,
		EnrollLead:      enrollLead,
		RemoveLead:      removeLead,
		RecordOutcome:   recordOutcome,
		ResumeExecution: resumeExecution,
		SequenceHistory: sequenceHistory,
		SequenceState:   sequenceState,
		SequenceEngine:  sequenceEngine,
		SequenceLimits:  sequenceLimits,
		Handoff:         handoff,
		EditorialReview: editorialReview,
		SubmitApproval:  submitApproval,
		CancelRequest:   cancelRequest,
		ApprovedAction:  approvedAction,

		ApprovedActionPublisher: approvedActionPublisher,
		Publication:             publicationService,
		Readiness:               readinessReport,
	}, nil
}
