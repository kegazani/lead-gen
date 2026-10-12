package analytics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	accessinmemory "lead-gen/backend/contexts/analytics/internal/adapters/access/inmemory"
	feedbackadapter "lead-gen/backend/contexts/analytics/internal/adapters/feedback"
	httpapi "lead-gen/backend/contexts/analytics/internal/adapters/http"
	persistenceinmemory "lead-gen/backend/contexts/analytics/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/analytics/internal/adapters/transport"
	"lead-gen/backend/contexts/analytics/internal/app/command"
	"lead-gen/backend/contexts/analytics/internal/app/query"
	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/messaging/inbox"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/readiness"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/contracts/lead-management/recommendationapply"
	"lead-gen/frontend/components"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из
// architecture/domain-catalog.yaml.
const ServiceID = "analytics"

// SystemClock — часы процесса, источник времени по умолчанию.
type SystemClock struct{}

// Now реализует ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// APIAddr — адрес HTTP-сервера роли api (дашборд клиента).
	APIAddr string

	// ReadModel — read-модель дашборда. nil → новая пустая проекция в памяти.
	ReadModel *readmodel.ReadModel

	// Access — решение о доступе. nil → локальная in-memory реализация смысла
	// контракта access-decision владельца platform-governance.
	Access ports.AccessDecider

	// ProjectionStore — хранилище эксплуатационной проекции дашборда. nil →
	// in-memory адаптер (dev): состояние витрины живёт в памяти процесса и
	// теряется при рестарте. PostgreSQL-адаптер задаётся при подключённой СУБД.
	ProjectionStore ports.ProjectionStore

	// Clock — источник времени прикладного слоя. nil → SystemClock.
	Clock ports.Clock

	// StaleDays — порог «не отвечают дольше N дней». 0 → значение по умолчанию.
	StaleDays int

	// ReportSource — источник агрегатов отчёта. nil → расчёт по read-модели
	// витрины (ModelReportSource). Тот же источник использует длительная выгрузка:
	// задание и прямой экспорт считают один и тот же срез.
	ReportSource query.ReportSource

	// ReportExports — хранилище заданий длительной выгрузки отчёта. nil →
	// in-memory адаптер (dev): задания и их результаты живут в памяти процесса.
	// PostgreSQL-адаптер задаётся при подключённой СУБД.
	ReportExports ports.ReportExportStore

	// InsightModel — проекция качества поиска (capability lead-quality-insights).
	// nil → новая пустая проекция в памяти.
	InsightModel *readmodel.InsightModel

	// InsightStore — хранилище снимков качества поиска и применений рекомендаций.
	// nil → in-memory адаптер (dev): снимки живут в памяти процесса и теряются при
	// рестарте. PostgreSQL-адаптер задаётся при подключённой СУБД.
	InsightStore ports.InsightStore

	// QualityFeedback — контур применения рекомендации в поиске (`lead-management`).
	// nil — контур не подключён: применение честно недоступно, а не выдумано.
	QualityFeedback ports.QualityFeedback

	// RecommendationApply — контракт владельца контура поиска
	// (recommendation-apply). Задан — Build собирает адаптер ports.QualityFeedback
	// поверх него; nil — контур не подключён (если не задан QualityFeedback) и
	// применение честно недоступно.
	RecommendationApply recommendationapply.Port

	// ExportInterval — интервал опроса очереди заданий выгрузки ролью worker.
	// 0 → значение по умолчанию раннера.
	ExportInterval time.Duration

	// ExportSignals — реестр отмены выполняющихся заданий выгрузки. nil — отмена
	// через сигнал недоступна; отмена по данным хранилища работает всё равно.
	ExportSignals *jobs.CancelRegistry

	// Consumer — потребитель очереди брокера, из которой воркер наполняет
	// проекцию доменными событиями. nil означает, что поток событий не подключён:
	// проекция наполняется только dev-приёмом, и роль worker честно об этом
	// сообщает.
	Consumer amqp.Consumer

	// Deduper — журнал обработанных event_id (доставка at-least-once). nil →
	// in-memory журнал: дедупликация в пределах процесса.
	Deduper inbox.Deduper

	// EventQueue — очередь нормализованных событий дашборда. Пусто → очередь по
	// умолчанию транспортного адаптера.
	EventQueue string

	// DeliveryQueue — очередь исходов доставки (контракт delivery-event владельца
	// message-delivery). Она питает витрину клиента реальным ходом цикла
	// (найдено → прокоммуницировано → …): без неё воркер читает только ручные
	// analytics-event. Пусто → очередь по умолчанию транспортного адаптера
	// (analytics.delivery-event). Подписка собирается только при заданном Consumer.
	DeliveryQueue string

	// Console — базовые адреса сервисов консоли для общей навигации по разделам
	// (Заявки, Исполнение, Лиды, Отчёты). Пустые адреса дают относительные пути.
	Console components.ConsoleBases

	// BasePath — внешний префикс пути сервера под единым origin (admin-proxy):
	// дашборд монтируется под /reports. Пусто — корень origin.
	BasePath string

	// ExternalPrefixes — префиксы других консолей: ссылки на них в навигации не
	// переписываются под собственный префикс.
	ExternalPrefixes []string

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger            observ.Logger
	Metrics           observ.Metrics
	Tracer            observ.Tracer
	FailureNormalizer jobs.FailureNormalizer
}

// Slice — собранный контекст среза: проекция, порты, адаптеры и use cases.
type Slice struct {
	ReadModel *readmodel.ReadModel
	Access    ports.AccessDecider

	// Server — HTTP-обработчики роли api. Реализует http.Handler.
	Server *httpapi.Server

	Dashboard *query.DashboardHandler
	Reports   *query.ReportHandler
	Events    *command.ApplyEventHandler

	// Insight — чтение и применение рекомендаций качества поиска.
	Insight             *readmodel.InsightModel
	Insights            *query.InsightHandler
	Applications        *query.ApplicationsHandler
	ApplyOutcome        *command.ApplyOutcomeHandler
	ApplyRecommendation *command.ApplyRecommendationHandler

	// Operations — операционные агрегаты процесса (контракт campaign-report).
	Operations *query.OperationsHandler

	// ExportReport и ExportJobs — постановка и чтение длительной выгрузки отчёта;
	// ExportStore — хранилище заданий, которым пользуется раннер роли worker;
	// Jobs — чтение DLQ и повтор упавших заданий.
	ExportReport    *command.ExportReport
	ExportJobs      *query.ExportJobHandler
	ExportStore     ports.ReportExportStore
	Jobs            *query.JobsHandler
	ProjectionStore ports.ProjectionStore
}

// Build собирает контекст среза из адаптеров и переданных зависимостей.
// Бизнес-правил здесь нет — соединение адаптеров, портов и use cases только
// своего контекста.
func Build(cfg Config) (*Slice, error) {
	model := cfg.ReadModel
	if model == nil {
		model = readmodel.New()
	}
	clock := cfg.Clock
	if clock == nil {
		clock = SystemClock{}
	}
	access := cfg.Access
	if access == nil {
		access = accessinmemory.New()
	}

	// Хранилище проекции: PostgreSQL при подключённой СУБД либо in-memory dev.
	// При наличии хранилища проекция восстанавливается из него до старта: витрина
	// переживает рестарт процесса. Без хранилища состояние живёт только в памяти,
	// и роль честно предупреждает об этом.
	store := cfg.ProjectionStore
	if store == nil {
		store = persistenceinmemory.New()
		if cfg.Logger != nil {
			cfg.Logger.Warn("проекция дашборда в памяти процесса (dev): PostgreSQL не подключён, состояние не переживёт рестарт", observ.Fields{}, "service", ServiceID)
		}
	} else {
		records, err := store.LoadCompanies(context.Background())
		if err != nil {
			return nil, fmt.Errorf("analytics: загрузка проекции из хранилища: %w", err)
		}
		for _, record := range records {
			model.Restore(record)
		}
	}

	dashboard := query.NewDashboardHandler(model, clock, cfg.StaleDays)
	reportSource := cfg.ReportSource
	if reportSource == nil {
		reportSource = query.ModelReportSource{Model: model}
	}
	reports := query.NewReportHandler(reportSource, clock)

	// Проекция качества поиска: снимок восстанавливается из хранилища до старта,
	// поэтому срез воспроизводим по watermark и версии формул после рестарта.
	insightModel := cfg.InsightModel
	if insightModel == nil {
		insightModel = readmodel.NewInsightModel()
	}
	insightStore := cfg.InsightStore
	if insightStore == nil {
		insightStore = persistenceinmemory.NewInsightStore()
		if cfg.Logger != nil {
			cfg.Logger.Warn("снимки качества поиска в памяти процесса (dev): PostgreSQL не подключён, состояние не переживёт рестарт", observ.Fields{}, "service", ServiceID)
		}
	} else {
		snapshots, err := insightStore.LoadSnapshots(context.Background())
		if err != nil {
			return nil, fmt.Errorf("analytics: загрузка снимков качества поиска: %w", err)
		}
		for _, snapshot := range snapshots {
			insightModel.RestoreSnapshot(snapshot)
		}
	}
	outcomes := command.NewApplyOutcomeHandler(insightModel, insightStore)
	insights := query.NewInsightHandler(query.ModelInsightSource{Model: insightModel}, clock)
	applications := query.NewApplicationsHandler(insightStore, query.ModelInsightSource{Model: insightModel})
	// Контур обратной связи: заданный контракт recommendation-apply оборачивается
	// адаптером ports.QualityFeedback. Без контракта (и без готового порта)
	// применение рекомендации честно недоступно — не выдумывается.
	feedback := cfg.QualityFeedback
	if feedback == nil && cfg.RecommendationApply != nil {
		feedback = feedbackadapter.New(cfg.RecommendationApply)
	}
	applyRecommendation := command.NewApplyRecommendationHandler(insightModel, feedback, insightStore, clock)

	// Агрегаты процесса для экрана операций (контракт campaign-report): сводные
	// цифры по всем клиентам и разбивка по времени из read-моделей владельца.
	operations := query.NewOperationsHandler(model, insightModel, clock, readmodel.MinGroup)

	events := command.NewApplyEventHandler(model, store, outcomes)

	// Хранилище заданий выгрузки: PostgreSQL при подключённой СУБД либо in-memory
	// dev. Второго владельца модели здесь нет: хранилище только держит состояние
	// задания и готовый файл.
	exportStore := cfg.ReportExports
	if exportStore == nil {
		exportStore = persistenceinmemory.NewReportExportStore()
		if cfg.Logger != nil {
			cfg.Logger.Warn("задания выгрузки отчёта в памяти процесса (dev): PostgreSQL не подключён, результаты не переживут рестарт", observ.Fields{}, "service", ServiceID)
		}
	}
	exportReport := command.NewExportReport(exportStore, reportSource, clock, cfg.Logger, cfg.Metrics, cfg.Tracer, ServiceID)
	exportJobs := query.NewExportJobHandler(exportStore)
	jobsHandler := query.NewJobsHandler(exportStore)

	server := httpapi.NewServer(httpapi.Dependencies{
		Dashboard:           dashboard,
		Reports:             reports,
		Events:              events,
		Exports:             exportReport,
		ExportJobs:          exportJobs,
		Jobs:                jobsHandler,
		Insights:            insights,
		Applications:        applications,
		ApplyRecommendation: applyRecommendation,
		Operations:          operations,
		Access:              access,
		Console:             cfg.Console,
		BasePath:            cfg.BasePath,
		ExternalPrefixes:    cfg.ExternalPrefixes,
		Logger:              cfg.Logger,
	})

	return &Slice{
		ReadModel:           model,
		ProjectionStore:     store,
		Access:              access,
		Server:              server,
		Dashboard:           dashboard,
		Reports:             reports,
		Events:              events,
		Insight:             insightModel,
		Insights:            insights,
		Applications:        applications,
		ApplyOutcome:        outcomes,
		ApplyRecommendation: applyRecommendation,
		Operations:          operations,
		ExportReport:        exportReport,
		ExportJobs:          exportJobs,
		ExportStore:         exportStore,
		Jobs:                jobsHandler,
	}, nil
}

// Service — собранный сервис analytics. Реализует composition.Service: раннер на
// каждую объявленную роль.
type Service struct {
	slice              *Slice
	subscriber         *transport.Subscriber
	deliverySubscriber *transport.Subscriber
	exportRunner       *jobs.Runner
	apiAddr            string
	logger             observ.Logger
	metrics            observ.Metrics
	tracer             observ.Tracer
	readiness          *readiness.Evaluator
	analyticsLoop      *workerSignal
	deliveryLoop       *workerSignal
	exportLoop         *workerSignal
}

// New собирает сервис из адаптеров и переданных зависимостей.
func New(cfg Config) (*Service, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("analytics: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("analytics: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("analytics: Tracer обязателен")
	}

	slice, err := Build(cfg)
	if err != nil {
		return nil, err
	}

	// Подписки на потоки событий собираются только тогда, когда задан потребитель
	// брокера: без него воркер не притворяется работающим, а честно сообщает об
	// отсутствии источника. Подписок две: нормализованные события дашборда
	// (analytics-event) и исходы доставки цикла (delivery-event); дедупликация и
	// повторная доставка у них общие.
	var (
		subscriber         *transport.Subscriber
		deliverySubscriber *transport.Subscriber
	)
	if cfg.Consumer != nil {
		deduper := cfg.Deduper
		if deduper == nil {
			deduper = inbox.NewMemoryDeduper()
		}
		subscriber = transport.NewSubscriber(cfg.Consumer, deduper, cfg.EventQueue)
		deliverySubscriber = transport.NewDeliveryEventSubscriber(cfg.Consumer, deduper, cfg.DeliveryQueue)
	}

	// Раннер заданий выгрузки — тот же примитив jobs, что и у прочих сервисов:
	// роль worker берёт queued-задание из очереди и доводит его до терминального
	// состояния, а готовый файл отдаётся по ссылке на результат.
	exportRunner := &jobs.Runner{
		Store:             slice.ExportStore,
		Handler:           slice.ExportReport.Handle,
		Interval:          cfg.ExportInterval,
		Signals:           cfg.ExportSignals,
		Logger:            cfg.Logger,
		FailureNormalizer: cfg.FailureNormalizer,
		// Предел попыток согласован с отбором DLQ: прерванное и исчерпавшее попытки
		// задание завершается отказом и становится видно администратору.
		MaxAttempts: ports.ExportJobMaxAttempts,
		// Код и формулировка отказа для прерванного перезапуском задания.
		FailCode:   string(command.ErrorCodeInterrupted),
		FailReason: command.ErrorCodeInterrupted.Message(),
		Rejected: func(jobID string, to jobs.Status) {
			cfg.Logger.Info("завершение выгрузки отклонено: задание уже терминально", observ.Fields{TaskID: jobID}, "service", ServiceID, "to", string(to))
		},
	}

	return &Service{
		slice:              slice,
		subscriber:         subscriber,
		deliverySubscriber: deliverySubscriber,
		exportRunner:       exportRunner,
		apiAddr:            cfg.APIAddr,
		logger:             cfg.Logger,
		metrics:            cfg.Metrics,
		tracer:             cfg.Tracer,
		readiness:          readiness.New(readiness.Config{Timeout: 250 * time.Millisecond, TTL: time.Second}),
		analyticsLoop:      &workerSignal{},
		deliveryLoop:       &workerSignal{},
		exportLoop:         &workerSignal{},
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
	default:
		return nil, false
	}
}

// runAPI поднимает HTTP-сервер дашборда и останавливает его по отмене контекста.
func (s *Service) runAPI(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		writeReadiness(w, s.Readiness(r.Context(), composition.RoleAPI))
	})
	mux.Handle("/", s.slice.Server)
	handler := observ.Middleware(s.logger, s.metrics, s.tracer, ServiceID)(mux)
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

	s.logger.Info("api: HTTP-сервер дашборда запущен", observ.Fields{}, "service", ServiceID, "addr", s.apiAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("api: работа HTTP-сервера: %w", err)
	}
	return nil
}

// runWorker выполняет фоновую работу контекста: читает очереди брокера и
// наполняет проекцию дашборда доменными событиями (нормализованные события и
// исходы доставки цикла), а также обрабатывает очередь заданий длительной выгрузки
// отчёта. Без подключённого потребителя воркер честно сообщает, что поток событий
// не подключён, и не делает вид, что обрабатывает сообщения: проекция наполняется
// только dev-приёмом. Очередь выгрузки работает независимо от источника событий.
func (s *Service) runWorker(ctx context.Context) error {
	tasks := make([]func(context.Context) error, 0, 3)
	if s.subscriber == nil && s.deliverySubscriber == nil {
		s.logger.Warn("worker: поток событий не подключён к брокеру; проекция наполняется только dev-приёмом", observ.Fields{}, "service", ServiceID)
	} else {
		if s.subscriber != nil {
			s.logger.Info("worker: подписка на поток событий дашборда запущена", observ.Fields{}, "service", ServiceID, "queue", transport.DefaultQueue)
			analyticsSubscriber := s.subscriber
			tasks = append(tasks, func(ctx context.Context) error {
				return runTracked(ctx, s.analyticsLoop, func(ctx context.Context) error {
					return analyticsSubscriber.Run(ctx, func(ctx context.Context, event command.ApplyEvent) error {
						return s.slice.Events.Handle(ctx, event)
					})
				})
			})
		}
		if s.deliverySubscriber != nil {
			s.logger.Info("worker: подписка на исходы доставки запущена", observ.Fields{}, "service", ServiceID, "queue", transport.DefaultDeliveryQueue)
			deliverySubscriber := s.deliverySubscriber
			tasks = append(tasks, func(ctx context.Context) error {
				return runTracked(ctx, s.deliveryLoop, func(ctx context.Context) error {
					return deliverySubscriber.Run(ctx, func(ctx context.Context, event command.ApplyEvent) error {
						return s.slice.Events.Handle(ctx, event)
					})
				})
			})
		}
	}
	tasks = append(tasks, s.runExportJobs)
	return runConcurrently(ctx, tasks)
}

// runExportJobs согласует осиротевшие задания выгрузки и запускает раннер. Строки
// и отмена распознаются примитивом jobs: согласование идёт до первого взятия
// задания, поэтому в этом процессе нет живых обработчиков, у которых задание
// можно было бы отобрать.
func (s *Service) runExportJobs(ctx context.Context) error {
	s.logger.Info("worker: раннер заданий выгрузки отчёта запущен", observ.Fields{}, "service", ServiceID)
	result, err := s.exportRunner.Reconcile(ctx)
	if err != nil {
		return fmt.Errorf("worker: согласование осиротевших заданий выгрузки: %w", err)
	}
	if result.Total() > 0 {
		s.logger.Info("worker: согласованы осиротевшие задания выгрузки", observ.Fields{}, "service", ServiceID,
			"requeued", result.Requeued, "cancelled", result.Cancelled, "failed", result.Failed)
	}
	if err := runTracked(ctx, s.exportLoop, s.exportRunner.Run); err != nil {
		return fmt.Errorf("worker: раннер заданий выгрузки: %w", err)
	}
	return nil
}

// runConcurrently выполняет фоновые задачи одной роли до первой ошибки или
// отмены контекста. Ошибка одной задачи отменяет остальные и возвращается
// вызывающему; штатное завершение по отмене ошибкой не считается.
func runConcurrently(ctx context.Context, tasks []func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg    sync.WaitGroup
		errCh = make(chan error, len(tasks))
	)
	for _, task := range tasks {
		wg.Add(1)
		go func(task func(context.Context) error) {
			defer wg.Done()
			if err := task(ctx); err != nil {
				errCh <- err
				cancel()
			}
		}(task)
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case err := <-errCh:
		return err
	case <-done:
		select {
		case err := <-errCh:
			return err
		default:
			return nil
		}
	}
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
