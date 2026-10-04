package platformgovernance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	httpapi "lead-gen/backend/contexts/platform-governance/internal/adapters/http"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/idgen"
	outboxinmemory "lead-gen/backend/contexts/platform-governance/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/platform-governance/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/backend/contexts/platform-governance/internal/app/projection"
	"lead-gen/backend/contexts/platform-governance/internal/app/query"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/request-fulfillment/requestintent"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "platform-governance"

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

	// Requests — порт опубликованного контракта request-intent (владелец —
	// request-fulfillment). nil означает, что владелец заявки не подключён:
	// портал честно отвечает «недоступен», а не выдумывает заявку.
	Requests requestintent.Port

	// Clock — источник времени прикладного слоя. nil → SystemClock.
	Clock ports.Clock

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

	CreateTenant  *command.CreateTenantHandler
	DisableTenant *command.DisableTenantHandler
	AssignRole    *command.AssignRoleHandler
	RevokeRole    *command.RevokeRoleHandler
	DecideAccess  *query.DecideAccessHandler
	AuditJournal  *query.AuditJournalHandler
}

// Build собирает контекст среза из адаптеров и переданных зависимостей.
// Бизнес-правил здесь нет — соединение адаптеров, портов и use cases только
// своего контекста.
func Build(cfg Config) (*Slice, error) {
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

	proj := projection.NewLastRequest()
	registerClient := command.NewRegisterClientHandler(uow, clock, ids)
	submitClientRequest := command.NewSubmitClientRequestHandler(requests, ids, proj)
	portalState := query.NewPortalStateHandler(requests, proj)
	createTenant := command.NewCreateTenantHandler(uow, clock, ids, cfg.Grants)
	disableTenant := command.NewDisableTenantHandler(uow, clock, ids, cfg.Grants)
	assignRole := command.NewAssignRoleHandler(uow, clock, ids, cfg.Grants)
	revokeRole := command.NewRevokeRoleHandler(uow, clock, ids, cfg.Grants)
	decideAccess := query.NewDecideAccessHandler(repository)
	auditJournal := query.NewAuditJournalHandler(repository)

	server := httpapi.NewServer(httpapi.Dependencies{
		RegisterClient:      registerClient,
		SubmitClientRequest: submitClientRequest,
		PortalState:         portalState,
		CreateTenant:        createTenant,
		DisableTenant:       disableTenant,
		AssignRole:          assignRole,
		RevokeRole:          revokeRole,
		DecideAccess:        decideAccess,
		AuditJournal:        auditJournal,
	})

	slice := &Slice{
		Repository:          repository,
		Projection:          proj,
		Server:              server,
		RegisterClient:      registerClient,
		SubmitClientRequest: submitClientRequest,
		PortalState:         portalState,
		CreateTenant:        createTenant,
		DisableTenant:       disableTenant,
		AssignRole:          assignRole,
		RevokeRole:          revokeRole,
		DecideAccess:        decideAccess,
		AuditJournal:        auditJournal,
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
	slice   *Slice
	apiAddr string
	logger  observ.Logger
	metrics observ.Metrics
	tracer  observ.Tracer
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
	return &Service{
		slice:   slice,
		apiAddr: cfg.APIAddr,
		logger:  cfg.Logger,
		metrics: cfg.Metrics,
		tracer:  cfg.Tracer,
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

	s.logger.Info("api: HTTP-сервер портала запущен", observ.Fields{}, "service", ServiceID, "addr", s.apiAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("api: работа HTTP-сервера: %w", err)
	}
	return nil
}

// runWorker — честное состояние роли worker: публикация outbox во внешний
// брокер не реализована (сетевой слой отложен, design.md «Отложено 2»). Роль
// сообщает об этом и не делает вид, что доставляет сообщения.
func (s *Service) runWorker(ctx context.Context) error {
	s.logger.Warn("worker: публикация outbox во внешний брокер не подключена",
		observ.Fields{}, "service", ServiceID)
	<-ctx.Done()
	return nil
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

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)

// Проверка соответствия порту контракта на этапе компиляции.
var _ requestintent.Port = unavailableRequestIntent{}
