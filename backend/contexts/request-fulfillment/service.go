package requestfulfillment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	call "lead-gen/backend/contexts/request-fulfillment/internal/adapters/call"
	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/fulfillment"
	httpapi "lead-gen/backend/contexts/request-fulfillment/internal/adapters/http"
	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/idgen"
	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	workflowadapter "lead-gen/backend/contexts/request-fulfillment/internal/adapters/workflow"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/consoleauth"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	platformworkflow "lead-gen/backend/platform/workflow"
	"lead-gen/contracts/integration-platform/vector"
	"lead-gen/contracts/lead-management/emailgeneration"
	"lead-gen/contracts/lead-management/leadsearch"
	"lead-gen/contracts/outreach-orchestration/callintent"
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
	Repository ports.RequestRepository
	Journal    platformoutbox.Journal
	UnitOfWork ports.UnitOfWork

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
	ReviseOffer            *command.ReviseOfferHandler
	RecordAudienceSnapshot *command.RecordAudienceSnapshotHandler
	RecordCampaign         *command.RecordCampaignHandler
	RecordDeal             *command.RecordDealHandler
	GetRequest             *query.GetRequestHandler
	FulfillmentStatus      *query.FulfillmentStatusHandler

	WorkflowStarter  ports.WorkflowStarter
	WorkflowStatus   ports.WorkflowStatusReader
	WorkflowSignaler ports.WorkflowSignaler

	// DraftStore — хранилище черновиков писем оператора.
	DraftStore ports.EmailDraftStore

	ValidationQueue *query.ValidationQueueHandler
	InitiateCall    *command.InitiateCallHandler
	RecordCall      *command.RecordCallHandler
	EmbedOffer      *command.EmbedOfferHandler
	EmailDrafts     *query.EmailDraftsHandler
}

// Build собирает контекст среза из адаптеров и переданных зависимостей.
// Бизнес-правил здесь нет — соединение адаптеров, портов и use cases только
// своего контекста.
func Build(cfg Config) (*Slice, error) {
	repository := cfg.Repository
	if repository == nil {
		repository = persistence.New()
	}
	journal := cfg.Journal
	if journal == nil {
		journal = platformoutbox.NewMemoryJournal()
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
		unitOfWork = inmemory.NewUnitOfWorkWith(memoryRepo, journal)
	}

	submit := command.NewSubmitRequestHandler(unitOfWork, clock, ids)
	startValidation := command.NewStartValidationHandler(unitOfWork, clock, ids)
	validate := command.NewValidateRequestHandler(unitOfWork, clock, ids)
	startProcessing := command.NewStartProcessingHandler(unitOfWork, clock, ids)
	fulfill := command.NewFulfillRequestHandler(unitOfWork, clock, ids)
	reject := command.NewRejectRequestHandler(unitOfWork, clock, ids)
	cancel := command.NewCancelRequestHandler(unitOfWork, clock, ids)
	reviseOffer := command.NewReviseOfferHandler(unitOfWork, clock, ids)
	recordAudience := command.NewRecordAudienceSnapshotHandler(unitOfWork)
	recordCampaign := command.NewRecordCampaignHandler(unitOfWork)
	recordDeal := command.NewRecordDealHandler(unitOfWork)

	getRequest := query.NewGetRequestHandler(repository)
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
	}

	server := httpapi.NewServer(httpapi.Dependencies{
		SubmitRequest:          submit,
		StartValidation:        startValidation,
		ValidateRequest:        validate,
		StartProcessing:        startProcessing,
		FulfillRequest:         fulfill,
		RejectRequest:          reject,
		CancelRequest:          cancel,
		ReviseOffer:            reviseOffer,
		RecordAudienceSnapshot: recordAudience,
		RecordCampaign:         recordCampaign,
		RecordDeal:             recordDeal,
		GetRequest:             getRequest,
		FulfillmentStatus:      fulfillmentStatus,
		WorkflowStatus:         workflowStatus,
		WorkflowStarter:        workflowStarter,
		WorkflowSignaler:       workflowSignaler,
		ValidationQueue:        validationQueue,
		ApprovalQueue:          query.NewApprovalQueueHandler(repository, workflowStatus),
		InitiateCall:           initiateCall,
		RecordCall:             recordCall,
		EmbedOffer:             embedOffer,
		EmailDrafts:            emailDrafts,
		Runtime:                cfg.Runtime,
		Console:                cfg.Console,
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
		relay = platformoutbox.NewRelay(journal, cfg.Publisher, cfg.Interval)
	}

	return &Slice{
		Repository: repository,
		Journal:    journal,
		UnitOfWork: unitOfWork,
		Relay:      relay,
		Server:     server,

		SubmitRequest:          submit,
		StartValidation:        startValidation,
		ValidateRequest:        validate,
		StartProcessing:        startProcessing,
		FulfillRequest:         fulfill,
		RejectRequest:          reject,
		CancelRequest:          cancel,
		ReviseOffer:            reviseOffer,
		RecordAudienceSnapshot: recordAudience,
		RecordCampaign:         recordCampaign,
		RecordDeal:             recordDeal,
		GetRequest:             getRequest,
		FulfillmentStatus:      fulfillmentStatus,

		WorkflowStarter:  workflowStarter,
		WorkflowStatus:   workflowStatus,
		WorkflowSignaler: workflowSignaler,

		DraftStore: draftStore,

		ValidationQueue: validationQueue,
		InitiateCall:    initiateCall,
		RecordCall:      recordCall,
		EmbedOffer:      embedOffer,
		EmailDrafts:     emailDrafts,
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

	slice, err := Build(cfg)
	if err != nil {
		return nil, err
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
		slice:          slice,
		apiAddr:        cfg.APIAddr,
		logger:         cfg.Logger,
		metrics:        cfg.Metrics,
		tracer:         cfg.Tracer,
		workflowWorker: workflowWorker,
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
	return &workflowadapter.Activities{
		StartProcessing:   slice.StartProcessing,
		Fulfill:           slice.FulfillRequest,
		Cancel:            slice.CancelRequest,
		Search:            search,
		Drafts:            drafts,
		Clock:             cfg.Clock,
		IDs:               cfg.IDs,
		HeartbeatInterval: cfg.LongActivityHeartbeat,
		Logger:            cfg.Logger,
	}
}

// ID реализует composition.Service.
func (s *Service) ID() string { return ServiceID }

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
// доставка at-least-once, а потребитель дедуплицирует по event_id. Потребителей
// очередей брокера в срезе нет — это не скрывается, а логируется.
func (s *Service) runWorker(ctx context.Context) error {
	if s.slice.Relay == nil && s.workflowWorker == nil && s.availabilityMonitor == nil {
		return fmt.Errorf("worker: ни публикация событий заявки, ни воркер workflow не собраны")
	}
	if s.slice.Relay != nil {
		s.logger.Info("worker: публикация событий заявки запущена", observ.Fields{}, "service", ServiceID)
	}
	if s.workflowWorker == nil {
		s.logger.Warn("worker: воркер workflow не собран (Temporal не подключён)", observ.Fields{}, "service", ServiceID)
	}
	s.logger.Warn("worker: обработчики очередей брокера не зарегистрированы", observ.Fields{}, "service", ServiceID)

	// Контекст контуров отменяется при падении любого из них: роль не должна
	// зависать на долгоживущем контуре (например, relay), пока сбойный уже упал.
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 3)
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
		run("workflow", s.workflowWorker.Run)
	}
	if s.availabilityMonitor != nil {
		run("runtime-monitor", s.availabilityMonitor.Run)
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
