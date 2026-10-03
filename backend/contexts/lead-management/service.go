package leadmanagement

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	httpapi "lead-gen/backend/contexts/lead-management/internal/adapters/http"
	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/forum"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/mail"
	"lead-gen/contracts/integration-platform/search"
)

// ServiceID — идентификатор сервиса, равный идентификатору контекста из каталога.
const ServiceID = "lead-management"

// Config — параметры сборки сервиса, задаваемые точкой входа cmd/lead-gen.
type Config struct {
	// APIAddr — адрес внутреннего HTTP-сервера роли api.
	APIAddr string

	// Репозитории и хранилище заданий впрыскиваются composition root'ом: сервис
	// не решает, какие адаптеры (pgx или in-memory) использовать.
	Profiles  ports.LeadProfileRepository
	Contacts  ports.ContactRepository
	Jobs      ports.SearchJobStore
	Messages  ports.OutboundMessageRepository
	Templates ports.EmailTemplateRepository
	Activity  ports.LeadActivityStore
	// Assessments — хранилище оценок компании относительно задания. nil —
	// оценка отключена (дополнительный результат, а не условие поиска).
	Assessments ports.LeadAssessmentStore

	// Events — издатель нормализованных событий дашборда клиента (контракт
	// analytics-event): lead-workflow при смене стадии и message-sent при
	// отправке письма. nil — публикация не собрана, события дашборда не уходят.
	Events ports.AnalyticsPublisher

	// TenantID — тенант, в срезе которого учитываются события дашборда. Пусто —
	// событие уходит без тенанта и проекцией игнорируется.
	TenantID string

	// Наблюдаемость сервиса: логгер, метрики и трейсер впрыскиваются
	// composition root'ом; обязательны.
	Logger  observ.Logger
	Metrics observ.Metrics
	Tracer  observ.Tracer

	// Порты внешних движков (впрыскиваются из модуля integration-platform).
	Search search.Port
	Crawl  crawl.Port
	LLM    llm.Port   // nil — без LLM-планирования
	Forum  forum.Port // nil — форумный источник не настроен
	Mail   mail.Port  // SMTP-адаптер отправки писем

	// Budget — фабрика бюджета Firecrawl на задачу; nil — без бюджета.
	Budget budget.Factory
	// Account — читатель остатка аккаунта Firecrawl (кредиты и токены);
	// nil — остаток в консоли не показывается.
	Account budget.AccountReader

	// MailFrom — адрес отправителя писем (конфигурация SMTP).
	MailFrom string

	// ConsoleUser / ConsolePassword — учётные данные оператора консоли. Пустые
	// значения означают вход с пустым логином и паролем (только для локального
	// dev-прогона без внешнего доступа).
	ConsoleUser     string
	ConsolePassword string

	// Clock — часы; nil — системное время.
	Clock ports.Clock

	// Limits — пределы обхода.
	Limits app.CrawlLimits

	// LeadSearchMaxResults — верхняя граница результатов Firecrawl-поиска (топ-N
	// сайтов) в синхронном поиске под заявку (контракт lead-search). 0 — значение
	// по умолчанию.
	LeadSearchMaxResults int
	// LeadSearchDeadline — предельное время фазы краулинга синхронного поиска под
	// заявку. 0 — значение по умолчанию.
	LeadSearchDeadline time.Duration

	// DialogueMaxTurns — предел числа ходов диалога ассистента (0 — значение по
	// умолчанию). Предохранитель от бесконечных уточнений: бюджета на токены LLM нет.
	DialogueMaxTurns int

	// Interval — интервал опроса очереди заданий ролью worker.
	Interval time.Duration

	// JobLeaseTTL — срок аренды задания для согласования осиротевших заданий:
	// восстанавливаются только задания, активность по которым не обновлялась
	// дольше этого срока. 0 — значение по умолчанию.
	JobLeaseTTL time.Duration
	// JobMaxAttempts — предел попыток прерванного задания при согласовании:
	// при исчерпании задание завершается отказом с кодом прерывания.
	// 0 — значение по умолчанию.
	JobMaxAttempts int
}

// Service — собранный сервис lead-management. Реализует composition.Service:
// раннер на каждую объявленную роль (api, worker).
type Service struct {
	apiAddr string
	server  *httpapi.Server
	runner  *jobs.Runner
	logger  observ.Logger
	metrics observ.Metrics
	tracer  observ.Tracer
}

// New собирает сервис из впрыснутых зависимостей. Обязательные зависимости
// проверяются на этапе сборки, чтобы расхождение конфигурации было видно сразу,
// а не при первом запросе.
func New(cfg Config) (*Service, error) {
	if cfg.Profiles == nil {
		return nil, fmt.Errorf("lead-management: LeadProfileRepository обязателен")
	}
	if cfg.Contacts == nil {
		return nil, fmt.Errorf("lead-management: ContactRepository обязателен")
	}
	if cfg.Jobs == nil {
		return nil, fmt.Errorf("lead-management: SearchJobStore обязателен")
	}
	if cfg.Messages == nil {
		return nil, fmt.Errorf("lead-management: OutboundMessageRepository обязателен")
	}
	if cfg.Templates == nil {
		return nil, fmt.Errorf("lead-management: EmailTemplateRepository обязателен")
	}
	if cfg.Activity == nil {
		return nil, fmt.Errorf("lead-management: LeadActivityStore обязателен")
	}
	if cfg.Search == nil {
		return nil, fmt.Errorf("lead-management: Search обязателен")
	}
	if cfg.Crawl == nil {
		return nil, fmt.Errorf("lead-management: Crawl обязателен")
	}
	if cfg.Mail == nil {
		return nil, fmt.Errorf("lead-management: Mail обязателен")
	}
	if cfg.Logger == nil {
		return nil, fmt.Errorf("lead-management: Logger обязателен")
	}
	if cfg.Metrics == nil {
		return nil, fmt.Errorf("lead-management: Metrics обязателен")
	}
	if cfg.Tracer == nil {
		return nil, fmt.Errorf("lead-management: Tracer обязателен")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}

	// Один реестр отмены на процесс: роль api сигналит, роль worker слушает.
	cancelSignals := jobs.NewCancelRegistry()
	runSearch := &app.RunSearch{
		Jobs:        cfg.Jobs,
		Profiles:    cfg.Profiles,
		Contacts:    cfg.Contacts,
		Search:      cfg.Search,
		Crawl:       cfg.Crawl,
		LLM:         cfg.LLM,
		Budget:      cfg.Budget,
		Assessments: cfg.Assessments,
		Signals:     cancelSignals,
		Clock:       cfg.Clock,
		Limits:      cfg.Limits,
		Logger:      cfg.Logger,
		Metrics:     cfg.Metrics,
		Tracer:      cfg.Tracer,
		ServiceName: ServiceID,
	}
	showcase := &app.Showcase{
		Jobs:     cfg.Jobs,
		Profiles: cfg.Profiles,
		Contacts: cfg.Contacts,
		Messages: cfg.Messages,
	}
	changeStage := &app.ChangeLeadStage{
		Profiles: cfg.Profiles,
		Activity: cfg.Activity,
		Clock:    cfg.Clock,
		Events:   cfg.Events,
		TenantID: cfg.TenantID,
		Logger:   cfg.Logger,
	}
	snooze := &app.SnoozeLead{Profiles: cfg.Profiles, Clock: cfg.Clock}
	sendToLead := &app.SendToLead{
		Profiles:    cfg.Profiles,
		Contacts:    cfg.Contacts,
		Messages:    cfg.Messages,
		Templates:   cfg.Templates,
		Mail:        cfg.Mail,
		From:        cfg.MailFrom,
		Clock:       cfg.Clock,
		Events:      cfg.Events,
		TenantID:    cfg.TenantID,
		Logger:      cfg.Logger,
		Metrics:     cfg.Metrics,
		ServiceName: ServiceID,
	}
	sendToSegment := &app.SendToSegment{
		Profiles:    cfg.Profiles,
		Contacts:    cfg.Contacts,
		Messages:    cfg.Messages,
		Templates:   cfg.Templates,
		Mail:        cfg.Mail,
		From:        cfg.MailFrom,
		Clock:       cfg.Clock,
		Metrics:     cfg.Metrics,
		ServiceName: ServiceID,
	}
	templateLibrary := &app.Templates{
		Repository: cfg.Templates,
		Profiles:   cfg.Profiles,
		Contacts:   cfg.Contacts,
		Clock:      cfg.Clock,
	}
	suggestNiches := &app.SuggestNiches{LLM: cfg.LLM}
	dialogue := &app.ConverseDialogue{LLM: cfg.LLM, MaxTurns: cfg.DialogueMaxTurns, Logger: cfg.Logger}
	forumSignals := &app.SearchForumSignals{Forum: cfg.Forum}
	searchLeads := &app.SearchLeadsForRequest{
		Profiles:    cfg.Profiles,
		Contacts:    cfg.Contacts,
		Search:      cfg.Search,
		Crawl:       cfg.Crawl,
		Clock:       cfg.Clock,
		Limits:      cfg.Limits,
		MaxResults:  cfg.LeadSearchMaxResults,
		Deadline:    cfg.LeadSearchDeadline,
		Logger:      cfg.Logger,
		Tracer:      cfg.Tracer,
		ServiceName: ServiceID,
	}
	draftEmail := &app.DraftEmailForLead{
		Profiles: cfg.Profiles,
		Contacts: cfg.Contacts,
		LLM:      cfg.LLM,
	}
	server := httpapi.NewServer(httpapi.Dependencies{
		RunSearch:       runSearch,
		Showcase:        showcase,
		ChangeStage:     changeStage,
		Snooze:          snooze,
		SendToLead:      sendToLead,
		SendToSegment:   sendToSegment,
		Templates:       templateLibrary,
		SuggestNiches:   suggestNiches,
		Dialogue:        dialogue,
		ForumSignals:    forumSignals,
		SearchLeads:     searchLeads,
		EmailGeneration: draftEmail,
		Account:         cfg.Account,
		Logger:          cfg.Logger,
		Auth: httpapi.AuthConfig{
			Username: cfg.ConsoleUser,
			Password: cfg.ConsolePassword,
		},
	})
	runner := &jobs.Runner{
		Store:    cfg.Jobs,
		Handler:  runSearch.Handle,
		Interval: cfg.Interval,
		Signals:  cancelSignals,
		// Отклонённый переход — не сбой: задание уже терминально (например,
		// отменено раньше, чем обработчик успел завершиться).
		Rejected: func(jobID string, to jobs.Status) {
			cfg.Logger.Info("завершение отклонено: задание уже терминально", observ.Fields{TaskID: jobID}, "service", ServiceID, "to", string(to))
		},
		// Согласование осиротевших заданий: аренда не даёт отобрать задание у
		// живой копии, а лимит попыток завершает безнадёжно прерванное задание
		// отказом с отдельным кодом.
		LeaseTTL:    cfg.JobLeaseTTL,
		MaxAttempts: cfg.JobMaxAttempts,
		FailCode:    string(app.ErrorCodeInterrupted),
		FailReason:  app.ErrorCodeInterrupted.Message(),
	}

	return &Service{
		apiAddr: cfg.APIAddr,
		server:  server,
		runner:  runner,
		logger:  cfg.Logger,
		metrics: cfg.Metrics,
		tracer:  cfg.Tracer,
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

// runAPI поднимает внутренний HTTP-сервер контекста и останавливает его по
// отмене контекста.
func (s *Service) runAPI(ctx context.Context) error {
	handler := observ.Middleware(s.logger, s.metrics, s.tracer, ServiceID)(s.server)
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

// runWorker запускает раннер заданий поиска.
func (s *Service) runWorker(ctx context.Context) error {
	s.logger.Info("worker: раннер заданий запущен", observ.Fields{}, "service", ServiceID)
	// Согласование сирот идёт до первого взятия задания: в этом процессе живых
	// обработчиков ещё нет, поэтому оно не может отобрать задание у работающего.
	result, err := s.runner.Reconcile(ctx)
	if err != nil {
		return fmt.Errorf("worker: согласование осиротевших заданий: %w", err)
	}
	if result.Total() > 0 {
		s.logger.Info("worker: согласованы осиротевшие задания", observ.Fields{}, "service", ServiceID,
			"requeued", result.Requeued, "cancelled", result.Cancelled, "failed", result.Failed)
	}
	if err := s.runner.Run(ctx); err != nil {
		return fmt.Errorf("worker: раннер заданий: %w", err)
	}
	return nil
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*Service)(nil)
