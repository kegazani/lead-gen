package communityintelligence

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/community-intelligence/internal/adapters/idgen"
	outboxinmemory "lead-gen/backend/contexts/community-intelligence/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/community-intelligence/internal/adapters/persistence/inmemory"
	queueinmemory "lead-gen/backend/contexts/community-intelligence/internal/adapters/queue/inmemory"
	"lead-gen/backend/contexts/community-intelligence/internal/adapters/telegram"
	"lead-gen/backend/contexts/community-intelligence/internal/app/command"
	"lead-gen/backend/contexts/community-intelligence/internal/app/query"
	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "community-intelligence"

// Значения по умолчанию для параметров сборки.
const (
	// defaultInterval — период регулярного сбора роли schedule.
	defaultInterval = time.Minute

	// defaultLookback — глубина окна чтения регулярного сбора.
	defaultLookback = 24 * time.Hour
)

// SystemClock — часы процесса, источник времени по умолчанию.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// Interval — период регулярного сбора роли schedule. Не положителен —
	// defaultInterval.
	Interval time.Duration

	// Lookback — глубина окна чтения регулярного сбора. Не положителен —
	// defaultLookback.
	Lookback time.Duration

	// QuoteThreshold — порог размера сообщества для включения цитат. Не
	// положителен — command.DefaultQuoteThreshold.
	QuoteThreshold int

	// MaxMessages — верхняя граница числа сообщений на одно чтение; 0 — без
	// ограничения.
	MaxMessages int

	// Registry — реестр разрешённых сообществ. nil → пустой in-memory реестр.
	Registry ports.CommunityRegistry

	// Tracker — список отслеживаемых сообществ для роли schedule. nil → реестр,
	// если он реализует ports.CommunityTracker, иначе сбор не собирается.
	Tracker ports.CommunityTracker

	// Reader — источник чтения сообществ за портом. nil → честно недоступный
	// адаптер (без сети): срез не выдаёт себя за подключённый к Telegram.
	Reader ports.CommunityReader

	// Digests — хранилище выжимок. nil → новое in-memory хранилище.
	Digests ports.DigestRepository

	// Outbox — outbox событий выжимки. nil → новый in-memory outbox.
	Outbox ports.Outbox

	// Queue — очередь команд чтения между schedule и worker. nil → новая
	// in-memory очередь.
	Queue ports.CommandQueue

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
	Registry ports.CommunityRegistry
	Tracker  ports.CommunityTracker
	Reader   ports.CommunityReader
	Digests  ports.DigestRepository
	Outbox   ports.Outbox
	Queue    ports.CommandQueue
	Clock    ports.Clock

	ReadCommunity *command.ReadCommunityHandler
	GetDigest     *query.GetDigestHandler
	ListDigests   *query.ListDigestsHandler
}

// Build собирает контекст среза из адаптеров и переданных зависимостей.
// Бизнес-правил здесь нет — соединение адаптеров, портов и use cases только
// своего контекста.
func Build(cfg Config) (*Slice, error) {
	if cfg.QuoteThreshold <= 0 {
		cfg.QuoteThreshold = command.DefaultQuoteThreshold
	}

	registry := cfg.Registry
	registryImpl, isRegistryImpl := registry.(*persistence.Registry)
	if registry == nil {
		registryImpl = persistence.NewRegistry()
		registry = registryImpl
		isRegistryImpl = true
	}

	tracker := cfg.Tracker
	if tracker == nil && isRegistryImpl {
		tracker = registryImpl
	}

	reader := cfg.Reader
	if reader == nil {
		reader = telegram.Unavailable{}
	}

	digests := cfg.Digests
	if digests == nil {
		digests = persistence.New()
	}

	outbox := cfg.Outbox
	if outbox == nil {
		outbox = outboxinmemory.New()
	}

	queue := cfg.Queue
	if queue == nil {
		queue = queueinmemory.New()
	}

	clock := cfg.Clock
	if clock == nil {
		clock = SystemClock{}
	}

	ids := cfg.IDs
	if ids == nil {
		ids = idgen.NewRandom()
	}

	readCommunity := command.NewReadCommunityHandler(
		registry, reader, digests, outbox, clock, ids, cfg.QuoteThreshold,
	)

	return &Slice{
		Registry:      registry,
		Tracker:       tracker,
		Reader:        reader,
		Digests:       digests,
		Outbox:        outbox,
		Queue:         queue,
		Clock:         clock,
		ReadCommunity: readCommunity,
		GetDigest:     query.NewGetDigestHandler(digests),
		ListDigests:   query.NewListDigestsHandler(digests),
	}, nil
}

// Service — собранный сервис community-intelligence. Реализует
// composition.Service: раннер на каждую объявленную каталогом роль.
type Service struct {
	slice       *Slice
	interval    time.Duration
	lookback    time.Duration
	maxMessages int
	logger      observ.Logger
	metrics     observ.Metrics
	tracer      observ.Tracer
}

// New собирает сервис из адаптеров и переданных зависимостей.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("community-intelligence: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("community-intelligence: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("community-intelligence: Tracer обязателен")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = defaultInterval
	}
	if cfg.Lookback <= 0 {
		cfg.Lookback = defaultLookback
	}

	slice, err := Build(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{
		slice:       slice,
		interval:    cfg.Interval,
		lookback:    cfg.Lookback,
		maxMessages: cfg.MaxMessages,
		logger:      cfg.Logger,
		metrics:     cfg.Metrics,
		tracer:      cfg.Tracer,
	}, nil
}

// Slice возвращает собранный контекст среза. Нужен точке сборки и тестам.
func (s *Service) Slice() *Slice { return s.slice }

// ID реализует composition.Service.
func (s *Service) ID() string { return ServiceID }

// RoleRunner реализует composition.Service. Каталог объявляет роли worker и
// schedule. Раннеры владеют только запуском и адаптерами: доменных правил здесь
// нет.
func (s *Service) RoleRunner(role composition.Role) (composition.RoleRunner, bool) {
	switch role {
	case composition.RoleWorker:
		return composition.RoleFunc(s.runWorker), true
	case composition.RoleSchedule:
		return composition.RoleFunc(s.runSchedule), true
	default:
		return nil, false
	}
}

// runSchedule — роль schedule: регулярный сбор. Тикер обходит отслеживаемые
// сообщества и кладёт команды чтения в очередь; разбирает их роль worker.
// Сбор выполняется сразу при старте, затем с заданным интервалом, пока контекст
// не отменён.
func (s *Service) runSchedule(ctx context.Context) error {
	if s.slice.Queue == nil {
		return fmt.Errorf("schedule: очередь команд не собрана")
	}
	if s.slice.Tracker == nil {
		s.logger.Warn("schedule: отслеживаемые сообщества не заданы, сбор не выполняется", observ.Fields{}, "service", ServiceID)
		<-ctx.Done()
		return nil
	}

	s.collectOnce(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("schedule: останов", observ.Fields{}, "service", ServiceID)
			return nil
		case <-ticker.C:
			s.collectOnce(ctx)
		}
	}
}

// collectOnce обходит отслеживаемые сообщества и ставит команды чтения в
// очередь. Ошибка отдельного сообщества не останавливает проход.
func (s *Service) collectOnce(ctx context.Context) {
	now := s.clock().Now()
	from := now.Add(-s.lookback)

	tracked, err := s.slice.Tracker.Tracked(ctx)
	if err != nil {
		s.logger.Error("schedule: список отслеживаемых сообществ", err, observ.Fields{}, "service", ServiceID)
		return
	}
	enqueued := 0
	for _, community := range tracked {
		cmd := ports.ReadCommand{
			CommunityID:   community.CommunityID,
			TenantID:      community.TenantID,
			From:          from,
			To:            now,
			MaxMessages:   firstPositive(community.MaxMessages, s.maxMessages),
			Actor:         domain.ActorID("system:schedule"),
			CorrelationID: domain.CorrelationID("schedule:" + community.CommunityID.String()),
		}
		if err := s.slice.Queue.Enqueue(ctx, cmd); err != nil {
			s.logger.Warn("schedule: постановка команды чтения", observ.Fields{CorrelationID: cmd.CorrelationID.String()}, "service", ServiceID, "community", community.CommunityID.String(), "error", err.Error())
			continue
		}
		enqueued++
	}
	if enqueued > 0 {
		s.logger.Info("schedule: проход сбора завершён", observ.Fields{}, "service", ServiceID, "enqueued", enqueued)
	}
}

// runWorker — роль worker: разбор очереди команд чтения и построение выжимки.
// Потребителей брокера в срезе нет; очередь in-memory, и это не скрывается.
func (s *Service) runWorker(ctx context.Context) error {
	if s.slice.Queue == nil {
		return fmt.Errorf("worker: очередь команд не собрана")
	}
	s.logger.Info("worker: разбор очереди команд чтения запущен", observ.Fields{}, "service", ServiceID)
	for {
		cmd, ok := s.slice.Queue.Dequeue(ctx)
		if !ok {
			s.logger.Info("worker: останов", observ.Fields{}, "service", ServiceID)
			return nil
		}
		s.handle(ctx, cmd)
	}
}

// handle исполняет одну команду чтения. Ошибка чтения не останавливает роль:
// сообщество может быть недоступно или ждать авторизации.
func (s *Service) handle(ctx context.Context, cmd ports.ReadCommand) {
	result, err := s.slice.ReadCommunity.Handle(ctx, command.ReadCommunity{
		CommunityID:   cmd.CommunityID,
		TenantID:      cmd.TenantID,
		From:          cmd.From,
		To:            cmd.To,
		MaxMessages:   cmd.MaxMessages,
		Actor:         cmd.Actor,
		CorrelationID: cmd.CorrelationID,
	})
	if err != nil {
		s.logger.Warn("worker: чтение сообщества", observ.Fields{CorrelationID: cmd.CorrelationID.String(), TenantID: cmd.TenantID.String()}, "service", ServiceID, "community", cmd.CommunityID.String(), "error", err.Error())
		return
	}
	s.logger.Info("worker: выжимка построена", observ.Fields{CorrelationID: cmd.CorrelationID.String(), TenantID: cmd.TenantID.String()}, "service", ServiceID, "digest", result.DigestID.String(), "community", result.CommunityID.String(), "topics", result.TopicCount)
}

// clock возвращает часы среза либо системные часы: сбор не должен падать из-за
// отсутствия инъекции.
func (s *Service) clock() ports.Clock {
	if s.slice.Clock != nil {
		return s.slice.Clock
	}
	return SystemClock{}
}

// firstPositive возвращает первое положительное значение.
func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
