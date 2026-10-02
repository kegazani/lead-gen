package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	analytics "lead-gen/backend/contexts/analytics"
	integrationplatform "lead-gen/backend/contexts/integration-platform"
	leadmanagement "lead-gen/backend/contexts/lead-management"
	outreachorchestration "lead-gen/backend/contexts/outreach-orchestration"
	platformgovernance "lead-gen/backend/contexts/platform-governance"
	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/migrate"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	platformworkflow "lead-gen/backend/platform/workflow"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/request-fulfillment/requestintent"
	"lead-gen/contracts/request-fulfillment/requestintent/httpport"
)

// buildOptions — параметры, общие для сборки сервисов в одном процессе.
type buildOptions struct {
	apiAddr          string
	requestIntentURL string
	publishInterval  time.Duration
	actor            string
	tenant           string

	// Брокер в памяти: реализует и Publisher, и Consumer, поэтому в совмещённом
	// деплое публикация одного сервиса доходит до подписки другого.
	consumer amqp.Consumer

	// Наблюдаемость процесса: логгер и метрики общие для сервисов, трейсер
	// строится на сервис (service.name).
	logger  observ.Logger
	metrics observ.Metrics

	// Конфигурация сервиса lead-management.
	leadGreenDSN        string // DSN зелёной зоны; пусто — in-memory адаптеры
	leadRedDSN          string // DSN красной зоны; пусто — DSN зелёной зоны
	firecrawlKey        string // ключ Firecrawl; обязателен для поиска и обхода
	firecrawlURL        string // базовый адрес Firecrawl; пусто — по умолчанию
	llmBaseURL          string // базовый адрес LLM (DeepSeek); пусто — по умолчанию
	llmModel            string // модель LLM; пусто — по умолчанию
	llmKey              string // ключ LLM; пусто — LLM-планирование отключено
	consoleUser         string // логин оператора консоли
	consolePassword     string // пароль оператора консоли
	leadMaxPages        int    // верхний предел страниц обхода на сайт; 0 — без предела
	leadMaxDepth        int    // максимальная глубина обхода
	leadMaxTimeSeconds  int    // предельное время обхода, с
	dialogueMaxTurns    int    // предел числа ходов диалога ассистента
	firecrawlMaxCredits int    // лимит кредитов Firecrawl на задачу; 0 — без бюджета
	smtpHost            string // адрес SMTP-сервера отправки писем
	smtpPort            int    // порт SMTP-сервера
	smtpUsername        string // имя учётной записи SMTP
	smtpPassword        string // пароль учётной записи SMTP
	smtpFrom            string // адрес отправителя писем

	// Рантайм исполнения заявок (Temporal). workflowClient равен nil, если
	// рантайм отключён или недоступен: воркер workflow не поднимается, а
	// заявка остаётся в текущем статусе.
	workflowClient    *platformworkflow.Client
	workflowTaskQueue string
	searchStubLeads   int // заглушка шага поиска; 0 — поиск не подключён
}

// buildService собирает сервис по его идентификатору и механизму группы
// процессов. Реализованные сервисы берут свою сборку из contexts/<context-id>;
// для остальных возвращается честная заглушка, не притворяющаяся работающим
// контекстом.
//
// Механизм группы задаёт способ связи синхронных контрактов сервиса с
// потребителями: `in-process` — co-location в одном процессе образа (выбор по
// топологии, design.md, Решение 2).
func buildService(
	id string,
	mechanism string,
	opts buildOptions,
	broker amqp.Publisher,
	stub *publicIngestStub,
) (composition.Service, error) {
	tracer := observ.NewTracer(id)
	switch id {
	case analytics.ServiceID:
		return analytics.New(analytics.Config{
			APIAddr:  opts.apiAddr,
			Consumer: opts.consumer,
			Logger:   opts.logger,
			Metrics:  opts.metrics,
			Tracer:   tracer,
		})
	case outreachorchestration.ServiceID:
		return outreachorchestration.New(outreachorchestration.Config{
			APIAddr:   opts.apiAddr,
			Interval:  opts.publishInterval,
			Grants:    outreachorchestration.GrantsFromStrings(opts.actor, opts.tenant),
			Risk:      outreachorchestration.NoUpstreamRiskChecks,
			Publisher: broker,
			Transport: mechanism,
			Logger:    opts.logger,
			Metrics:   opts.metrics,
			Tracer:    tracer,
		})
	case leadmanagement.ServiceID:
		return buildLeadManagement(opts, tracer, broker)
	case requestfulfillment.ServiceID:
		return requestfulfillment.New(requestfulfillment.Config{
			APIAddr:           opts.apiAddr,
			Interval:          opts.publishInterval,
			Publisher:         broker,
			WorkflowClient:    opts.workflowClient,
			WorkflowTaskQueue: opts.workflowTaskQueue,
			SearchLeads:       opts.searchStubLeads,
			Logger:            opts.logger,
			Metrics:           opts.metrics,
			Tracer:            tracer,
		})
	case platformgovernance.ServiceID:
		return buildPlatformGovernance(opts, tracer)
	case integrationplatform.ServiceID:
		return integrationplatform.New(integrationplatform.Config{
			Logger:  opts.logger,
			Metrics: opts.metrics,
			Tracer:  tracer,
		})
	default:
		return &stubService{id: id, ingest: stub}, nil
	}
}

// buildPlatformGovernance собирает сервис platform-governance. Портал вызывает
// создание заявки не напрямую, а через опубликованный контракт request-intent:
// in-proc адаптер при co-location, сетевой HTTP-адаптер при раздельном запуске.
func buildPlatformGovernance(opts buildOptions, tracer observ.Tracer) (composition.Service, error) {
	requests, err := requestIntentPort(opts)
	if err != nil {
		return nil, fmt.Errorf("platform-governance: порт заявок: %w", err)
	}
	return platformgovernance.New(platformgovernance.Config{
		APIAddr:  opts.apiAddr,
		Requests: requests,
		Logger:   opts.logger,
		Metrics:  opts.metrics,
		Tracer:   tracer,
	})
}

// requestIntentPort выбирает адаптер контракта request-intent по топологии:
// задан адрес сервиса заявок — сетевой HTTP-адаптер; иначе in-proc (co-location).
func requestIntentPort(opts buildOptions) (requestintent.Port, error) {
	if addr := strings.TrimSpace(opts.requestIntentURL); addr != "" {
		return httpport.New(addr)
	}
	requests, err := requestfulfillment.Build(requestfulfillment.Config{})
	if err != nil {
		return nil, fmt.Errorf("срез заявок: %w", err)
	}
	return requestfulfillment.NewRequestIntent(requests), nil
}

// buildLeadManagement собирает сервис lead-management и впрыскивает адаптеры.
// Репозитории берутся из PostgreSQL по DSN зон либо из памяти (dev); поиск и
// обход обслуживает Firecrawl, планирование — LLM (DeepSeek), если задан ключ.
// broker — издатель брокера: поверх него собирается публикация событий дашборда
// (lead-workflow, message-sent) в контракте analytics-event.
func buildLeadManagement(opts buildOptions, tracer observ.Tracer, broker amqp.Publisher) (composition.Service, error) {
	if strings.TrimSpace(opts.firecrawlKey) == "" {
		return nil, errors.New("lead-management: firecrawl-key обязателен для поиска и обхода")
	}

	firecrawlClient, err := integrationplatform.NewFirecrawl(integrationplatform.FirecrawlConfig{
		APIKey:  opts.firecrawlKey,
		BaseURL: opts.firecrawlURL,
		Logger:  opts.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("lead-management: Firecrawl: %w", err)
	}

	// Бюджетная обёртка Firecrawl строится всегда при заданном ключе: она нужна
	// не только для кредитного лимита задания, но и для измерения фактического
	// расхода. Задание без явного бюджета (maxCredits == 0) обрабатывается самой
	// обёрткой как безлимитное: лимит не применяется, расход фиксируется.
	guard, err := integrationplatform.NewFirecrawlGuard(integrationplatform.FirecrawlGuardConfig{
		APIKey:  opts.firecrawlKey,
		BaseURL: opts.firecrawlURL,
		Logger:  opts.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("lead-management: Firecrawl guard: %w", err)
	}
	firecrawlBudget := budget.Factory(guard)
	firecrawlAccount := budget.AccountReader(guard)

	// Форумный источник по умолчанию — тот же поисковый движок, ограниченный
	// доменами форумов; отдельного ключа не требует.
	forumClient := integrationplatform.NewForum(integrationplatform.ForumConfig{Search: firecrawlClient})

	var repos leadmanagement.Repositories
	if opts.leadGreenDSN == "" {
		// Dev без внешней СУБД: состояние живёт в памяти процесса.
		repos = leadmanagement.NewMemoryRepositories()
	} else {
		greenPool, err := newLeadPool(opts.leadGreenDSN, "green")
		if err != nil {
			return nil, err
		}
		if opts.leadRedDSN == "" {
			greenPool.Close()
			return nil, errors.New("lead-management: lead-red-dsn обязателен, если задан lead-green-dsn: красная зона не должна сливаться с зелёной")
		}
		redPool, err := newLeadPool(opts.leadRedDSN, "red")
		if err != nil {
			greenPool.Close()
			return nil, err
		}

		if err := applyMigrations(greenPool, "backend/contexts/lead-management/migrations/green"); err != nil {
			greenPool.Close()
			redPool.Close()
			return nil, err
		}
		if err := applyMigrations(redPool, "backend/contexts/lead-management/migrations/red"); err != nil {
			greenPool.Close()
			redPool.Close()
			return nil, err
		}

		// Пулы живут столько же, сколько процесс: сервис не владеет их закрытием
		// (нет shutdown-хука в composition.Service), поэтому закрываются на выходе.
		repos = leadmanagement.NewPostgresRepositories(greenPool, redPool)
	}

	var llmPort llm.Port
	if opts.llmKey != "" {
		llmPort = integrationplatform.NewOpenAI(integrationplatform.OpenAIConfig{
			BaseURL: opts.llmBaseURL,
			Model:   opts.llmModel,
			APIKey:  opts.llmKey,
		})
	}

	smtpHost := opts.smtpHost
	if strings.TrimSpace(smtpHost) == "" {
		smtpHost = "localhost"
	}
	smtpClient, err := integrationplatform.NewSmtp(integrationplatform.SmtpConfig{
		Host:     smtpHost,
		Port:     opts.smtpPort,
		Username: opts.smtpUsername,
		Password: opts.smtpPassword,
	})
	if err != nil {
		return nil, fmt.Errorf("lead-management: SMTP: %w", err)
	}

	// Консоль с пустыми учётными данными — dev-режим без внешнего доступа. О
	// нём честно сообщается, чтобы пустые дефолты флагов не выглядели тихой
	// подменой секретов.
	if opts.consoleUser == "" || opts.consolePassword == "" {
		opts.logger.Warn("lead-management: консоль работает с пустыми учётными данными (только dev, без внешнего доступа)", observ.Fields{})
	}

	return leadmanagement.New(leadmanagement.Config{
		APIAddr:          opts.apiAddr,
		Profiles:         repos.Profiles,
		Contacts:         repos.Contacts,
		Jobs:             repos.Jobs,
		Messages:         repos.Messages,
		Templates:        repos.Templates,
		Activity:         repos.Activity,
		Assessments:      repos.Assessments,
		Events:           leadmanagement.NewAnalyticsPublisher(broker, opts.tenant),
		TenantID:         opts.tenant,
		Search:           firecrawlClient,
		Crawl:            firecrawlClient,
		Budget:           firecrawlBudget,
		Account:          firecrawlAccount,
		LLM:              llmPort,
		DialogueMaxTurns: opts.dialogueMaxTurns,
		Forum:            forumClient,
		Mail:             smtpClient,
		MailFrom:         opts.smtpFrom,
		ConsoleUser:      opts.consoleUser,
		ConsolePassword:  opts.consolePassword,
		Logger:           opts.logger,
		Metrics:          opts.metrics,
		Tracer:           tracer,
		Limits: leadmanagement.CrawlLimits{
			MaxDepth:       opts.leadMaxDepth,
			MaxTimeSeconds: opts.leadMaxTimeSeconds,
			MaxPages:       opts.leadMaxPages,
			IncludePaths:   []string{"contact", "team", "about", "staff", "impressum"},
		},
		Interval: time.Second,
	})
}

// newLeadPool собирает пул соединений PostgreSQL для зоны данных.
func newLeadPool(dsn, zone string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, fmt.Errorf("lead-management: пул зоны %s: %w", zone, err)
	}
	return pool, nil
}

// applyMigrations загружает миграции схемы сервиса из каталога и применяет их к
// пулу. Принадлежность схеме проверяет migrate.Load до выполнения.
func applyMigrations(pool *pgxpool.Pool, dir string) error {
	migrations, err := migrate.Load(dir, migrate.OwnSchema(leadmanagement.ServiceID))
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if _, err := pool.Exec(context.Background(), m.SQL); err != nil {
			return fmt.Errorf("lead-management: миграция %s: %w", m.Name, err)
		}
	}
	return nil
}

// stubService — сервис, для которого composition-пакет ещё не написан. Роль не
// выдумывает поведения: там, где реализации нет, она честно сообщает об этом и
// не делает вид, что обработала сообщение.
type stubService struct {
	id     string
	ingest *publicIngestStub
}

// ID реализует composition.Service.
func (s *stubService) ID() string { return s.id }

// RoleRunner реализует composition.Service. Единственная роль, которую честно
// поднять без контекста, — публичный приём: его заглушка отвечает 404, потому
// что обработчиков ещё нет. Остальные роли не зарегистрированы.
func (s *stubService) RoleRunner(role composition.Role) (composition.RoleRunner, bool) {
	if role == composition.RoleIngest {
		return composition.RoleFunc(s.ingest.run), true
	}
	return nil, false
}

// publicIngestStub — публичный HTTP-сервер-заглушка роли ingest. Поднимается
// один на процесс, сколько бы сервисов ни объявляли роль ingest: без
// зарегистрированных обработчиков все пути отвечают 404.
type publicIngestStub struct {
	addr   string
	logger observ.Logger
	once   sync.Once
}

func newPublicIngestStub(addr string, logger observ.Logger) *publicIngestStub {
	return &publicIngestStub{addr: addr, logger: logger}
}

// run поднимает сервер при первом вызове и блокируется до отмены контекста.
// Повторные вызовы (от других сервисов) только ждут отмены: второй сервер на
// том же адресе не поднимается.
func (p *publicIngestStub) run(ctx context.Context) error {
	p.once.Do(func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "публичный контур: обработчики приёма не зарегистрированы", http.StatusNotFound)
		})
		server := &http.Server{
			Addr:              p.addr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			p.logger.Info("ingest: публичный HTTP-сервер запущен (заглушка 404)", observ.Fields{}, "addr", p.addr)
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				p.logger.Error("ingest: работа HTTP-сервера", err, observ.Fields{})
			}
		}()
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = server.Shutdown(shutdownCtx)
		}()
	})
	<-ctx.Done()
	return nil
}

// Проверка соответствия контракту сервисной сборки на этапе компиляции.
var _ composition.Service = (*stubService)(nil)
