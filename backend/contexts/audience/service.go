// Package audience собирает сервис контекста audience: соединение адаптеров,
// портов и use cases своего контекста и публикацию его ролей.
//
// Контекст отвечает за скоринг, сегментацию и фиксацию целевой аудитории
// кампании (architecture/domain-catalog.yaml).
//
// Это единственное место сборки своего контекста; бизнес-правил здесь нет.
package audience

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"lead-gen/backend/contexts/audience/internal/app/command"
	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/platform/composition"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/observability/observ"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "audience"

// defaultInterval — интервал регулярного пересчёта роли schedule и публикации
// outbox роли worker, если вызывающий не задал свой.
const defaultInterval = time.Second

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// APIAddr — адрес внутреннего HTTP-сервера роли api.
	APIAddr string

	// Interval — интервал регулярного пересчёта (schedule) и проходов публикации
	// (worker).
	Interval time.Duration

	// Publisher — издатель брокера. nil означает, что публикация событий не
	// собирается: роль worker честно сообщает об этом, а не притворяется
	// публикующей.
	Publisher platformoutbox.Publisher

	// Persistence — адаптеры хранилища контекста. Нулевое значение означает
	// dev-сборку на in-memory адаптерах; при заданном DSN точка входа передаёт
	// сюда NewPostgresPersistence, и тогда состояние переживает рестарт.
	Persistence Persistence

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer
}

// Service — собранный сервис audience. Реализует composition.Service: честный
// раннер на каждую объявленную каталогом роль.
type Service struct {
	slice    *Slice
	relay    *platformoutbox.Relay
	apiAddr  string
	interval time.Duration
	logger   observ.Logger
	metrics  observ.Metrics
	tracer   observ.Tracer
}

// New собирает сервис из адаптеров хранилища. При заданном DSN точка входа
// передаёт PostgreSQL-адаптеры (Config.Persistence); без них сборка идёт на
// in-memory адаптерах (dev), о чём честно предупреждает. Публикация собирается
// только тогда, когда задан Publisher: без него роль worker не публикует события.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("audience: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("audience: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("audience: Tracer обязателен")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultInterval
	}

	// Хранилище контекста: PostgreSQL-адаптеры при заданном DSN, иначе in-memory
	// (dev). О dev-сборке честно сообщается: пустой DSN не должен выглядеть
	// готовой персистентностью.
	if !cfg.Persistence.configured() {
		cfg.Logger.Warn("audience: PostgreSQL не подключён (DSN пуст), сборка на in-memory адаптерах (только dev)", observ.Fields{}, "service", ServiceID)
	}

	slice, err := Build(Params{
		Segments:   cfg.Persistence.Segments,
		Snapshots:  cfg.Persistence.Snapshots,
		Journal:    cfg.Persistence.Journal,
		UnitOfWork: cfg.Persistence.UnitOfWork,
	})
	if err != nil {
		return nil, fmt.Errorf("audience: сборка среза: %w", err)
	}

	var relay *platformoutbox.Relay
	if cfg.Publisher != nil {
		relay = platformoutbox.NewRelay(slice.Journal, cfg.Publisher, interval)
	}

	return &Service{
		slice:    slice,
		relay:    relay,
		apiAddr:  cfg.APIAddr,
		interval: interval,
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

// Slice возвращает собранный срез сервиса. Нужен точке входа, чтобы связать
// опубликованные контракты контекста его реализацией (NewAudienceSnapshot,
// NewSegmentMembership).
func (s *Service) Slice() *Slice { return s.slice }

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

// runWorker публикует доменные события контекста из outbox в брокер.
//
// Если издатель не задан, публикация не собрана: роль честно сообщает об этом и
// не притворяется публикующей. Иначе worker запускает транспортный relay, который
// доставляет записи журнала по мере их появления.
func (s *Service) runWorker(ctx context.Context) error {
	if s.relay == nil {
		s.logger.Warn("worker: издатель не задан, публикация событий не собирается", observ.Fields{}, "service", ServiceID)
		<-ctx.Done()
		return nil
	}
	s.logger.Info("worker: публикация доменных событий запущена", observ.Fields{}, "service", ServiceID)
	if err := s.relay.Run(ctx); err != nil {
		return fmt.Errorf("worker: публикация доменных событий: %w", err)
	}
	return nil
}

// runSchedule регулярно пересчитывает сегментацию и фиксирует снимки изменившихся
// сегментов. Первый проход выполняется сразу, чтобы результат не ждал интервала.
func (s *Service) runSchedule(ctx context.Context) error {
	s.logger.Info("schedule: регулярный пересчёт запущен", observ.Fields{}, "service", ServiceID, "interval", s.interval.String())
	s.refreshOnce(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("schedule: останов", observ.Fields{}, "service", ServiceID)
			return nil
		case <-ticker.C:
			s.refreshOnce(ctx)
		}
	}
}

// scheduleActor — актор регулярного пересчёта: системная операция роли schedule,
// а не действие пользователя. Значение стабильно, чтобы происхождение событий
// пересчёта было различимо при аудите.
const scheduleActor domain.ActorID = "system:audience-schedule"

// refreshCommand собирает команду пересчёта для роли schedule.
func refreshCommand() command.RefreshAudience {
	return command.RefreshAudience{Actor: scheduleActor}
}

// refreshOnce выполняет один проход пересчёта и логирует результат. Ошибка
// прохода не останавливает роль: следующий тик повторит пересчёт.
func (s *Service) refreshOnce(ctx context.Context) {
	result, err := s.slice.RefreshAudience.Refresh(ctx, refreshCommand())
	if err != nil {
		s.logger.Error("schedule: пересчёт аудитории", err, observ.Fields{}, "service", ServiceID)
		return
	}
	s.logger.Info("schedule: проход пересчёта завершён", observ.Fields{}, "service", ServiceID,
		"segments", result.Segments,
		"memberships_changed", result.MembershipsChanged,
		"snapshots_fixed", result.SnapshotsFixed,
	)
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
