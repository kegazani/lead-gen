package datagovernance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/data-governance/internal/adapters/access"
	httpapi "lead-gen/backend/contexts/data-governance/internal/adapters/http"
	"lead-gen/backend/contexts/data-governance/internal/adapters/idgen"
	persistence "lead-gen/backend/contexts/data-governance/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/data-governance/internal/app/command"
	"lead-gen/backend/contexts/data-governance/internal/app/query"
	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "data-governance"

// defaultSweepInterval — период фонового прохода истечения полномочий роли
// worker, если вызывающий не задал свой.
const defaultSweepInterval = time.Minute

// Grant — выданное право актора действовать в тенанте. Права задаёт вызывающий:
// composition не выдумывает стартовую политику доступа.
type Grant struct {
	Actor  domain.ActorID
	Tenant domain.TenantID
}

// SystemClock — часы процесса, источник времени по умолчанию. Не адаптер внешней
// системы: используется, когда вызывающий не передал свой Clock.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// APIAddr — адрес внутреннего HTTP-сервера роли api.
	APIAddr string

	// Interval — период фонового прохода истечения полномочий роли worker.
	Interval time.Duration

	// Grants — права доступа среза. По умолчанию доступ закрыт.
	Grants []Grant

	// Repository — репозиторий оснований. nil и пустой DSN → новый in-memory
	// репозиторий (dev). Если задан, используется независимо от DSN.
	Repository ports.ProcessingBasisRepository

	// DSN — строка подключения PostgreSQL зелёной зоны. Если Repository не задан
	// и DSN пуст, сборка идёт в in-memory dev-режиме и предупреждает об этом. При
	// заданном DSN сборка создаёт пул сама, применяет миграции из MigrationsDir и
	// использует PostgreSQL-репозиторий.
	DSN string

	// MigrationsDir — каталог SQL-миграций сервиса для применения при сборке по
	// DSN. Пусто → каталог по умолчанию (backend/contexts/data-governance/migrations).
	MigrationsDir string

	// Clock — источник времени. nil → SystemClock.
	Clock ports.Clock

	// IDs — генератор идентификаторов событий. nil → криптостойкий Random.
	IDs ports.IDGenerator

	// Access — решение о доступе актора к тенанту. nil → in-memory список прав
	// среза, собранный из Grants.
	Access ports.AccessDecision

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Slice — собранный контекст среза: порты, адаптеры и use cases.
type Slice struct {
	Repository ports.ProcessingBasisRepository
	Access     ports.AccessDecision

	// pool — пул PostgreSQL, если сборка создала его по DSN. Закрывается Close.
	pool *pgxpool.Pool

	// Server — HTTP-обработчики роли api. Реализует http.Handler.
	Server *httpapi.Server

	CreateBasis       *command.CreateBasisHandler
	ChangeAuthority   *command.ChangeAuthorityHandler
	RevokeBasis       *command.RevokeBasisHandler
	SuppressContact   *command.SuppressContactHandler
	LiftSuppression   *command.LiftSuppressionHandler
	ExpireAuthorities *command.ExpireAuthoritiesHandler

	GetBasis       *query.GetBasisHandler
	CheckZone      *query.CheckZoneHandler
	CheckAuthority *query.CheckAuthorityHandler
	CheckSuppress  *query.CheckSuppressionHandler
}

// Build собирает контекст среза из адаптеров и переданных зависимостей.
// Бизнес-правил здесь нет — соединение адаптеров, портов и use cases только
// своего контекста.
func Build(cfg Config) (*Slice, error) {
	var pool *pgxpool.Pool
	repository := cfg.Repository
	if repository == nil && strings.TrimSpace(cfg.DSN) != "" {
		built, builtPool, err := buildPostgresRepository(cfg.DSN, cfg.MigrationsDir)
		if err != nil {
			return nil, err
		}
		repository = built
		pool = builtPool
	}
	if repository == nil {
		// Без DSN и без готового адаптера контекст идёт в in-memory dev-режиме. О
		// режиме сообщается честно: состояние не переживёт рестарт процесса.
		if cfg.Logger != nil {
			cfg.Logger.Warn(
				"data-governance: PostgreSQL DSN не задан — in-memory dev-режим (состояние не переживает рестарт)",
				observ.Fields{},
			)
		}
		repository = persistence.New()
	}
	clock := cfg.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	ids := cfg.IDs
	if ids == nil {
		ids = idgen.NewRandom()
	}

	decider := access.NewDecider()
	for _, grant := range cfg.Grants {
		if err := decider.Grant(grant.Actor, grant.Tenant); err != nil {
			return nil, fmt.Errorf("сборка: выдача права доступа: %w", err)
		}
	}
	accessDecision := cfg.Access
	if accessDecision == nil {
		accessDecision = decider
	}

	createBasis := command.NewCreateBasisHandler(repository, clock, ids)
	changeAuthority := command.NewChangeAuthorityHandler(repository, clock, ids)
	revokeBasis := command.NewRevokeBasisHandler(repository, clock, ids)
	suppressContact := command.NewSuppressContactHandler(repository, clock, ids)
	liftSuppression := command.NewLiftSuppressionHandler(repository, clock, ids)
	expireAuthorities := command.NewExpireAuthoritiesHandler(repository, clock, ids)

	getBasis := query.NewGetBasisHandler(repository)
	checkZone := query.NewCheckZoneHandler(repository, clock)
	checkAuthority := query.NewCheckAuthorityHandler(repository, clock)
	checkSuppress := query.NewCheckSuppressionHandler(repository)

	server := httpapi.NewServer(httpapi.Dependencies{
		CreateBasis:     createBasis,
		ChangeAuthority: changeAuthority,
		RevokeBasis:     revokeBasis,
		SuppressContact: suppressContact,
		LiftSuppression: liftSuppression,
		GetBasis:        getBasis,
		CheckZone:       checkZone,
		CheckSuppress:   checkSuppress,
		Access:          accessDecision,
	})

	return &Slice{
		Repository:        repository,
		Access:            accessDecision,
		pool:              pool,
		Server:            server,
		CreateBasis:       createBasis,
		ChangeAuthority:   changeAuthority,
		RevokeBasis:       revokeBasis,
		SuppressContact:   suppressContact,
		LiftSuppression:   liftSuppression,
		ExpireAuthorities: expireAuthorities,
		GetBasis:          getBasis,
		CheckZone:         checkZone,
		CheckAuthority:    checkAuthority,
		CheckSuppress:     checkSuppress,
	}, nil
}

// Close освобождает пул PostgreSQL, созданный сборкой по DSN. Повторный вызов
// безопасен.
func (sl *Slice) Close() error {
	if sl == nil || sl.pool == nil {
		return nil
	}
	sl.pool.Close()
	sl.pool = nil
	return nil
}

// Service — собранный сервис data-governance. Реализует composition.Service:
// раннер на каждую объявленную каталогом роль (api, worker, chains).
type Service struct {
	slice    *Slice
	apiAddr  string
	interval time.Duration
	logger   observ.Logger
	metrics  observ.Metrics
	tracer   observ.Tracer
}

// New собирает сервис из переданных зависимостей.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("data-governance: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("data-governance: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("data-governance: Tracer обязателен")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultSweepInterval
	}
	slice, err := Build(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{
		slice:    slice,
		apiAddr:  cfg.APIAddr,
		interval: cfg.Interval,
		logger:   cfg.Logger,
		metrics:  cfg.Metrics,
		tracer:   cfg.Tracer,
	}, nil
}

// GrantsFromStrings строит права доступа среза из строковых значений флагов
// процесса. Пустое значение любого из них означает закрытый доступ: сборка не
// выдумывает стартовую политику.
func GrantsFromStrings(actor, tenant string) []Grant {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(tenant) == "" {
		return nil
	}
	return []Grant{{Actor: domain.ActorID(actor), Tenant: domain.TenantID(tenant)}}
}

// Close освобождает пул PostgreSQL, если сборка создала его по DSN. Для
// in-memory сборки ничего не делает.
func (s *Service) Close() error { return s.slice.Close() }

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
	case composition.RoleChains:
		return composition.RoleFunc(s.runChains), true
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

// runWorker запускает фоновое истечение полномочий: тикер вызывает прикладной
// сценарий, который отзывает основания с истёкшим окном действия. Роль владеет
// только запуском и адаптерами — доменных правил здесь нет.
//
// Обработчиков очередей брокера в срезе нет: это не скрывается, а логируется.
func (s *Service) runWorker(ctx context.Context) error {
	s.logger.Warn("worker: обработчики очередей не зарегистрированы", observ.Fields{}, "service", ServiceID)
	s.sweepExpired(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("worker: останов", observ.Fields{}, "service", ServiceID)
			return nil
		case <-ticker.C:
			s.sweepExpired(ctx)
		}
	}
}

// sweepExpired выполняет один проход истечения полномочий и логирует результат.
// Ошибка прохода не останавливает роль: следующий тик повторит попытку.
func (s *Service) sweepExpired(ctx context.Context) {
	result, err := s.slice.ExpireAuthorities.Handle(ctx, command.ExpireAuthorities{})
	if err != nil {
		s.logger.Error("worker: истечение полномочий", err, observ.Fields{}, "service", ServiceID)
		return
	}
	if result.Expired > 0 {
		s.logger.Info("worker: полномочия истекли", observ.Fields{}, "service", ServiceID, "expired", result.Expired)
	}
}

// runChains — каркас роли chains. Durable-планировщик долгих состояний в срезе не
// реализован: роль честно сообщает об этом и не делает вид, что ведёт цепочки.
func (s *Service) runChains(ctx context.Context) error {
	s.logger.Warn("chains: планировщик долгих состояний не реализован", observ.Fields{}, "service", ServiceID)
	<-ctx.Done()
	return nil
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
