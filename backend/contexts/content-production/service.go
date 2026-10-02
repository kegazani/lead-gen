// Package contentproduction собирает сервис контекста content-production:
// соединение адаптеров, портов и use cases своего контекста и публикацию его
// ролей.
//
// Контекст отвечает за генерацию, версионирование и рендеринг текстов,
// презентаций, документов и лендингов (architecture/domain-catalog.yaml).
//
// Пакет лежит в каталоге с дефисом (contexts/content-production), потому что
// идентификатор сервиса равен идентификатору контекста из каталога, а имя
// Go-пакета должно быть валидным идентификатором.
//
// Это единственное место сборки своего контекста; бизнес-правил здесь нет.
package contentproduction

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	httpapi "lead-gen/backend/contexts/content-production/internal/adapters/http"
	"lead-gen/backend/contexts/content-production/internal/adapters/idgen"
	outboxinmemory "lead-gen/backend/contexts/content-production/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/content-production/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/content-production/internal/adapters/renderer/deterministic"
	"lead-gen/backend/contexts/content-production/internal/app/command"
	"lead-gen/backend/contexts/content-production/internal/app/query"
	"lead-gen/backend/contexts/content-production/internal/ports"
	composition "lead-gen/backend/platform/composition"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/observability/observ"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "content-production"

// defaultInterval — интервал проходов relay и воркера рендеринга, если задано
// некорректное значение.
const defaultInterval = time.Second

// renderBatch — размер порции заданий за один проход воркера рендеринга.
const renderBatch = 100

// SystemClock — часы процесса, источник времени по умолчанию.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// APIAddr — адрес внутреннего HTTP-сервера роли api.
	APIAddr string

	// Interval — интервал проходов relay публикации событий и воркера
	// рендеринга.
	Interval time.Duration

	// Store — in-memory хранилище контент-пакетов и заданий рендеринга.
	// nil → новое хранилище.
	Store *persistence.Store

	// Journal — журнал outbox событий контекста. nil → журнал в памяти.
	Journal platformoutbox.Journal

	// Renderer — движок рендеринга за портом. nil → детерминированный
	// адаптер-заглушка (internal/adapters/renderer/deterministic).
	Renderer ports.Renderer

	// Clock — источник времени прикладного слоя. nil → SystemClock.
	Clock ports.Clock

	// IDs — генератор идентификаторов событий. nil → случайный генератор.
	IDs ports.IDGenerator

	// Publisher — издатель брокера. nil означает, что публикация не собирается:
	// процесс не запускает relay событий контекста.
	Publisher platformoutbox.Publisher

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Slice — собранный контекст среза: порты, адаптеры и use cases.
type Slice struct {
	Store      *persistence.Store
	Content    *persistence.ContentRepository
	RenderJobs *persistence.RenderJobRepository
	Journal    platformoutbox.Journal
	UnitOfWork *outboxinmemory.UnitOfWork
	Renderer   ports.Renderer

	// Relay не равен nil только если при сборке задан Publisher.
	Relay *platformoutbox.Relay

	// Server — HTTP-обработчики роли api. Реализует http.Handler.
	Server *httpapi.Server

	CreateContentPackage  *command.CreateContentPackageHandler
	PublishContentVersion *command.PublishContentVersionHandler
	RequestRender         *command.RequestRenderHandler
	ProduceContent        *command.ProduceContentHandler
	ProcessRender         *command.ProcessRenderHandler

	GetContent      *query.GetContentHandler
	GetRenderJob    *query.GetRenderJobHandler
	GetLanding      *query.GetLandingHandler
	GetPresentation *query.GetPresentationHandler
}

// Build собирает контекст среза из адаптеров и переданных зависимостей.
// Бизнес-правил здесь нет — соединение адаптеров, портов и use cases только
// своего контекста.
func Build(cfg Config) (*Slice, error) {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultInterval
	}
	store := cfg.Store
	if store == nil {
		store = persistence.New()
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
	renderer := cfg.Renderer
	if renderer == nil {
		renderer = deterministic.New()
	}

	unitOfWork := outboxinmemory.NewUnitOfWorkWith(store, journal)

	createPackage := command.NewCreateContentPackageHandler(unitOfWork, clock, ids)
	publishVersion := command.NewPublishContentVersionHandler(unitOfWork, clock, ids)
	requestRender := command.NewRequestRenderHandler(unitOfWork)
	produceContent := command.NewProduceContentHandler(unitOfWork, clock, ids)
	processRender := command.NewProcessRenderHandler(unitOfWork, renderer, clock, ids)

	getContent := query.NewGetContentHandler(store.Content())
	getRenderJob := query.NewGetRenderJobHandler(store.RenderJobs())
	getLanding := query.NewGetLandingHandler(store.Content())
	getPresentation := query.NewGetPresentationHandler(store.RenderJobs())

	server := httpapi.NewServer(httpapi.Dependencies{
		CreateContentPackage:  createPackage,
		PublishContentVersion: publishVersion,
		RequestRender:         requestRender,
		ProduceContent:        produceContent,
		GetContent:            getContent,
		GetRenderJob:          getRenderJob,
	})

	var relay *platformoutbox.Relay
	if cfg.Publisher != nil {
		relay = platformoutbox.NewRelay(journal, cfg.Publisher, cfg.Interval)
	}

	return &Slice{
		Store:      store,
		Content:    store.Content(),
		RenderJobs: store.RenderJobs(),
		Journal:    journal,
		UnitOfWork: unitOfWork,
		Renderer:   renderer,
		Relay:      relay,
		Server:     server,

		CreateContentPackage:  createPackage,
		PublishContentVersion: publishVersion,
		RequestRender:         requestRender,
		ProduceContent:        produceContent,
		ProcessRender:         processRender,

		GetContent:      getContent,
		GetRenderJob:    getRenderJob,
		GetLanding:      getLanding,
		GetPresentation: getPresentation,
	}, nil
}

// Service — собранный сервис content-production. Реализует composition.Service:
// раннер на каждую объявленную каталогом роль.
type Service struct {
	slice    *Slice
	apiAddr  string
	interval time.Duration
	logger   observ.Logger
	metrics  observ.Metrics
	tracer   observ.Tracer
}

// New собирает сервис из адаптеров и переданных зависимостей.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("content-production: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("content-production: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("content-production: Tracer обязателен")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultInterval
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

// Slice возвращает собранный срез контекста. Нужен composition root'у и тестам
// для прямого вызова сценариев.
func (s *Service) Slice() *Slice { return s.slice }

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

// runWorker запускает фоновые контуры роли worker: воркер рендеринга и (если
// собран) relay публикации событий контекста. Контуры независимы и работают до
// отмены контекста; падение любого из них останавливает роль.
//
// Воркер рендеринга забирает задания в состояниях requested и rendering, вызывает
// движок за портом Renderer и завершает задание артефактом либо отказом. Событие
// content-rendered уходит через outbox атомарно с состоянием задания.
//
// Транспортный relay публикует подтверждённые записи outbox в брокер. Событие, не
// прошедшее подтверждение, остаётся в журнале и будет опубликовано повторно:
// доставка at-least-once, а потребитель дедуплицирует по event_id. Потребителей
// очередей брокера в срезе нет — это не скрывается, а логируется.
func (s *Service) runWorker(ctx context.Context) error {
	if s.slice.Renderer == nil {
		return fmt.Errorf("worker: движок рендеринга не собран")
	}
	s.logger.Info("worker: воркер рендеринга запущен", observ.Fields{}, "service", ServiceID)
	if s.slice.Relay != nil {
		s.logger.Info("worker: публикация событий контента запущена", observ.Fields{}, "service", ServiceID)
	} else {
		s.logger.Warn("worker: relay публикации не собран (брокер не подключён)", observ.Fields{}, "service", ServiceID)
	}
	s.logger.Warn("worker: обработчики очередей брокера не зарегистрированы", observ.Fields{}, "service", ServiceID)

	errCh := make(chan error, 2)
	if s.slice.Relay != nil {
		go func() { errCh <- s.slice.Relay.Run(ctx) }()
	}
	go func() { errCh <- s.runRenderLoop(ctx) }()

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

// runRenderLoop периодически исполняет накопившиеся задания рендеринга, пока не
// отменён контекст. Ошибка отдельного задания не останавливает цикл: она
// логируется, а задание остаётся в очереди для следующего прохода.
func (s *Service) runRenderLoop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	// Немедленный первый проход: задание, поставленное до старта воркера,
	// не ждёт интервала.
	s.drainRenderJobs(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			s.drainRenderJobs(ctx)
		}
	}
}

// drainRenderJobs исполняет порцию ожидающих заданий рендеринга.
func (s *Service) drainRenderJobs(ctx context.Context) {
	jobs, err := s.slice.RenderJobs.ListPending(ctx, renderBatch)
	if err != nil {
		s.logger.Error("worker: чтение очереди рендеринга", err, observ.Fields{}, "service", ServiceID)
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		result, err := s.slice.ProcessRender.Handle(ctx, command.ProcessRender{RenderJobID: job.ID()})
		if err != nil {
			s.logger.Error("worker: исполнение задания рендеринга", err, observ.Fields{},
				"service", ServiceID, "render_job_id", job.ID().String())
			continue
		}
		if result.AlreadyDone {
			continue
		}
		s.logger.Info("worker: задание рендеринга обработано", observ.Fields{},
			"service", ServiceID,
			"render_job_id", result.RenderJobID.String(),
			"state", string(result.State),
		)
	}
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
