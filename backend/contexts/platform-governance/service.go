package platformgovernance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	httpapi "lead-gen/backend/contexts/platform-governance/internal/adapters/http"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/idgen"
	persistence "lead-gen/backend/contexts/platform-governance/internal/adapters/persistence/inmemory"
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
	// APIAddr — адрес HTTP-сервера роли api (портал клиента).
	APIAddr string

	// Repository — репозиторий тенантов. nil → новый in-memory репозиторий.
	Repository *persistence.Repository

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
	Repository *persistence.Repository
	Projection *projection.LastRequest

	// Server — HTTP-обработчики роли api. Реализует http.Handler.
	Server *httpapi.Server

	RegisterClient      *command.RegisterClientHandler
	SubmitClientRequest *command.SubmitClientRequestHandler
	PortalState         *query.PortalStateHandler
}

// Build собирает контекст среза из адаптеров и переданных зависимостей.
// Бизнес-правил здесь нет — соединение адаптеров, портов и use cases только
// своего контекста.
func Build(cfg Config) (*Slice, error) {
	repository := cfg.Repository
	if repository == nil {
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
	requests := cfg.Requests
	if requests == nil {
		requests = unavailableRequestIntent{}
	}

	proj := projection.NewLastRequest()
	registerClient := command.NewRegisterClientHandler(repository, clock, ids)
	submitClientRequest := command.NewSubmitClientRequestHandler(requests, ids, proj)
	portalState := query.NewPortalStateHandler(requests, proj)

	server := httpapi.NewServer(httpapi.Dependencies{
		RegisterClient:      registerClient,
		SubmitClientRequest: submitClientRequest,
		PortalState:         portalState,
	})

	return &Slice{
		Repository:          repository,
		Projection:          proj,
		Server:              server,
		RegisterClient:      registerClient,
		SubmitClientRequest: submitClientRequest,
		PortalState:         portalState,
	}, nil
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

// runWorker — честная заглушка роли worker: обработчиков очередей у портала нет,
// роль сообщает об этом и не делает вид, что обрабатывает сообщения.
func (s *Service) runWorker(ctx context.Context) error {
	s.logger.Warn("worker: обработчиков очередей нет", observ.Fields{}, "service", ServiceID)
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
