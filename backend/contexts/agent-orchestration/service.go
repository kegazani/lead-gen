package agentorchestration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/blocks"
	httpapi "lead-gen/backend/contexts/agent-orchestration/internal/adapters/http"
	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/idgen"
	outboxinmemory "lead-gen/backend/contexts/agent-orchestration/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/persistence/inmemory"
	outboxpersistence "lead-gen/backend/contexts/agent-orchestration/internal/adapters/persistence/postgres"
	workflowadapter "lead-gen/backend/contexts/agent-orchestration/internal/adapters/workflow"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/command"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/query"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/registry"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/supervisor"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	composition "lead-gen/backend/platform/composition"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/readiness"
	"lead-gen/backend/platform/transport/amqp"
	platformworkflow "lead-gen/backend/platform/workflow"
	"lead-gen/contracts/content-production/contentrequest"
	"lead-gen/contracts/lead-management/leadsearch"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "agent-orchestration"

// defaultPublishInterval — интервал проходов relay публикации событий агента, если
// задано некорректное значение.
const defaultPublishInterval = time.Second

// defaultScheduleInterval — интервал проходов расписания, запускающего очередь
// супервизора.
const defaultScheduleInterval = 5 * time.Second

// SystemClock — часы процесса, источник времени по умолчанию.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	readinessWorkerActive *atomic.Bool
	// APIAddr — адрес внутреннего HTTP-сервера роли api.
	APIAddr string

	// Persistence — адаптеры хранилища. Нулевое значение означает dev-режим:
	// сборка берёт in-memory адаптеры. PostgreSQL-набор задаётся полностью (иначе
	// подмена одного адаптера молчаливым in-memory была бы ложью).
	Persistence Persistence

	// Clock — источник времени прикладного слоя. nil → SystemClock.
	Clock ports.Clock

	// IDs — генератор идентификаторов. nil → случайный генератор.
	IDs ports.IDGenerator

	// Blocks — реестр зарегистрированных блоков (состав пайплайна). nil — пустой
	// реестр: публикация пайплайна со ссылкой на блок отвергается.
	Blocks *domain.BlockRegistry

	// Executors — исполнители блоков по контрактам владельцев. Незарегистрированный
	// исполнитель даёт честную недоступность шага, а не фиктивный результат.
	Executors []ports.BlockExecutor

	// LeadSearch — порт опубликованного контракта lead-search (владелец —
	// lead-management). Если задан, блок поиска исполняется через контракт
	// владельца; nil — блок поиска честно недоступен.
	LeadSearch leadsearch.Port

	// Research — отдельный durable worker нового исследовательского пути.
	// nil сохраняет существующий demo; запускается только в роли worker.
	Research *ResearchService

	// SearchLimit — сколько компаний просит блок поиска; 0 — одна компания.
	SearchLimit int

	// LeadRelevance — порт read-контракта lead-relevance (владелец —
	// lead-management). Если задан, блок оценки релевантности исполняется через
	// контракт владельца; nil — блок честно недоступен.
	LeadRelevance ports.RelevanceEvaluator

	// ContentRequest — порт опубликованного контракта content-request (владелец —
	// content-production). Если задан, блок производства черновика исполняется
	// через контракт владельца; nil — блок честно недоступен.
	ContentRequest contentrequest.Port

	// ContentLanguage — язык материала письма. Пусто — значение по умолчанию.
	ContentLanguage string

	// ApprovalSubmitter — порт контракта утверждения черновика (владелец —
	// outreach-orchestration, форма campaign-command). Если задан, блок ожидания
	// утверждения подаёт черновик владельцу; nil — блок честно недоступен.
	ApprovalSubmitter ports.ApprovalSubmitter

	// ApprovedAction — издатель контракта approved-action для шага отправки
	// (исполняет message-delivery). nil — блок отправки честно недоступен.
	ApprovedAction approvedaction.Publisher

	// ApprovedActionSource — источник утверждённого действия по черновику для шага
	// отправки. nil — блок отправки честно недоступен.
	ApprovedActionSource ports.ApprovedActionSource

	// Workflows — переопределение порта долгих процессов (тесты). nil — адаптер
	// поверх WorkflowClient, если он задан; иначе рантайм недоступен.
	Workflows ports.WorkflowRunner

	// WorkflowClient — клиент Temporal. nil означает, что рантайм долгих процессов
	// не подключён: агенты остаются в очереди, а роли worker/chains не поднимают
	// воркер.
	WorkflowClient *platformworkflow.Client

	// WorkflowTaskQueue — очередь задач воркера. Пусто — очередь клиента по
	// умолчанию.
	WorkflowTaskQueue string

	// Publisher — издатель брокера. nil означает, что публикация не собирается:
	// процессы не запускают relay событий агента.
	Publisher amqp.Publisher

	// Interval — интервал прохода relay публикации событий агента.
	Interval time.Duration

	// MaxConcurrent — предел одновременной обработки компаний (на тенант).
	MaxConcurrent int

	// DefaultMaxAttempts — предел попыток шага по умолчанию.
	DefaultMaxAttempts int

	// ApprovalTTL — срок паузы утверждения необратимого шага.
	ApprovalTTL time.Duration

	// ExecutionTimeout — предельное время исполнения запуска.
	ExecutionTimeout time.Duration

	// ActivityStartToClose — StartToCloseTimeout шага.
	ActivityStartToClose time.Duration

	// ActivityMaxAttempts — число попыток activity шага.
	ActivityMaxAttempts int

	// WorkerStopTimeout — время graceful drain воркера workflow при остановке.
	WorkerStopTimeout time.Duration

	// WorkerMaxConcurrentActivities — предел одновременных activities воркера.
	WorkerMaxConcurrentActivities int

	// WorkerMaxConcurrentWorkflows — предел одновременных задач workflow воркера.
	WorkerMaxConcurrentWorkflows int

	// Access — проверка права актора действовать в тенанте (access-decision). nil —
	// владелец решения не подключён: HTTP-поверхность честно отвечает 503.
	Access ports.AccessChecker

	// Runtime — признак доступности рантайма долгих процессов для пробы готовности
	// /readyz. nil — рантайм не подключён.
	Runtime *platformworkflow.Availability

	// ScheduleTenants — тенанты, очередь которых разбирает роль schedule. Пусто —
	// расписание не запускает никого и логирует пропуск.
	ScheduleTenants []string

	// ScheduleInterval — интервал проходов расписания.
	ScheduleInterval time.Duration

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются composition
	// root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Slice — собранный контекст: порты, адаптеры и use cases.
type Slice struct {
	Pipelines  ports.PipelineRepository
	Runs       ports.RunRepository
	UnitOfWork ports.UnitOfWork
	Journal    platformoutbox.Journal

	// Relay не равен nil только если при сборке задан Publisher.
	Relay *platformoutbox.Relay

	// Server — HTTP-обработчики роли api. Реализует http.Handler.
	Server *httpapi.Server

	PublishPipeline *command.PublishPipelineHandler
	StartRun        *command.StartRunHandler
	StopRun         *command.StopRunHandler
	RecordApproval  *command.RecordApprovalHandler
	RunStep         *command.RunStepHandler
	RunState        *query.RunStateHandler
	ListRuns        *query.ListRunsHandler

	Supervisor *supervisor.Supervisor
	Workflows  ports.WorkflowRunner
}

// Build собирает контекст среза из адаптеров и переданных зависимостей. Бизнес-правил
// здесь нет — соединение адаптеров, портов и use cases только своего контекста.
func Build(cfg Config) (*Slice, error) {
	pipelines := cfg.Persistence.Pipelines
	runs := cfg.Persistence.Runs
	journal := cfg.Persistence.Journal
	unitOfWork := cfg.Persistence.UnitOfWork

	if pipelines == nil && runs == nil && unitOfWork == nil {
		// Dev-режим: in-memory адаптеры разделяют одно состояние.
		repos := inmemory.New()
		pipelines, runs = repos.Pipelines, repos.Runs
		if journal == nil {
			journal = platformoutbox.NewMemoryJournal()
		}
		if inMemoryJournal, ok := journal.(*platformoutbox.MemoryJournal); ok {
			unitOfWork = outboxinmemory.NewUnitOfWork(repos.Store(), inMemoryJournal)
		} else {
			unitOfWork = outboxinmemory.NewUnitOfWork(repos.Store(), journal)
		}
	} else {
		if journal == nil {
			journal = platformoutbox.NewMemoryJournal()
		}
		if unitOfWork == nil {
			return nil, errors.New("agent-orchestration: unit-of-work обязателен для не in-memory адаптеров")
		}
	}

	clock := cfg.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	ids := cfg.IDs
	if ids == nil {
		ids = idgen.NewRandom()
	}

	blockRegistry := cfg.Blocks
	if blockRegistry == nil {
		empty, err := domain.NewBlockRegistry()
		if err != nil {
			return nil, err
		}
		blockRegistry = empty
	}

	executors := append([]ports.BlockExecutor(nil), cfg.Executors...)
	if cfg.LeadSearch != nil {
		executors = append(executors, blocks.NewSearchExecutor(cfg.LeadSearch, cfg.SearchLimit))
	}
	if cfg.LeadRelevance != nil {
		executors = append(executors, blocks.NewClassifyExecutor(cfg.LeadRelevance))
	}
	if cfg.ContentRequest != nil {
		executors = append(executors, blocks.NewGenerateEmailExecutor(cfg.ContentRequest, cfg.ContentLanguage))
	}
	if cfg.ApprovalSubmitter != nil {
		executors = append(executors, blocks.NewAwaitApprovalExecutor(cfg.ApprovalSubmitter))
	}
	if cfg.ApprovedAction != nil && cfg.ApprovedActionSource != nil {
		executors = append(executors, blocks.NewSendExecutor(cfg.ApprovedAction, cfg.ApprovedActionSource))
	}
	executorsRegistry, err := registry.New(executors...)
	if err != nil {
		return nil, err
	}

	workflows := cfg.Workflows
	if workflows == nil && cfg.WorkflowClient != nil {
		workflows = workflowadapter.NewClient(cfg.WorkflowClient, cfg.WorkflowTaskQueue, workflowadapter.ExecutionPolicy{
			ApprovalTTL:          cfg.ApprovalTTL,
			ExecutionTimeout:     cfg.ExecutionTimeout,
			ActivityStartToClose: cfg.ActivityStartToClose,
			ActivityMaxAttempts:  cfg.ActivityMaxAttempts,
		})
	}

	sup := supervisor.New(unitOfWork, runs, workflows, clock, ids, cfg.MaxConcurrent)

	publishPipeline := command.NewPublishPipelineHandler(unitOfWork, blockRegistry, pipelines, clock, ids)
	startRun := command.NewStartRunHandler(unitOfWork, pipelines, runs, clock, ids, sup)
	stopRun := command.NewStopRunHandler(sup)
	recordApproval := command.NewRecordApprovalHandler(unitOfWork, workflows, clock, ids)
	runStep := command.NewRunStepHandler(unitOfWork, executorsRegistry, clock, ids)
	runState := query.NewRunStateHandler(runs)
	listRuns := query.NewListRunsHandler(runs)

	server := httpapi.NewServer(httpapi.Dependencies{
		StartRun:           startRun,
		StopRun:            stopRun,
		RetryStep:          sup,
		RunStep:            runStep,
		RecordApproval:     recordApproval,
		RunState:           runState,
		ListRuns:           listRuns,
		Access:             cfg.Access,
		Runtime:            runtimeProbe(cfg.Runtime),
		Readiness:          readinessProfile(cfg.Persistence.Pool, cfg.Runtime, cfg.WorkflowClient, cfg.readinessWorkerActive),
		ReadinessEvaluator: readiness.New(readiness.Config{Timeout: 750 * time.Millisecond, TTL: 2 * time.Second}),
	})

	var relay *platformoutbox.Relay
	if cfg.Publisher != nil {
		relay = platformoutbox.NewRelay(journal, cfg.Publisher, cfg.Interval).
			WithLogger(cfg.Logger).
			WithMetrics(cfg.Metrics)
	}

	return &Slice{
		Pipelines:       pipelines,
		Runs:            runs,
		UnitOfWork:      unitOfWork,
		Journal:         journal,
		Relay:           relay,
		Server:          server,
		PublishPipeline: publishPipeline,
		StartRun:        startRun,
		StopRun:         stopRun,
		RecordApproval:  recordApproval,
		RunStep:         runStep,
		RunState:        runState,
		ListRuns:        listRuns,
		Supervisor:      sup,
		Workflows:       workflows,
	}, nil
}

// runtimeProbe приводит признак доступности к узкому интерфейсу HTTP-адаптера.
func runtimeProbe(availability *platformworkflow.Availability) httpapi.RuntimeProbe {
	if availability == nil {
		return nil
	}
	return availability
}

// Service — собранный сервис agent-orchestration. Реализует composition.Service:
// раннер на каждую объявленную роль.
type Service struct {
	slice   *Slice
	apiAddr string
	config  Config
	logger  observ.Logger
	metrics observ.Metrics
	tracer  observ.Tracer

	// workflowWorker не равен nil, только если подключён рантайм Temporal.
	workflowWorker *platformworkflow.Worker

	// availabilityMonitor следит за доступностью рантайма для пробы готовности.
	availabilityMonitor   *platformworkflow.AvailabilityMonitor
	readinessWorkerActive *atomic.Bool
}

// New собирает сервис из адаптеров и переданных зависимостей.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("agent-orchestration: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("agent-orchestration: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("agent-orchestration: Tracer обязателен")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultPublishInterval
	}
	if cfg.ScheduleInterval <= 0 {
		cfg.ScheduleInterval = defaultScheduleInterval
	}

	workerActive := &atomic.Bool{}
	cfg.readinessWorkerActive = workerActive
	slice, err := Build(cfg)
	if err != nil {
		return nil, err
	}

	var workflowWorker *platformworkflow.Worker
	if cfg.WorkflowClient != nil {
		reg := platformworkflow.NewRegistry()
		workflowadapter.Register(reg, &workflowadapter.Activities{
			Steps:  slice.RunStep,
			Runs:   slice.Runs,
			Logger: cfg.Logger,
		})
		workflowWorker, err = platformworkflow.NewWorker(cfg.WorkflowClient, reg, platformworkflow.WorkerOptions{
			TaskQueue:                          cfg.WorkflowTaskQueue,
			Logger:                             cfg.Logger,
			WorkerStopTimeout:                  cfg.WorkerStopTimeout,
			MaxConcurrentActivityExecution:     cfg.WorkerMaxConcurrentActivities,
			MaxConcurrentWorkflowTaskExecution: cfg.WorkerMaxConcurrentWorkflows,
		})
		if err != nil {
			return nil, fmt.Errorf("agent-orchestration: воркер workflow: %w", err)
		}
	}

	service := &Service{
		slice:                 slice,
		apiAddr:               cfg.APIAddr,
		config:                cfg,
		logger:                cfg.Logger,
		metrics:               cfg.Metrics,
		tracer:                cfg.Tracer,
		workflowWorker:        workflowWorker,
		readinessWorkerActive: workerActive,
	}
	if cfg.WorkflowClient != nil && cfg.Runtime != nil {
		service.availabilityMonitor = &platformworkflow.AvailabilityMonitor{
			Checker:      cfg.WorkflowClient,
			Availability: cfg.Runtime,
			Logger:       cfg.Logger,
		}
	}
	return service, nil
}

// ID реализует composition.Service.
func (s *Service) ID() string { return ServiceID }

// RoleRunner реализует composition.Service. Раннеры ролей владеют только запуском и
// адаптерами: доменных правил здесь нет.
func (s *Service) RoleRunner(role composition.Role) (composition.RoleRunner, bool) {
	switch role {
	case composition.RoleAPI:
		return composition.RoleFunc(s.runAPI), true
	case composition.RoleWorker:
		return composition.RoleFunc(s.runWorker), true
	case composition.RoleChains:
		return composition.RoleFunc(s.runChains), true
	case composition.RoleSchedule:
		return composition.RoleFunc(s.runSchedule), true
	default:
		return nil, false
	}
}

// runAPI поднимает внутренний HTTP-сервер контекста и останавливает его по отмене
// контекста.
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

// runWorker запускает фоновые контуры роли worker: публикацию событий агента (relay
// outbox) и воркер workflow, исполняющий шаги агентов. Падение любого контура
// останавливает роль.
func (s *Service) runWorker(ctx context.Context) error {
	if s.slice.Relay == nil && s.workflowWorker == nil && s.availabilityMonitor == nil && s.config.Research == nil {
		return fmt.Errorf("worker: ни публикация событий агента, ни воркер workflow не собраны")
	}
	if s.slice.Relay != nil {
		s.logger.Info("worker: публикация событий агента запущена", observ.Fields{}, "service", ServiceID)
	}
	if s.workflowWorker == nil {
		s.logger.Warn("worker: воркер workflow не собран (Temporal не подключён)", observ.Fields{}, "service", ServiceID)
	}
	workflowRun := s.runWorkflowWorker
	if s.workflowWorker == nil {
		workflowRun = nil
	}
	return s.runContours(ctx, []namedContour{
		{name: "relay", fn: relayRunner(s.slice.Relay)},
		{name: "workflow", fn: workflowRun},
		{name: "research", fn: researchRunner(s.config.Research, s.logger)},
		{name: "runtime-monitor", fn: monitorRunner(s.availabilityMonitor)},
	})
}

// runChains запускает контур долгих пауз: воркер workflow, ведущий исполнения с
// человеческими паузами утверждения.
func (s *Service) runChains(ctx context.Context) error {
	if s.workflowWorker == nil {
		return fmt.Errorf("chains: воркер workflow не собран (Temporal не подключён)")
	}
	s.logger.Info("chains: воркер долгих пауз запущен", observ.Fields{}, "service", ServiceID)
	return s.runContours(ctx, []namedContour{{name: "workflow", fn: s.runWorkflowWorker}})
}

func (s *Service) runWorkflowWorker(ctx context.Context) error {
	if s.workflowWorker == nil || s.readinessWorkerActive == nil {
		return fmt.Errorf("workflow: воркер не собран")
	}
	s.readinessWorkerActive.Store(true)
	defer s.readinessWorkerActive.Store(false)
	return s.workflowWorker.Run(ctx)
}

// runSchedule запускает тикер, разбирающий очередь супервизора по тенантам
// идемпотентными окнами.
func (s *Service) runSchedule(ctx context.Context) error {
	tenants := make([]domain.TenantID, 0, len(s.config.ScheduleTenants))
	for _, tenant := range s.config.ScheduleTenants {
		if tenant != "" {
			tenants = append(tenants, domain.TenantID(tenant))
		}
	}
	if len(tenants) == 0 {
		s.logger.Warn("schedule: тенанты не заданы, расписание не запускает очередь", observ.Fields{}, "service", ServiceID)
	}
	ticker := time.NewTicker(s.config.ScheduleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for _, tenant := range tenants {
				if _, err := s.slice.Supervisor.Pump(ctx, tenant); err != nil {
					s.logger.Error("schedule: проход очереди", err, observ.Fields{TenantID: string(tenant)}, "service", ServiceID)
				}
			}
		}
	}
}

// namedContour — именованный фоновый контур роли.
type namedContour struct {
	name string
	fn   func(context.Context) error
}

// runContours запускает контуры и ждёт первого исхода: отмены контекста или ошибки.
// Ошибка отменяет общий контекст, остальные контуры завершаются graceful.
func (s *Service) runContours(ctx context.Context, contours []namedContour) error {
	active := make([]namedContour, 0, len(contours))
	for _, contour := range contours {
		if contour.fn != nil {
			active = append(active, contour)
		}
	}
	if len(active) == 0 {
		return fmt.Errorf("worker: ни одного контура не собрано")
	}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(active))
	var wg sync.WaitGroup
	for _, contour := range active {
		wg.Add(1)
		go func(c namedContour) {
			defer wg.Done()
			if err := c.fn(workerCtx); err != nil {
				errCh <- fmt.Errorf("%s: %w", c.name, err)
			}
		}(contour)
	}

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

// relayRunner, workerRunner и monitorRunner приводят контуры к единому виду с
// учётом возможного nil.
func relayRunner(relay *platformoutbox.Relay) func(context.Context) error {
	if relay == nil {
		return nil
	}
	return relay.Run
}

func workerRunner(worker *platformworkflow.Worker) func(context.Context) error {
	if worker == nil {
		return nil
	}
	return worker.Run
}

func monitorRunner(monitor *platformworkflow.AvailabilityMonitor) func(context.Context) error {
	if monitor == nil {
		return nil
	}
	return monitor.Run
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)

// Проверка использования имени схемы адаптера (граница данных зафиксирована в
// миграциях и адаптере).
var _ = outboxpersistence.Schema

// researchRunner не регистрируется в chains: там живут старые Temporal-процессы.
// Каждое действие нового worker уже имеет собственный persisted lease и epoch.
func researchRunner(research *ResearchService, logger observ.Logger) func(context.Context) error {
	if research == nil {
		return nil
	}
	return func(ctx context.Context) error {
		return research.RunWorker(ctx, 16, func(err error) {
			logger.Error("research: проход долговечной очереди", err, observ.Fields{}, "service", ServiceID)
		})
	}
}
