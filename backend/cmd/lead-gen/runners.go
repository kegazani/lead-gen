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

	contentproduction "lead-gen/backend/contexts/content-production"
	datagovernance "lead-gen/backend/contexts/data-governance"
	engagementobservation "lead-gen/backend/contexts/engagement-observation"
	integrationplatform "lead-gen/backend/contexts/integration-platform"
	leadmanagement "lead-gen/backend/contexts/lead-management"
	messagedelivery "lead-gen/backend/contexts/message-delivery"
	outreachorchestration "lead-gen/backend/contexts/outreach-orchestration"
	platformgovernance "lead-gen/backend/contexts/platform-governance"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"

	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/migrate"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	platformworkflow "lead-gen/backend/platform/workflow"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/vector"
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
	requestGreenDSN string // DSN зелёной зоны; пусто — in-memory адаптеры

	// Конфигурация хранилища supporting-контекстов зелёной зоны (analytics,
	// audience, content-production, data-governance, engagement-observation,
	// integration-platform, platform-governance, message-delivery). Пусто —
	// каждый контекст собирается на in-memory адаптерах (dev).
	supportGreenDSN string // DSN зелёной зоны; пусто — in-memory адаптеры

	// Конфигурация реального движка рендеринга content-production (headless
	// Chromium). Пусто — контекст остаётся на детерминированном dev-двойнике
	// рендера и предупреждает об этом: продуктовый движок не подменяется молча.
	rendererExecPath string        // путь к Chromium/Chrome; пусто — браузер не задан
	rendererWSURL    string        // адрес CDP-эндпоинта запущенного браузера; пусто — не задан
	rendererTimeout  time.Duration // таймаут одной операции рендера

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

	// Параметры повторных окон доставки message-delivery.
	deliveryRetryInterval time.Duration // интервал тика повторных окон
	deliveryMaxAttempts   int           // предельное число попыток (включая первую)

	// Мост approved-action → delivery-command: резолвер получателя
	// (lead-management) + источник команд роли sender (message-delivery) +
	// приёмник утверждённых действий (outreach-orchestration). Собирается один раз
	// на процесс в main и разделяется сервисами; nil — мост не собран.
	bridge             *messagedelivery.ApprovedActionBridge
	approvedActionSink outreachorchestration.SinkFunc

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
		cfg := analytics.Config{
			APIAddr:  opts.apiAddr,
			Consumer: opts.consumer,
			Console:  opts.console,
			Logger:   opts.logger,
			Metrics:  opts.metrics,
			Tracer:   tracer,
		}
		// Проекция дашборда: PostgreSQL при заданном DSN зелёной зоны, иначе
		// in-memory dev. Витрина переживает рестарт только с хранилищем.
		if dsn := strings.TrimSpace(opts.supportGreenDSN); dsn != "" {
			pool, err := supportPool(dsn, analytics.ServiceID, "backend/contexts/analytics/migrations")
			if err != nil {
				return nil, err
			}
			cfg.ProjectionStore = analytics.NewPostgresPersistence(pool).Projection
		}
		return analytics.New(cfg)
	case outreachorchestration.ServiceID:
		cfg := outreachorchestration.Config{
			APIAddr:   opts.apiAddr,
			Interval:  opts.publishInterval,
			Grants:    outreachorchestration.GrantsFromStrings(opts.actor, opts.tenant),
			Risk:      outreachorchestration.NoUpstreamRiskChecks,
			Publisher: broker,
			Transport: mechanism,
			// Приёмник утверждённых действий: если собран мост в message-delivery,
			// действия из outbox-релиза идут ему (замыкание «утверждено → отправлено»),
			// а не в AMQP. Иначе — прежнее поведение (Publisher/AMQP).
			ApprovedActionSink: opts.approvedActionSink,
			Logger:             opts.logger,
			Metrics:            opts.metrics,
			Tracer:             tracer,
		}
		// Кампании и цепочки: PostgreSQL при заданном DSN зелёной зоны, иначе
		// in-memory dev. Движок последовательностей переживает рестарт только с
		// хранилищем.
		// Консоль рассылок: общие учётные данные и общая навигация, как у консолей
		// lead-management/request-fulfillment. Сессии — в общей зелёной базе, чтобы
		// вход в одну консоль действовал в другой; без DSN — память процесса.
		cfg.Console = opts.console
		cfg.ConsoleAuth = consoleauth.Config{Username: opts.consoleUser, Password: opts.consolePassword}
		if opts.consoleUser == "" || opts.consolePassword == "" {
			opts.logger.Warn("outreach-orchestration: консоль рассылок работает с пустыми учётными данными (только dev)", observ.Fields{})
		}
		if dsn := strings.TrimSpace(opts.supportGreenDSN); dsn != "" {
			pool, err := supportPool(dsn, outreachorchestration.ServiceID, "backend/contexts/outreach-orchestration/migrations")
			if err != nil {
				return nil, err
			}
			if err := applyMigrations(pool, "backend/platform/consoleauth/migrations/green", consoleauth.Schema); err != nil {
				pool.Close()
				return nil, err
			}
			cfg.Persistence = outreachorchestration.NewPostgresPersistence(pool)
			cfg.ConsoleAuth.Store = consoleauth.NewPostgresStore(pool)
		}
		return outreachorchestration.New(cfg)
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
		// Векторное хранилище за provider-port (контракт vector): эмбеддинг оффера
		// заявки. Без DSN/ключа — порт не собирается, и эмбеддинг честно недоступен.
		vectorPort, err := vectorRecallPort(opts)
		if err != nil {
			return nil, fmt.Errorf("request-fulfillment: векторный порт: %w", err)
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
			Vector:                        vectorPort,
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
		// Контекст сам собирает персистентность по DSN зелёной зоны: без DSN —
		// in-memory dev с предупреждением.
		return integrationplatform.New(integrationplatform.Config{
			DSN:     opts.supportGreenDSN,
			Logger:  opts.logger,
			Metrics: opts.metrics,
			Tracer:  tracer,
		})
	case datagovernance.ServiceID:
		// Период фонового истечения полномочий оставляем доменному значению по
		// умолчанию (defaultSweepInterval): -publish-interval задаёт публикацию, а
		// не проход полномочий. Персистентность контекст собирает сам по DSN
		// зелёной зоны.
		return datagovernance.New(datagovernance.Config{
			APIAddr: opts.apiAddr,
			DSN:     opts.supportGreenDSN,
			Grants:  datagovernance.GrantsFromStrings(opts.actor, opts.tenant),
			Logger:  opts.logger,
			Metrics: opts.metrics,
			Tracer:  tracer,
		})
	case audience.ServiceID:
		cfg := audience.Config{
			APIAddr:   opts.apiAddr,
			Interval:  opts.publishInterval,
			Publisher: broker,
			Logger:    opts.logger,
			Metrics:   opts.metrics,
			Tracer:    tracer,
		}
		if dsn := strings.TrimSpace(opts.supportGreenDSN); dsn != "" {
			pool, err := supportPool(dsn, audience.ServiceID, "backend/contexts/audience/migrations")
			if err != nil {
				return nil, err
			}
			cfg.Persistence = audience.NewPostgresPersistence(pool)
		}
		return audience.New(cfg)
	case contentproduction.ServiceID:
		// Контекст сам собирает персистентность по DSN зелёной зоны: без DSN —
		// in-memory dev с предупреждением. Реальный движок рендера (headless
		// Chromium) подключается при заданном пути или CDP-адресе; пустые значения
		// оставляют контекст на детерминированном dev-двойнике рендера.
		if strings.TrimSpace(opts.rendererExecPath) == "" && strings.TrimSpace(opts.rendererWSURL) == "" {
			opts.logger.Warn("content-production: движок рендера не сконфигурирован (renderer-exec-path/renderer-ws-url пусты), рендер на dev-двойнике (dev-режим)", observ.Fields{})
		}
		return contentproduction.New(contentproduction.Config{
			APIAddr:          opts.apiAddr,
			Interval:         opts.publishInterval,
			DSN:              opts.supportGreenDSN,
			RendererExecPath: opts.rendererExecPath,
			RendererWSURL:    opts.rendererWSURL,
			RendererTimeout:  opts.rendererTimeout,
			Publisher:        broker,
			LLM:              llmPortFromOpts(opts),
			Logger:           opts.logger,
			Metrics:          opts.metrics,
			Tracer:           tracer,
		})
	case messagedelivery.ServiceID:
		// У контекста нет роли api: адрес внутреннего сервера не задаётся.
		return buildMessageDelivery(opts, tracer, broker)
	case engagementobservation.ServiceID:
		cfg := engagementobservation.Config{
			IngestAddr: opts.ingestAddr,
			Interval:   opts.publishInterval,
			Publisher:  broker,
			Consumer:   opts.consumer,
			Logger:     opts.logger,
			Metrics:    opts.metrics,
			Tracer:     tracer,
		}
		if dsn := strings.TrimSpace(opts.supportGreenDSN); dsn != "" {
			pool, err := supportPool(dsn, engagementobservation.ServiceID, "backend/contexts/engagement-observation/migrations")
			if err != nil {
				return nil, err
			}
			cfg.Persistence = engagementobservation.NewPostgresPersistence(pool)
		}
		return engagementobservation.New(cfg)
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
	cfg := platformgovernance.Config{
		APIAddr:  opts.apiAddr,
		Requests: requests,
		Logger:   opts.logger,
		Metrics:  opts.metrics,
		Tracer:   tracer,
	}
	// Хранилище тенантов и пользователей: PostgreSQL по пулу зелёной зоны,
	// иначе in-memory dev. Миграции схемы применяет точка сборки.
	if dsn := strings.TrimSpace(opts.supportGreenDSN); dsn != "" {
		pool, err := supportPool(dsn, platformgovernance.ServiceID, "backend/contexts/platform-governance/migrations")
		if err != nil {
			return nil, err
		}
		cfg.Pool = pool
	}
	return platformgovernance.New(cfg)
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

// llmPortFromOpts собирает порт LLM владельца integration-platform, если задан
// ключ. Без ключа порт не собирается (nil): потребители честно сообщают о
// недоступности, детерминированная подмена не ставится.
func llmPortFromOpts(opts buildOptions) llm.Port {
	if strings.TrimSpace(opts.llmKey) == "" {
		return nil
	}
	return integrationplatform.NewOpenAI(integrationplatform.OpenAIConfig{
		BaseURL: opts.llmBaseURL,
		Model:   opts.llmModel,
		APIKey:  opts.llmKey,
	})
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

// vectorRecallPort собирает векторное хранилище за provider-port (контракт vector):
// реальный эмбеддер поверх OpenAI-совместимого endpoint и персистентный индекс
// integration-platform (schema-per-service). Без DSN зелёной зоны или без ключа
// провайдера эмбеддингов порт не собирается (nil): потребитель честно сообщает о
// недоступности, а детерминированная подмена на продуктовом пути не ставится.
func vectorRecallPort(opts buildOptions) (vector.Port, error) {
	dsn := strings.TrimSpace(opts.supportGreenDSN)
	if dsn == "" || strings.TrimSpace(opts.llmKey) == "" {
		return nil, nil
	}
	pool, err := supportPool(dsn, integrationplatform.ServiceID, "backend/contexts/integration-platform/migrations")
	if err != nil {
		return nil, err
	}
	embedder, err := integrationplatform.NewVectorEmbedder(integrationplatform.VectorEmbedderConfig{
		BaseURL: opts.llmBaseURL,
		Model:   opts.llmModel,
		APIKey:  opts.llmKey,
	})
	if err != nil {
		return nil, err
	}
	return integrationplatform.NewVectorPostgres(pool, embedder), nil
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

	// Векторное хранилище за provider-port (контракт vector): семантический recall
	// и эмбеддинг компании. Без DSN/ключа — порт не собирается, recall честно
	// недоступен (кандидаты не выдумываются).
	vectorPort, err := vectorRecallPort(opts)
	if err != nil {
		return nil, fmt.Errorf("lead-management: векторный порт: %w", err)
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
		Vector:           vectorPort,
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

// buildMessageDelivery собирает сервис message-delivery: PostgreSQL по DSN
// зелёной зоны, реальный SMTP-адаптер (владелец integration-platform) и стоп-лист
// через контракт suppression-policy (владелец data-governance). Без DSN и
// провайдера контекст честно уходит в in-memory dev-режим с предупреждением —
// заглушка на продуктовый путь не ставится.
func buildMessageDelivery(opts buildOptions, tracer observ.Tracer, broker amqp.Publisher) (composition.Service, error) {
	cfg := messagedelivery.Config{
		Interval:         opts.publishInterval,
		RetryInterval:    opts.deliveryRetryInterval,
		MaxRetryAttempts: opts.deliveryMaxAttempts,
		Publisher:        broker,
		Logger:           opts.logger,
		Metrics:          opts.metrics,
		Tracer:           tracer,
	}

	// Персистентность: PostgreSQL по DSN зелёной зоны (схема green). Миграции
	// применяет точка сборки; пул живёт столько же, сколько процесс.
	dsn := strings.TrimSpace(opts.supportGreenDSN)
	if dsn != "" {
		pool, err := supportPool(dsn, messagedelivery.ServiceID, "backend/contexts/message-delivery/migrations/green")
		if err != nil {
			return nil, err
		}
		cfg.Pool = pool
	}

	// Реальный почтовый адаптер: SMTP-клиент владельца integration-platform.
	// Адрес отправителя обязателен, его проверяет сборка контекста.
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
		return nil, fmt.Errorf("message-delivery: SMTP: %w", err)
	}
	cfg.MailPort = smtpClient
	cfg.MailFrom = opts.smtpFrom

	// Мост approved-action → delivery-command: если он собран, роль sender читает
	// команды из него (утверждённое действие → разрешённая команда → отправка).
	// Без моста роль sender остаётся без источника (прежнее поведение).
	if opts.bridge != nil {
		opts.bridge.Bind(&cfg)
	}

	// Стоп-лист: контракт suppression-policy владельца data-governance. Без DSN
	// реального источника нет — оставляем стоп-лист незаданным, чтобы контекст
	// честно предупредил о dev-режиме, а не выдал пустой in-memory список за
	// рабочий стоп-лист.
	if dsn != "" {
		govSlice, err := datagovernance.Build(datagovernance.Config{
			DSN:    dsn,
			Logger: opts.logger,
		})
		if err != nil {
			return nil, fmt.Errorf("message-delivery: стоп-лист data-governance: %w", err)
		}
		suppressions, err := messagedelivery.NewSuppressionList(datagovernance.NewSuppressionPolicy(govSlice))
		if err != nil {
			return nil, fmt.Errorf("message-delivery: стоп-лист: %w", err)
		}
		cfg.Suppressions = suppressions
	}

	return messagedelivery.New(cfg)
}

// supportPool создаёт пул PostgreSQL зелёной зоны для supporting-контекста и
// применяет миграции его схемы. Вызывается только при заданном DSN. Пул живёт
// столько же, сколько процесс: сервис не владеет его закрытием.
func supportPool(dsn, service, migrationsDir string) (*pgxpool.Pool, error) {
	pool, err := newServicePool(dsn, service)
	if err != nil {
		return nil, err
	}
	if err := applyMigrations(pool, migrationsDir, service); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
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
