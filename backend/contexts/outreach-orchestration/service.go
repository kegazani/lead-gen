package outreachorchestration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/relay"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста
// (design.md, Решение 1). Совпадает с id в architecture/domain-catalog.yaml.
const ServiceID = "outreach-orchestration"

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// APIAddr — адрес внутреннего HTTP-сервера роли api.
	APIAddr string

	// Interval — интервал публикации approved-action транспортным relay.
	Interval time.Duration

	// Grants — права доступа среза. По умолчанию доступ закрыт.
	Grants []Grant

	// Risk — внешний предикат признаков риска пакетного пути. ОБЯЗАТЕЛЕН:
	// передаётся в Build явно (реальный или осознанная заглушка
	// NoUpstreamRiskChecks). См. Params.Risk.
	Risk domain.CanBeDecidedInPackage

	// Publisher — издатель брокера. nil означает, что публикация не собирается:
	// процесс не публикует approved-action (например, сборка только под api).
	Publisher amqp.Publisher

	// Transport — механизм группы процессов из deploy/services.yaml, задающий
	// способ связи синхронного контракта approved-action с потребителем. Пустое
	// значение означает in-process (совмещённый деплой); сетевой транспорт в
	// объёме среза не реализован.
	Transport string

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Service — собранный сервис outreach-orchestration. Реализует
// composition.Service: раннер на каждую объявленную роль.
type Service struct {
	slice    *Slice
	sink     *AMQPSink
	apiAddr  string
	interval time.Duration
	logger   observ.Logger
	metrics  observ.Metrics
	tracer   observ.Tracer
}

// New собирает сервис из in-memory адаптеров. Публикация собирается только
// тогда, когда задан Publisher: без него срез не публикует approved-action.
func New(cfg Config) (*Service, error) {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	if cfg.Logger == nil {
		return nil, fmt.Errorf("outreach-orchestration: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("outreach-orchestration: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("outreach-orchestration: Tracer обязателен")
	}

	// Способ связи синхронного контракта выбирается топологией, а не сборкой:
	// неизвестный механизм — ошибка, а не молчаливый выбор транспорта.
	transport, err := ParseTransport(cfg.Transport)
	if err != nil {
		return nil, err
	}

	var sink relay.Sink
	var amqpSink *AMQPSink
	if cfg.Publisher != nil {
		amqpSink = NewAMQPSink(cfg.Publisher, cfg.Interval)
		sink = amqpSink
	}

	slice, err := Build(Params{
		Grants:                  cfg.Grants,
		Risk:                    cfg.Risk,
		Sink:                    sink,
		ApprovedActionTransport: transport,
	})
	if err != nil {
		return nil, err
	}
	return &Service{
		slice:    slice,
		sink:     amqpSink,
		apiAddr:  cfg.APIAddr,
		interval: cfg.Interval,
		logger:   cfg.Logger,
		metrics:  cfg.Metrics,
		tracer:   cfg.Tracer,
	}, nil
}

// GrantsFromStrings строит права доступа среза из строковых значений флагов
// процесса. Пустое значение любого из них означает закрытый доступ: сборка не
// выдумывает стартовую политику.
func GrantsFromStrings(actor, tenant string) []Grant {
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(tenant) == "" {
		return nil
	}
	return []Grant{{Actor: domain.ActorID(actor), Tenant: domain.TenantID(tenant)}}
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
	case composition.RoleChains:
		return composition.RoleFunc(s.runChains), true
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

// runWorker публикует approved-action из доменного outbox и честно сообщает об
// отсутствии зарегистрированных обработчиков очередей.
//
// В объёме среза у worker одна осмысленная обязанность: публикация.
// Транспортный outbox публикует записи в брокер, а доменный relay среза
// перекладывает в него утверждённые действия. Потребителей очередей движков
// контекста в срезе нет — это не скрывается, а логируется.
func (s *Service) runWorker(ctx context.Context) error {
	if s.slice.Relay == nil || s.sink == nil {
		return fmt.Errorf("worker: публикация approved-action не собрана (Publisher не задан)")
	}

	go func() {
		if err := s.sink.Run(ctx); err != nil {
			s.logger.Error("worker: транспортный relay", err, observ.Fields{}, "service", ServiceID)
		}
	}()

	s.logger.Warn("worker: обработчики очередей не зарегистрированы", observ.Fields{}, "service", ServiceID)

	s.logger.Info("worker: публикация approved-action запущена", observ.Fields{}, "service", ServiceID)
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
		s.logger.Error("worker: публикация approved-action", err, observ.Fields{}, "service", ServiceID, "published", published)
		return
	}
	if published > 0 {
		s.logger.Info("worker: проход публикации завершён", observ.Fields{}, "service", ServiceID, "published", published)
	}
}

// runChains — каркас роли chains. Durable-планировщик долгих состояний
// (design.md, Решение 6) в срезе не реализован: роли честно сообщают об этом и
// не делают вид, что ведут цепочки.
func (s *Service) runChains(ctx context.Context) error {
	s.logger.Warn("chains: планировщик долгих состояний не реализован", observ.Fields{}, "service", ServiceID)
	<-ctx.Done()
	return nil
}

// runSchedule — каркас роли schedule. Тикер идемпотентных окон в срезе не
// реализован: роль только сообщает о себе и завершается по отмене контекста.
func (s *Service) runSchedule(ctx context.Context) error {
	s.logger.Warn("schedule: тикер идемпотентных окон не реализован", observ.Fields{}, "service", ServiceID)
	<-ctx.Done()
	return nil
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
