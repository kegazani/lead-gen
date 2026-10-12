package engagementobservation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	httpapi "lead-gen/backend/contexts/engagement-observation/internal/adapters/http"
	"lead-gen/backend/contexts/engagement-observation/internal/adapters/idgen"
	outboxinmemory "lead-gen/backend/contexts/engagement-observation/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/engagement-observation/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/engagement-observation/internal/adapters/transport"
	"lead-gen/backend/contexts/engagement-observation/internal/app/command"
	"lead-gen/backend/contexts/engagement-observation/internal/app/query"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/messaging/inbox"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/readiness"
	"lead-gen/backend/platform/transport/amqp"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "engagement-observation"

// defaultPublishInterval — интервал проходов relay публикации событий
// взаимодействия, если задано некорректное значение.
const defaultPublishInterval = time.Second

// SystemClock — часы процесса, источник времени по умолчанию.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// IngestAddr — адрес публичного HTTP-сервера роли ingest.
	IngestAddr string

	// Interval — интервал прохода relay публикации событий взаимодействия.
	Interval time.Duration

	// Persistence — адаптеры хранилища контекста. Нулевое значение означает
	// dev-режим: сборка берёт in-memory адаптеры, и состояние не переживает
	// рестарт. Продуктовая сборка задаёт PostgreSQL-адаптеры
	// (NewPostgresPersistence) по DSN зелёной зоны.
	Persistence Persistence

	// Clock — источник времени прикладного слоя. nil → SystemClock.
	Clock ports.Clock

	// IDs — генератор идентификаторов агрегата и событий. nil → случайный
	// генератор.
	IDs ports.IDGenerator

	// Publisher — издатель брокера. nil означает, что публикация не собирается:
	// процесс не запускает relay событий взаимодействия.
	Publisher amqp.Publisher

	// Consumer — потребитель очереди события доставки. nil означает, что поток
	// событий не подключён: роль worker честно об этом сообщает.
	Consumer amqp.Consumer

	// Deduper — журнал обработанных event_id (доставка at-least-once). nil →
	// in-memory журнал.
	Deduper inbox.Deduper

	// EventQueue — очередь события доставки. Пусто → очередь транспортного
	// адаптера.
	EventQueue string

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются composition
	// root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Slice — собранный контекст среза: порты, адаптеры и use cases.
type Slice struct {
	Repository ports.EngagementRepository
	Ledger     ports.SignalLedger
	Links      ports.TransitionLinks
	Journal    platformoutbox.Journal
	UnitOfWork ports.UnitOfWork

	// Relay не равен nil только если при сборке задан Publisher.
	Relay *platformoutbox.Relay

	// Server — HTTP-обработчики роли ingest. Реализует http.Handler.
	Server    *httpapi.Server
	Readiness RoleReadinessReporter

	IngestEvent      *command.IngestEventHandler
	RecordTransition *command.RecordTransitionHandler
	GetEngagement    *query.GetEngagementHandler
}

// storage выбирает адаптеры хранилища: заданную персистентность либо dev-набор
// в памяти. Частичная заданность отвергается: иначе чтение и запись работали бы
// с разными носителями (например, чтение из PostgreSQL, запись в память).
func storage(p Persistence) (ports.EngagementRepository, ports.SignalLedger, ports.TransitionLinks, platformoutbox.Journal, ports.UnitOfWork, error) {
	if p.UnitOfWork != nil {
		if p.Repository == nil || p.Ledger == nil || p.Links == nil || p.Journal == nil {
			return nil, nil, nil, nil, nil, errors.New("engagement-observation: неполная персистентность: unit-of-work без хранилищ")
		}
		return p.Repository, p.Ledger, p.Links, p.Journal, p.UnitOfWork, nil
	}
	if p.Repository != nil || p.Ledger != nil || p.Links != nil || p.Journal != nil {
		return nil, nil, nil, nil, nil, errors.New("engagement-observation: неполная персистентность: хранилища без unit-of-work")
	}

	memRepository := persistence.New()
	memLedger := persistence.NewLedger()
	memLinks := persistence.NewLinks()
	journal := platformoutbox.NewMemoryJournal()
	unitOfWork := outboxinmemory.NewUnitOfWorkWith(memRepository, memLedger, memLinks, journal)
	return memRepository, memLedger, memLinks, journal, unitOfWork, nil
}

// Build собирает контекст среза из адаптеров и переданных зависимостей.
// Бизнес-правил здесь нет — соединение адаптеров, портов и use cases только
// своего контекста.
func Build(cfg Config) (*Slice, error) {
	repository, ledger, links, journal, unitOfWork, err := storage(cfg.Persistence)
	if err != nil {
		return nil, err
	}
	clock := cfg.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	ids := cfg.IDs
	if ids == nil {
		ids = idgen.NewRandom()
	}

	ingest := command.NewIngestEventHandler(unitOfWork, clock, ids)
	recordTransition := command.NewRecordTransitionHandler(links, ingest, ids)
	getEngagement := query.NewGetEngagementHandler(repository)

	readinessState := newReadinessReporter(cfg.Persistence, cfg.Consumer, cfg.Publisher, cfg.Deduper)
	server := httpapi.NewServer(httpapi.Dependencies{
		Readiness: func(ctx context.Context) readiness.Snapshot {
			return readinessState.RoleReadiness(ctx, composition.RoleIngest)
		},
		IngestEvent:      ingest,
		RecordTransition: recordTransition,
		GetEngagement:    getEngagement,
	})

	var relay *platformoutbox.Relay
	if cfg.Publisher != nil {
		relay = platformoutbox.NewRelay(journal, cfg.Publisher, cfg.Interval).
			WithLogger(cfg.Logger).
			WithMetrics(cfg.Metrics)
	}

	return &Slice{
		Repository:       repository,
		Ledger:           ledger,
		Links:            links,
		Journal:          journal,
		UnitOfWork:       unitOfWork,
		Relay:            relay,
		Server:           server,
		Readiness:        readinessState,
		IngestEvent:      ingest,
		RecordTransition: recordTransition,
		GetEngagement:    getEngagement,
	}, nil
}

// Service — собранный сервис engagement-observation. Реализует
// composition.Service: раннер на каждую объявленную роль.
type Service struct {
	slice      *Slice
	ingestAddr string
	subscriber *transport.Subscriber
	logger     observ.Logger
	metrics    observ.Metrics
	tracer     observ.Tracer
}

// RoleReadiness сообщает владельческую готовность роли, не запуская подписчика.
func (s *Service) RoleReadiness(ctx context.Context, role composition.Role) readiness.Snapshot {
	if s == nil || s.slice == nil || s.slice.Readiness == nil {
		return readiness.Snapshot{Capability: string(role), Status: readiness.StatusUnknown, Ready: false}
	}
	return s.slice.Readiness.RoleReadiness(ctx, role)
}

// New собирает сервис из адаптеров и переданных зависимостей.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("engagement-observation: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("engagement-observation: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("engagement-observation: Tracer обязателен")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultPublishInterval
	}
	if cfg.Persistence.UnitOfWork == nil {
		cfg.Logger.Warn(
			"engagement-observation: персистентность в памяти процесса (dev-режим, состояние не переживает рестарт); задайте DSN зелёной зоны для PostgreSQL",
			observ.Fields{},
			"service", ServiceID,
		)
	}

	slice, err := Build(cfg)
	if err != nil {
		return nil, err
	}

	// Подписка на поток события доставки собирается только тогда, когда задан
	// потребитель брокера: без него воркер не притворяется работающим, а честно
	// сообщает об отсутствии источника.
	var subscriber *transport.Subscriber
	if cfg.Consumer != nil {
		deduper := cfg.Deduper
		if deduper == nil {
			deduper = inbox.NewMemoryDeduper()
		}
		subscriber = transport.NewSubscriber(cfg.Consumer, deduper, cfg.EventQueue, slice.IngestEvent)
	}

	return &Service{
		slice:      slice,
		ingestAddr: cfg.IngestAddr,
		subscriber: subscriber,
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
	case composition.RoleIngest:
		return composition.RoleFunc(s.runIngest), true
	case composition.RoleWorker:
		return composition.RoleFunc(s.runWorker), true
	default:
		return nil, false
	}
}

// runIngest поднимает публичный HTTP-сервер приёма и останавливает его по отмене
// контекста.
func (s *Service) runIngest(ctx context.Context) error {
	handler := observ.Middleware(s.logger, s.metrics, s.tracer, ServiceID)(s.slice.Server)
	server := &http.Server{
		Addr:              s.ingestAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("ingest: останов HTTP-сервера", err, observ.Fields{}, "service", ServiceID)
		}
	}()

	s.logger.Info("ingest: публичный HTTP-сервер приёма запущен", observ.Fields{}, "service", ServiceID, "addr", s.ingestAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("ingest: работа HTTP-сервера: %w", err)
	}
	return nil
}

// runWorker запускает фоновые контуры роли worker: публикацию событий
// взаимодействия (relay outbox) и подписку на очередь события доставки. Контуры
// независимы и работают до отмены контекста; падение любого из них останавливает
// роль.
//
// Транспортный relay публикует подтверждённые записи outbox в брокер. Событие, не
// прошедшее подтверждение, остаётся в журнале и будет опубликовано повторно:
// доставка at-least-once, а потребитель дедуплицирует по event_id.
func (s *Service) runWorker(ctx context.Context) error {
	if s.slice.Relay == nil && s.subscriber == nil {
		s.logger.Warn("worker: ни публикация событий, ни поток доставки не подключены", observ.Fields{}, "service", ServiceID)
		<-ctx.Done()
		return nil
	}

	errCh := make(chan error, 2)
	if s.slice.Relay != nil {
		s.logger.Info("worker: публикация событий взаимодействия запущена", observ.Fields{}, "service", ServiceID)
		go func() { errCh <- s.slice.Relay.Run(ctx) }()
	}
	if s.subscriber != nil {
		s.logger.Info("worker: подписка на поток события доставки запущена", observ.Fields{}, "service", ServiceID)
		go func() { errCh <- s.subscriber.Run(ctx) }()
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

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
