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
	audience "lead-gen/backend/contexts/audience"
	billing "lead-gen/backend/contexts/billing"
	communityintelligence "lead-gen/backend/contexts/community-intelligence"
	contentproduction "lead-gen/backend/contexts/content-production"
	datagovernance "lead-gen/backend/contexts/data-governance"
	engagementobservation "lead-gen/backend/contexts/engagement-observation"
	integrationplatform "lead-gen/backend/contexts/integration-platform"
	leadmanagement "lead-gen/backend/contexts/lead-management"
	messagedelivery "lead-gen/backend/contexts/message-delivery"
	outreachorchestration "lead-gen/backend/contexts/outreach-orchestration"
	platformgovernance "lead-gen/backend/contexts/platform-governance"
	publishing "lead-gen/backend/contexts/publishing"
	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	salesworkflow "lead-gen/backend/contexts/sales-workflow"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/migrate"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	platformworkflow "lead-gen/backend/platform/workflow"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/lead-management/emailgeneration"
	emailgenerationhttp "lead-gen/contracts/lead-management/emailgeneration/httpport"
	"lead-gen/contracts/lead-management/leadsearch"
	leadsearchhttp "lead-gen/contracts/lead-management/leadsearch/httpport"
	"lead-gen/contracts/request-fulfillment/requestintent"
	"lead-gen/contracts/request-fulfillment/requestintent/httpport"
	"lead-gen/frontend/components"
)

// buildOptions — параметры, общие для сборки сервисов в одном процессе.
type buildOptions struct {
	apiAddr            string
	ingestAddr         string
	requestIntentURL   string
	leadSearchURL      string
	emailGenerationURL string
	publishInterval    time.Duration
	actor              string
	tenant             string

	// Брокер в памяти: реализует и Publisher, и Consumer, поэтому в совмещённом
	// деплое публикация одного сервиса доходит до подписки другого.
	consumer amqp.Consumer

	// Наблюдаемость процесса: логгер и метрики общие для сервисов, трейсер
	// строится на сервис (service.name).
	logger  observ.Logger
	metrics observ.Metrics

	// Конфигурация сервиса lead-management.
	leadGreenDSN string // DSN зелёной зоны; пусто — in-memory адаптеры
	leadRedDSN   string // DSN красной зоны; пусто — DSN зелёной зоны

	// Конфигурация хранилища сервиса request-fulfillment.
	requestGreenDSN     string                  // DSN зелёной зоны; пусто — in-memory адаптеры
	firecrawlKey        string                  // ключ Firecrawl; обязателен для поиска и обхода
	firecrawlURL        string                  // базовый адрес Firecrawl; пусто — по умолчанию
	llmBaseURL          string                  // базовый адрес LLM (DeepSeek); пусто — по умолчанию
	llmModel            string                  // модель LLM; пусто — по умолчанию
	llmKey              string                  // ключ LLM; пусто — LLM-планирование отключено
	consoleUser         string                  // логин оператора консоли
	consolePassword     string                  // пароль оператора консоли
	console             components.ConsoleBases // базовые адреса сервисов консоли
	leadMaxPages        int                     // верхний предел страниц обхода на сайт; 0 — без предела
	leadMaxDepth        int                     // максимальная глубина обхода
	leadMaxTimeSeconds  int                     // предельное время обхода, с
	dialogueMaxTurns    int                     // предел числа ходов диалога ассистента
	firecrawlMaxCredits int                     // лимит кредитов Firecrawl на задачу; 0 — без бюджета
	smtpHost            string                  // адрес SMTP-сервера отправки писем
	smtpPort            int                     // порт SMTP-сервера
	smtpUsername        string                  // имя учётной записи SMTP
	smtpPassword        string                  // пароль учётной записи SMTP
	smtpFrom            string                  // адрес отправителя писем

	// Рантайм исполнения заявок (Temporal). workflowClient равен nil, если
	// рантайм отключён или недоступен: воркер workflow не поднимается, а
	// заявка остаётся в текущем статусе.
	workflowClient    *platformworkflow.Client
	workflowTaskQueue string
	searchStubLeads   int // заглушка шага поиска; 0 — поиск не подключён

	// runtime — признак доступности рантайма для пробы готовности /readyz.
	runtime *platformworkflow.Availability

	// Политики исполнения заявки: срок паузы утверждения, предельное время
	// исполнения, таймауты и heartbeat длинных activities.
	approvalTTL        time.Duration
	executionTimeout   time.Duration
	transitionTimeout  time.Duration
	transitionAttempts int
	longTimeout        time.Duration
	heartbeatTimeout   time.Duration
	longAttempts       int

	// Устойчивость воркера workflow: graceful drain и лимиты конкурентности.
	workerStopTimeout   time.Duration
	workerMaxActivities int
	workerMaxWorkflows  int
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
			Console:  opts.console,
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
		leadSearch, err := leadSearchPort(opts)
		if err != nil {
			return nil, fmt.Errorf("request-fulfillment: порт поиска: %w", err)
		}
		emailGeneration, err := emailGenerationPort(opts)
		if err != nil {
			return nil, fmt.Errorf("request-fulfillment: порт генерации писем: %w", err)
		}

		// Хранилище заявок: PostgreSQL по DSN зелёной зоны либо in-memory (dev).
		// Durable-исполнение Temporal требует персистентного адаптера: состояние
		// заявки и её outbox переживают рестарт процесса.
		var persist requestfulfillment.Persistence
		// Стор сессий консоли в общей (зелёной) базе: сессия, выданная другой
		// консолью той же базы, принимается и здесь. Без DSN — память процесса.
		var consoleStore consoleauth.Store
		if strings.TrimSpace(opts.requestGreenDSN) != "" {
			pool, err := newServicePool(opts.requestGreenDSN, requestfulfillment.ServiceID)
			if err != nil {
				return nil, err
			}
			if err := applyMigrations(pool, "backend/contexts/request-fulfillment/migrations/green", requestfulfillment.ServiceID); err != nil {
				pool.Close()
				return nil, err
			}
			if err := applyMigrations(pool, "backend/platform/consoleauth/migrations/green", consoleauth.Schema); err != nil {
				pool.Close()
				return nil, err
			}
			persist = requestfulfillment.NewPostgresPersistence(pool)
			consoleStore = consoleauth.NewPostgresStore(pool)
		}

		// Консоль заявок с пустыми учётными данными — dev-режим без внешнего
		// доступа. О нём честно сообщается, чтобы пустые дефолты флагов не
		// выглядели тихой подменой секретов.
		if opts.consoleUser == "" || opts.consolePassword == "" {
			opts.logger.Warn("request-fulfillment: консоль работает с пустыми учётными данными (только dev, без внешнего доступа)", observ.Fields{})
		}

		return requestfulfillment.New(requestfulfillment.Config{
			APIAddr:                       opts.apiAddr,
			Interval:                      opts.publishInterval,
			Repository:                    persist.Repository,
			UnitOfWork:                    persist.UnitOfWork,
			Journal:                       persist.Journal,
			DraftStore:                    persist.DraftStore,
			Publisher:                     broker,
			WorkflowClient:                opts.workflowClient,
			WorkflowTaskQueue:             opts.workflowTaskQueue,
			Runtime:                       opts.runtime,
			ApprovalTTL:                   opts.approvalTTL,
			WorkflowExecutionTimeout:      opts.executionTimeout,
			TransitionActivityTimeout:     opts.transitionTimeout,
			TransitionActivityAttempts:    opts.transitionAttempts,
			LongActivityTimeout:           opts.longTimeout,
			LongActivityHeartbeat:         opts.heartbeatTimeout,
			LongActivityAttempts:          opts.longAttempts,
			WorkerStopTimeout:             opts.workerStopTimeout,
			WorkerMaxConcurrentActivities: opts.workerMaxActivities,
			WorkerMaxConcurrentWorkflows:  opts.workerMaxWorkflows,
			SearchLeads:                   opts.searchStubLeads,
			LeadSearch:                    leadSearch,
			EmailGeneration:               emailGeneration,
			ConsoleUser:                   opts.consoleUser,
			ConsolePassword:               opts.consolePassword,
			ConsoleStore:                  consoleStore,
			Console:                       opts.console,
			Logger:                        opts.logger,
			Metrics:                       opts.metrics,
			Tracer:                        tracer,
		})
	case platformgovernance.ServiceID:
		return buildPlatformGovernance(opts, tracer)
	case integrationplatform.ServiceID:
		return integrationplatform.New(integrationplatform.Config{
			Logger:  opts.logger,
			Metrics: opts.metrics,
			Tracer:  tracer,
		})
	case datagovernance.ServiceID:
		// Период фонового истечения полномочий оставляем доменному значению по
		// умолчанию (defaultSweepInterval): -publish-interval задаёт публикацию, а
		// не проход полномочий.
		return datagovernance.New(datagovernance.Config{
			APIAddr: opts.apiAddr,
			Grants:  datagovernance.GrantsFromStrings(opts.actor, opts.tenant),
			Logger:  opts.logger,
			Metrics: opts.metrics,
			Tracer:  tracer,
		})
	case audience.ServiceID:
		return audience.New(audience.Config{
			APIAddr:   opts.apiAddr,
			Interval:  opts.publishInterval,
			Publisher: broker,
			Logger:    opts.logger,
			Metrics:   opts.metrics,
			Tracer:    tracer,
		})
	case contentproduction.ServiceID:
		return contentproduction.New(contentproduction.Config{
			APIAddr:   opts.apiAddr,
			Interval:  opts.publishInterval,
			Publisher: broker,
			Logger:    opts.logger,
			Metrics:   opts.metrics,
			Tracer:    tracer,
		})
	case messagedelivery.ServiceID:
		// У контекста нет роли api: адрес внутреннего сервера не задаётся.
		return messagedelivery.New(messagedelivery.Config{
			Interval:  opts.publishInterval,
			Publisher: broker,
			Logger:    opts.logger,
			Metrics:   opts.metrics,
			Tracer:    tracer,
		})
	case engagementobservation.ServiceID:
		return engagementobservation.New(engagementobservation.Config{
			IngestAddr: opts.ingestAddr,
			Interval:   opts.publishInterval,
			Publisher:  broker,
			Consumer:   opts.consumer,
			Logger:     opts.logger,
			Metrics:    opts.metrics,
			Tracer:     tracer,
		})
	case salesworkflow.ServiceID:
		return salesworkflow.New(salesworkflow.Config{
			APIAddr:    opts.apiAddr,
			IngestAddr: opts.ingestAddr,
			Interval:   opts.publishInterval,
			Publisher:  broker,
			Logger:     opts.logger,
			Metrics:    opts.metrics,
			Tracer:     tracer,
		})
	case communityintelligence.ServiceID:
		// У контекста нет роли api, а период регулярного сбора оставляем
		// доменному значению по умолчанию (defaultInterval = 1 мин).
		return communityintelligence.New(communityintelligence.Config{
			Logger:  opts.logger,
			Metrics: opts.metrics,
			Tracer:  tracer,
		})
	case publishing.ServiceID:
		return publishing.New(publishing.Config{
			APIAddr:   opts.apiAddr,
			Interval:  opts.publishInterval,
			Publisher: broker,
			Logger:    opts.logger,
			Metrics:   opts.metrics,
			Tracer:    tracer,
		})
	case billing.ServiceID:
		return billing.New(billing.Config{
			APIAddr:   opts.apiAddr,
			Interval:  opts.publishInterval,
			Publisher: broker,
			Logger:    opts.logger,
			Metrics:   opts.metrics,
			Tracer:    tracer,
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

// leadSearchPort выбирает адаптер контракта lead-search по топологии: задан
// адрес сервиса поиска — сетевой HTTP-адаптер; иначе nil, и шаг поиска падает на
// прежнюю заглушку (SearchLeads).
func leadSearchPort(opts buildOptions) (leadsearch.Port, error) {
	if addr := strings.TrimSpace(opts.leadSearchURL); addr != "" {
		return leadsearchhttp.New(addr)
	}
	return nil, nil
}

// emailGenerationPort выбирает адаптер контракта email-generation по топологии:
// задан адрес сервиса генерации — сетевой HTTP-адаптер; иначе nil, и шаг
// генерации черновиков штатно пропускается.
func emailGenerationPort(opts buildOptions) (emailgeneration.Port, error) {
	if addr := strings.TrimSpace(opts.emailGenerationURL); addr != "" {
		return emailgenerationhttp.New(addr)
	}
	return nil, nil
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
	// Стор сессий консоли в общей (зелёной) базе: сессия, выданная другой
	// консолью той же базы, принимается и здесь. Без DSN — память процесса.
	var consoleStore consoleauth.Store
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

		if err := applyMigrations(greenPool, "backend/contexts/lead-management/migrations/green", leadmanagement.ServiceID); err != nil {
			greenPool.Close()
			redPool.Close()
			return nil, err
		}
		if err := applyMigrations(redPool, "backend/contexts/lead-management/migrations/red", leadmanagement.ServiceID); err != nil {
			greenPool.Close()
			redPool.Close()
			return nil, err
		}
		// Миграция общей схемы сессий консоли (consoleauth) — в зелёной зоне.
		if err := applyMigrations(greenPool, "backend/platform/consoleauth/migrations/green", consoleauth.Schema); err != nil {
			greenPool.Close()
			redPool.Close()
			return nil, err
		}

		// Пулы живут столько же, сколько процесс: сервис не владеет их закрытием
		// (нет shutdown-хука в composition.Service), поэтому закрываются на выходе.
		repos = leadmanagement.NewPostgresRepositories(greenPool, redPool)
		consoleStore = consoleauth.NewPostgresStore(greenPool)
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
		ConsoleStore:     consoleStore,
		Console:          opts.console,
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

// newServicePool собирает пул соединений PostgreSQL для сервиса. Пул живёт
// столько же, сколько процесс: сервис не владеет его закрытием.
func newServicePool(dsn, service string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, fmt.Errorf("%s: пул соединений: %w", service, err)
	}
	return pool, nil
}

// applyMigrations загружает миграции схемы сервиса из каталога и применяет их к
// пулу. Принадлежность схеме проверяет migrate.Load до выполнения.
func applyMigrations(pool *pgxpool.Pool, dir, service string) error {
	migrations, err := migrate.Load(dir, migrate.OwnSchema(service))
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if _, err := pool.Exec(context.Background(), m.SQL); err != nil {
			return fmt.Errorf("%s: миграция %s: %w", service, m.Name, err)
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
