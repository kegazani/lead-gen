package outreachorchestration

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/relay"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/scheduling"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/readiness"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/contracts/content-production/contentversion"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
	"lead-gen/contracts/platform-governance/accessdecision"
	"lead-gen/frontend/components"
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

	// Grants — права доступа dev/test-двойника: используются только когда не
	// задан Access (dev-сборка без владельца). По умолчанию доступ закрыт.
	Grants []Grant

	// Access — решение о доступе из контракта access-decision владельца
	// platform-governance. Продуктовый путь (cmd/lead-gen) задаёт его явно,
	// включая честный недоступный решатель, когда владелец не собран. nil —
	// dev-сборка на правах Grants.
	Access accessdecision.Decider

	// Risk — внешний предикат признаков риска пакетного пути. ОБЯЗАТЕЛЕН:
	// собирается через NewRiskFacts поверх портов владельцев и передаётся в Build
	// явно (см. Params.Risk). Заглушки на продуктовом пути нет.
	Risk domain.CanBeDecidedInPackage

	// Publisher — издатель брокера. nil означает, что публикация не собирается:
	// процесс не публикует approved-action (например, сборка только под api).
	Publisher amqp.Publisher

	// ApprovedActionSink — явное назначение релиза approved-action в форме
	// функции (SinkFunc): задано — утверждённые действия из outbox-релиза
	// доставляются сюда, а транспортный AMQP-Sink не собирается. nil — назначение
	// выбирается из ApprovedActionConsumer, затем из Publisher. См.
	// Params.ApprovedActionSink.
	ApprovedActionSink SinkFunc

	// Transport — механизм группы процессов из deploy/services.yaml, задающий
	// способ связи синхронного контракта approved-action с потребителем. Пустое
	// значение означает in-process (совмещённый деплой); сетевой транспорт в
	// объёме среза не реализован.
	Transport string

	// ApprovedActionConsumer — внешний потребитель утверждённых действий
	// (например, мост в message-delivery), реализующий потребительский интерфейс
	// контракта. nil — прежнее поведение: владелец собирает свой in-memory
	// publication-сервис (Slice.Publication). См. Params.ApprovedActionConsumer.
	ApprovedActionConsumer approvedaction.Publisher

	// Persistence — адаптеры хранилища контекста. Нулевое значение означает
	// dev-сборку на in-memory адаптерах; при заданном DSN точка входа передаёт
	// сюда NewPostgresPersistence, и тогда агрегат и события переживают рестарт,
	// а движок последовательностей пишет их атомарно в PostgreSQL.
	Persistence Persistence
	// ContentVersions — опубликованный источник exact-version владельца
	// content-production для редакторского чтения.
	ContentVersions contentversion.Source

	// ConsoleAuth — учётные данные оператора консольных экранов рассылок (срез 4
	// web-frontend-architecture). Пустое значение означает, что консоль не
	// настроена: точка входа задаёт логин, пароль и общий стор сессий.
	ConsoleAuth consoleauth.Config

	// Console — базовые адреса сервисов консоли для общей навигации по разделам.
	Console components.ConsoleBases

	// BasePath — внешний префикс пути консоли под единым origin (admin-proxy).
	// Пусто — корень origin.
	BasePath string

	// ExternalPrefixes — префиксы других консолей единого origin: ссылки на них
	// в навигации не переписываются под собственный префикс.
	ExternalPrefixes []string

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Service — собранный сервис outreach-orchestration. Реализует
// composition.Service: раннер на каждую объявленную роль.
type Service struct {
	slice     *Slice
	sink      *AMQPSink
	apiAddr   string
	interval  time.Duration
	logger    observ.Logger
	metrics   observ.Metrics
	tracer    observ.Tracer
	readiness *ReadinessReport
}

// New собирает сервис из in-memory адаптеров. Публикация собирается, если задан
// явный ApprovedActionSink, внешний ApprovedActionConsumer или Publisher: тогда
// срез публикует approved-action. Без них публикация не собирается.
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

	// Назначение релиза approved-action. Если задан внешний Sink или потребитель,
	// транспортный AMQP-Sink не собирается: утверждённые действия идут внешнему
	// получателю. Иначе сохраняется прежнее поведение (AMQP при заданном
	// Publisher).
	var sink relay.Sink
	var amqpSink *AMQPSink
	if cfg.ApprovedActionSink == nil && cfg.ApprovedActionConsumer == nil && cfg.Publisher != nil {
		amqpSink = NewAMQPSink(cfg.Publisher, cfg.Interval).
			WithLogger(cfg.Logger).
			WithMetrics(cfg.Metrics)
		sink = amqpSink
	}

	// Хранилище контекста: PostgreSQL-адаптеры при заданном Persistence, иначе
	// in-memory (dev). О dev-сборке честно сообщается: пустой DSN не должен
	// выглядеть готовой персистентностью.
	if !cfg.Persistence.configured() {
		cfg.Logger.Warn("outreach-orchestration: PostgreSQL не подключён (DSN пуст), сборка на in-memory адаптерах (только dev)", observ.Fields{}, "service", ServiceID)
	}

	slice, err := Build(Params{
		Grants:                  cfg.Grants,
		Access:                  cfg.Access,
		Risk:                    cfg.Risk,
		Sink:                    sink,
		ApprovedActionSink:      cfg.ApprovedActionSink,
		ApprovedActionTransport: transport,
		ApprovedActionConsumer:  cfg.ApprovedActionConsumer,
		Persistence:             cfg.Persistence,
		ContentVersions:         cfg.ContentVersions,
		ConsoleAuth:             cfg.ConsoleAuth,
		Console:                 cfg.Console,
		BasePath:                cfg.BasePath,
		ExternalPrefixes:        cfg.ExternalPrefixes,
	})
	if err != nil {
		return nil, err
	}
	var publicationDestination any = amqpSink
	if cfg.ApprovedActionSink != nil {
		publicationDestination = cfg.ApprovedActionSink
	} else if cfg.ApprovedActionConsumer != nil {
		publicationDestination = cfg.ApprovedActionConsumer
	}
	readinessReport := NewReadinessReport(cfg.Persistence, publicationDestination)
	slice.Readiness = readinessReport
	slice.Server.SetReadiness(func(ctx context.Context) readiness.Snapshot {
		return readinessReport.RoleReadiness(ctx, composition.RoleAPI)
	})
	return &Service{
		slice:     slice,
		sink:      amqpSink,
		apiAddr:   cfg.APIAddr,
		interval:  cfg.Interval,
		logger:    cfg.Logger,
		metrics:   cfg.Metrics,
		tracer:    cfg.Tracer,
		readiness: readinessReport,
	}, nil
}

// RoleReadiness сообщает владельческую готовность роли, не запуская её раннер.
func (s *Service) RoleReadiness(ctx context.Context, role composition.Role) readiness.Snapshot {
	if s == nil || s.readiness == nil {
		return readiness.Snapshot{Capability: string(role), Status: readiness.StatusUnknown, Ready: false}
	}
	return s.readiness.RoleReadiness(ctx, role)
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
	if s.slice.Relay == nil {
		return fmt.Errorf("worker: публикация approved-action не собрана (не задан ApprovedActionSink, ApprovedActionConsumer или Publisher)")
	}

	// Транспортный relay (AMQP) запускается только тогда, когда собран. Внешний
	// Sink/потребитель доставляет действие синхронно в проходе PublishPending и
	// отдельного Run не требует.
	if s.sink != nil {
		go func() {
			if err := s.sink.Run(ctx); err != nil {
				s.logger.Error("worker: транспортный relay", err, observ.Fields{}, "service", ServiceID)
			}
		}()
	}

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

// runChains запускает движок последовательностей: он формирует черновики шагов
// и продвигает цепочки. Отправка не выполняется: каждый шаг ждёт отдельного
// утверждения оператора (design.md, Решение 1).
func (s *Service) runChains(ctx context.Context) error {
	engine := s.slice.SequenceEngine
	if engine == nil {
		return fmt.Errorf("chains: движок последовательностей не собран")
	}

	s.logger.Info("chains: движок последовательностей запущен", observ.Fields{}, "service", ServiceID)

	s.advanceSequences(ctx, engine)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("chains: останов", observ.Fields{}, "service", ServiceID)
			return nil
		case <-ticker.C:
			s.advanceSequences(ctx, engine)
		}
	}
}

// advanceSequences выполняет один проход движка и логирует результат. Простой
// цепочек не устраняется, а сообщается: неутверждённый шаг остаётся
// неисполненным (design.md, Решение 2).
func (s *Service) advanceSequences(ctx context.Context, engine *scheduling.Engine) {
	report, err := engine.RunOnce(ctx)
	if err != nil {
		s.logger.Error("chains: проход движка последовательностей", err, observ.Fields{}, "service", ServiceID)
		return
	}
	if report.Drafted > 0 || report.Advanced > 0 || report.Completed > 0 {
		s.logger.Info("chains: проход завершён", observ.Fields{}, "service", ServiceID,
			"drafted", report.Drafted, "advanced", report.Advanced, "completed", report.Completed)
	}
	for _, notice := range report.Stalled {
		s.logger.Warn("chains: простой — шаг ждёт утверждения оператора", observ.Fields{}, "service", ServiceID,
			"campaign", notice.CampaignID, "executions", len(notice.Executions))
	}
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
