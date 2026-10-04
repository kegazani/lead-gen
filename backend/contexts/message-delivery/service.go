package messagedelivery

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	outboxinmemory "lead-gen/backend/contexts/message-delivery/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/outbox/relay"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/contracts/data-governance/suppressionpolicy"
	"lead-gen/contracts/integration-platform/mail"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "message-delivery"

// defaultMaxRetryAttempts — предельное число попыток доставки по умолчанию, если
// конфигурация не задала своё. Считает все попытки, включая первую.
const defaultMaxRetryAttempts = 3

// defaultRetryInterval — интервал повторных окон по умолчанию.
const defaultRetryInterval = time.Minute

// retryBatch — предельный размер порции сообщений за один проход повтора.
const retryBatch = 100

// Config — параметры сборки сервиса, задаваемые точкой входа.
type Config struct {
	// Interval — интервал проходов публикации delivery-event транспортным relay.
	Interval time.Duration

	// RetryInterval — интервал тикера повторных окон роли schedule. Ноль →
	// defaultRetryInterval.
	RetryInterval time.Duration

	// MaxRetryAttempts — предельное число попыток доставки (включая первую).
	// Ноль → defaultMaxRetryAttempts; отрицательное — повторные окна отключены.
	MaxRetryAttempts int

	// Publisher — издатель брокера. nil означает, что публикация не собирается:
	// процесс не публикует delivery-event.
	Publisher amqp.Publisher

	// Pool — пул соединений PostgreSQL зелёной зоны. Не nil → персистентность
	// PostgreSQL (сообщения, репутация, outbox) с миграциями, применяемыми точкой
	// сборки. nil → in-memory адаптеры (dev).
	Pool *pgxpool.Pool

	// MailPort — реальный почтовый провайдер (контракт mail владельца
	// integration-platform). MailFrom — адрес отправителя: обязателен, если
	// задан MailPort.
	MailPort mail.Port
	MailFrom string

	// SuppressionPort — стоп-лист владельца data-governance (контракт
	// suppression-policy). Недоступность источника остаётся ошибкой.
	SuppressionPort suppressionpolicy.Port

	// Mailer — явный порт отправки (тесты/dev). Приоритет над MailPort.
	Mailer ports.Mailer

	// Suppressions — явный стоп-лист (тесты/dev). Приоритет над SuppressionPort.
	Suppressions ports.SuppressionList

	// Source — источник команд доставки роли sender. nil → очередь в памяти.
	Source ports.CommandSource

	// Clock — источник времени. nil → SystemClock.
	Clock ports.Clock

	// UnitOfWork — явная транзакция контекста (тесты/линейная сборка). Приоритет
	// над Pool.
	UnitOfWork ports.UnitOfWork

	// RelaySource — журнал outbox для публикации. Обязателен, если задан
	// UnitOfWork: публикация читает то же состояние, что пишет транзакция.
	RelaySource relay.Source

	// Retryables — выборка сообщений для повторных окон. Обязательна, если
	// задан UnitOfWork: роль schedule читает то же состояние.
	Retryables ports.RetryableMessages

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Service — собранный сервис message-delivery. Реализует composition.Service:
// раннер на каждую объявленную каталогом роль.
//
// Роль sender — единственная точка отправки, изолированная от обработки
// API-запросов: у контекста нет роли api. worker публикует delivery-event,
// schedule обслуживает повторные окна доставки.
type Service struct {
	slice         *Slice
	sink          *AMQPSink
	interval      time.Duration
	retryInterval time.Duration
	logger        observ.Logger
	metrics       observ.Metrics
	tracer        observ.Tracer
}

// New собирает сервис. Персистентность, почтовый провайдер и стоп-лист
// выбираются из конфигурации; при отсутствии внешних зависимостей собирается
// явный dev-режим на in-memory адаптерах с предупреждением в лог, а не молчаливая
// подмена заглушкой.
func New(cfg Config) (*Service, error) {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	retryInterval := cfg.RetryInterval
	if retryInterval <= 0 {
		retryInterval = defaultRetryInterval
	}
	if cfg.Logger == nil {
		return nil, fmt.Errorf("message-delivery: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("message-delivery: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("message-delivery: Tracer обязателен")
	}

	var sink relay.Sink
	var amqpSink *AMQPSink
	if cfg.Publisher != nil {
		amqpSink = NewAMQPSink(cfg.Publisher, cfg.Interval)
		sink = amqpSink
	}

	unitOfWork, relaySource, retryables := cfg.UnitOfWork, cfg.RelaySource, cfg.Retryables
	if unitOfWork == nil && cfg.Pool != nil {
		unitOfWork = postgres.NewUnitOfWork(cfg.Pool)
		relaySource = postgres.NewJournal(cfg.Pool)
		retryables = postgres.NewMessageRepository(cfg.Pool)
	} else if unitOfWork == nil {
		cfg.Logger.Warn("message-delivery: DSN не задан — состояние в памяти процесса (dev)",
			observ.Fields{}, "service", ServiceID)
	}

	mailer := cfg.Mailer
	if mailer == nil && cfg.MailPort != nil {
		built, err := NewProviderMailer(cfg.MailPort, cfg.MailFrom)
		if err != nil {
			return nil, fmt.Errorf("message-delivery: почтовый адаптер: %w", err)
		}
		mailer = built
	}
	if mailer == nil {
		cfg.Logger.Warn("message-delivery: почтовый провайдер не задан — in-memory mailer (dev)",
			observ.Fields{}, "service", ServiceID)
	}

	suppressions := cfg.Suppressions
	if suppressions == nil && cfg.SuppressionPort != nil {
		built, err := NewSuppressionList(cfg.SuppressionPort)
		if err != nil {
			return nil, fmt.Errorf("message-delivery: стоп-лист: %w", err)
		}
		suppressions = built
	}
	if suppressions == nil {
		cfg.Logger.Warn("message-delivery: стоп-лист не задан — пустой in-memory (dev)",
			observ.Fields{}, "service", ServiceID)
	}

	maxRetryAttempts := cfg.MaxRetryAttempts
	if maxRetryAttempts == 0 {
		maxRetryAttempts = defaultMaxRetryAttempts
	}

	slice, err := Build(Params{
		Clock:            cfg.Clock,
		UnitOfWork:       unitOfWork,
		RelaySource:      relaySource,
		Retryables:       retryables,
		Sink:             sink,
		Mailer:           mailer,
		Suppressions:     suppressions,
		Source:           cfg.Source,
		MaxRetryAttempts: maxRetryAttempts,
	})
	if err != nil {
		return nil, err
	}
	return &Service{
		slice:         slice,
		sink:          amqpSink,
		interval:      cfg.Interval,
		retryInterval: retryInterval,
		logger:        cfg.Logger,
		metrics:       cfg.Metrics,
		tracer:        cfg.Tracer,
	}, nil
}

// ID реализует composition.Service.
func (s *Service) ID() string { return ServiceID }

// RoleRunner реализует composition.Service. Раннеры ролей владеют только
// запуском и адаптерами: доменных правил здесь нет.
func (s *Service) RoleRunner(role composition.Role) (composition.RoleRunner, bool) {
	switch role {
	case composition.RoleSender:
		return composition.RoleFunc(s.runSender), true
	case composition.RoleWorker:
		return composition.RoleFunc(s.runWorker), true
	case composition.RoleSchedule:
		return composition.RoleFunc(s.runSchedule), true
	default:
		return nil, false
	}
}

// runSender — изолированный контур отправки. Роль читает команды доставки из
// источника и исполняет их сценарием доставки: отправка идёт через порт
// провайдера, исход фиксируется событием в outbox. Доменный отказ по одной
// команде не останавливает роль; сбой чтения источника завершает её с ошибкой.
func (s *Service) runSender(ctx context.Context) error {
	if s.slice.Source == nil {
		return fmt.Errorf("sender: источник команд не собран")
	}
	s.logger.Info("sender: приём команд доставки запущен", observ.Fields{}, "service", ServiceID)

	for {
		cmd, ok, err := s.slice.Source.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				s.logger.Info("sender: останов", observ.Fields{}, "service", ServiceID)
				return nil
			}
			return fmt.Errorf("sender: чтение команды доставки: %w", err)
		}
		if !ok {
			s.logger.Info("sender: источник команд закрыт", observ.Fields{}, "service", ServiceID)
			return nil
		}

		result, handleErr := s.slice.DeliverMessage.Handle(ctx, cmd)
		if handleErr != nil {
			s.logger.Error("sender: доставка команды", handleErr, observ.Fields{},
				"service", ServiceID, "command_id", cmd.CommandID.String())
			continue
		}
		s.logger.Info("sender: команда обработана", observ.Fields{},
			"service", ServiceID,
			"message_id", result.MessageID.String(),
			"status", string(result.Status),
			"replayed", result.Replayed)
	}
}

// runWorker публикует delivery-event из доменного outbox и честно сообщает об
// отсутствии зарегистрированных обработчиков очередей.
func (s *Service) runWorker(ctx context.Context) error {
	if s.slice.Relay == nil || s.sink == nil {
		return fmt.Errorf("worker: публикация delivery-event не собрана (Publisher не задан)")
	}

	go func() {
		if err := s.sink.Run(ctx); err != nil {
			s.logger.Error("worker: транспортный relay", err, observ.Fields{}, "service", ServiceID)
		}
	}()

	s.logger.Warn("worker: обработчики очередей не зарегистрированы", observ.Fields{}, "service", ServiceID)
	s.logger.Info("worker: публикация delivery-event запущена", observ.Fields{}, "service", ServiceID)
	s.publishPending(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("worker: останов", observ.Fields{}, "service", ServiceID)
			return nil
		case <-ticker.C:
			s.publishPending(ctx)
		}
	}
}

// publishPending выполняет один проход публикации и логирует результат. Ошибка
// прохода не останавливает роль: запись останется в outbox и будет доставлена
// повторно.
func (s *Service) publishPending(ctx context.Context) {
	published, err := s.slice.Relay.PublishPending(ctx)
	if err != nil {
		s.logger.Error("worker: публикация delivery-event", err, observ.Fields{},
			"service", ServiceID, "published", published)
		return
	}
	if published > 0 {
		s.logger.Info("worker: проход публикации завершён", observ.Fields{}, "service", ServiceID, "published", published)
	}
}

// runSchedule — роль schedule: тикер повторных окон доставки. Первый проход
// выполняется сразу, затем с заданным интервалом, пока контекст не отменён.
// Повтор подхватывает сообщения, последняя попытка которых завершилась временным
// исходом (недоступность провайдера), и запускает отправку заново.
func (s *Service) runSchedule(ctx context.Context) error {
	if s.slice.RetryPending == nil {
		s.logger.Warn("schedule: повторные окна отключены (не задана выборка сообщений)",
			observ.Fields{}, "service", ServiceID)
		<-ctx.Done()
		return nil
	}

	s.logger.Info("schedule: тикер повторных окон запущен", observ.Fields{},
		"service", ServiceID, "interval", s.retryInterval.String())
	s.retryOnce(ctx)

	ticker := time.NewTicker(s.retryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("schedule: останов", observ.Fields{}, "service", ServiceID)
			return nil
		case <-ticker.C:
			s.retryOnce(ctx)
		}
	}
}

// retryOnce выполняет один проход повторных окон и логирует результат. Ошибка
// прохода не останавливает роль: сообщение остаётся в хранилище.
func (s *Service) retryOnce(ctx context.Context) {
	summary, err := s.slice.RetryPending.RetryDue(ctx, retryBatch)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		s.logger.Error("schedule: повторные окна", err, observ.Fields{}, "service", ServiceID)
		return
	}
	if summary.Attempted > 0 || summary.Skipped > 0 {
		s.logger.Info("schedule: проход повторных окон завершён", observ.Fields{},
			"service", ServiceID,
			"attempted", summary.Attempted,
			"delivered", summary.Delivered,
			"failed", summary.Failed,
			"skipped", summary.Skipped)
	}
}

// Journal возвращает журнал outbox среза. Нужен точке сборки и проверкам. nil,
// если состояние вынесено во внешнюю (PostgreSQL) транзакцию.
func (s *Service) Journal() *outboxinmemory.Journal { return s.slice.Journal }

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
