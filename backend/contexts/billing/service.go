package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	httpapi "lead-gen/backend/contexts/billing/internal/adapters/http"
	"lead-gen/backend/contexts/billing/internal/adapters/idgen"
	"lead-gen/backend/contexts/billing/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/billing/internal/adapters/payment"
	persistence "lead-gen/backend/contexts/billing/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/billing/internal/app/command"
	"lead-gen/backend/contexts/billing/internal/app/query"
	"lead-gen/backend/contexts/billing/internal/ports"
	composition "lead-gen/backend/platform/composition"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "billing"

// defaultInterval — интервал фоновых проходов роли worker (расчётный контур и
// публикация событий), если задано некорректное значение.
const defaultInterval = time.Second

// SystemClock — часы процесса, источник времени по умолчанию.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// APIAddr — адрес внутреннего HTTP-сервера роли api.
	APIAddr string

	// Interval — интервал фоновых проходов роли worker.
	Interval time.Duration

	// SubscriptionRepository — репозиторий подписок. nil → новый in-memory.
	SubscriptionRepository *persistence.SubscriptionRepository

	// SuccessFeeRepository — репозиторий вознаграждений. nil → новый in-memory.
	SuccessFeeRepository *persistence.SuccessFeeRepository

	// Journal — журнал outbox событий billing. nil → новый журнал в памяти.
	Journal platformoutbox.Journal

	// PaymentGateway — порт внешней платёжной системы. nil означает, что система
	// не подключена: списание честно отвечает «недоступно».
	PaymentGateway ports.PaymentGateway

	// Clock — источник времени прикладного слоя. nil → SystemClock.
	Clock ports.Clock

	// IDs — генератор идентификаторов событий. nil → случайный генератор.
	IDs ports.IDGenerator

	// Publisher — издатель брокера. nil означает, что публикация не собирается:
	// процесс не запускает relay событий billing.
	Publisher amqp.Publisher

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Slice — собранный контекст среза: порты, адаптеры и use cases.
type Slice struct {
	SubscriptionRepository *persistence.SubscriptionRepository
	SuccessFeeRepository   *persistence.SuccessFeeRepository
	Journal                platformoutbox.Journal
	UnitOfWork             *inmemory.UnitOfWork

	// Relay не равен nil только если при сборке задан Publisher.
	Relay *platformoutbox.Relay

	// Server — HTTP-обработчики роли api. Реализует http.Handler.
	Server *httpapi.Server

	ActivateSubscription *command.ActivateSubscriptionHandler
	SuspendSubscription  *command.SuspendSubscriptionHandler
	ResumeSubscription   *command.ResumeSubscriptionHandler
	RecordUsage          *command.RecordUsageHandler
	IssueInvoice         *command.IssueInvoiceHandler
	ChargeInvoice        *command.ChargeInvoiceHandler
	CalculateSuccessFee  *command.CalculateSuccessFeeHandler
	DisputeSuccessFee    *command.DisputeSuccessFeeHandler
	SettleDuePeriods     *command.SettleDuePeriodsHandler

	GetSubscription *query.GetSubscriptionHandler
	GetEntitlement  *query.GetEntitlementHandler
	GetSuccessFee   *query.GetSuccessFeeHandler

	payment ports.PaymentGateway
	clock   ports.Clock
	ids     ports.IDGenerator
}

// Build собирает контекст среза из адаптеров и переданных зависимостей.
// Бизнес-правил здесь нет — соединение адаптеров, портов и use cases только
// своего контекста.
func Build(cfg Config) (*Slice, error) {
	subscriptions := cfg.SubscriptionRepository
	if subscriptions == nil {
		subscriptions = persistence.NewSubscriptionRepository()
	}
	fees := cfg.SuccessFeeRepository
	if fees == nil {
		fees = persistence.NewSuccessFeeRepository()
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
	gateway := cfg.PaymentGateway
	if gateway == nil {
		gateway = payment.Unavailable{}
	}

	unitOfWork := inmemory.NewUnitOfWorkWith(subscriptions, fees, journal)

	activate := command.NewActivateSubscriptionHandler(unitOfWork, clock)
	suspend := command.NewSuspendSubscriptionHandler(unitOfWork, clock)
	resume := command.NewResumeSubscriptionHandler(unitOfWork, clock)
	recordUsage := command.NewRecordUsageHandler(unitOfWork, clock, ids)
	issueInvoice := command.NewIssueInvoiceHandler(unitOfWork, clock, ids)
	chargeInvoice := command.NewChargeInvoiceHandler(unitOfWork, gateway, clock)
	calculateFee := command.NewCalculateSuccessFeeHandler(unitOfWork, clock, ids)
	disputeFee := command.NewDisputeSuccessFeeHandler(unitOfWork, clock)
	settle := command.NewSettleDuePeriodsHandler(unitOfWork, clock, ids)

	getSubscription := query.NewGetSubscriptionHandler(subscriptions)
	getEntitlement := query.NewGetEntitlementHandler(subscriptions)
	getSuccessFee := query.NewGetSuccessFeeHandler(fees)

	server := httpapi.NewServer(httpapi.Dependencies{
		ActivateSubscription: activate,
		GetSubscription:      getSubscription,
		SuspendSubscription:  suspend,
		ResumeSubscription:   resume,
		RecordUsage:          recordUsage,
		IssueInvoice:         issueInvoice,
		ChargeInvoice:        chargeInvoice,
		GetEntitlement:       getEntitlement,
		CalculateSuccessFee:  calculateFee,
		GetSuccessFee:        getSuccessFee,
		DisputeSuccessFee:    disputeFee,
	})

	var relay *platformoutbox.Relay
	if cfg.Publisher != nil {
		relay = platformoutbox.NewRelay(journal, cfg.Publisher, cfg.Interval)
	}

	return &Slice{
		SubscriptionRepository: subscriptions,
		SuccessFeeRepository:   fees,
		Journal:                journal,
		UnitOfWork:             unitOfWork,
		Relay:                  relay,
		Server:                 server,

		ActivateSubscription: activate,
		SuspendSubscription:  suspend,
		ResumeSubscription:   resume,
		RecordUsage:          recordUsage,
		IssueInvoice:         issueInvoice,
		ChargeInvoice:        chargeInvoice,
		CalculateSuccessFee:  calculateFee,
		DisputeSuccessFee:    disputeFee,
		SettleDuePeriods:     settle,

		GetSubscription: getSubscription,
		GetEntitlement:  getEntitlement,
		GetSuccessFee:   getSuccessFee,

		payment: gateway,
		clock:   clock,
		ids:     ids,
	}, nil
}

// Service — собранный сервис billing. Реализует composition.Service: раннер на
// каждую объявленную роль.
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
		return nil, fmt.Errorf("billing: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("billing: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("billing: Tracer обязателен")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultInterval
	}

	slice, err := Build(cfg)
	if err != nil {
		return nil, err
	}
	return &Service{
		slice:    slice,
		apiAddr:  cfg.APIAddr,
		interval: interval,
		logger:   cfg.Logger,
		metrics:  cfg.Metrics,
		tracer:   cfg.Tracer,
	}, nil
}

// ID реализует composition.Service.
func (s *Service) ID() string { return ServiceID }

// RoleRunner реализует composition.Service. Раннеры ролей владеют только запуском
// и адаптерами: доменных правил здесь нет.
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

// runAPI поднимает внутренний HTTP-сервер контекста и останавливает его по отмене
// контекста.
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

// runWorker запускает фоновые контуры роли worker: расчётный проход по периодам
// подписок и, если собран, relay публикации событий billing. Контуры независимы и
// работают до отмены контекста; падение любого из них останавливает роль.
func (s *Service) runWorker(ctx context.Context) error {
	s.logger.Info("worker: расчётный контур периодов запущен", observ.Fields{}, "service", ServiceID)
	if s.slice.Relay == nil {
		s.logger.Warn("worker: публикация событий billing не собрана (брокер не подключён)", observ.Fields{}, "service", ServiceID)
	}
	s.logger.Warn("worker: обработчики очередей брокера не зарегистрированы", observ.Fields{}, "service", ServiceID)

	errCh := make(chan error, 2)
	go func() { errCh <- s.runSettlement(ctx) }()
	if s.slice.Relay != nil {
		go func() { errCh <- s.slice.Relay.Run(ctx) }()
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

// runSettlement выполняет расчётный проход по расписанию. Ошибка прохода не
// прерывает цикл: следующий тик повторит попытку.
func (s *Service) runSettlement(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if _, err := s.slice.SettleDuePeriods.Handle(ctx); err != nil {
				s.logger.Error("worker: расчётный проход", err, observ.Fields{}, "service", ServiceID)
			}
		}
	}
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
