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
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/batchlimits"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	publicationapp "lead-gen/backend/contexts/outreach-orchestration/internal/app/publication"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/query"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/scheduling"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
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

// NoUpstreamRiskChecks — предикат-заглушка ТЕКУЩЕГО состояния: он ВСЕГДА
// возвращает «рисков нет» и потому НЕ выполняет никаких проверок риска.
//
// Это не политика и не решение, а явно названная дыра в сборке: реальные
// признаки риска должны приходить от data-governance (processing-authority,
// правило остановки) и lead-management (свежесть верификации адреса), но эти
// интеграции ещё НЕ подключены. До их появления все черновики считаются не
// отмеченными риском.
//
// Заглушка экспортирована и названа отрицанием («никаких проверок upstream»),
// чтобы при аудите её было видно и нельзя было принять за настоящую политику.
// Замена этого предиката на реальный — обязательное условие ввода пакетного
// утверждения в эксплуатацию. Значение передаётся в Params.Risk явно: Build
// отвергает nil, поэтому случайно вернуться к молчаливому «рисков нет» нельзя.
var NoUpstreamRiskChecks domain.CanBeDecidedInPackage = func(domain.DraftID, int) []domain.RiskReason {
	return nil
}

// Params — параметры сборки среза. Хранилище задаётся через Persistence:
// нулевое значение означает in-memory адаптеры (прежнее поведение), заданное —
// адаптеры PostgreSQL. Сборка принимает порты, а не конкретные реализации,
// поэтому точка входа подключает нужное хранилище без правки среза.
type Params struct {
	// Clock — источник времени. nil → SystemClock.
	Clock ports.Clock

	// Grants — права доступа среза. По умолчанию доступ закрыт.
	Grants []Grant

	// Persistence — адаптеры хранилища среза. Нулевое значение означает
	// in-memory-сборку (тесты и dev); заполненное — PostgreSQL-адаптеры,
	// полученные из NewPostgresPersistence. Частично заданная конфигурация
	// отвергается: собрать половину на in-memory, половину на PostgreSQL нельзя.
	Persistence Persistence

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

	// Risk — внешний предикат признаков риска пакетного пути. ОБЯЗАТЕЛЕН:
	// Build возвращает ошибку, если предикат не задан. Тихий nil-дефолт «рисков
	// нет» недопустим — домен трактует nil как отсутствие рисков, поэтому
	// незаданный предикат молча отключил бы исключение рисковых черновиков.
	//
	// Значение инъецируется вызывающим: composition не вычисляет признаки сам
	// и не выводит политику. Один и тот же предикат получают все пакетные
	// сценарии, чтобы решение отвергало то, что исключил состав. Когда реальных
	// проверок ещё нет, вызывающий осознанно передаёт NoUpstreamRiskChecks.
	Risk domain.CanBeDecidedInPackage

	// SequenceLimits — ограничения участия лида в цепочках (предел касаний за
	// период и объём неутверждённых черновиков). Нулевое значение означает
	// ограничения по умолчанию; частично заданная конфигурация отвергается.
	SequenceLimits domain.ParticipationLimits
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
	Access      *access.Decider
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

	DefineSequence  *command.DefineSequenceHandler
	EnrollLead      *command.EnrollLeadHandler
	RemoveLead      *command.RemoveLeadHandler
	RecordOutcome   *command.RecordOutcomeHandler
	ResumeExecution *command.ResumeExecutionHandler
	SequenceHistory *query.SequenceHistoryHandler
	SequenceState   *query.SequenceStateHandler

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
}

// Build собирает контекст среза из переданного хранилища (in-memory по
// умолчанию, PostgreSQL — при заданном Params.Persistence) и переданных
// зависимостей.
func Build(params Params) (*Slice, error) {
	// Предикат риска обязателен: nil здесь означал бы молчаливое отключение
	// исключения рисковых черновиков (домен трактует nil как «рисков нет»).
	// Ошибка сборки заставляет вызывающий выбрать предикат явно — реальный или
	// осознанную заглушку NoUpstreamRiskChecks.
	if params.Risk == nil {
		return nil, fmt.Errorf(
			"сборка: не задан предикат признаков риска (Params.Risk): " +
				"укажите реальный предикат или осознанную заглушку NoUpstreamRiskChecks",
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

	decider := access.NewDecider()
	for _, grant := range params.Grants {
		if err := decider.Grant(grant.Actor, grant.Tenant); err != nil {
			return nil, fmt.Errorf("сборка: выдача права доступа: %w", err)
		}
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

	createCampaign := command.NewCreateCampaignHandler(decider, unitOfWork)
	draftAction := command.NewDraftActionHandler(decider, clock, unitOfWork)
	approveDraft := command.NewApproveDraftHandler(decider, clock, unitOfWork)
	rejectDraft := command.NewRejectDraftHandler(decider, clock, unitOfWork)
	reviseDraft := command.NewReviseDraftHandler(decider, unitOfWork)
	expireApproval := command.NewExpireApprovalHandler(decider, clock, unitOfWork)
	approvalQueue := query.NewApprovalQueueHandler(repository, decider)
	draftDecision := query.NewDraftDecisionHandler(repository, decider)

	// Пакетный путь того же среза: формирование состава окном черновиков,
	// пакетное утверждение и пакетная проекция очереди. Все три получают один
	// инъецированный предикат риска; утверждение идёт через тот же UnitOfWork,
	// поэтому action-approved попадают в outbox в одной транзакции с агрегатом.
	buildBatch := command.NewBuildBatchHandler(repository, decider, params.Risk)
	approveBatch := command.NewApproveBatchHandler(decider, clock, unitOfWork, params.Risk)
	batchQueue := query.NewBatchQueueHandler(repository, params.Risk, decider)

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
	defineSequence := command.NewDefineSequenceHandler(decider, unitOfWork)
	enrollLead := command.NewEnrollLeadHandler(decider, clock, unitOfWork, sequenceLimits)
	removeLead := command.NewRemoveLeadHandler(decider, clock, unitOfWork)
	recordOutcome := command.NewRecordOutcomeHandler(decider, clock, unitOfWork)
	resumeExecution := command.NewResumeExecutionHandler(decider, clock, unitOfWork)
	sequenceHistory := query.NewSequenceHistoryHandler(repository, decider)
	sequenceState := query.NewSequenceStateHandler(repository, decider)
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

		DefineSequence:  defineSequence,
		EnrollLead:      enrollLead,
		RemoveLead:      removeLead,
		RecordOutcome:   recordOutcome,
		ResumeExecution: resumeExecution,
		SequenceHistory: sequenceHistory,
		SequenceState:   sequenceState,
	})

	return &Slice{
		Repository:     repository,
		Journal:        journal,
		UnitOfWork:     unitOfWork,
		Access:         decider,
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

		DefineSequence:  defineSequence,
		EnrollLead:      enrollLead,
		RemoveLead:      removeLead,
		RecordOutcome:   recordOutcome,
		ResumeExecution: resumeExecution,
		SequenceHistory: sequenceHistory,
		SequenceState:   sequenceState,
		SequenceEngine:  sequenceEngine,
		SequenceLimits:  sequenceLimits,

		ApprovedActionPublisher: approvedActionPublisher,
		Publication:             publicationService,
	}, nil
}
