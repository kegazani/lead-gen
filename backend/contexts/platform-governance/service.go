package platformgovernance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	cockpitsettingsinmemory "lead-gen/backend/contexts/platform-governance/internal/adapters/cockpitsettings/inmemory"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/crypto"
	httpapi "lead-gen/backend/contexts/platform-governance/internal/adapters/http"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/idgen"
	outboxinmemory "lead-gen/backend/contexts/platform-governance/internal/adapters/outbox/inmemory"
	outboxpostgres "lead-gen/backend/contexts/platform-governance/internal/adapters/outbox/postgres"
	persistence "lead-gen/backend/contexts/platform-governance/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/session"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/backend/contexts/platform-governance/internal/app/projection"
	"lead-gen/backend/contexts/platform-governance/internal/app/query"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/observability/prometheus"
	"lead-gen/backend/platform/readiness"
	"lead-gen/backend/platform/topology"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/contracts/analytics/campaignreport"
	"lead-gen/contracts/request-fulfillment/requestintent"
	"lead-gen/frontend/components"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "platform-governance"

// AdapterStatus — состояние адаптера интеграции в форме, публикуемой корневым
// пакетом. Псевдоним ports.AdapterStatus (поля — строки Adapter, State, Reason):
// composition root и тесты реализуют порт AdapterCatalog, не импортируя
// internal/ports.
type AdapterStatus = ports.AdapterStatus

// AdapterCatalog — порт чтения состояния адаптеров интеграций (владелец —
// integration-platform). Псевдоним ports.AdapterCatalog: реализацию передаёт
// точка сборки в Config.Adapters.
type AdapterCatalog = ports.AdapterCatalog

// TopologyMetrics — клиент Prometheus для серверного прокси метрик топологии
// (пакет backend/platform/observability/prometheus удовлетворяет интерфейсу).
type TopologyMetrics = httpapi.MetricsQuerier

// TopologyPanel — объявленная панель метрик консоли топологии.
type TopologyPanel = httpapi.TopologyPanel

// TopologyLayoutSource — источник сохранённых позиций раскладки топологии.
type TopologyLayoutSource = httpapi.TopologyLayoutSource

// OperationsData — источник цифр, ленты и агрегатов операционного экрана.
type OperationsData = httpapi.OperationsData

// WorkflowReader — серверное чтение процессов durable-исполнения (Temporal).
type WorkflowReader = httpapi.WorkflowReader

// RequestSummary — минимальная строка заявки из списка владельца.
type RequestSummary = httpapi.RequestSummary

// RequestExecution — фактические данные исполнения конкретной заявки.
type RequestExecution = httpapi.RequestExecution

// RequestExecutionReader — read-only порт списка заявок и точечного чтения
// workflow; точка сборки передаёт сетевой или in-process адаптер.
type RequestExecutionReader = httpapi.RequestExecutionReader

// CockpitPanel — расширяемая панель экрана операций.
type CockpitPanel = httpapi.CockpitPanel

// NodePosition — позиция узла сохранённой раскладки топологии.
type NodePosition = httpapi.NodePosition

// SystemClock — часы процесса, источник времени по умолчанию.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// APIAddr — адрес HTTP-сервера роли api (портал клиента и API доступа).
	APIAddr string

	// Repository — репозиторий тенантов для чтения (явный адаптер: тесты,
	// линейная сборка). nil → репозиторий транзакции.
	Repository ports.TenantRepository

	// UnitOfWork — транзакция записи агрегата, событий и ключей идемпотентности.
	// nil → in-memory (dev). PostgreSQL-персистентность задаётся парой
	// Repository + UnitOfWork из postgres.
	UnitOfWork ports.UnitOfWork

	// Pool — пул соединений PostgreSQL зелёной зоны. Не nil → персистентность
	// PostgreSQL (тенанты, аудит, outbox, ключи команд). nil и отсутствие
	// Repository/UnitOfWork → in-memory адаптер (dev).
	Pool *pgxpool.Pool

	// Grants — платформенные права акторов (кто управляет тенантами и ролями).
	// Начальное состояние задаёт вызывающий; composition не выдумывает политику.
	// nil → grant'ов нет: изменяющие команды доступа отклоняются как запрещённые.
	Grants ports.Grants

	// ConsoleAuth — учётные данные и стор сессий оператора консоли платформы.
	// Пустой Username отключает вход консоли: разделы доступны по ?actor=
	// (dev, тесты). В контуре логин задаётся CONSOLE_USER, пароль загружается
	// точкой входа из внешнего secret file.
	ConsoleAuth consoleauth.Config

	// Console — базовые адреса сервисов общей консоли. Объединённая навигация
	// ведёт из платформенной консоли в операторские разделы; пустая карта даёт
	// относительные пути.
	Console components.ConsoleBases

	// BasePath — внешний префикс пути консоли платформы под единым origin
	// (admin-proxy). Пусто — корень origin. Клиентский портал (/login, /portal)
	// этим префиксом не затрагивается: он смонтирован под префиксом
	// platform-governance отдельно, а консоль — под /admin.
	BasePath string

	// ExternalPrefixes — префиксы операторских консолей единого origin: ссылки
	// на них в навигации не переписываются под префикс консоли платформы.
	ExternalPrefixes []string

	// Operators — субъекты платформенного масштаба: операторы, действующие во
	// всех активных тенантах (обычно один оператор консолей). Пустой список
	// сохраняет изоляцию: доступ решают только роли тенанта.
	Operators []string

	// Requests — порт опубликованного контракта request-intent (владелец —
	// request-fulfillment). nil означает, что владелец заявки не подключён:
	// портал честно отвечает «недоступен», а не выдумывает заявку.
	Requests requestintent.Port

	// Report — порт опубликованного read-контракта campaign-report (владелец —
	// analytics): цифры процесса для метрик главного экрана портала. nil
	// означает, что владелец не подключён: портал честно показывает «—», а не
	// нули (AGENTS.md §3).
	Report campaignreport.Port

	// PortalStreamPoll — период опроса владельца заявок в SSE-эндпоинте портала.
	// 0 — значение по умолчанию. Используется только для расчёта периода, не для
	// подмены владельца: каждый кадр честно читает requestintent.List.
	PortalStreamPoll time.Duration

	// PortalStreamHeartbeat — период heartbeat SSE-потока портала. 0 — значение
	// по умолчанию.
	PortalStreamHeartbeat time.Duration

	// Adapters — порт чтения состояния адаптеров интеграций (владелец —
	// integration-platform). Элемент состояния — AdapterStatus (Adapter, State,
	// Reason). nil означает, что владелец не подключён: раздел «Интеграции»
	// честно сообщает о недоступности, а не показывает пустой список как успех.
	Adapters ports.AdapterCatalog

	// Topology — загрузчик модели графа сервисной топологии (backend/platform/
	// topology). nil означает, что модель не подключена: экран честно сообщает о
	// недоступности, а не выдумывает граф.
	Topology *topology.Loader

	// TopologyMetrics — клиент Prometheus для серверного прокси метрик. nil
	// означает, что метрики недоступны (available:false) без нулевых точек.
	TopologyMetrics TopologyMetrics

	// TopologyPanels — объявленный перечень панелей метрик. nil — встроенный
	// набор DefaultTopologyPanels; произвольный PromQL от клиента не принимается.
	TopologyPanels []TopologyPanel

	// TopologyLayout — источник сохранённых позиций раскладки. nil или ошибка →
	// детерминированная раскладка по умолчанию.
	TopologyLayout TopologyLayoutSource

	// Operations — источник цифр процесса, ленты агентов и агрегатов по клиентам
	// (владелец — agent-orchestration и аналитика). nil означает, что владелец не
	// подключён: экран операций честно отвечает «недоступно», а не нулями.
	Operations OperationsData

	// WorkflowReader — серверное чтение процессов Temporal для графа процессов.
	// nil означает, что рантайм не подключён: раздел показывает «недоступно».
	WorkflowReader WorkflowReader

	// RequestExecutions — источник списка заявок и точечных сведений исполнения
	// для компактного блока в разделе «Операции». nil/ошибка показываются честно.
	RequestExecutions RequestExecutionReader

	// CockpitMetrics — клиент Prometheus для метрик-панелей экрана операций. nil
	// откатывается на TopologyMetrics (тот же Prometheus контура).
	CockpitMetrics TopologyMetrics

	// CockpitMetricPanels — объявленный перечень метрик-панелей экрана операций.
	// nil — встроенный набор.
	CockpitMetricPanels []prometheus.Panel

	// CockpitPanels — дополнительные панели экрана (расширяемость регистрацией).
	CockpitPanels []CockpitPanel

	// CockpitSettings — настройки экрана операций по субъекту. nil — состояние в
	// памяти процесса (настройки интерфейса, не доменные данные).
	CockpitSettings ports.CockpitSettings

	// CockpitMinGroup — порог минимальной группы полноэкранного дашборда.
	CockpitMinGroup int

	// Clock — источник времени прикладного слоя. nil → SystemClock.
	Clock ports.Clock

	// Publisher — реальный AMQP transport с publisher confirms. Памятный broker
	// разрешён только тестам/dev; рабочая роль worker требует этот порт.
	Publisher amqp.Publisher
	// OutboxInterval — период опроса PostgreSQL outbox; <= 0 означает 1 секунду.
	OutboxInterval time.Duration

	// IDs — генератор идентификаторов. nil → случайный генератор.
	IDs ports.IDGenerator

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Slice — собранный контекст среза: порты, адаптеры и use cases.
type Slice struct {
	Repository ports.TenantRepository
	Projection *projection.LastRequest

	// Journal — журнал outbox, доступный для in-memory сборки (проверки,
	// публикация в одном процессе). nil для прочих реализаций.
	Journal *outboxinmemory.Journal

	// Server — HTTP-обработчики роли api. Реализует http.Handler.
	Server *httpapi.Server

	RegisterClient      *command.RegisterClientHandler
	SubmitClientRequest *command.SubmitClientRequestHandler
	PortalState         *query.PortalStateHandler
	ListClientRequests  *query.ListClientRequestsHandler

	ApproveRegistration *command.ApproveRegistrationHandler
	RejectRegistration  *command.RejectRegistrationHandler
	SaveProfile         *command.SaveProfileHandler
	SubmitProfile       *command.SubmitProfileHandler
	ApproveProfile      *command.ApproveProfileHandler
	RejectProfile       *command.RejectProfileHandler
	AuthenticateClient  *command.AuthenticateClientHandler
	ResetClientPassword *command.ResetClientPasswordHandler
	TenantProfile       *query.TenantProfileHandler

	CreateTenant  *command.CreateTenantHandler
	DisableTenant *command.DisableTenantHandler
	AssignRole    *command.AssignRoleHandler
	RevokeRole    *command.RevokeRoleHandler
	DecideAccess  *query.DecideAccessHandler
	AuditJournal  *query.AuditJournalHandler
	TenantUsers   *query.TenantUsersHandler
	ListTenants   *query.ListTenantsHandler
	Adapters      *query.AdapterCatalogHandler
}

// Build собирает контекст среза из адаптеров и переданных зависимостей.
// Бизнес-правил здесь нет — соединение адаптеров, портов и use cases только
// своего контекста.
func Build(cfg Config) (*Slice, error) {
	consoleUsernameConfigured := strings.TrimSpace(cfg.ConsoleAuth.Username) != ""
	consolePasswordConfigured := len(cfg.ConsoleAuth.Password) > 0
	if consoleUsernameConfigured != consolePasswordConfigured {
		return nil, fmt.Errorf("platform-governance: console auth требует одновременно username и непустой password")
	}

	repository := cfg.Repository
	uow := cfg.UnitOfWork
	if repository != nil && uow == nil {
		return nil, fmt.Errorf("platform-governance: Repository задан без UnitOfWork: атомарность записи не обеспечена")
	}
	if uow == nil {
		memory := outboxinmemory.NewUnitOfWork()
		uow = memory
		if repository == nil {
			repository = memory.Repository()
		}
	}
	if repository == nil {
		provider, ok := uow.(interface {
			Repository() *persistence.Repository
		})
		if !ok {
			return nil, fmt.Errorf("platform-governance: не удалось получить репозиторий тенантов")
		}
		repository = provider.Repository()
	}

	// Список тенантов читает тот же адаптер хранилища через read-порт
	// TenantDirectory. Отсутствие реализации — ошибка сборки, а не пустой
	// экран: продуктовая функция не подменяется заглушкой.
	directory, ok := repository.(ports.TenantDirectory)
	if !ok {
		return nil, fmt.Errorf("platform-governance: репозиторий тенантов не реализует чтение списка (ports.TenantDirectory)")
	}

	clock := cfg.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	ids := cfg.IDs
	if ids == nil {
		ids = idgen.NewRandom()
	}
	requests := cfg.Requests
	if requests == nil {
		requests = unavailableRequestIntent{}
	}
	report := cfg.Report
	if report == nil {
		report = unavailableReport{}
	}
	cockpitMetrics := cfg.CockpitMetrics
	if cockpitMetrics == nil {
		cockpitMetrics = cfg.TopologyMetrics
	}
	cockpitSettings := cfg.CockpitSettings
	if cockpitSettings == nil {
		cockpitSettings = cockpitsettingsinmemory.New()
	}

	proj := projection.NewLastRequest()
	hasher := crypto.New()
	registerClient := command.NewRegisterClientHandler(uow, clock, ids, hasher)
	submitClientRequest := command.NewSubmitClientRequestHandler(requests, ids, proj)
	portalState := query.NewPortalStateHandler(requests, proj)
	listClientRequests := query.NewListClientRequestsHandler(requests)
	approveRegistration := command.NewApproveRegistrationHandler(uow, clock, ids, cfg.Grants)
	rejectRegistration := command.NewRejectRegistrationHandler(uow, clock, ids, cfg.Grants)
	saveProfile := command.NewSaveProfileHandler(uow)
	submitProfile := command.NewSubmitProfileHandler(uow)
	approveProfile := command.NewApproveProfileHandler(uow, clock, ids, cfg.Grants)
	rejectProfile := command.NewRejectProfileHandler(uow, clock, ids, cfg.Grants)
	authenticateClient := command.NewAuthenticateClientHandler(repository, hasher)
	resetClientPassword := command.NewResetClientPasswordHandler(uow, clock, ids, cfg.Grants, hasher)
	tenantProfile := query.NewTenantProfileHandler(repository)
	createTenant := command.NewCreateTenantHandler(uow, clock, ids, cfg.Grants)
	disableTenant := command.NewDisableTenantHandler(uow, clock, ids, cfg.Grants)
	assignRole := command.NewAssignRoleHandler(uow, clock, ids, cfg.Grants)
	revokeRole := command.NewRevokeRoleHandler(uow, clock, ids, cfg.Grants)
	decideAccess := query.NewDecideAccessHandler(repository, cfg.Operators...)
	auditJournal := query.NewAuditJournalHandler(repository)
	tenantUsers := query.NewTenantUsersHandler(repository)
	listTenants := query.NewListTenantsHandler(directory)
	adapters := query.NewAdapterCatalogHandler(cfg.Adapters)

	// Сессии портала: PostgreSQL при наличии пула, иначе память процесса (dev).
	var portalSessions *session.Manager
	if cfg.Pool != nil {
		portalSessions = session.NewManager(session.NewPostgresStore(cfg.Pool), clock.Now)
	} else {
		portalSessions = session.NewManager(session.NewMemoryStore(), clock.Now)
	}

	// Вход оператора консоли платформы: общий consoleauth. Пустая пара
	// учётных данных отключает вход — консоль доступна по ?actor= (dev, тесты).
	var consoleAuth *consoleauth.Manager
	if consoleUsernameConfigured {
		consoleAuth = consoleauth.New(cfg.ConsoleAuth)
	}

	server := httpapi.NewServer(httpapi.Dependencies{
		RegisterClient:        registerClient,
		SubmitClientRequest:   submitClientRequest,
		PortalState:           portalState,
		ListClientRequests:    listClientRequests,
		Report:                report,
		PortalStreamPoll:      cfg.PortalStreamPoll,
		PortalStreamHeartbeat: cfg.PortalStreamHeartbeat,
		ApproveRegistration:   approveRegistration,
		RejectRegistration:    rejectRegistration,
		SaveProfile:           saveProfile,
		SubmitProfile:         submitProfile,
		ApproveProfile:        approveProfile,
		RejectProfile:         rejectProfile,
		AuthenticateClient:    authenticateClient,
		ResetClientPassword:   resetClientPassword,
		TenantProfile:         tenantProfile,
		Sessions:              portalSessions,
		CreateTenant:          createTenant,
		DisableTenant:         disableTenant,
		AssignRole:            assignRole,
		RevokeRole:            revokeRole,
		DecideAccess:          decideAccess,
		AuditJournal:          auditJournal,
		TenantUsers:           tenantUsers,
		ListTenants:           listTenants,
		Adapters:              adapters,
		ConsoleAuth:           consoleAuth,
		Console:               cfg.Console,
		BasePath:              cfg.BasePath,
		ExternalPrefixes:      cfg.ExternalPrefixes,
		Topology:              cfg.Topology,
		TopologyMetrics:       cfg.TopologyMetrics,
		TopologyPanels:        cfg.TopologyPanels,
		TopologyLayout:        cfg.TopologyLayout,
		Operations:            cfg.Operations,
		WorkflowReader:        cfg.WorkflowReader,
		RequestExecutions:     cfg.RequestExecutions,
		CockpitMetrics:        cockpitMetrics,
		CockpitMetricPanels:   cfg.CockpitMetricPanels,
		CockpitPanels:         cfg.CockpitPanels,
		CockpitSettings:       cockpitSettings,
		CockpitMinGroup:       cfg.CockpitMinGroup,
		IDs:                   ids,
		Logger:                cfg.Logger,
	})

	slice := &Slice{
		Repository:          repository,
		Projection:          proj,
		Server:              server,
		RegisterClient:      registerClient,
		SubmitClientRequest: submitClientRequest,
		PortalState:         portalState,
		ListClientRequests:  listClientRequests,
		ApproveRegistration: approveRegistration,
		RejectRegistration:  rejectRegistration,
		SaveProfile:         saveProfile,
		SubmitProfile:       submitProfile,
		ApproveProfile:      approveProfile,
		RejectProfile:       rejectProfile,
		AuthenticateClient:  authenticateClient,
		ResetClientPassword: resetClientPassword,
		TenantProfile:       tenantProfile,
		CreateTenant:        createTenant,
		DisableTenant:       disableTenant,
		AssignRole:          assignRole,
		RevokeRole:          revokeRole,
		DecideAccess:        decideAccess,
		AuditJournal:        auditJournal,
		TenantUsers:         tenantUsers,
		ListTenants:         listTenants,
		Adapters:            adapters,
	}
	if provider, ok := uow.(interface {
		Journal() *outboxinmemory.Journal
	}); ok {
		slice.Journal = provider.Journal()
	}
	return slice, nil
}

// Service — собранный сервис platform-governance. Реализует composition.Service:
// раннер на каждую объявленную роль.
type Service struct {
	slice     *Slice
	apiAddr   string
	logger    observ.Logger
	metrics   observ.Metrics
	tracer    observ.Tracer
	relay     interface{ Run(context.Context) error }
	pool      *pgxpool.Pool
	readiness *readiness.Evaluator
	publisher amqp.Publisher
	relayLoop *workerSignal
}

// New собирает сервис из адаптеров и переданных зависимостей.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("platform-governance: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("platform-governance: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("platform-governance: Tracer обязателен")
	}

	// Хранилище: PostgreSQL по пулу, явные адаптеры либо in-memory (dev).
	// О dev-режиме сообщается предупреждением, а не молчаливой подменой
	// заглушкой на продуктовом пути.
	if cfg.Repository == nil && cfg.UnitOfWork == nil {
		if cfg.Pool != nil {
			cfg.Repository = postgres.New(cfg.Pool)
			cfg.UnitOfWork = postgres.NewUnitOfWork(cfg.Pool)
		} else {
			cfg.Logger.Warn("platform-governance: DSN не задан — состояние в памяти процесса (dev)",
				observ.Fields{}, "service", ServiceID)
		}
	}

	slice, err := Build(cfg)
	if err != nil {
		return nil, err
	}
	var relay interface{ Run(context.Context) error }
	if cfg.Pool != nil && cfg.Publisher != nil {
		relay, err = outboxpostgres.NewRelay(cfg.Pool, cfg.Publisher, cfg.Logger, cfg.OutboxInterval)
		if err != nil {
			return nil, err
		}
	}
	return &Service{
		slice:     slice,
		apiAddr:   cfg.APIAddr,
		logger:    cfg.Logger,
		metrics:   cfg.Metrics,
		tracer:    cfg.Tracer,
		relay:     relay,
		pool:      cfg.Pool,
		readiness: readiness.New(readiness.Config{Timeout: 250 * time.Millisecond, TTL: time.Second}),
		publisher: cfg.Publisher,
		relayLoop: &workerSignal{},
	}, nil
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

// Slice возвращает собранный срез (для межконтекстной сборки в одном процессе).
func (s *Service) Slice() *Slice { return s.slice }

// runAPI поднимает HTTP-сервер портала и останавливает его по отмене контекста.
func (s *Service) runAPI(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		writeReadiness(w, s.Readiness(r.Context(), composition.RoleAPI))
	})
	mux.Handle("/", s.slice.Server)
	handler := observ.Middleware(s.logger, s.metrics, s.tracer, ServiceID)(mux)
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

	s.logger.Info("api: HTTP-сервер портала запущен", observ.Fields{}, "service", ServiceID, "addr", s.apiAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("api: работа HTTP-сервера: %w", err)
	}
	return nil
}

// runWorker запускает durable PostgreSQL outbox relay. Без реальных зависимостей
// role завершается ошибкой, а не изображает успешную доставку.
func (s *Service) runWorker(ctx context.Context) error {
	if s.relay == nil {
		return errors.New("platform-governance: worker требует PostgreSQL outbox и AMQP publisher с подтверждениями")
	}
	return runTracked(ctx, s.relayLoop, s.relay.Run)
}

// unavailableRequestIntent — заглушка контракта request-intent: владелец заявки
// не подключён. Она не притворяется успехом, а честно сообщает о недоступности.
type unavailableRequestIntent struct{}

// Submit реализует requestintent.Port.
func (unavailableRequestIntent) Submit(context.Context, requestintent.SubmitIntent) (requestintent.SubmitResult, error) {
	return requestintent.SubmitResult{}, requestintent.ErrUnavailable
}

// Status реализует requestintent.Port.
func (unavailableRequestIntent) Status(context.Context, requestintent.StatusQuery) (requestintent.StatusResult, error) {
	return requestintent.StatusResult{}, requestintent.ErrUnavailable
}

// List реализует requestintent.Port.
func (unavailableRequestIntent) List(context.Context, requestintent.ListQuery) ([]requestintent.ListEntry, error) {
	return nil, requestintent.ErrUnavailable
}

// unavailableReport — заглушка read-контракта campaign-report: владелец агрегатов
// (analytics) не подключён. Она не притворяется пустым успехом и не подставляет
// нули, а честно сообщает о недоступности (AGENTS.md §3).
type unavailableReport struct{}

// Numbers реализует campaignreport.Port.
func (unavailableReport) Numbers(context.Context, campaignreport.Request) (campaignreport.NumbersResponse, error) {
	return campaignreport.NumbersResponse{}, campaignreport.ErrUnavailable
}

// Clients реализует campaignreport.Port.
func (unavailableReport) Clients(context.Context, campaignreport.Request) (campaignreport.ClientsResponse, error) {
	return campaignreport.ClientsResponse{}, campaignreport.ErrUnavailable
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)

// Проверка соответствия порту контракта на этапе компиляции.
var _ requestintent.Port = unavailableRequestIntent{}

// Проверка соответствия read-контракту campaign-report на этапе компиляции.
var _ campaignreport.Port = unavailableReport{}
