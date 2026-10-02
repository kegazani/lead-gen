package messagedelivery

import (
	"context"
	"fmt"
	"time"

	outboxinmemory "lead-gen/backend/contexts/message-delivery/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/outbox/relay"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "message-delivery"

// Config — параметры сборки сервиса, задаваемые точкой входа.
type Config struct {
	// Interval — интервал проходов публикации delivery-event транспортным relay.
	Interval time.Duration

	// Publisher — издатель брокера. nil означает, что публикация не собирается:
	// процесс не публикует delivery-event.
	Publisher amqp.Publisher

	// Mailer — порт отправки почты. nil → in-memory адаптер среза.
	Mailer ports.Mailer

	// Suppressions — стоп-лист получателей. nil → пустой in-memory стоп-лист.
	Suppressions ports.SuppressionList

	// Source — источник команд доставки роли sender. nil → очередь в памяти.
	Source ports.CommandSource

	// Clock — источник времени. nil → SystemClock.
	Clock ports.Clock

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
// schedule обслуживает повторные окна (в объёме среза — честная заглушка).
type Service struct {
	slice    *Slice
	sink     *AMQPSink
	interval time.Duration
	logger   observ.Logger
	metrics  observ.Metrics
	tracer   observ.Tracer
}

// New собирает сервис из in-memory адаптеров. Публикация собирается только
// тогда, когда задан Publisher: без него срез не публикует delivery-event.
func New(cfg Config) (*Service, error) {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
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

	slice, err := Build(Params{
		Clock:        cfg.Clock,
		Sink:         sink,
		Mailer:       cfg.Mailer,
		Suppressions: cfg.Suppressions,
		Source:       cfg.Source,
	})
	if err != nil {
		return nil, err
	}
	return &Service{
		slice:    slice,
		sink:     amqpSink,
		interval: cfg.Interval,
		logger:   cfg.Logger,
		metrics:  cfg.Metrics,
		tracer:   cfg.Tracer,
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

// runSchedule — каркас роли schedule. Тикер повторных окон доставки в объёме
// среза не реализован: роль честно сообщает об этом и не делает вид, что ведёт
// повторные попытки.
func (s *Service) runSchedule(ctx context.Context) error {
	s.logger.Warn("schedule: тикер повторных попыток не реализован", observ.Fields{}, "service", ServiceID)
	<-ctx.Done()
	return nil
}

// Journal возвращает журнал outbox среза. Нужен точке сборки и проверкам.
func (s *Service) Journal() *outboxinmemory.Journal { return s.slice.Journal }

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
