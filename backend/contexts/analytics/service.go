package analytics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	accessinmemory "lead-gen/backend/contexts/analytics/internal/adapters/access/inmemory"
	httpapi "lead-gen/backend/contexts/analytics/internal/adapters/http"
	"lead-gen/backend/contexts/analytics/internal/adapters/transport"
	"lead-gen/backend/contexts/analytics/internal/app/command"
	"lead-gen/backend/contexts/analytics/internal/app/query"
	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/messaging/inbox"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/frontend/components"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "analytics"

// SystemClock — часы процесса, источник времени по умолчанию.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// APIAddr — адрес HTTP-сервера роли api (дашборд клиента).
	APIAddr string

	// ReadModel — read-модель дашборда. nil → новая пустая проекция в памяти.
	ReadModel *readmodel.ReadModel

	// Access — решение о доступе. nil → локальная in-memory реализация смысла
	// контракта access-decision владельца platform-governance.
	Access ports.AccessDecider

	// Clock — источник времени прикладного слоя. nil → SystemClock.
	Clock ports.Clock

	// StaleDays — порог «не отвечают дольше N дней». 0 → значение по умолчанию.
	StaleDays int

	// Consumer — потребитель очереди брокера, из которой воркер наполняет
	// проекцию доменными событиями. nil означает, что поток событий не подключён:
	// проекция наполняется только dev-приёмом, и роль worker честно об этом
	// сообщает.
	Consumer amqp.Consumer

	// Deduper — журнал обработанных event_id (доставка at-least-once). nil →
	// in-memory журнал: дедупликация в пределах процесса.
	Deduper inbox.Deduper

	// EventQueue — очередь нормализованных событий дашборда. Пусто → очередь по
	// умолчанию транспортного адаптера.
	EventQueue string

	// Console — базовые адреса сервисов консоли для общей навигации по разделам
	// (Заявки, Исполнение, Лиды, Отчёты). Пустые адреса дают относительные пути.
	Console components.ConsoleBases

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Slice — собранный контекст среза: проекция, порты, адаптеры и use cases.
type Slice struct {
	ReadModel *readmodel.ReadModel
	Access    ports.AccessDecider

	// Server — HTTP-обработчики роли api. Реализует http.Handler.
	Server *httpapi.Server

	Dashboard *query.DashboardHandler
	Events    *command.ApplyEventHandler
}

// Build собирает контекст среза из адаптеров и переданных зависимостей.
// Бизнес-правил здесь нет — соединение адаптеров, портов и use cases только
// своего контекста.
func Build(cfg Config) (*Slice, error) {
	model := cfg.ReadModel
	if model == nil {
		model = readmodel.New()
	}
	clock := cfg.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	access := cfg.Access
	if access == nil {
		access = accessinmemory.New()
	}

	dashboard := query.NewDashboardHandler(model, clock, cfg.StaleDays)
	events := command.NewApplyEventHandler(model)

	server := httpapi.NewServer(httpapi.Dependencies{
		Dashboard: dashboard,
		Events:    events,
		Access:    access,
		Console:   cfg.Console,
	})

	return &Slice{
		ReadModel: model,
		Access:    access,
		Server:    server,
		Dashboard: dashboard,
		Events:    events,
	}, nil
}

// Service — собранный сервис analytics. Реализует composition.Service: раннер на
// каждую объявленную роль.
type Service struct {
	slice      *Slice
	subscriber *transport.Subscriber
	apiAddr    string
	logger     observ.Logger
	metrics    observ.Metrics
	tracer     observ.Tracer
}

// New собирает сервис из адаптеров и переданных зависимостей.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("analytics: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("analytics: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("analytics: Tracer обязателен")
	}

	slice, err := Build(cfg)
	if err != nil {
		return nil, err
	}

	// Подписка на поток событий собирается только тогда, когда задан потребитель
	// брокера: без него воркер не притворяется работающим, а честно сообщает об
	// отсутствии источника.
	var subscriber *transport.Subscriber
	if cfg.Consumer != nil {
		deduper := cfg.Deduper
		if deduper == nil {
			deduper = inbox.NewMemoryDeduper()
		}
		subscriber = transport.NewSubscriber(cfg.Consumer, deduper, cfg.EventQueue)
	}

	return &Service{
		slice:      slice,
		subscriber: subscriber,
		apiAddr:    cfg.APIAddr,
		logger:     cfg.Logger,
		metrics:    cfg.Metrics,
		tracer:     cfg.Tracer,
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

// runAPI поднимает HTTP-сервер дашборда и останавливает его по отмене контекста.
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

	s.logger.Info("api: HTTP-сервер дашборда запущен", observ.Fields{}, "service", ServiceID, "addr", s.apiAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("api: работа HTTP-сервера: %w", err)
	}
	return nil
}

// runWorker читает очередь брокера и наполняет проекцию дашборда доменными
// событиями. Без заданного потребителя роль честно сообщает, что поток событий не
// подключён, и не делает вид, что обрабатывает сообщения: проекция наполняется
// только dev-приёмом.
func (s *Service) runWorker(ctx context.Context) error {
	if s.subscriber == nil {
		s.logger.Warn("worker: поток событий не подключён к брокеру; проекция наполняется только dev-приёмом", observ.Fields{}, "service", ServiceID)
		<-ctx.Done()
		return nil
	}

	s.logger.Info("worker: подписка на поток событий дашборда запущена", observ.Fields{}, "service", ServiceID)
	return s.subscriber.Run(ctx, func(ctx context.Context, event command.ApplyEvent) error {
		s.slice.Events.Handle(ctx, event)
		return nil
	})
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
