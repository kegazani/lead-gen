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

	agentorchestration "lead-gen/backend/contexts/agent-orchestration"
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
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/messaging/inbox"
	"lead-gen/backend/platform/migrate"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/observability/prometheus"
	topology "lead-gen/backend/platform/topology"
	"lead-gen/backend/platform/transport/amqp"
	platformworkflow "lead-gen/backend/platform/workflow"
	agentrunstate "lead-gen/contracts/agent-orchestration/agentrunstate"
	agentrunstatehttp "lead-gen/contracts/agent-orchestration/agentrunstate/httpport"
	"lead-gen/contracts/analytics/campaignreport"
	campaignreporthttp "lead-gen/contracts/analytics/campaignreport/httpport"
	"lead-gen/contracts/content-production/contentrequest"
	contentrequesthttp "lead-gen/contracts/content-production/contentrequest/httpport"
	"lead-gen/contracts/content-production/contentversion"
	contentversionhttp "lead-gen/contracts/content-production/contentversion/httpport"
	"lead-gen/contracts/integration-platform/adapterstatus"
	adapterstatushttp "lead-gen/contracts/integration-platform/adapterstatus/httpport"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/vector"
	"lead-gen/contracts/lead-management/deliverytarget"
	deliverytargethttp "lead-gen/contracts/lead-management/deliverytarget/httpport"
	"lead-gen/contracts/lead-management/emailgeneration"
	emailgenerationhttp "lead-gen/contracts/lead-management/emailgeneration/httpport"
	"lead-gen/contracts/lead-management/leadsearch"
	leadsearchhttp "lead-gen/contracts/lead-management/leadsearch/httpport"
	"lead-gen/contracts/lead-management/recommendationapply"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
	approvedactionhttp "lead-gen/contracts/outreach-orchestration/approvedaction/httpport"
	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
	outreachhandoffhttp "lead-gen/contracts/outreach-orchestration/outreachhandoff/httpport"
	"lead-gen/contracts/platform-governance/accessdecision"
	accessdecisioninproc "lead-gen/contracts/platform-governance/accessdecision/inproc"
	"lead-gen/contracts/request-fulfillment/requestintent"
	"lead-gen/contracts/request-fulfillment/requestintent/httpport"
	"lead-gen/frontend/components"
)

// buildOptions — параметры, общие для сборки сервисов в одном процессе.
// (wired: content-request и outreach-handoff)
type buildOptions struct {
	apiAddr    string
	ingestAddr string

	// apiAddrResolver — адрес API по идентификатору сервиса для совмещённого
	// запуска нескольких api-сервисов (флаг -api-port-base). nil — у всех сервисов
	// адрес apiAddr (один сервис на процесс).
	apiAddrResolver      func(serviceID string) string
	requestIntentURL     string
	requestExecutionsURL string
	leadSearchURL        string
	emailGenerationURL   string

	// Сетевой транспорт синхронных контрактов (live-e2e-contour, Фаза 2).
	// Пустой адрес — соответствующий адаптер собирается in-proc (co-location),
	// как прежде. Заданный — сетевой HTTP-адаптер к сервису-владельцу.
	approvedActionURL        string // базовый адрес приёма approved-action у message-delivery
	approvedActionIntakeAddr string // адрес слушателя приёма approved-action (роль worker)
	deliveryTargetURL        string // базовый адрес контракта delivery-target у lead-management
	contentVersionURL        string // базовый адрес контракта content-version у content-production
	integrationPlatformURL   string // базовый адрес HTTP-поверхности integration-platform (контракт adapter-status)
	outreachHandoffURL       string // базовый адрес контракта outreach-handoff у outreach-orchestration
	campaignCommandURL       string // базовый адрес контракта campaign-command у outreach-orchestration (подача черновика на утверждение)
	draftDecisionURL         string // базовый адрес read-контракта draftdecision у outreach-orchestration (чтение решения)
	contentRequestURL        string // базовый адрес контракта content-request у content-production

	publishInterval time.Duration
	actor           string
	tenant          string

	// Брокер в памяти: реализует и Publisher, и Consumer, поэтому в совмещённом
	// деплое публикация одного сервиса доходит до подписки другого.
	consumer amqp.Consumer

	// requestDeliveryQueue — имя очереди подписки request-fulfillment на события
	// доставки (capability request-delivery-conclusion). Пусто — очередь по
	// умолчанию `request-fulfillment.delivery-event`.
	requestDeliveryQueue string

	// analyticsDeliveryQueue — имя очереди подписки analytics на исходы доставки
	// (контракт delivery-event; питает витрину клиента ходом цикла). Пусто — очередь
	// по умолчанию `analytics.delivery-event`.
	analyticsDeliveryQueue string

	// Наблюдаемость процесса: логгер и метрики общие для сервисов, трейсер
	// строится на сервис (service.name).
	logger            observ.Logger
	metrics           observ.Metrics
	failureNormalizer jobs.FailureNormalizer

	// Конфигурация сервиса lead-management.
	leadGreenDSN string // DSN зелёной зоны; пусто — in-memory адаптеры
	leadRedDSN   string // DSN красной зоны; пусто — DSN зелёной зоны

	// Конфигурация хранилища сервиса request-fulfillment.
	requestGreenDSN string // DSN зелёной зоны; пусто — in-memory адаптеры

	// Конфигурация хранилища supporting-контекстов зелёной зоны (agent-orchestration,
	// analytics, audience, content-production, data-governance, engagement-observation,
	// integration-platform, platform-governance, message-delivery). Пусто —
	// каждый контекст собирается на in-memory адаптерах (dev).
	supportGreenDSN string // DSN зелёной зоны; пусто — in-memory адаптеры

	// Runtime-only DB policy применяется только к отдельной intake-группе.
	dbPolicy             intakeDBPolicy
	intakeRuntimeDSNs    *intakeRuntimeDSNs
	intakeRuntimeDBOps   intakeRuntimeDBOps
	intakeRuntimeFactory intakeRuntimeConstructors
	runtimeDBResources   *runtimePoolRegistry
	intakeRuntimePools   *intakeRuntimePools

	// Конфигурация сервиса agent-orchestration (change intelligent-lead-operations).
	agentWorkflowTaskQueue string   // очередь задач воркера workflow агентов (отдельная от заявок)
	agentSearchLimit       int      // сколько компаний просит блок поиска агента; 0 — одна
	agentMaxConcurrent     int      // предел одновременной обработки компаний; 0 — по умолчанию
	agentScheduleTenants   []string // тенанты расписания роли schedule

	// cockpitMinGroup — порог минимальной группы полноэкранного дашборда
	// операций; 0 — встроенное значение по умолчанию.
	cockpitMinGroup int

	// agentOrchestrationURL — базовый адрес HTTP-поверхности agent-orchestration
	// (контракт agent-run-state). Пусто — источник запусков экрана операций
	// связывается in-proc владельцем при co-location; задан — сетевым клиентом
	// контракта при разделении сервисов.
	agentOrchestrationURL string

	// analyticsURL — базовый адрес HTTP-поверхности analytics (контракт
	// campaign-report: цифры процесса и агрегаты по клиентам). Пусто — агрегаты
	// экрана операций связываются in-proc владельцем analytics при co-location;
	// задан — сетевым клиентом контракта при разделении сервисов.
	analyticsURL string

	// agentBootstrapPipeline — разовая публикация первого набора шагов пайплайна
	// агента при сборке сервиса поверх реестра блоков первого набора. Выключено
	// по умолчанию: идентификатор и состав первого пайплайна — данные развёртывания,
	// а не выдуманное точкой входа значение.
	agentBootstrapPipeline bool

	// Порты владельцев для блоков agent-orchestration и контура обратной связи
	// analytics (change intelligent-lead-operations, задача 7.3; change
	// search-quality-feedback). Собираются точкой входа до сборки сервисов:
	// co-location — ленивым разрешением владельца, разделение сервисов — сетевым
	// клиентом контракта. Незаданный порт оставляет блок честно недоступным, а не
	// подменяется выдуманным результатом (AGENTS.md §3).
	leadRelevance        agentorchestration.RelevanceEvaluator
	approvalSubmitter    agentorchestration.ApprovalSubmitter
	approvedAction       approvedaction.Publisher
	approvedActionSource agentorchestration.ApprovedActionSource
	agentContentRequest  contentrequest.Port
	agentContentLanguage string

	// recommendationApply — контракт recommendation-apply владельца lead-management
	// для Config.RecommendationApply аналитики. nil — контур обратной связи не
	// подключён, применение рекомендации честно недоступно.
	recommendationApply recommendationapply.Port

	// leadStore — общие адаптеры хранилища lead-management, собранные сервисом
	// владельца; co-location-контракты (lead-relevance, recommendation-apply)
	// читают то же хранилище. nil — владелец не собран (тесты, раздельный запуск).
	leadStore *leadManagementStore

	// Базовые адреса контрактов владельцев (флаги/ENV LEADGEN_*). Пусто —
	// соответствующий контракт связывается in-proc владельцем при co-location.
	leadRelevanceURL       string // базовый адрес контракта lead-relevance у lead-management
	recommendationApplyURL string // базовый адрес контракта recommendation-apply у lead-management
	// operations — read-поверхности владельцев экрана операций, собранные точкой
	// входа до сборки platform-governance. Незаданная поверхность даёт честную
	// недоступность только своего раздела, а не нули (AGENTS.md §3).
	operations platformgovernance.OperationsInput

	// report — опубликованный контракт campaign-report (владелец analytics) для
	// метрик главного экрана клиентского портала. Незаданный порт даёт честное
	// «—» на экране, а не нули (AGENTS.md §3).
	report campaignreport.Port

	// Конфигурация реального движка рендеринга content-production (headless
	// Chromium). Пусто — контекст остаётся на детерминированном dev-двойнике
	// рендера и предупреждает об этом: продуктовый движок не подменяется молча.
	rendererExecPath string        // путь к Chromium/Chrome; пусто — браузер не задан
	rendererWSURL    string        // адрес CDP-эндпоинта запущенного браузера; пусто — не задан
	rendererTimeout  time.Duration // таймаут одной операции рендера

	// Консоль топологии: пути канонических источников графа и адрес Prometheus.
	// Пустой путь каталога — граф честно недоступен; пустой адрес — метрики
	// недоступны (`available:false`), а не ряд с нулями.
	topologyCatalogPath        string
	topologyServicesPath       string
	topologyInfrastructurePath string
	prometheusURL              string

	firecrawlKey     string                  // ключ Firecrawl; обязателен для поиска и обхода
	firecrawlURL     string                  // базовый адрес Firecrawl; пусто — по умолчанию
	llmBaseURL       string                  // базовый адрес LLM (DeepSeek); пусто — по умолчанию
	llmModel         string                  // модель LLM; пусто — по умолчанию
	llmKey           string                  // ключ LLM; пусто — LLM-планирование отключено
	embeddingBaseURL string                  // базовый адрес API эмбеддингов; пусто — по умолчанию
	embeddingModel   string                  // модель эмбеддингов; пусто — по умолчанию
	embeddingKey     string                  // ключ провайдера эмбеддингов; пусто — вектор недоступен
	consoleUser      string                  // логин оператора консоли
	consolePassword  string                  // пароль оператора консоли
	console          components.ConsoleBases // базовые адреса сервисов консоли
	// Внешние префиксы путей консолей под единым origin (admin-proxy): каждый
	// сервис переписывает свои ссылки/редиректы под свой префикс общим middleware.
	consoleLeadsBase    string // префикс консоли lead-management
	consoleRequestsBase string // префикс консоли request-fulfillment
	consoleReportsBase  string // префикс консоли analytics
	consoleOutreachBase string // префикс консоли outreach-orchestration
	consolePlatformBase string // префикс консоли platform-governance
	// consolePrefixes — все префиксы контуров консолей (для исключения из
	// переписывания ссылок на другие сервисы в пределах единого origin).
	consolePrefixes        []string
	leadMaxPages           int           // верхний предел страниц обхода на сайт; 0 — без предела
	leadMaxDepth           int           // максимальная глубина обхода
	leadMaxTimeSeconds     int           // предельное время обхода, с
	dialogueMaxTurns       int           // предел числа ходов диалога ассистента
	firecrawlMaxCredits    int           // per-task cap Firecrawl; 0 — cap не задан
	firecrawlCrawlInterval time.Duration // пауза между запусками Firecrawl crawl
	leadSearchMaxResults   int           // верхний предел кандидатов поиска под заявку
	leadSearchDeadline     time.Duration // предельное время поиска под заявку
	smtpHost               string        // адрес SMTP-сервера отправки писем
	smtpPort               int           // порт SMTP-сервера
	smtpUsername           string        // имя учётной записи SMTP
	smtpPassword           string        // пароль учётной записи SMTP
	smtpFrom               string        // адрес отправителя писем

	// Параметры повторных окон доставки message-delivery.
	deliveryRetryInterval time.Duration // интервал тика повторных окон
	deliveryMaxAttempts   int           // предельное число попыток (включая первую)

	// Мост approved-action → delivery-command: резолвер получателя
	// (lead-management) + источник команд роли sender (message-delivery) +
	// приёмник утверждённых действий (outreach-orchestration). Собирается один раз
	// на процесс в main и разделяется сервисами; nil — мост не собран.
	bridge             *messagedelivery.ApprovedActionBridge
	approvedActionSink outreachorchestration.SinkFunc

	// accessDecision — решение о доступе владельца platform-governance, общее для
	// потребителей контракта access-decision (outreach-orchestration,
	// data-governance). Точка входа задаёт его до сборки потребителей: реальное
	// решение владельца или честный недоступный решатель; nil на продуктовый
	// путь не попадает.
	accessDecision accessdecision.Decider

	// riskFacts — внешний предикат признаков риска пакетного пути
	// outreach-orchestration, собранный точкой входа поверх портов владельцев
	// (change risk-screening). nil — сборщик не собран, и сервис честно недоступен
	// по риску, а не идёт «без проверки» (AGENTS.md §3).
	riskFacts outreachorchestration.RiskFacts

	// Базовые адреса и ключи владельца верификации адреса (lead-management,
	// контракт address-verification). Пустой адрес — проверка честно недоступна,
	// а не подменяется статусом.
	addressVerifierURL string
	addressVerifierKey string
	// addressVerificationURL — базовый адрес контракта address-verification у
	// lead-management при разделении сервисов. Пусто — владелец берётся in-proc
	// при co-location; задан — сетевой клиент (владелец в другом контейнере).
	addressVerificationURL string

	// accessDecisionURL — базовый адрес HTTP-поверхности решения доступа владельца
	// platform-governance. Пусто — решение связывается in-proc владельцем при
	// co-location; задан — сетевым клиентом контракта при разделении сервисов.
	accessDecisionURL string

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

// apiAddrFor возвращает адрес API сервиса: per-service адрес при заданном
// резолвере (совмещённый запуск), иначе общий apiAddr (один сервис на процесс).
func (o buildOptions) apiAddrFor(serviceID string) string {
	if o.apiAddrResolver == nil {
		return o.apiAddr
	}
	return o.apiAddrResolver(serviceID)
}

// externalConsolePrefixes возвращает префиксы всех консолей, кроме собственного:
// ссылки на эти префиксы — адреса других сервисов внутри единого origin, и общий
// middleware их не переписывает.
func externalConsolePrefixes(all []string, own string) []string {
	out := make([]string, 0, len(all))
	for _, p := range all {
		if trimmed := strings.TrimSpace(p); trimmed != "" && trimmed != strings.TrimSpace(own) {
			out = append(out, p)
		}
	}
	return out
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
			ConsoleAuth:       consoleauth.Config{Username: opts.consoleUser, Password: opts.consolePassword},
			APIAddr:           opts.apiAddrFor(analytics.ServiceID),
			Consumer:          opts.consumer,
			DeliveryQueue:     opts.analyticsDeliveryQueue,
			Console:           opts.console,
			BasePath:          opts.consoleReportsBase,
			ExternalPrefixes:  externalConsolePrefixes(opts.consolePrefixes, opts.consoleReportsBase),
			Logger:            opts.logger,
			Metrics:           opts.metrics,
			Tracer:            tracer,
			FailureNormalizer: opts.failureNormalizer,
			// Контур обратной связи: рекомендация анализа применяется владельцем
			// контура поиска (контракт recommendation-apply). Незаданный порт
			// оставляет применение честно недоступным.
			RecommendationApply: opts.recommendationApply,
		}
		// Проекция дашборда и задания длительной выгрузки отчёта: PostgreSQL при
		// заданном DSN зелёной зоны, иначе in-memory dev. Витрина и результаты
		// переживают рестарт только с хранилищем.
		if dsn := strings.TrimSpace(opts.supportGreenDSN); dsn != "" {
			pool, err := supportPool(dsn, analytics.ServiceID, "backend/contexts/analytics/migrations")
			if err != nil {
				return nil, err
			}
			persistence := analytics.NewPostgresPersistence(pool)
			cfg.ProjectionStore = persistence.Projection
			cfg.ReportExports = persistence.Exports
			// Снимки качества поиска (lead-quality-insights) переживают рестарт:
			// без хранилища они остаются in-memory dev и предупреждают об этом.
			cfg.InsightStore = persistence.Insight
			if err := applyMigrations(pool, "backend/platform/consoleauth/migrations/green", consoleauth.Schema); err != nil {
				pool.Close()
				return nil, err
			}
			cfg.ConsoleAuth.Store = consoleauth.NewPostgresStore(pool)
		}
		return analytics.New(cfg)
	case outreachorchestration.ServiceID:
		// Признаки риска пакетного пути: предикат собран точкой входа поверх
		// контрактов владельцев (assembleRiskFacts). Если сборщик не собран (тесты,
		// сборка без владельцев), ставится честно недоступный предикат: пакетное
		// утверждение отвечает отказом, а не «рисков нет» (AGENTS.md §3).
		risk := opts.riskFacts
		if risk == nil {
			risk = outreachorchestration.NewRiskFacts(outreachorchestration.RiskFactsPorts{})
		}
		cfg := outreachorchestration.Config{
			APIAddr:   opts.apiAddrFor(outreachorchestration.ServiceID),
			Interval:  opts.publishInterval,
			Grants:    outreachorchestration.GrantsFromStrings(opts.actor, opts.tenant),
			Access:    opts.accessDecision,
			Risk:      risk,
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
		cfg.BasePath = opts.consoleOutreachBase
		cfg.ExternalPrefixes = externalConsolePrefixes(opts.consolePrefixes, opts.consoleOutreachBase)
		cfg.ConsoleAuth = consoleauth.Config{Username: opts.consoleUser, Password: opts.consolePassword}
		if opts.consoleUser == "" || opts.consolePassword == "" {
			opts.logger.Warn("outreach-orchestration: консоль рассылок работает с пустыми учётными данными (только dev)", observ.Fields{})
		}
		// Пакетный путь утверждения использует сборщик признаков риска поверх
		// владельцев (risk-screening). Если владелец недоступен, сборщик честно
		// отказывает, и пакетное утверждение не выдаётся — заглушки «рисков нет»
		// на продуктовом пути нет.
		opts.logger.Info("outreach-orchestration: сборщик признаков риска подключён (risk-screening)", observ.Fields{})
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
		var runtimePools *intakeRuntimePools
		registeredRuntimePools := false
		if opts.dbPolicy == intakeDBPolicyRuntimeOnly {
			if opts.runtimeDBResources == nil {
				return nil, errors.New("request-fulfillment: runtime pool registry не собран")
			}
			ops := opts.intakeRuntimeDBOps
			if ops.openPool == nil {
				ops = defaultIntakeRuntimeDBOps()
			}
			var err error
			runtimePools, err = openVerifiedIntakePools(context.Background(), opts.intakeRuntimeDSNs, ops)
			if err != nil {
				return nil, err
			}
			defer func() {
				if !registeredRuntimePools {
					runtimePools.close()
				}
			}()
			opts.intakeRuntimePools = runtimePools
		}
		leadSearch, err := leadSearchPort(opts)
		if err != nil {
			return nil, fmt.Errorf("request-fulfillment: порт поиска: %w", err)
		}
		emailGeneration, err := emailGenerationPort(opts)
		if err != nil {
			return nil, fmt.Errorf("request-fulfillment: порт генерации писем: %w", err)
		}
		contentRequest, err := contentRequestPort(opts)
		if err != nil {
			return nil, fmt.Errorf("request-fulfillment: порт материала писем: %w", err)
		}
		handoff, err := outreachHandoffPort(opts)
		if err != nil {
			return nil, fmt.Errorf("request-fulfillment: порт передачи писем: %w", err)
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
		if opts.dbPolicy == intakeDBPolicyRuntimeOnly {
			factory := opts.intakeRuntimeFactory.normalized()
			persist = factory.request(runtimePools.request)
			consoleStore = factory.console(runtimePools.consoleAuth)
		} else if strings.TrimSpace(opts.requestGreenDSN) != "" {
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

		cfg := requestfulfillment.Config{
			APIAddr:                       opts.apiAddrFor(requestfulfillment.ServiceID),
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
			ContentRequest:                contentRequest,
			Handoff:                       handoff,
			Vector:                        vectorPort,
			ConsoleUser:                   opts.consoleUser,
			ConsolePassword:               opts.consolePassword,
			ConsoleStore:                  consoleStore,
			Console:                       opts.console,
			BasePath:                      opts.consoleRequestsBase,
			ExternalPrefixes:              externalConsolePrefixes(opts.consolePrefixes, opts.consoleRequestsBase),
			Logger:                        opts.logger,
			Metrics:                       opts.metrics,
			Tracer:                        tracer,
		}
		// Подписка на события доставки (capability request-delivery-conclusion):
		// заявка завершается по наблюдаемому исходу доставки, а не ждёт вечно
		// сигнала консоли, которого в недоборном контуре нет. Источник — общий
		// потребитель брокера процесса; повтор дедуплицируется журналом входящих
		// событий (как у engagement-observation). Без брокера подписка не
		// собирается: заявка ждёт прежним способом, а не завершается выдуманно.
		if opts.consumer != nil {
			cfg.DeliveryEvents = opts.consumer
			cfg.DeliveryEventsDeduper = inbox.NewMemoryDeduper()
			cfg.DeliveryEventsQueue = opts.requestDeliveryQueue
		}
		service, err := requestfulfillment.New(cfg)
		if err != nil {
			return nil, err
		}
		if runtimePools != nil {
			if err := opts.runtimeDBResources.add(runtimePools); err != nil {
				return nil, err
			}
			registeredRuntimePools = true
		}
		return service, nil
	case agentorchestration.ServiceID:
		return buildAgentOrchestration(opts, tracer, broker)
	case platformgovernance.ServiceID:
		return buildPlatformGovernance(opts, tracer, broker)
	case integrationplatform.ServiceID:
		// Контекст сам собирает персистентность по DSN зелёной зоны: без DSN —
		// in-memory dev с предупреждением. Роль api отдаёт состояние объявленных
		// адаптеров (контракт adapter-status) — источник тот же, что у раздела
		// интеграций консоли, поэтому консоль не заводит своего реестра.
		return integrationplatform.New(integrationplatform.Config{
			APIAddr:  opts.apiAddrFor(integrationplatform.ServiceID),
			DSN:      opts.supportGreenDSN,
			Adapters: adapterStatusSource(opts),
			Logger:   opts.logger,
			Metrics:  opts.metrics,
			Tracer:   tracer,
		})
	case datagovernance.ServiceID:
		// Период фонового истечения полномочий оставляем доменному значению по
		// умолчанию (defaultSweepInterval): -publish-interval задаёт публикацию, а
		// не проход полномочий. Персистентность контекст собирает сам по DSN
		// зелёной зоны.
		return datagovernance.New(datagovernance.Config{
			APIAddr: opts.apiAddrFor(datagovernance.ServiceID),
			DSN:     opts.supportGreenDSN,
			Grants:  datagovernance.GrantsFromStrings(opts.actor, opts.tenant),
			Access:  opts.accessDecision,
			Logger:  opts.logger,
			Metrics: opts.metrics,
			Tracer:  tracer,
		})
	case audience.ServiceID:
		cfg := audience.Config{
			APIAddr:   opts.apiAddrFor(audience.ServiceID),
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
		cfg := contentproduction.Config{
			APIAddr:          opts.apiAddrFor(contentproduction.ServiceID),
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
		}
		// Движок сбора и разбора — Firecrawl (движок 3.1) владельца
		// integration-platform: один клиент реализует и сбор с сайта, и разбор
		// предоставленных документов. Без ключа оба порта остаются nil: сбор и разбор
		// честно недоступны, работа идёт на ручных материалах (не молчаливая подмена).
		if strings.TrimSpace(opts.firecrawlKey) != "" {
			fc, err := integrationplatform.NewFirecrawl(integrationplatform.FirecrawlConfig{
				APIKey:  opts.firecrawlKey,
				BaseURL: opts.firecrawlURL,
				Logger:  opts.logger,
			})
			if err != nil {
				return nil, fmt.Errorf("content-production: Firecrawl: %w", err)
			}
			cfg.SiteCollector = fc
			cfg.DocumentParser = fc
		}
		return contentproduction.New(cfg)
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
func buildPlatformGovernance(opts buildOptions, tracer observ.Tracer, broker amqp.Publisher) (composition.Service, error) {
	requests, err := requestIntentPort(opts)
	if err != nil {
		return nil, fmt.Errorf("platform-governance: порт заявок: %w", err)
	}
	cfg := platformgovernance.Config{
		APIAddr:   opts.apiAddrFor(platformgovernance.ServiceID),
		Requests:  requests,
		Report:    opts.report,
		Logger:    opts.logger,
		Metrics:   opts.metrics,
		Tracer:    tracer,
		Publisher: broker,
		// Базовые адреса сервисов общей консоли: объединённая навигация ведёт из
		// платформенной консоли в операторские разделы.
		Console: opts.console,
		// Внешний префикс консоли платформы под единым origin (admin-proxy).
		BasePath: opts.consolePlatformBase,
		// Префиксы операторских консолей: ссылки на них не переписываются.
		ExternalPrefixes: externalConsolePrefixes(opts.consolePrefixes, opts.consolePlatformBase),
	}
	if strings.TrimSpace(opts.requestExecutionsURL) != "" {
		reader, err := newRequestExecutionHTTPReader(opts.requestExecutionsURL)
		if err != nil {
			return nil, fmt.Errorf("platform-governance: request execution read source configuration is invalid")
		}
		cfg.RequestExecutions = reader
	}
	// Платформенный оператор — субъект, действующий во всех тенантах: пока это
	// оператор консолей (те же учётные данные), задаваемый конфигурацией. Пустые
	// учётные данные оставляют изоляцию без платформенного оператора.
	operator := strings.TrimSpace(opts.consoleUser)
	if operator != "" {
		cfg.Operators = []string{operator}
		operatorGrants, err := platformgovernance.OperatorGrants(operator)
		if err != nil {
			return nil, fmt.Errorf("platform-governance: права оператора: %w", err)
		}
		cfg.Grants = operatorGrants
	}
	// Передаём неполную пару в Build без потерь, чтобы context builder сам
	// fail-closed проверил оба компонента даже при прямом вызове runner builder.
	// Пароль — буквальная строка: допустимы пробелы, trim применяется только к логину.
	if operator != "" || len(opts.consolePassword) > 0 {
		cfg.ConsoleAuth = consoleauth.Config{Username: operator, Password: opts.consolePassword}
	}
	// Хранилище тенантов и пользователей: PostgreSQL по пулу зелёной зоны,
	// иначе in-memory dev. Миграции схемы применяет точка сборки.
	if dsn := strings.TrimSpace(opts.supportGreenDSN); dsn != "" {
		if opts.runtimeDBResources == nil {
			return nil, fmt.Errorf("platform-governance: runtime pool registry обязателен для владения PostgreSQL pool")
		}
		pool, err := supportPool(dsn, platformgovernance.ServiceID, "backend/contexts/platform-governance/migrations")
		if err != nil {
			return nil, err
		}
		if broker != nil {
			if _, ok := broker.(*amqp.RabbitMQBroker); !ok {
				pool.Close()
				return nil, fmt.Errorf("platform-governance: PostgreSQL outbox требует реальный RabbitMQ publisher с подтверждениями")
			}
		}
		if err := opts.runtimeDBResources.addServicePool(pool); err != nil {
			return nil, fmt.Errorf("platform-governance: регистрация PostgreSQL pool: %w", err)
		}
		if err := applyMigrations(pool, "backend/platform/consoleauth/migrations/green", consoleauth.Schema); err != nil {
			return nil, err
		}
		cfg.Pool = pool
		cfg.ConsoleAuth.Store = consoleauth.NewPostgresStore(pool)
	}
	// Раздел интеграций: состояние адаптеров отдаёт владелец
	// integration-platform; консоль не заводит собственного реестра адаптеров.
	// Co-location — источник владельца напрямую; раздельный запуск — сетевой
	// адаптер контракта adapter-status.
	catalog, err := integrationAdapterCatalog(opts)
	if err != nil {
		return nil, err
	}
	cfg.Adapters = catalog
	// Консоль топологии: граф строится из канонических источников, метрики — через
	// серверный прокси Prometheus. Пустые значения оставляют соответствующую часть
	// честно недоступной.
	if strings.TrimSpace(opts.topologyCatalogPath) != "" {
		cfg.Topology = topology.NewLoader(topology.Sources{
			CatalogPath:        opts.topologyCatalogPath,
			ServicesPath:       opts.topologyServicesPath,
			InfrastructurePath: opts.topologyInfrastructurePath,
		}, 30*time.Second)
	}
	if strings.TrimSpace(opts.prometheusURL) != "" {
		cfg.TopologyMetrics = prometheus.NewClient(opts.prometheusURL, nil)
	}
	// Экран операций: источник цифр/ленты/агрегатов собирается точкой входа из
	// read-поверхностей владельцев (agent-orchestration — лента и состояние
	// запусков; analytics — агрегаты процесса). Источник всегда не-nil: раздел
	// без подключённого владельца экран честно помечает «недоступно», а не
	// показывает нули (AGENTS.md §3, спека operations-cockpit).
	cfg.Operations = platformgovernance.NewOperationsSource(opts.operations)
	if opts.operations.Feed == nil && opts.operations.RunState == nil && opts.operations.Aggregates == nil {
		opts.logger.Warn("platform-governance: контуры экрана операций не подключены — цифры, лента и агрегаты честно недоступны", observ.Fields{}, "service", platformgovernance.ServiceID)
	}
	// Серверное чтение процессов Temporal для графа процессов. Метрики-панели по
	// умолчанию откатываются на TopologyMetrics (тот же Prometheus контура) —
	// отдельного второго клиента не заводим.
	if opts.workflowClient != nil {
		cfg.WorkflowReader = opts.workflowClient
	} else {
		opts.logger.Warn("platform-governance: рантайм Temporal не подключён — граф процессов экрана честно недоступен", observ.Fields{}, "service", platformgovernance.ServiceID)
	}
	return platformgovernance.New(cfg)
}

// buildAgentOrchestration собирает сервис agent-orchestration: персистентность по
// DSN зелёной зоны (иначе in-memory dev), порт опубликованного контракта lead-search
// поверх которого собирается реестр исполнителей блоков, издатель событий,
// клиент Temporal и решение о доступе. Роли api/worker/chains/schedule регистрирует
// сам контекст; здесь только соединение адаптеров.
//
// Честность (AGENTS.md §3): без DSN состояние живёт в памяти процесса; без
// lead-search шаг поиска недоступен; без Temporal роли worker/chains не поднимают
// воркер, а запуски остаются в очереди. Реестр объявленных блоков задаётся
// публичным API контекста (DefaultBlocks); при включённом флаге точка входа
// разово публикует первый набор шагов пайплайна для тенантов расписания.
func buildAgentOrchestration(opts buildOptions, tracer observ.Tracer, broker amqp.Publisher) (composition.Service, error) {
	leadSearch, err := leadSearchPort(opts)
	if err != nil {
		return nil, fmt.Errorf("agent-orchestration: порт поиска: %w", err)
	}
	cfg := agentorchestration.Config{
		APIAddr:     opts.apiAddrFor(agentorchestration.ServiceID),
		LeadSearch:  leadSearch,
		SearchLimit: opts.agentSearchLimit,

		// Порты владельцев для блоков: оценка релевантности (read-контракт
		// lead-relevance), производство материала (content-request) и язык
		// материала. Незаданный порт оставляет блок честно недоступным.
		LeadRelevance:   opts.leadRelevance,
		ContentRequest:  opts.agentContentRequest,
		ContentLanguage: opts.agentContentLanguage,

		// Необратимая отправка: издатель approved-action и источник утверждённого
		// действия. Источника решения (read-контракт) владелец не публикует,
		// поэтому блок отправки остаётся честно недоступен.
		ApprovalSubmitter:    opts.approvalSubmitter,
		ApprovedAction:       opts.approvedAction,
		ApprovedActionSource: opts.approvedActionSource,

		WorkflowClient:    opts.workflowClient,
		WorkflowTaskQueue: opts.agentWorkflowTaskQueue,
		Runtime:           opts.runtime,

		Publisher: broker,
		Interval:  opts.publishInterval,

		MaxConcurrent: opts.agentMaxConcurrent,
		Access:        opts.accessDecision,

		ScheduleTenants: opts.agentScheduleTenants,

		ApprovalTTL:          opts.approvalTTL,
		ExecutionTimeout:     opts.executionTimeout,
		ActivityStartToClose: opts.longTimeout,
		ActivityMaxAttempts:  opts.longAttempts,

		WorkerStopTimeout:             opts.workerStopTimeout,
		WorkerMaxConcurrentActivities: opts.workerMaxActivities,
		WorkerMaxConcurrentWorkflows:  opts.workerMaxWorkflows,

		Logger:  opts.logger,
		Metrics: opts.metrics,
		Tracer:  tracer,
	}

	// Реестр объявленных блоков первого набора (design.md, Реш. 3/16): без него
	// публикация версии пайплайна отвергается неизвестным блоком. Состав — данные
	// контекста, а не ветвление точки входа.
	blocks, err := agentorchestration.DefaultBlocks()
	if err != nil {
		return nil, fmt.Errorf("agent-orchestration: реестр объявленных блоков: %w", err)
	}
	cfg.Blocks = blocks

	// Персистентность по DSN зелёной зоны: пайплайны, запуски и журнал outbox
	// переживают рестарт процесса. Без DSN — in-memory dev с честным предупреждением.
	if dsn := strings.TrimSpace(opts.supportGreenDSN); dsn != "" {
		pool, err := supportPool(dsn, agentorchestration.ServiceID, "backend/contexts/agent-orchestration/migrations/green")
		if err != nil {
			return nil, err
		}
		cfg.Persistence = agentorchestration.NewPostgresPersistence(pool)
	} else {
		opts.logger.Warn("agent-orchestration: состояние в памяти процесса (dev): DSN зелёной зоны не задан, пайплайны и запуски не переживут рестарт", observ.Fields{}, "service", agentorchestration.ServiceID)
	}
	if leadSearch == nil {
		opts.logger.Warn("agent-orchestration: контракт lead-search не подключён — блок поиска честно недоступен", observ.Fields{}, "service", agentorchestration.ServiceID)
	}
	if opts.leadRelevance == nil {
		opts.logger.Warn("agent-orchestration: контракт lead-relevance не подключён — блок classify честно недоступен", observ.Fields{}, "service", agentorchestration.ServiceID)
	}
	if opts.agentContentRequest == nil {
		opts.logger.Warn("agent-orchestration: контракт content-request не подключён — блок производства материала честно недоступен", observ.Fields{}, "service", agentorchestration.ServiceID)
	}
	if opts.approvalSubmitter == nil || opts.approvedActionSource == nil {
		opts.logger.Warn("agent-orchestration: контракты утверждения/источника решения не подключены — блоки await-approval и send честно недоступны", observ.Fields{}, "service", agentorchestration.ServiceID)
	}
	if opts.workflowClient == nil {
		opts.logger.Warn("agent-orchestration: рантайм Temporal не подключён — роли worker/chains не поднимают воркер, запуски остаются в очереди", observ.Fields{}, "service", agentorchestration.ServiceID)
	}
	if opts.accessDecision == nil {
		opts.logger.Warn("agent-orchestration: решение о доступе не собрано — HTTP-поверхность отвечает отказом", observ.Fields{}, "service", agentorchestration.ServiceID)
	}
	service, err := agentorchestration.New(cfg)
	if err != nil {
		return nil, err
	}
	if opts.agentBootstrapPipeline {
		bootstrapAgentPipeline(opts.agentScheduleTenants, service, opts.logger)
	}
	return service, nil
}

// agentDefaultPipelineID — идентификатор первого набора шагов пайплайна агента,
// публикуемого точкой входа при включённом флаге -agent-bootstrap-pipeline. Состав
// шагов берётся из DefaultPipelineSteps (данные контекста), а не выдумывается.
const agentDefaultPipelineID = "company-agent-pipeline"

// agentBootstrapTimeout ограничивает разовую публикацию первого пайплайна: сбойная
// база не должна задерживать старт процесса больше этого срока.
const agentBootstrapTimeout = 30 * time.Second

// bootstrapAgentPipeline разово публикует первый набор шагов пайплайна агента для
// заданных тенантов, чтобы запуск агента не упирался в отсутствие опубликованной
// версии. Публикация идёт публичной точкой входа владельца (Service.PublishPipeline)
// поверх реестра блоков первого набора (Config.Blocks).
//
// Ошибка не маскируется: повторная публикация уже выпущенной версии отвергается
// доменом (неизменяемость), и на перезапуске это ожидаемо — факт сообщается как
// есть, а не выдаётся за успешную публикацию (AGENTS.md §3).
func bootstrapAgentPipeline(tenants []string, service *agentorchestration.Service, logger observ.Logger) {
	unique := make([]string, 0, len(tenants))
	seen := make(map[string]bool, len(tenants))
	for _, tenant := range tenants {
		trimmed := strings.TrimSpace(tenant)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		unique = append(unique, trimmed)
	}
	if len(unique) == 0 {
		logger.Warn("agent-orchestration: тенанты публикации первого пайплайна не заданы — версия не опубликована", observ.Fields{}, "service", agentorchestration.ServiceID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), agentBootstrapTimeout)
	defer cancel()
	steps := agentorchestration.DefaultPipelineSteps()
	for _, tenant := range unique {
		result, err := service.PublishPipeline(ctx, agentorchestration.PublishPipelineRequest{
			PipelineID: agentDefaultPipelineID,
			TenantID:   tenant,
			Version:    1,
			Steps:      steps,
			Actor:      "system",
		})
		if err != nil {
			logger.Warn("agent-orchestration: первый пайплайн не опубликован (возможно, версия уже выпущена)", observ.Fields{TenantID: tenant}, "service", agentorchestration.ServiceID, "error", err.Error())
			continue
		}
		logger.Info("agent-orchestration: первый пайплайн опубликован", observ.Fields{TenantID: tenant}, "service", agentorchestration.ServiceID, "version", result.Version)
	}
}

// agentRunFeed адаптирует read-поверхность запусков agent-orchestration
// (agentrunstate.Lister) к ленте экрана операций (platformgovernance.AgentRunFeed).
// Используется для сетевого клиента контракта; при co-location применяется
// agentRunSource.
type agentRunFeed struct {
	lister agentrunstate.Lister
}

// Runs реализует platformgovernance.AgentRunFeed. Без подключённого Lister лента
// честно недоступна, а не подменяется пустым успехом.
func (f agentRunFeed) Runs(ctx context.Context, query platformgovernance.AgentRunQuery) ([]platformgovernance.AgentRunSummary, error) {
	if f.lister == nil {
		return nil, platformgovernance.ErrOperationsUnavailable
	}
	return runRows(ctx, f.lister, query)
}

// agentRunSource — ленивая read-поверхность запусков co-located владельца. Сервис
// agent-orchestration собирается точкой входа позже, чем сам источник (после
// связывания решения доступа), поэтому поверхность берётся при чтении, а не в
// момент сборки. Реализует и ленту экрана, и состояние запуска.
type agentRunSource struct {
	buildOnce func(selectedService) (composition.Service, error)
	sel       selectedService
}

// model разрешает read-поверхность владельца по требованию.
func (s *agentRunSource) model() (agentorchestration.RunReadModel, error) {
	service, err := s.buildOnce(s.sel)
	if err != nil {
		return nil, err
	}
	owner, ok := service.(*agentorchestration.Service)
	if !ok {
		return nil, fmt.Errorf("agent-orchestration: владелец собран не тем типом сервиса: %T", service)
	}
	return owner.RunReadModel(), nil
}

// Runs реализует platformgovernance.AgentRunFeed.
func (s *agentRunSource) Runs(ctx context.Context, query platformgovernance.AgentRunQuery) ([]platformgovernance.AgentRunSummary, error) {
	model, err := s.model()
	if err != nil {
		return nil, err
	}
	return runRows(ctx, model, query)
}

// RunState реализует agentrunstate.Reader.
func (s *agentRunSource) RunState(ctx context.Context, request agentrunstate.Request) (agentrunstate.RunState, error) {
	model, err := s.model()
	if err != nil {
		return agentrunstate.RunState{}, err
	}
	return model.RunState(ctx, request)
}

// runRows переводит сводки запусков владельца в строки ленты экрана: идентификаторы,
// код состояния и текущий шаг. Код категории процесса и отдельный client_id
// контрактом agent-run-state не отдаются и здесь не выдумываются: выборка
// тенант-скоупна, а client_id в этой платформе совпадает с tenant_id.
func runRows(ctx context.Context, lister agentrunstate.Lister, query platformgovernance.AgentRunQuery) ([]platformgovernance.AgentRunSummary, error) {
	summaries, err := lister.List(ctx, agentrunstate.ListRequest{
		TenantID: query.TenantID,
		Limit:    query.Limit,
	})
	if err != nil {
		return nil, err
	}
	runs := make([]platformgovernance.AgentRunSummary, 0, len(summaries))
	for _, summary := range summaries {
		step := summary.CurrentStep
		if strings.TrimSpace(step) == "" {
			step = summary.CurrentBlock
		}
		runs = append(runs, platformgovernance.AgentRunSummary{
			RunID:       summary.RunID,
			TenantID:    summary.TenantID,
			CompanyID:   summary.CompanyID,
			State:       summary.Status,
			CurrentStep: step,
			UpdatedAt:   summary.UpdatedAt,
		})
	}
	return runs, nil
}

// analyticsOperationsPort — ленивый campaignreport.Port co-located владельца
// analytics. Сервис analytics собирается точкой входа позже, чем источник экрана
// операций (после связывания решения доступа), поэтому опубликованный read-контракт
// берётся при чтении, а не в момент сборки: иначе сборка analytics внутри
// связывания источника создала бы цикл с решением доступа. Реализует и цифры
// процесса, и агрегаты по клиентам.
type analyticsOperationsPort struct {
	buildOnce func(selectedService) (composition.Service, error)
	sel       selectedService
}

// port разрешает опубликованный read-контракт владельца по требованию.
func (p *analyticsOperationsPort) port() (campaignreport.Port, error) {
	service, err := p.buildOnce(p.sel)
	if err != nil {
		return nil, err
	}
	owner, ok := service.(*analytics.Service)
	if !ok {
		return nil, fmt.Errorf("analytics: владелец собран не тем типом сервиса: %T", service)
	}
	return owner.OperationsPort(), nil
}

// Numbers реализует campaignreport.Port.
func (p *analyticsOperationsPort) Numbers(ctx context.Context, request campaignreport.Request) (campaignreport.NumbersResponse, error) {
	port, err := p.port()
	if err != nil {
		return campaignreport.NumbersResponse{}, err
	}
	return port.Numbers(ctx, request)
}

// Clients реализует campaignreport.Port.
func (p *analyticsOperationsPort) Clients(ctx context.Context, request campaignreport.Request) (campaignreport.ClientsResponse, error) {
	port, err := p.port()
	if err != nil {
		return campaignreport.ClientsResponse{}, err
	}
	return port.Clients(ctx, request)
}

// adapterStatusSource объявляет адаптеры интеграционной платформы точке сборки:
// имя, признак конфигурации и код причины не в норме. Свободного текста и данных
// доступа здесь нет, поэтому секреты не могут попасть в раздел консоли.
func adapterStatusSource(opts buildOptions) *integrationplatform.AdapterStatusSource {
	firecrawlProbe := integrationplatform.NewFirecrawlProbe(integrationplatform.FirecrawlConfig{
		APIKey: opts.firecrawlKey, BaseURL: opts.firecrawlURL,
	})
	llmProbe := integrationplatform.NewLLMProbe(integrationplatform.OpenAIConfig{
		BaseURL: opts.llmBaseURL, Model: opts.llmModel, APIKey: opts.llmKey,
	})
	smtpProbe := integrationplatform.NewSMTPProbe(integrationplatform.SmtpConfig{
		Host: opts.smtpHost, Port: opts.smtpPort, Username: opts.smtpUsername, Password: opts.smtpPassword,
	})
	return integrationplatform.NewAdapterStatusSource(integrationplatform.AdapterStatusConfig{
		Adapters: []integrationplatform.AdapterSpec{
			{Name: "firecrawl", Configured: strings.TrimSpace(opts.firecrawlKey) != "", Probe: firecrawlProbe, Reason: integrationplatform.AdapterReasonMisconfigured},
			{Name: "llm", Configured: strings.TrimSpace(opts.llmKey) != "", Probe: llmProbe, Reason: integrationplatform.AdapterReasonMisconfigured},
			{Name: "smtp", Configured: strings.TrimSpace(opts.smtpHost) != "", Probe: smtpProbe, Reason: integrationplatform.AdapterReasonMisconfigured},
		},
	})
}

// integrationAdapterCatalog собирает источник состояния адаптеров для раздела
// интеграций консоли. При заданном адресе integration-platform используется
// сетевой адаптер контракта adapter-status; иначе — источник владельца in-proc
// (co-location). Источник отдаёт только значения закрытых словарей, поэтому
// секреты в раздел не попадают.
func integrationAdapterCatalog(opts buildOptions) (platformgovernance.AdapterCatalog, error) {
	if url := strings.TrimSpace(opts.integrationPlatformURL); url != "" {
		client, err := adapterstatushttp.New(url)
		if err != nil {
			return nil, fmt.Errorf("platform-governance: сетевой источник состояния адаптеров: %w", err)
		}
		return adapterCatalog{statuses: client.Statuses}, nil
	}
	source := adapterStatusSource(opts)
	return adapterCatalog{statuses: func(ctx context.Context) ([]adapterstatus.Status, error) {
		return source.AdapterStatuses(ctx, "")
	}}, nil
}

// adapterCatalog связывает источник состояния адаптеров владельца с портом
// консоли platform-governance. Источник — функция, поэтому co-location и сетевой
// вызов подставляются одной обёрткой; поля переносятся один в один.
type adapterCatalog struct {
	statuses func(context.Context) ([]adapterstatus.Status, error)
}

func (a adapterCatalog) States(ctx context.Context) ([]platformgovernance.AdapterStatus, error) {
	if a.statuses == nil {
		return nil, errors.New("источник состояния адаптеров не подключён")
	}
	statuses, err := a.statuses(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]platformgovernance.AdapterStatus, 0, len(statuses))
	for _, status := range statuses {
		out = append(out, platformgovernance.AdapterStatus{
			Adapter: status.Adapter,
			State:   status.State,
			Reason:  status.Reason,
		})
	}
	return out, nil
}

// requestIntentPort выбирает адаптер контракта request-intent по топологии:
// задан адрес сервиса заявок — сетевой HTTP-адаптер; иначе in-proc (co-location).
// contentRequestPort собирает порт контракта content-request (владелец —
// content-production) для производства материала письма. Пустой адрес — порт не
// собран: передача писем честно недоступна, а не подменяется выдуманным материалом.
func contentRequestPort(opts buildOptions) (contentrequest.Port, error) {
	if addr := strings.TrimSpace(opts.contentRequestURL); addr != "" {
		return contentrequesthttp.New(addr)
	}
	return nil, nil
}

// outreachHandoffPort собирает порт контракта outreach-handoff (потребитель —
// outreach-orchestration) для передачи писем заявки на утверждение. Пустой адрес —
// порт не собран: передача честно недоступна, а не выдаётся за выполненную.
func outreachHandoffPort(opts buildOptions) (outreachhandoff.Port, error) {
	if addr := strings.TrimSpace(opts.outreachHandoffURL); addr != "" {
		return outreachhandoffhttp.New(addr)
	}
	return nil, nil
}

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

// assembleApprovedActionBridge выбирает транспорт связывания утверждённого
// действия с message-delivery по топологии процесса (live-e2e-contour, Фаза 2):
//
//   - задан -approved-action-url — утверждённые действия идут сетевым клиентом
//     в отдельный процесс message-delivery; in-proc мост здесь не собирается;
//   - иначе — in-proc мост (co-location), как прежде: резолвер получателя и
//     источник материала берутся из co-located lead-management/content-production
//     либо из сетевых адаптеров, если заданы их адреса.
//
// Возвращаемые sink и bridge могут быть nil: nil-sink означает прежнее
// поведение (публикация вне этого пути), nil-bridge — что мост не собирается
// (нет владельца получателя, нет sender-domain или message-delivery не в этом
// процессе). Заглушка на место моста не ставится — действие либо идёт к
// реальному потребителю, либо публикация остаётся прежней.
func assembleApprovedActionBridge(
	selected []selectedService,
	opts buildOptions,
	buildOnce func(selectedService) (composition.Service, error),
	logger observ.Logger,
) (outreachorchestration.SinkFunc, *messagedelivery.ApprovedActionBridge, error) {
	// Раздельные процессы: outreach публикует сетевым клиентом.
	if url := strings.TrimSpace(opts.approvedActionURL); url != "" {
		client, err := approvedactionhttp.New(url)
		if err != nil {
			return nil, nil, fmt.Errorf("approved-action: сетевой клиент: %w", err)
		}
		return outreachorchestration.SinkFunc(client.Publish), nil, nil
	}

	// Co-location: мост читает sender. Если message-delivery не в этом процессе,
	// мост некому читать — публикация остаётся прежней (AMQP/брокер).
	if _, ok := findSelected(selected, messagedelivery.ServiceID); !ok {
		return nil, nil, nil
	}

	// Резолвер получателя: сетевой адаптер либо co-located lead-management.
	var recipients deliverytarget.Resolver
	switch {
	case strings.TrimSpace(opts.deliveryTargetURL) != "":
		client, err := deliverytargethttp.New(opts.deliveryTargetURL)
		if err != nil {
			return nil, nil, fmt.Errorf("delivery-target: сетевой клиент: %w", err)
		}
		recipients = client
	default:
		if lm, ok := findSelected(selected, leadmanagement.ServiceID); ok {
			service, err := buildOnce(lm)
			if err != nil {
				return nil, nil, err
			}
			if lms, ok := service.(*leadmanagement.Service); ok {
				recipients = lms.DeliveryTargetResolver(opts.tenant)
			}
		}
	}
	if recipients == nil {
		logger.Warn("lead-gen: владелец получателя недоступен — мост approved-action не собран", observ.Fields{})
		return nil, nil, nil
	}

	// Источник материала: сетевой адаптер либо co-located content-production.
	var contentSource contentversion.Source
	switch {
	case strings.TrimSpace(opts.contentVersionURL) != "":
		client, err := contentversionhttp.New(opts.contentVersionURL)
		if err != nil {
			return nil, nil, fmt.Errorf("content-version: сетевой клиент: %w", err)
		}
		contentSource = client
	default:
		if cp, ok := findSelected(selected, contentproduction.ServiceID); ok {
			service, err := buildOnce(cp)
			if err != nil {
				return nil, nil, err
			}
			if cps, ok := service.(*contentproduction.Service); ok {
				contentSource = contentproduction.NewContentVersion(cps.Slice())
			}
		}
	}

	domain := senderDomainFromAddr(opts.smtpFrom)
	if domain == "" {
		logger.Warn("lead-gen: sender-domain не выведен из smtp-from — мост approved-action не собран", observ.Fields{})
		return nil, nil, nil
	}

	materials := messagedelivery.NewContentMaterialResolver(contentSource)
	var bridge *messagedelivery.ApprovedActionBridge
	var err error
	if dsn := strings.TrimSpace(opts.supportGreenDSN); dsn != "" {
		// Durable inbox и message-delivery service используют одну владельческую
		// схему. Повторное применение миграций сериализуется migrate.Apply и
		// безопасно при отдельном старте процессов.
		pool, poolErr := supportPool(dsn, messagedelivery.ServiceID, "backend/contexts/message-delivery/migrations/green")
		if poolErr != nil {
			return nil, nil, fmt.Errorf("message-delivery: PostgreSQL inbox: %w", poolErr)
		}
		bridge, err = messagedelivery.NewPostgresApprovedActionBridge(pool, recipients, materials, domain)
	} else {
		if opts.dbPolicy == intakeDBPolicyRuntimeOnly {
			return nil, nil, errors.New("message-delivery: runtime-only требует подключённый PostgreSQL inbox pool")
		}
		logger.Warn("message-delivery: PostgreSQL inbox не задан — approved-action queue доступна только в dev memory-режиме", observ.Fields{})
		bridge, err = messagedelivery.NewApprovedActionBridge(recipients, materials, domain)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("мост approved-action → delivery-command: %w", err)
	}
	logger.Info("lead-gen: мост approved-action → delivery-command собран", observ.Fields{}, "sender_domain", domain)
	return outreachorchestration.SinkFunc(bridge.Sink()), bridge, nil
}

// assembleAccessDecision собирает решение о доступе для потребителей контракта
// access-decision (outreach-orchestration, data-governance) из владельца
// platform-governance. Выбор транспорта задаётся топологией:
//
//   - задан адрес владельца — решение идёт сетевым клиентом контракта (сервисы
//     разделены по процессам);
//   - адрес пуст, владелец выбран — его срез связывается in-process адаптером
//     (co-location);
//   - владелец не выбран или не собран — честный недоступный решатель:
//     потребители отвечают отказом, а не получают выдуманное разрешение.
//
// Значение записывается в opts.accessDecision и не маскируется под успех.
func assembleAccessDecision(
	selected []selectedService,
	opts *buildOptions,
	buildOnce func(selectedService) (composition.Service, error),
	logger observ.Logger,
) {
	unavailable := func(reason string) {
		opts.accessDecision = accessdecision.Unavailable{}
		logger.Warn("lead-gen: решение о доступе недоступно — потребители отвечают отказом: "+reason, observ.Fields{})
	}

	// Сеть: владелец в другом контейнере. Адрес задаётся флагом/ENV.
	if addr := strings.TrimSpace(opts.accessDecisionURL); addr != "" {
		client, err := accessDecisionNetworkPort(addr)
		if err != nil {
			unavailable("сборка сетевого клиента контракта access-decision")
			logger.Error("lead-gen: сетевой клиент решения о доступе", err, observ.Fields{}, "addr", addr)
			return
		}
		opts.accessDecision = client
		logger.Info("lead-gen: решение о доступе подключено к владельцу platform-governance по сети", observ.Fields{}, "addr", addr)
		return
	}

	sel, ok := findSelected(selected, platformgovernance.ServiceID)
	if !ok {
		unavailable("владелец platform-governance не выбран к запуску")
		return
	}
	service, err := buildOnce(sel)
	if err != nil {
		unavailable("сборка владельца завершилась ошибкой")
		logger.Error("lead-gen: сборка владельца доступа", err, observ.Fields{}, "service", platformgovernance.ServiceID)
		return
	}
	owner, ok := service.(*platformgovernance.Service)
	if !ok {
		unavailable("владелец platform-governance собран не тем типом сервиса")
		return
	}
	bound, err := accessdecisioninproc.Bind(platformgovernance.NewAccessDecision(owner.Slice()))
	if err != nil {
		unavailable("связывание контракта access-decision")
		return
	}
	opts.accessDecision = bound
	logger.Info("lead-gen: решение о доступе подключено к владельцу platform-governance", observ.Fields{})
}

// assembleOperationsSource собирает read-поверхности владельцев экрана операций
// (platform-governance) до сборки самого сервиса. Источников два, и транспорт
// каждого выбирается по топологии процесса независимо:
//
//   - запуски агентов (лента и состояние) — владелец agent-orchestration;
//   - агрегаты процесса (цифры и агрегаты по клиентам) — владелец analytics.
//
// Для каждого источника: задан адрес владельца — он читается сетевым клиентом
// контракта (раздельные сервисы); адрес пуст, владелец выбран — read-поверхность
// связывается in-proc при co-location лениво (сервис собирается позже, после
// связывания решения доступа); иначе поверхность остаётся незаданной, и
// соответствующий раздел экрана честно недоступен, а не показывает нули
// (AGENTS.md §3).
func assembleOperationsSource(
	selected []selectedService,
	opts *buildOptions,
	buildOnce func(selectedService) (composition.Service, error),
	logger observ.Logger,
) {
	assembleAgentRunSource(selected, opts, buildOnce, logger)
	assembleAggregatesSource(selected, opts, buildOnce, logger)
}

// assembleAgentRunSource связывает ленту и состояние запусков владельца
// agent-orchestration (контракт agent-run-state).
func assembleAgentRunSource(
	selected []selectedService,
	opts *buildOptions,
	buildOnce func(selectedService) (composition.Service, error),
	logger observ.Logger,
) {
	// Сеть: владелец состояния запусков в другом контейнере.
	if addr := strings.TrimSpace(opts.agentOrchestrationURL); addr != "" {
		client, err := agentrunstatehttp.New(addr)
		if err != nil {
			logger.Error("lead-gen: сетевой клиент состояния агентов", err, observ.Fields{}, "addr", addr)
			return
		}
		opts.operations.Feed = agentRunFeed{lister: client}
		opts.operations.RunState = client
		logger.Info("lead-gen: экран операций читает состояние агентов по сети", observ.Fields{}, "addr", addr)
		return
	}

	sel, ok := findSelected(selected, agentorchestration.ServiceID)
	if !ok {
		logger.Warn("lead-gen: владелец agent-orchestration не выбран — лента и состояние запусков экрана операций честно недоступны", observ.Fields{})
		return
	}
	// Co-location: поверхность разрешается при чтении. Сборка владельца здесь ещё
	// не выполнена — точка входа собирает сервис после связывания решения доступа,
	// поэтому сервис берётся через общий кэш сборки в момент запроса.
	source := &agentRunSource{buildOnce: buildOnce, sel: sel}
	opts.operations.Feed = source
	opts.operations.RunState = source
	logger.Info("lead-gen: экран операций читает состояние агентов от владельца (co-location)", observ.Fields{})
}

// assembleAggregatesSource связывает агрегаты процесса владельца analytics
// (опубликованный контракт campaign-report: цифры процесса и агрегаты по
// клиентам). Сетевой путь — при заданном адресе analytics; иначе co-location
// ленивый; иначе Aggregates остаётся незаданным, и разделы цифр и полноэкранного
// дашборда честно недоступны, а не показывают нули (AGENTS.md §3).
func assembleAggregatesSource(
	selected []selectedService,
	opts *buildOptions,
	buildOnce func(selectedService) (composition.Service, error),
	logger observ.Logger,
) {
	// Сеть: владелец агрегатов в другом контейнере.
	if addr := strings.TrimSpace(opts.analyticsURL); addr != "" {
		client, err := campaignreporthttp.New(addr)
		if err != nil {
			logger.Error("lead-gen: сетевой клиент агрегатов аналитики", err, observ.Fields{}, "addr", addr)
			return
		}
		opts.operations.Aggregates = platformgovernance.NewOperationsAggregates(client)
		opts.report = client
		logger.Info("lead-gen: экран операций читает агрегаты процесса по сети", observ.Fields{}, "addr", addr)
		return
	}

	sel, ok := findSelected(selected, analytics.ServiceID)
	if !ok {
		logger.Warn("lead-gen: владелец analytics не выбран — цифры процесса и агрегаты по клиентам экрана операций честно недоступны", observ.Fields{})
		return
	}
	// Co-location: опубликованный контракт разрешается при чтении (см.
	// analyticsOperationsPort): сборка analytics происходит после связывания
	// источника, поэтому порт берётся через общий кэш сборки в момент запроса.
	port := &analyticsOperationsPort{buildOnce: buildOnce, sel: sel}
	opts.operations.Aggregates = platformgovernance.NewOperationsAggregates(port)
	opts.report = port
	logger.Info("lead-gen: экран операций читает агрегаты процесса от владельца (co-location)", observ.Fields{})
}

// vectorRecallPort собирает векторное хранилище за provider-port (контракт vector):
// реальный эмбеддер поверх OpenAI-совместимого endpoint и персистентный индекс
// integration-platform (schema-per-service). Без DSN зелёной зоны или без ключа
// провайдера эмбеддингов порт не собирается (nil): потребитель честно сообщает о
// недоступности, а детерминированная подмена на продуктовом пути не ставится.
func vectorRecallPort(opts buildOptions) (vector.Port, error) {
	if opts.dbPolicy == intakeDBPolicyRuntimeOnly {
		if opts.intakeRuntimePools == nil || opts.intakeRuntimePools.vector == nil {
			return nil, errors.New("runtime-only intake требует проверенный vector runtime pool")
		}
		return vectorPortWithPool(opts, opts.intakeRuntimePools.vector, true)
	}
	dsn := strings.TrimSpace(opts.supportGreenDSN)
	// Эмбеддинги отделены от чат-LLM (embedding-config-separation): провайдер
	// эмбеддингов задаётся своим ключом. Чат-провайдер без эмбеддингов (DeepSeek)
	// больше не ломает шаг одобрения: без ключа эмбеддингов recall честно недоступен.
	key := strings.TrimSpace(opts.embeddingKey)
	if dsn == "" || key == "" {
		return nil, nil
	}
	pool, err := supportPool(dsn, integrationplatform.ServiceID, "backend/contexts/integration-platform/migrations")
	if err != nil {
		return nil, err
	}
	port, err := vectorPortWithPool(opts, pool, false)
	if err != nil {
		pool.Close()
		return nil, err
	}
	return port, nil
}

func vectorPortWithPool(opts buildOptions, pool *pgxpool.Pool, required bool) (vector.Port, error) {
	key := strings.TrimSpace(opts.embeddingKey)
	if key == "" {
		if required {
			return nil, errors.New("runtime-only intake требует credentials провайдера embeddings")
		}
		return nil, nil
	}
	embedder, err := integrationplatform.NewVectorEmbedder(integrationplatform.VectorEmbedderConfig{
		BaseURL: opts.embeddingBaseURL,
		Model:   opts.embeddingModel,
		APIKey:  key,
	})
	if err != nil {
		return nil, err
	}
	if required {
		return opts.intakeRuntimeFactory.normalized().vector(pool, embedder), nil
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
		APIKey:        opts.firecrawlKey,
		BaseURL:       opts.firecrawlURL,
		Logger:        opts.logger,
		CrawlInterval: opts.firecrawlCrawlInterval,
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
	// Сессии отделены от лидов: общая support DB позволяет принимать вход
	// платформенной консоли при раздельных базах контекстов.
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
	if dsn := strings.TrimSpace(opts.supportGreenDSN); dsn != "" {
		pool, err := newServicePool(dsn, consoleauth.Schema)
		if err != nil {
			return nil, err
		}
		if err := applyMigrations(pool, "backend/platform/consoleauth/migrations/green", consoleauth.Schema); err != nil {
			pool.Close()
			return nil, err
		}
		if opts.runtimeDBResources == nil {
			pool.Close()
			return nil, errors.New("lead-management: runtime pool registry обязателен для общей базы сессий")
		}
		if err := opts.runtimeDBResources.addServicePool(pool); err != nil {
			return nil, err
		}
		consoleStore = consoleauth.NewPostgresStore(pool)
	}

	// Та же сборка хранилища публикуется для co-location-контрактов
	// (lead-relevance, recommendation-apply): потребители читают те же адаптеры,
	// что и сам сервис, а не второй экземпляр (особенно вторую in-memory базу).
	opts.leadStore.put(repos)

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
		APIAddr:                 opts.apiAddrFor(leadmanagement.ServiceID),
		Profiles:                repos.Profiles,
		Contacts:                repos.Contacts,
		Jobs:                    repos.Jobs,
		Messages:                repos.Messages,
		Templates:               repos.Templates,
		Activity:                repos.Activity,
		Assessments:             repos.Assessments,
		Events:                  leadmanagement.NewAnalyticsPublisher(broker, opts.tenant),
		AddressVerifierEndpoint: opts.addressVerifierURL,
		AddressVerifierAPIKey:   opts.addressVerifierKey,
		TenantID:                opts.tenant,
		Search:                  firecrawlClient,
		Crawl:                   firecrawlClient,
		Budget:                  firecrawlBudget,
		FirecrawlMaxCredits:     opts.firecrawlMaxCredits,
		LeadSearchMaxResults:    opts.leadSearchMaxResults,
		LeadSearchDeadline:      opts.leadSearchDeadline,
		Account:                 firecrawlAccount,
		LLM:                     llmPort,
		Vector:                  vectorPort,
		DialogueMaxTurns:        opts.dialogueMaxTurns,
		Forum:                   forumClient,
		Mail:                    smtpClient,
		MailFrom:                opts.smtpFrom,
		ConsoleUser:             opts.consoleUser,
		ConsolePassword:         opts.consolePassword,
		ConsoleStore:            consoleStore,
		Console:                 opts.console,
		BasePath:                opts.consoleLeadsBase,
		ExternalPrefixes:        externalConsolePrefixes(opts.consolePrefixes, opts.consoleLeadsBase),
		Logger:                  opts.logger,
		Metrics:                 opts.metrics,
		Tracer:                  tracer,
		FailureNormalizer:       opts.failureNormalizer,
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

	// Приём сетевого контракта approved-action (роль worker): при разделении
	// процессов утверждённые действия outreach доходят до того же моста, который
	// читает sender. Пустой адрес — приём не поднимается (co-location: in-proc).
	if addr := strings.TrimSpace(opts.approvedActionIntakeAddr); addr != "" && opts.bridge != nil {
		cfg.IntakeAddr = addr
		cfg.Intake = opts.bridge.Publisher()
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

// applyMigrations применяет миграции схемы сервиса к пулу. Принадлежность схеме,
// межпроцессную сериализацию и учёт уже применённых версий обеспечивает
// migrate.Apply.
func applyMigrations(pool *pgxpool.Pool, dir, service string) error {
	return migrate.Apply(context.Background(), pool, dir, migrate.OwnSchema(service))
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
