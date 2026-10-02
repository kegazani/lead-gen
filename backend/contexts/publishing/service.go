package publishing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	digestadapter "lead-gen/backend/contexts/publishing/internal/adapters/digest"
	httpapi "lead-gen/backend/contexts/publishing/internal/adapters/http"
	"lead-gen/backend/contexts/publishing/internal/adapters/idgen"
	"lead-gen/backend/contexts/publishing/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/publishing/internal/adapters/persistence/inmemory"
	platformadapter "lead-gen/backend/contexts/publishing/internal/adapters/platform"
	"lead-gen/backend/contexts/publishing/internal/app/command"
	"lead-gen/backend/contexts/publishing/internal/app/query"
	"lead-gen/backend/contexts/publishing/internal/ports"
	composition "lead-gen/backend/platform/composition"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/observability/observ"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "publishing"

// defaultPublishInterval — интервал проходов relay публикации событий публикации,
// если задано некорректное значение.
const defaultPublishInterval = time.Second

// defaultScheduleInterval — интервал тика роли schedule, если задано
// некорректное значение.
const defaultScheduleInterval = time.Minute

// SystemClock — часы процесса, источник времени по умолчанию.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// APIAddr — адрес внутреннего HTTP-сервера роли api.
	APIAddr string

	// Interval — интервал прохода relay публикации событий публикации.
	Interval time.Duration

	// ScheduleInterval — интервал тика роли schedule.
	ScheduleInterval time.Duration

	// Repository — репозиторий публикаций. nil → новый in-memory репозиторий.
	Repository *persistence.Repository

	// Journal — журнал outbox событий публикации. nil → новый журнал в памяти.
	Journal platformoutbox.Journal

	// Publisher — приёмник публикации outbox. nil означает, что публикация
	// событий не собирается: процесс не запускает relay событий публикации.
	Publisher platformoutbox.Publisher

	// Platform — порт публикации вовне. nil → недоступный адаптер: контекст
	// честно сообщает о недоступности площадки, а не имитирует публикацию.
	Platform ports.PlatformPublisher

	// Digest — порт источника выжимки-источника. nil → недоступный адаптер:
	// отсутствие источника не блокирует приём команды, но и не выдаётся за
	// успешную проверку.
	Digest ports.DigestSource

	// Clock — источник времени прикладного слоя. nil → SystemClock.
	Clock ports.Clock

	// IDs — генератор идентификаторов событий. nil → случайный генератор.
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
	Journal    platformoutbox.Journal
	UnitOfWork *inmemory.UnitOfWork

	// Relay не равен nil только если при сборке задан Publisher.
	Relay *platformoutbox.Relay

	// Server — HTTP-обработчики роли api. Реализует http.Handler.
	Server *httpapi.Server

	// Platform и Digest — собранные внешние порты: видно, какой адаптер
	// подключён, без раскрытия внутренних пакетов.
	Platform ports.PlatformPublisher
	Digest   ports.DigestSource

	RequestPublication *command.RequestPublicationHandler
	Approve            *command.ApproveHandler
	Reject             *command.RejectHandler
	Publish            *command.PublishHandler
	GetPublication     *query.GetPublicationHandler
	PublicationQueue   *query.PublicationQueueHandler
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
	platform := cfg.Platform
	if platform == nil {
		platform = platformadapter.Unavailable{}
	}
	digest := cfg.Digest
	if digest == nil {
		digest = digestadapter.Unavailable{}
	}

	unitOfWork := inmemory.NewUnitOfWorkWith(repository, journal)

	requestPublication := command.NewRequestPublicationHandler(unitOfWork, digest, clock)
	approve := command.NewApproveHandler(unitOfWork, clock)
	reject := command.NewRejectHandler(unitOfWork, clock)
	publish := command.NewPublishHandler(unitOfWork, clock, ids, platform)

	getPublication := query.NewGetPublicationHandler(repository)
	publicationQueue := query.NewPublicationQueueHandler(repository)

	server := httpapi.NewServer(httpapi.Dependencies{
		RequestPublication: requestPublication,
		Approve:            approve,
		Reject:             reject,
		Publish:            publish,
		GetPublication:     getPublication,
		PublicationQueue:   publicationQueue,
	})

	var relay *platformoutbox.Relay
	if cfg.Publisher != nil {
		relay = platformoutbox.NewRelay(journal, cfg.Publisher, cfg.Interval)
	}

	return &Slice{
		Repository:         repository,
		Journal:            journal,
		UnitOfWork:         unitOfWork,
		Relay:              relay,
		Server:             server,
		Platform:           platform,
		Digest:             digest,
		RequestPublication: requestPublication,
		Approve:            approve,
		Reject:             reject,
		Publish:            publish,
		GetPublication:     getPublication,
		PublicationQueue:   publicationQueue,
	}, nil
}

// Service — собранный сервис publishing. Реализует composition.Service: раннер на
// каждую объявленную каталогом роль.
type Service struct {
	slice            *Slice
	apiAddr          string
	interval         time.Duration
	scheduleInterval time.Duration
	logger           observ.Logger
	metrics          observ.Metrics
	tracer           observ.Tracer
}

// New собирает сервис из адаптеров и переданных зависимостей.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("publishing: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("publishing: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("publishing: Tracer обязателен")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultPublishInterval
	}
	if cfg.ScheduleInterval <= 0 {
		cfg.ScheduleInterval = defaultScheduleInterval
	}

	slice, err := Build(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{
		slice:            slice,
		apiAddr:          cfg.APIAddr,
		interval:         cfg.Interval,
		scheduleInterval: cfg.ScheduleInterval,
		logger:           cfg.Logger,
		metrics:          cfg.Metrics,
		tracer:           cfg.Tracer,
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
	case composition.RoleSchedule:
		return composition.RoleFunc(s.runSchedule), true
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

// runWorker запускает фоновый контур роли worker: публикацию событий публикации
// (relay outbox). Контур работает до отмены контекста.
//
// Публикация вовне идёт синхронно командой (роль api / порт площадки), а worker
// доставляет событие material-published потребителям через брокер. Если приёмник
// публикации не собран, worker честно сообщает об этом, а не притворяется
// работающим.
func (s *Service) runWorker(ctx context.Context) error {
	if s.slice.Relay == nil {
		s.logger.Warn("worker: публикация событий не собрана (Publisher не задан)", observ.Fields{}, "service", ServiceID)
		<-ctx.Done()
		return nil
	}
	s.logger.Info("worker: публикация событий публикации запущена", observ.Fields{}, "service", ServiceID)
	s.logger.Warn("worker: обработчики очередей брокера не зарегистрированы", observ.Fields{}, "service", ServiceID)
	return s.slice.Relay.Run(ctx)
}

// runSchedule — роль schedule. По design.md, Решение 5, расписание формирует
// материал, но не публикует: автоматическая публикация без утверждения человека
// запрещена. Подготовка материала по расписанию требует генерации текста
// (capability linguistics), которая в срез не подключена, поэтому роль честно
// сообщает о неготовности и не публикует ничего.
func (s *Service) runSchedule(ctx context.Context) error {
	s.logger.Warn(
		"schedule: подготовка по расписанию не подключена (генерация материала — linguistics); автопубликация запрещена",
		observ.Fields{}, "service", ServiceID,
	)
	ticker := time.NewTicker(s.scheduleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.logger.Warn(
				"schedule: тик без действий — расписание не публикует",
				observ.Fields{}, "service", ServiceID,
			)
		}
	}
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
