package salesworkflow

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	persistence "lead-gen/backend/contexts/sales-workflow/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
	composition "lead-gen/backend/platform/composition"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "sales-workflow"

// defaultPublishInterval — интервал проходов relay публикации событий, если
// задано некорректное значение.
const defaultPublishInterval = time.Second

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// APIAddr — адрес внутреннего HTTP-сервера роли api.
	APIAddr string

	// IngestAddr — адрес публичного HTTP-сервера приёма событий CRM (роль
	// ingest).
	IngestAddr string

	// Interval — интервал прохода relay публикации событий.
	Interval time.Duration

	// CRM — порт внешней CRM (системы-источника сделок). nil означает in-memory
	// стенд-ин внешней CRM: сети нет, сделки хранятся в памяти процесса. Реальный
	// сетевой адаптер CRM в срезе не реализован — это осознанная заглушка.
	CRM ports.DealRepository

	// Syncs — репозиторий синхронизации с CRM. nil → новый in-memory репозиторий.
	Syncs *persistence.Repository

	// Journal — журнал outbox событий. nil → новый журнал в памяти.
	Journal platformoutbox.Journal

	// Clock — источник времени прикладного слоя. nil → SystemClock.
	Clock ports.Clock

	// IDs — генератор идентификаторов событий. nil → случайный генератор.
	IDs ports.IDGenerator

	// Publisher — издатель брокера. nil означает, что публикация не собирается:
	// процесс не запускает relay событий.
	Publisher amqp.Publisher

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Service — собранный сервис sales-workflow. Реализует composition.Service:
// раннер на каждую объявленную роль.
type Service struct {
	slice      *Slice
	apiAddr    string
	ingestAddr string
	logger     observ.Logger
	metrics    observ.Metrics
	tracer     observ.Tracer
}

// New собирает сервис из адаптеров и переданных зависимостей.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("sales-workflow: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("sales-workflow: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("sales-workflow: Tracer обязателен")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultPublishInterval
	}

	slice, err := Build(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{
		slice:      slice,
		apiAddr:    cfg.APIAddr,
		ingestAddr: cfg.IngestAddr,
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
	case composition.RoleIngest:
		return composition.RoleFunc(s.runIngest), true
	case composition.RoleWorker:
		return composition.RoleFunc(s.runWorker), true
	default:
		return nil, false
	}
}

// runAPI поднимает внутренний HTTP-сервер контекста и останавливает его по
// отмене контекста.
func (s *Service) runAPI(ctx context.Context) error {
	handler := observ.Middleware(s.logger, s.metrics, s.tracer, ServiceID)(s.slice.APIServer)
	return s.serve(ctx, s.apiAddr, handler, "api")
}

// runIngest поднимает публичный HTTP-сервер приёма событий CRM и останавливает его
// по отмене контекста.
func (s *Service) runIngest(ctx context.Context) error {
	handler := observ.Middleware(s.logger, s.metrics, s.tracer, ServiceID)(s.slice.IngestServer)
	return s.serve(ctx, s.ingestAddr, handler, "ingest")
}

// serve поднимает HTTP-сервер роли с общими настройками и корректным остановом.
func (s *Service) serve(ctx context.Context, addr string, handler http.Handler, role string) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			s.logger.Error(role+": останов HTTP-сервера", err, observ.Fields{}, "service", ServiceID)
		}
	}()

	s.logger.Info(role+": HTTP-сервер запущен", observ.Fields{}, "service", ServiceID, "addr", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("%s: работа HTTP-сервера: %w", role, err)
	}
	return nil
}

// runWorker запускает фоновые контуры роли worker: публикацию событий (relay
// outbox). Контур работает до отмены контекста; падение relay останавливает роль.
//
// Транспортный relay публикует подтверждённые записи outbox в брокер. Событие, не
// прошедшее подтверждение, остаётся в журнале и будет опубликовано повторно:
// доставка at-least-once, а потребитель дедуплицирует по event_id. Потребителей
// очередей брокера в срезе нет — это не скрывается, а логируется.
func (s *Service) runWorker(ctx context.Context) error {
	if s.slice.Relay == nil {
		return fmt.Errorf("worker: публикация событий не собрана (Publisher не задан)")
	}
	s.logger.Info("worker: публикация событий запущена", observ.Fields{}, "service", ServiceID)
	s.logger.Warn("worker: обработчики очередей брокера не зарегистрированы", observ.Fields{}, "service", ServiceID)
	return s.slice.Relay.Run(ctx)
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
