package requestfulfillment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	call "lead-gen/backend/contexts/request-fulfillment/internal/adapters/call"
	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/fulfillment"
	httpapi "lead-gen/backend/contexts/request-fulfillment/internal/adapters/http"
	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/idgen"
	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	transport "lead-gen/backend/contexts/request-fulfillment/internal/adapters/transport"
	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/watch"
	workflowadapter "lead-gen/backend/contexts/request-fulfillment/internal/adapters/workflow"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/researchbridge"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/messaging/inbox"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/readiness"
	"lead-gen/backend/platform/transport/amqp"
	platformworkflow "lead-gen/backend/platform/workflow"
	"lead-gen/contracts/content-production/contentrequest"
	"lead-gen/contracts/integration-platform/vector"
	"lead-gen/contracts/lead-management/emailgeneration"
	"lead-gen/contracts/lead-management/leadsearch"
	"lead-gen/contracts/outreach-orchestration/callintent"
	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
	"lead-gen/frontend/components"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "request-fulfillment"

// defaultPublishInterval — интервал проходов relay публикации событий заявки,
// если задано некорректное значение.
const defaultPublishInterval = time.Second

// SystemClock — часы процесса, источник времени по умолчанию.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	readinessWorkerActive   *atomic.Bool
	readinessResearchWorker *researchWorkerHealth
	// Research — явно подключённый исследовательский путь утверждённой заявки.
	Research *ResearchConfig
	// ExecutionProtocol выбирает путь команд исполнения. Нулевое значение
	// fail-closed и означает v1; production composition не переключается на v2.
	ExecutionProtocol ExecutionProtocol
	// ExecutionConsumer удостоверяет, что у v2 есть зарегистрированный активный
	// потребитель durable intent. Без него Build отклоняет v2 до HTTP admission.
	ExecutionConsumer ExecutionIntentConsumer
	// ExecutionManifestActive выставляется composition только по активному
	// подписанному manifest. Прямое локальное включение v2 без release gate запрещено.
	ExecutionManifestActive bool

	// APIAddr — адрес внутреннего HTTP-сервера роли api.
	APIAddr string

	// Interval — интервал прохода relay публикации событий заявки.
	Interval time.Duration

	// Repository — репозиторий заявок. nil → новый in-memory репозиторий
	// (dev-реализация); durable-исполнение требует PostgreSQL-адаптера.
	Repository ports.RequestRepository

	// UnitOfWork — транзакционный порт заявок. nil → in-memory unit-of-work над
	// Repository и Journal; PostgreSQL-репозиторий требует явного unit-of-work.
	UnitOfWork ports.UnitOfWork

	// Journal — журнал outbox событий заявки. nil → новый журнал в памяти.
	Journal platformoutbox.Journal

	// DraftStore — хранилище черновиков писем оператора. nil → in-memory
	// хранилище (dev-реализация).
	DraftStore ports.EmailDraftStore

	// Query — синхронный query-контракт чтения статусов артефактов фулфилмента.
	// nil → запрос, честно отвечающий отказом (контракт не подключён).
	Query ports.FulfillmentQuery

	// Clock — источник времени прикладного слоя. nil → SystemClock.
	Clock ports.Clock

	// IDs — генератор идентификаторов событий. nil → случайный генератор.
	IDs ports.IDGenerator

	// Publisher — издатель брокера. nil означает, что публикация не собирается:
	// процесс не запускает relay событий заявки.
	Publisher amqp.Publisher

	// WorkflowClient — клиент Temporal. nil означает, что рантайм исполнения не
	// подключён: воркер workflow не поднимается, а порты честно отвечают
	// «недоступен» (заявка остаётся в текущем статусе).
	WorkflowClient *platformworkflow.Client

	// WorkflowTaskQueue — очередь задач воркера workflow. Пусто — очередь
	// клиента по умолчанию.
	WorkflowTaskQueue string

	// WorkflowStarter — переопределение порта запуска workflow (тесты). nil —
	// адаптер поверх WorkflowClient.
	WorkflowStarter ports.WorkflowStarter

	// WorkflowStatus — переопределение порта чтения состояния исполнения
	// (тесты). nil — адаптер поверх WorkflowClient.
	WorkflowStatus ports.WorkflowStatusReader

	// WorkflowSignaler — переопределение порта сигналов человеческих пауз
	// workflow (тесты). nil — адаптер поверх WorkflowClient.
	WorkflowSignaler ports.WorkflowSignaler

	// ExecutionPolicy — таймауты и сроки исполнения заявки задаются отдельными
	// полями ниже; нулевые поля заполняются осмысленными значениями по умолчанию.

	// ApprovalTTL — срок паузы утверждения результата. По истечении заявка
	// переводится в cancelled.
	ApprovalTTL time.Duration

	// WorkflowExecutionTimeout — предельное время исполнения заявки; согласовано
	// со сроком паузы (не меньше ApprovalTTL).
	WorkflowExecutionTimeout time.Duration

	// TransitionActivityTimeout — StartToCloseTimeout коротких переходов.
	TransitionActivityTimeout time.Duration

	// TransitionActivityAttempts — число попыток коротких переходов.
	TransitionActivityAttempts int

	// LongActivityTimeout — StartToCloseTimeout длинных activities (минуты).
	LongActivityTimeout time.Duration

	// LongActivityHeartbeat — HeartbeatTimeout длинных activities.
	LongActivityHeartbeat time.Duration

	// LongActivityAttempts — число попыток длинных внешних шагов (ограничено).
	LongActivityAttempts int

	// WorkerStopTimeout — время graceful drain воркера workflow при остановке.
	WorkerStopTimeout time.Duration

	// WorkerMaxConcurrentActivities — предел одновременных activities воркера.
	WorkerMaxConcurrentActivities int

	// WorkerMaxConcurrentWorkflows — предел одновременных задач workflow воркера.
	WorkerMaxConcurrentWorkflows int

	// Runtime — признак доступности рантайма durable-исполнения для пробы
	// готовности /readyz. nil — рантайм не подключён: отсутствие зависимости не
	// считается неготовностью.
	Runtime *platformworkflow.Availability

	// CallContract — порт опубликованного контракта call-intent (владелец —
	// outreach-orchestration). nil означает, что провайдер звонков не подключён:
	// порт честно отвечает «недоступен».
	CallContract callintent.Port

	// CallInitiator — переопределение порта звонков (тесты). nil — адаптер поверх
	// CallContract либо недоступный адаптер.
	CallInitiator ports.CallInitiator

	// Vector — векторное хранилище за provider-port (эмбеддинг оффера). nil —
	// эмбеддинг недоступен: порт честно отвечает отказом.
	Vector vector.Port

	// SearchLeads — заглушка шага поиска: сколько лидов «находит» activity, когда
	// контракт lead-search не подключён. 0 — заглушка не находит лидов. Если задан
	// LeadSearch, значение игнорируется: шаг идёт через контракт.
	SearchLeads int

	// LeadSearch — порт опубликованного контракта lead-search (владелец —
	// lead-management). nil означает, что поиск не подключён: шаг падает на
	// прежнюю заглушку (SearchLeads).
	LeadSearch leadsearch.Port

	// EmailGeneration — порт опубликованного контракта email-generation (владелец —
	// lead-management). nil означает, что генерация писем не подключена: шаг
	// генерации черновиков штатно пропускается и логируется, а заявка идёт дальше.
	EmailGeneration emailgeneration.Port

	// ContentRequest — порт опубликованного контракта content-request (владелец —
	// content-production): производит материал письма перед передачей. nil или
	// незаданный Handoff означает, что шаг передачи не может быть выполнен: он
	// штатно пропускается и логируется, а не подменяется выдуманным материалом.
	ContentRequest contentrequest.Port

	// Handoff — порт опубликованного контракта outreach-handoff (потребитель —
	// outreach-orchestration): передаёт письма заявки в очередь утверждения. nil
	// означает, что передача не подключена: шаг штатно пропускается и логируется,
	// а не выдаётся за успешную передачу.
	Handoff outreachhandoff.Port

	// DeliveryEvents — источник событий доставки (подписка брокера). nil означает,
	// что завершение по доставке не собирается: заявка ждёт прежним способом
	// (сигнал/таймер), а не завершается выдуманно. Проводка — точка входа
	// cmd/lead-gen.
	DeliveryEvents amqp.Consumer

	// DeliveryEventsDeduper — журнал дедупликации входящих событий доставки
	// (доставка at-least-once). nil при заданном DeliveryEvents — честная ошибка
	// сборки подписки, а не тихое дублирование эффекта.
	DeliveryEventsDeduper inbox.Deduper

	// DeliveryEventsQueue — имя очереди подписки. Пусто — очередь по умолчанию.
	DeliveryEventsQueue string

	// ConsoleUser / ConsolePassword — учётные данные оператора консоли заявок.
	// Пустые значения означают вход с пустым логином и паролем (только для
	// локального dev-прогона без внешнего доступа).
	ConsoleUser     string
	ConsolePassword string

	// ConsoleStore — хранилище сессий оператора. nil — память процесса
	// (поведение как раньше). Общий стор (PostgreSQL) делает сессию, выданную
	// другой консолью, действительной и здесь.
	ConsoleStore consoleauth.Store

	// Console — базовые адреса сервисов консоли для общей навигации по разделам
	// (Заявки, Исполнение, Лиды, Отчёты). Пустые адреса дают относительные пути.
	Console components.ConsoleBases

	// BasePath — внешний префикс пути консоли под единым origin (admin-proxy).
	// Пусто — корень origin.
	BasePath string

	// ExternalPrefixes — префиксы других консолей единого origin: ссылки на них
	// в навигации не переписываются под собственный префикс.
	ExternalPrefixes []string

	// ConsoleIdleTimeout — период неактивности сессии оператора, после которого
	// она истекает. Ноль означает период по умолчанию пакета consoleauth; на
	// поведение по умолчанию не влияет.
	ConsoleIdleTimeout time.Duration

	// Now — часы проверки истечения сессии оператора консоли (тесты). nil —
	// системное время. Рантайм-поведение по умолчанию не меняется.
	Now func() time.Time

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Slice — собранный контекст среза: порты, адаптеры и use cases.
type Slice struct {
	researchReconciler *researchbridge.Reconciler
	Repository         ports.RequestRepository
	Journal            platformoutbox.Journal
	UnitOfWork         ports.UnitOfWork

	// Relay не равен nil только если при сборке задан Publisher.
	Relay *platformoutbox.Relay

	// Server — HTTP-обработчики роли api. Реализует http.Handler.
	Server *httpapi.Server

	SubmitRequest          *command.SubmitRequestHandler
	StartValidation        *command.StartValidationHandler
	ValidateRequest        *command.ValidateRequestHandler
	StartProcessing        *command.StartProcessingHandler
	FulfillRequest         *command.FulfillRequestHandler
	RejectRequest          *command.RejectRequestHandler
	CancelRequest          *command.CancelRequestHandler
	FailRequest            *command.FailRequestHandler
	RerunRequest           *command.RerunRequestHandler
	AdmitExecution         *command.AdmitExecutionHandler
	ReturnForRevision      *command.ReturnForRevisionHandler
	ResubmitRequest        *command.ResubmitRequestHandler
	ReviseOffer            *command.ReviseOfferHandler
	RecordAudienceSnapshot *command.RecordAudienceSnapshotHandler
	RecordCampaign         *command.RecordCampaignHandler
	RecordDeal             *command.RecordDealHandler
	GetRequest             *query.GetRequestHandler
	ListRequests           *query.ListRequestsHandler
	FulfillmentStatus      *query.FulfillmentStatusHandler
	ExecutionState         *query.ExecutionStateHandler

	WorkflowStarter  ports.WorkflowStarter
	WorkflowStatus   ports.WorkflowStatusReader
	WorkflowSignaler ports.WorkflowSignaler

	// RequestWatcher — подписка на изменения заявок тенанта (SSE-поток экрана
	// заявок компании). Единый источник уведомлений для сервера и unit-of-work.
	RequestWatcher ports.RequestWatcher

	// DraftStore — хранилище черновиков писем оператора.
	DraftStore ports.EmailDraftStore

	ValidationQueue *query.ValidationQueueHandler
	InitiateCall    *command.InitiateCallHandler
	RecordCall      *command.RecordCallHandler
	EmbedOffer      *command.EmbedOfferHandler
	EmailDrafts     *query.EmailDraftsHandler

	// ConcludeDelivery — обработчик события доставки (завершение заявки по
	// доставке, capability request-delivery-conclusion).
	ConcludeDelivery *command.ConcludeDeliveryHandler
}

// Build собирает контекст среза из адаптеров и переданных зависимостей.
// Бизнес-правил здесь нет — соединение адаптеров, портов и use cases только
// своего контекста.
func Build(cfg Config) (*Slice, error) {
	protocol, err := normalizeExecutionProtocol(cfg.ExecutionProtocol)
	if err != nil {
		return nil, err
	}
	if protocol == ExecutionProtocolV2 && (cfg.ExecutionConsumer == nil || !cfg.ExecutionConsumer.Active()) {
		return nil, ErrExecutionConsumerUnavailable
	}
	if protocol == ExecutionProtocolV2 && !cfg.ExecutionManifestActive {
		return nil, ErrExecutionManifestRequired
	}
	repository := cfg.Repository
	if repository == nil {
		repository = persistence.New()
	}
	journal := cfg.Journal
	if journal == nil {
		journal = inmemory.NewTransactionalJournal()
	}
	clock := cfg.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	ids := cfg.IDs
	if ids == nil {
		ids = idgen.NewRandom()
	}

	unitOfWork := cfg.UnitOfWork
	if unitOfWork == nil {
		// Dev-режим: in-memory unit-of-work работает только с in-memory
		// репозиторием. Для PostgreSQL адаптер и unit-of-work задаются вместе, и
		// подмена одного из них молчаливым in-memory была бы ложью.
		memoryRepo, ok := repository.(*persistence.Repository)
		if !ok {
			return nil, errors.New("request-fulfillment: unit-of-work обязателен для не in-memory репозитория")
		}
		if _, ok := journal.(*inmemory.TransactionalJournal); !ok {
			return nil, errors.New("request-fulfillment: memory unit-of-work требует transactional outbox того же контекста")
		}
		unitOfWork = inmemory.NewUnitOfWorkWith(memoryRepo, journal)
	}
	var executionState *query.ExecutionStateHandler
	if protocol == ExecutionProtocolV2 {
		attempts, ok := repository.(ports.ExecutionAttemptRepository)
		if !ok {
			return nil, errors.New("request-fulfillment: ExecutionProtocol v2 требует durable owner attempt repository")
		}
		executionState = query.NewExecutionStateHandler(attempts)
	}

	// Подписка на изменения заявок тенанта (SSE-поток админки): уведомление
	// рассылается после успешного коммита транзакции, а не на каждый Save, чтобы
	// подписчик никогда не читал не зафиксированное состояние. Обёртка общая для
	// in-memory и PostgreSQL — она работает с портом транзакции.
	notifier := watch.NewNotifier()
	unitOfWork = watch.NewUnitOfWork(unitOfWork, notifier)

	submit := command.NewSubmitRequestHandlerWithExecutionHistoryKnown(unitOfWork, clock, ids, protocol == ExecutionProtocolV2)
	startValidation := command.NewStartValidationHandler(unitOfWork, clock, ids)
	validate := command.NewValidateRequestHandler(unitOfWork, clock, ids)
	startProcessing := command.NewStartProcessingHandler(unitOfWork, clock, ids)
	fulfill := command.NewFulfillRequestHandler(unitOfWork, clock, ids)
	reject := command.NewRejectRequestHandler(unitOfWork, clock, ids)
	cancel := command.NewCancelRequestHandler(unitOfWork, clock, ids)
	failRequest := command.NewFailRequestHandler(unitOfWork, clock, ids)
	rerunRequest := command.NewRerunRequestHandler(unitOfWork, clock, ids)
	admitExecution := command.NewAdmitExecutionHandler(unitOfWork, clock, ids)
	returnForRevision := command.NewReturnForRevisionHandler(unitOfWork, clock, ids)
	resubmit := command.NewResubmitRequestHandler(unitOfWork, clock, ids)
	reviseOffer := command.NewReviseOfferHandler(unitOfWork, clock, ids)
	recordAudience := command.NewRecordAudienceSnapshotHandler(unitOfWork)
	recordCampaign := command.NewRecordCampaignHandler(unitOfWork)
	recordDeal := command.NewRecordDealHandler(unitOfWork)

	getRequest := query.NewGetRequestHandler(repository)
	listRequests := query.NewListRequestsHandler(repository)
	fulfillmentStatus := query.NewFulfillmentStatusHandler(repository, queryPort(cfg.Query))
	validationQueue := query.NewValidationQueueHandler(repository)

	// Черновики писем: хранилище оператора. nil → in-memory (dev); PostgreSQL
	// задаёт хранилище при заданном DSN. Оно есть всегда: шаг генерации просто
	// не наполняется, если контракт email-generation не подключён, а чтение честно
	// отдаёт пустой список.
	draftStore := cfg.DraftStore
	if draftStore == nil {
		draftStore = persistence.NewDraftStore()
	}
	emailDrafts := query.NewEmailDraftsHandler(repository, draftStore)

	// Порт звонков: явное переопределение (тесты) либо адаптер поверх контракта.
	// Без провайдера порт честно отвечает «недоступен».
	callInitiator := cfg.CallInitiator
	if callInitiator == nil {
		if cfg.CallContract != nil {
			callInitiator = call.NewContract(cfg.CallContract)
		} else {
			callInitiator = call.Unavailable{}
		}
	}
	initiateCall := command.NewInitiateCallHandler(unitOfWork, callInitiator)
	recordCall := command.NewRecordCallHandler(unitOfWork)

	// Эмбеддинг оффера собирается только при подключённом векторном хранилище:
	// без него шаг не выполняется, а не отвечает отказом на каждом одобрении.
	var embedOffer *command.EmbedOfferHandler
	if cfg.Vector != nil {
		embedOffer = command.NewEmbedOfferHandler(unitOfWork, cfg.Vector)
	}

	// Порты workflow: явное переопределение (тесты) или адаптер поверх клиента
	// Temporal. Без клиента и без переопределения порты остаются nil, и
	// прикладной слой честно отвечает «рантайм недоступен».
	workflowStarter := cfg.WorkflowStarter
	workflowStatus := cfg.WorkflowStatus
	workflowSignaler := cfg.WorkflowSignaler
	var workflowCanceller ports.WorkflowCanceller
	if cfg.WorkflowClient != nil {
		policy := workflowadapter.ExecutionPolicy{
			ApprovalTTL:            cfg.ApprovalTTL,
			ExecutionTimeout:       cfg.WorkflowExecutionTimeout,
			TransitionStartToClose: cfg.TransitionActivityTimeout,
			TransitionMaxAttempts:  cfg.TransitionActivityAttempts,
			LongStartToClose:       cfg.LongActivityTimeout,
			LongHeartbeat:          cfg.LongActivityHeartbeat,
			LongMaxAttempts:        cfg.LongActivityAttempts,
		}
		adapter := workflowadapter.NewClient(cfg.WorkflowClient, cfg.WorkflowTaskQueue, policy)
		if workflowStarter == nil {
			workflowStarter = adapter
		}
		if workflowStatus == nil {
			workflowStatus = adapter
		}
		if workflowSignaler == nil {
			workflowSignaler = adapter
		}
		workflowCanceller = adapter
	}

	// Обработчик события доставки: сопоставляет событие с заявкой по кампании и
	// маршрутизирует исход бегущему исполнению. Без подключённого рантайма порт
	// сигнала остаётся nil — обработчик честно сообщает недоступность.
	concludeDelivery := command.NewConcludeDeliveryHandler(repository, workflowSignaler, cfg.Logger)

	researchBridge, err := buildResearchBridge(cfg.Research, getRequest, validate, workflowStatus)
	if err != nil {
		return nil, err
	}
	var researchReconciler *researchbridge.Reconciler
	if researchBridge != nil {
		researchReconciler = researchbridge.NewReconciler(repository, cfg.Research.Missions, startProcessing, fulfill, failRequest, cancel)
		researchBridge.SetReconciler(researchReconciler)
	}
	server := httpapi.NewServer(httpapi.Dependencies{
		Research:               researchBridge,
		SubmitRequest:          submit,
		StartValidation:        startValidation,
		ValidateRequest:        validate,
		StartProcessing:        startProcessing,
		FulfillRequest:         fulfill,
		RejectRequest:          reject,
		CancelRequest:          cancel,
		FailRequest:            failRequest,
		RerunRequest:           rerunRequest,
		ReturnForRevision:      returnForRevision,
		Resubmit:               resubmit,
		ReviseOffer:            reviseOffer,
		RecordAudienceSnapshot: recordAudience,
		RecordCampaign:         recordCampaign,
		RecordDeal:             recordDeal,
		GetRequest:             getRequest,
		ListRequests:           listRequests,
		FulfillmentStatus:      fulfillmentStatus,
		ExecutionState:         executionState,
		WorkflowStatus:         workflowStatus,
		WorkflowStarter:        workflowStarter,
		ExecutionProtocol:      protocol,
		ExecutionConsumer:      cfg.ExecutionConsumer,
		AdmitExecution:         admitExecution,
		WorkflowSignaler:       workflowSignaler,
		WorkflowCanceller:      workflowCanceller,
		OutreachCancellation:   cfg.Handoff,
		ValidationQueue:        validationQueue,
		ApprovalQueue:          query.NewApprovalQueueHandler(repository, workflowStatus),
		InitiateCall:           initiateCall,
		RecordCall:             recordCall,
		EmbedOffer:             embedOffer,
		EmailDrafts:            emailDrafts,
		RequestWatcher:         notifier,
		Runtime:                cfg.Runtime,
		Readiness:              requestReadinessProfile(repository, cfg, protocol),
		ReadinessEvaluator:     readiness.New(readiness.Config{Timeout: 750 * time.Millisecond, TTL: 2 * time.Second}),
		Console:                cfg.Console,
		BasePath:               cfg.BasePath,
		ExternalPrefixes:       cfg.ExternalPrefixes,
		Auth: consoleauth.Config{
			Username:    cfg.ConsoleUser,
			Password:    cfg.ConsolePassword,
			IdleTimeout: cfg.ConsoleIdleTimeout,
			Now:         cfg.Now,
			Store:       cfg.ConsoleStore,
		},
	})

	var relay *platformoutbox.Relay
	if cfg.Publisher != nil {
		relay = platformoutbox.NewRelay(journal, cfg.Publisher, cfg.Interval).
			WithLogger(cfg.Logger).
			WithMetrics(cfg.Metrics)
	}

	return &Slice{
		researchReconciler: researchReconciler,
		Repository:         repository,
		Journal:            journal,
		UnitOfWork:         unitOfWork,
		Relay:              relay,
		Server:             server,

		SubmitRequest:          submit,
		StartValidation:        startValidation,
		ValidateRequest:        validate,
		StartProcessing:        startProcessing,
		FulfillRequest:         fulfill,
		RejectRequest:          reject,
		CancelRequest:          cancel,
		FailRequest:            failRequest,
		RerunRequest:           rerunRequest,
		AdmitExecution:         admitExecution,
		ReturnForRevision:      returnForRevision,
		ResubmitRequest:        resubmit,
		ReviseOffer:            reviseOffer,
		RecordAudienceSnapshot: recordAudience,
		RecordCampaign:         recordCampaign,
		RecordDeal:             recordDeal,
		GetRequest:             getRequest,
		ListRequests:           listRequests,
		FulfillmentStatus:      fulfillmentStatus,
		ExecutionState:         executionState,

		WorkflowStarter:  workflowStarter,
		WorkflowStatus:   workflowStatus,
		WorkflowSignaler: workflowSignaler,

		RequestWatcher: notifier,

		DraftStore: draftStore,

		ValidationQueue: validationQueue,
		InitiateCall:    initiateCall,
		RecordCall:      recordCall,
		EmbedOffer:      embedOffer,
		EmailDrafts:     emailDrafts,

		ConcludeDelivery: concludeDelivery,
	}, nil
}

// Service — собранный сервис request-fulfillment. Реализует composition.Service:
// раннер на каждую объявленную роль.
type Service struct {
	slice   *Slice
	apiAddr string
	logger  observ.Logger
	metrics observ.Metrics
	tracer  observ.Tracer

	// workflowWorker не равен nil, только если подключён рантайм Temporal.
	workflowWorker *platformworkflow.Worker

	// availabilityMonitor следит за доступностью рантайма и переключает признак
	// готовности /readyz. nil, если рантайм или признак не подключены.
	availabilityMonitor *platformworkflow.AvailabilityMonitor

	// deliverySubscriber подписан на события доставки и маршрутизирует их к
	// исполнениям заявок. nil, если подписка не собрана (источник не задан).
	deliverySubscriber      *transport.Subscriber
	readinessWorkerActive   *atomic.Bool
	readinessResearchWorker *researchWorkerHealth
}

// New собирает сервис из адаптеров и переданных зависимостей.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("request-fulfillment: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("request-fulfillment: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("request-fulfillment: Tracer обязателен")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultPublishInterval
	}

	workerActive := &atomic.Bool{}
	cfg.readinessWorkerActive = workerActive
	researchWorker := &researchWorkerHealth{}
	cfg.readinessResearchWorker = researchWorker
	slice, err := Build(cfg)
	if err != nil {
		return nil, err
	}

	// Подписка на события доставки собирается только при заданном источнике:
	// без него заявка ждёт прежним способом, а не завершается выдуманно.
	var deliverySubscriber *transport.Subscriber
	if cfg.DeliveryEvents != nil {
		deliverySubscriber = transport.NewSubscriber(cfg.DeliveryEvents, cfg.DeliveryEventsDeduper, cfg.DeliveryEventsQueue, slice.ConcludeDelivery)
	}

	var workflowWorker *platformworkflow.Worker
	if cfg.WorkflowClient != nil {
		registry := platformworkflow.NewRegistry()
		workflowadapter.Register(registry, newActivities(slice, cfg))
		workflowWorker, err = platformworkflow.NewWorker(cfg.WorkflowClient, registry, platformworkflow.WorkerOptions{
			TaskQueue:                          cfg.WorkflowTaskQueue,
			Logger:                             cfg.Logger,
			WorkerStopTimeout:                  cfg.WorkerStopTimeout,
			MaxConcurrentActivityExecution:     cfg.WorkerMaxConcurrentActivities,
			MaxConcurrentWorkflowTaskExecution: cfg.WorkerMaxConcurrentWorkflows,
		})
		if err != nil {
			return nil, fmt.Errorf("request-fulfillment: воркер workflow: %w", err)
		}
	}

	service := &Service{
		slice:                   slice,
		apiAddr:                 cfg.APIAddr,
		logger:                  cfg.Logger,
		metrics:                 cfg.Metrics,
		tracer:                  cfg.Tracer,
		workflowWorker:          workflowWorker,
		deliverySubscriber:      deliverySubscriber,
		readinessWorkerActive:   workerActive,
		readinessResearchWorker: researchWorker,
	}
	// Монитор доступности живёт вместе с ролью, владеющей клиентом: она же
	// запускает воркер. Без подключённого рантайма и без пробы готовности монитор
	// не нужен.
	if cfg.WorkflowClient != nil && cfg.Runtime != nil {
		service.availabilityMonitor = &platformworkflow.AvailabilityMonitor{
			Checker:      cfg.WorkflowClient,
			Availability: cfg.Runtime,
			Logger:       cfg.Logger,
		}
	}
	return service, nil
}

// newActivities собирает activities фулфилмента как тонкие адаптеры к
// application services контекста. Доменных решений здесь нет.
func newActivities(slice *Slice, cfg Config) *workflowadapter.Activities {
	var search workflowadapter.SearchStub = workflowadapter.NoSearch{}
	if cfg.SearchLeads > 0 {
		search = workflowadapter.FixedSearch{Leads: cfg.SearchLeads}
	}
	if cfg.LeadSearch != nil {
		search = workflowadapter.ContractSearch{Requests: slice.Repository, Contract: cfg.LeadSearch}
	}

	// Шаг генерации черновиков собирается только при подключённом контракте
	// email-generation: без него activity честно пропускает шаг, а не выдумывает
	// письма.
	var drafts workflowadapter.DraftGenerator
	if cfg.EmailGeneration != nil {
		drafts = workflowadapter.ContractDrafts{
			Requests: slice.Repository,
			Contract: cfg.EmailGeneration,
			Drafts:   slice.DraftStore,
			Logger:   cfg.Logger,
		}
	}

	// Шаг передачи собирается только при подключённом контракте передачи. Если не
	// подключён материал, адаптер сам честно пропустит шаг с логом: заявка идёт
	// дальше, а передача не выдаётся за успех.
	var handoff workflowadapter.HandoffSender
	if cfg.Handoff != nil {
		handoff = workflowadapter.ContractHandoff{
			Drafts:   slice.DraftStore,
			Content:  cfg.ContentRequest,
			Outreach: cfg.Handoff,
			Logger:   cfg.Logger,
		}
	}
	return &workflowadapter.Activities{
		StartProcessing:   slice.StartProcessing,
		Fulfill:           slice.FulfillRequest,
		Cancel:            slice.CancelRequest,
		Fail:              slice.FailRequest,
		RecordCampaign:    slice.RecordCampaign,
		Search:            search,
		Drafts:            drafts,
		Handoff:           handoff,
		Clock:             cfg.Clock,
		IDs:               cfg.IDs,
		HeartbeatInterval: cfg.LongActivityHeartbeat,
		Logger:            cfg.Logger,
	}
}

// ID реализует composition.Service.
func (s *Service) ID() string { return ServiceID }

// HTTPHandler возвращает тот же API, которым владеет роль api собранного сервиса.
// Сам обработчик не запускает фоновые роли и не подменяет их готовность.
func (s *Service) HTTPHandler() http.Handler { return s.slice.Server }

// RoleRunner реализует composition.Service. Раннеры ролей владеют только
// запуском и адаптерами: доменных правил здесь нет.
func (s *Service) RoleRunner(role composition.Role) (composition.RoleRunner, bool) {
	switch role {
	case composition.RoleAPI:
		return composition.RoleFunc(s.runAPI), true
	case composition.RoleWorker:
		return composition.RoleFunc(s.runWorker), true
	default:
		return nil, false
	}
}

// runAPI поднимает внутренний HTTP-сервер контекста и останавливает его по
// отмене контекста.
func (s *Service) runAPI(ctx context.Context) error {
	handler := observ.Middleware(s.logger, s.metrics, s.tracer, ServiceID)(s.slice.Server)
	server := &http.Server{
		Addr:              s.apiAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("api: останов HTTP-сервера", err, observ.Fields{}, "service", ServiceID)
		}
	}()

	s.logger.Info("api: HTTP-сервер запущен", observ.Fields{}, "service", ServiceID, "addr", s.apiAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("api: работа HTTP-сервера: %w", err)
	}
	return nil
}

// runWorker запускает фоновые контуры роли worker: публикацию событий заявки
// (relay outbox), воркер workflow фулфилмента и монитор доступности рантайма
// (для пробы готовности). Падение любого контура останавливает роль: ошибка
// отменяет общий контекст, и остальные контуры завершаются, а не ждут друг
// друга.
//
// Транспортный relay публикует подтверждённые записи outbox в брокер. Событие,
// не прошедшее подтверждение, остаётся в журнале и будет опубликовано повторно:
// доставка at-least-once, а потребитель дедуплицирует по event_id. Подписка на
// события доставки собирается при заданном источнике; без неё это честно
// логируется, а не скрывается.
func (s *Service) runWorker(ctx context.Context) error {
	if s.slice.Relay == nil && s.workflowWorker == nil && s.availabilityMonitor == nil && s.deliverySubscriber == nil && s.slice.researchReconciler == nil {
		return fmt.Errorf("worker: ни публикация событий заявки, ни воркер workflow, ни подписка на доставку не собраны")
	}
	if s.slice.Relay != nil {
		s.logger.Info("worker: публикация событий заявки запущена", observ.Fields{}, "service", ServiceID)
	}
	if s.workflowWorker == nil {
		s.logger.Warn("worker: воркер workflow не собран (Temporal не подключён)", observ.Fields{}, "service", ServiceID)
	}
	if s.deliverySubscriber != nil {
		s.logger.Info("worker: подписка на события доставки запущена", observ.Fields{}, "service", ServiceID)
	} else {
		s.logger.Warn("worker: подписка на события доставки не собрана (источник не задан)", observ.Fields{}, "service", ServiceID)
	}

	// Контекст контуров отменяется при падении любого из них: роль не должна
	// зависать на долгоживущем контуре (например, relay), пока сбойный уже упал.
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 5)
	var wg sync.WaitGroup
	run := func(name string, fn func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(workerCtx); err != nil {
				errCh <- fmt.Errorf("%s: %w", name, err)
			}
		}()
	}
	if s.slice.Relay != nil {
		run("relay", s.slice.Relay.Run)
	}
	if s.workflowWorker != nil {
		run("workflow", s.runWorkflowWorker)
	}
	if s.deliverySubscriber != nil {
		run("delivery-events", s.deliverySubscriber.Run)
	}
	if s.availabilityMonitor != nil {
		run("runtime-monitor", s.availabilityMonitor.Run)
	}
	if s.slice.researchReconciler != nil {
		run("research-request-status", s.runResearchReconciler)
	}

	// Ждём первого исхода: отмены контекста или ошибки контура. Ошибка отменяет
	// контекст — остальные контуры получают отмену и завершаются, а не ждут друг
	// друга. При штатной отмене не выходим сразу: дожидаемся graceful drain
	// воркера (он завершает взятые задачи в пределах WorkerStopTimeout).
	var firstErr error
	select {
	case <-ctx.Done():
	case err := <-errCh:
		firstErr = err
		cancel()
	}
	wg.Wait()
	select {
	case err := <-errCh:
		if firstErr == nil {
			firstErr = err
		}
	default:
	}
	return firstErr
}

func (s *Service) runWorkflowWorker(ctx context.Context) error {
	if s.workflowWorker == nil || s.readinessWorkerActive == nil {
		return fmt.Errorf("worker: воркер workflow не собран")
	}
	s.readinessWorkerActive.Store(true)
	defer s.readinessWorkerActive.Store(false)
	return s.workflowWorker.Run(ctx)
}

// queryPort возвращает переданный query-контракт либо запрос, честно отвечающий
// отказом: сборка не выдумывает статусы чужих артефактов.
func queryPort(provided ports.FulfillmentQuery) ports.FulfillmentQuery {
	if provided != nil {
		return provided
	}
	return fulfillment.Unavailable{}
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
