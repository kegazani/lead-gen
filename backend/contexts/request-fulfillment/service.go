package requestfulfillment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	platformworkflow "lead-gen/backend/platform/workflow"
	"lead-gen/contracts/integration-platform/vector"
	"lead-gen/contracts/outreach-orchestration/callintent"
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

	// Repository — репозиторий заявок. nil → новый in-memory репозиторий.
	Repository *persistence.Repository

	// Journal — журнал outbox событий заявки. nil → новый журнал в памяти.
	Journal platformoutbox.Journal

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

	// SearchLeads — заглушка шага поиска: сколько лидов «находит» activity. 0 —
	// поиск не подключён (ноль найденных лидов). Реальный вызов lead-management
	// придёт адаптером контракта в change №4 (semantic-lead-search).
	SearchLeads int

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Slice — собранный контекст среза: порты, адаптеры и use cases.
type Slice struct {
	Repository *persistence.Repository
	Journal    platformoutbox.Journal
	UnitOfWork *inmemory.UnitOfWork

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

	ValidationQueue *query.ValidationQueueHandler
	InitiateCall    *command.InitiateCallHandler
	RecordCall      *command.RecordCallHandler
	EmbedOffer      *command.EmbedOfferHandler
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

	unitOfWork := inmemory.NewUnitOfWorkWith(repository, journal)

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
		adapter := workflowadapter.NewClient(cfg.WorkflowClient, cfg.WorkflowTaskQueue)
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

		ValidationQueue: validationQueue,
		InitiateCall:    initiateCall,
		RecordCall:      recordCall,
		EmbedOffer:      embedOffer,
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
			TaskQueue: cfg.WorkflowTaskQueue,
			Logger:    cfg.Logger,
		})
		if err != nil {
			return nil, fmt.Errorf("request-fulfillment: воркер workflow: %w", err)
		}
	}

	return &Service{
		slice:          slice,
		apiAddr:        cfg.APIAddr,
		logger:         cfg.Logger,
		metrics:        cfg.Metrics,
		tracer:         cfg.Tracer,
		workflowWorker: workflowWorker,
	}, nil
}

// newActivities собирает activities фулфилмента как тонкие адаптеры к
// application services контекста. Доменных решений здесь нет.
func newActivities(slice *Slice, cfg Config) *workflowadapter.Activities {
	var search workflowadapter.SearchStub = workflowadapter.NoSearch{}
	if cfg.SearchLeads > 0 {
		search = workflowadapter.FixedSearch{Leads: cfg.SearchLeads}
	}
	return &workflowadapter.Activities{
		StartProcessing: slice.StartProcessing,
		Fulfill:         slice.FulfillRequest,
		Cancel:          slice.CancelRequest,
		Search:          search,
		Clock:           cfg.Clock,
		IDs:             cfg.IDs,
		Logger:          cfg.Logger,
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
// (relay outbox) и воркер workflow фулфилмента. Контуры независимы и работают
// до отмены контекста; падение любого из них останавливает роль.
//
// Транспортный relay публикует подтверждённые записи outbox в брокер. Событие,
// не прошедшее подтверждение, остаётся в журнале и будет опубликовано повторно:
// доставка at-least-once, а потребитель дедуплицирует по event_id. Потребителей
// очередей брокера в срезе нет — это не скрывается, а логируется.
func (s *Service) runWorker(ctx context.Context) error {
	if s.slice.Relay == nil && s.workflowWorker == nil {
		return fmt.Errorf("worker: ни публикация событий заявки, ни воркер workflow не собраны")
	}
	if s.slice.Relay != nil {
		s.logger.Info("worker: публикация событий заявки запущена", observ.Fields{}, "service", ServiceID)
	}
	if s.workflowWorker == nil {
		s.logger.Warn("worker: воркер workflow не собран (Temporal не подключён)", observ.Fields{}, "service", ServiceID)
	}
	s.logger.Warn("worker: обработчики очередей брокера не зарегистрированы", observ.Fields{}, "service", ServiceID)

	errCh := make(chan error, 2)
	if s.slice.Relay != nil {
		go func() { errCh <- s.slice.Relay.Run(ctx) }()
	}
	if s.workflowWorker != nil {
		go func() { errCh <- s.workflowWorker.Run(ctx) }()
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("worker: %w", err)
		}
		return nil
	}
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
