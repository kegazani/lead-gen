// Команда lead-gen — единая точка входа единого образа (design.md, Решение 6).
//
// Она читает deploy/services.yaml, выбирает группы процессов и сервисы и
// поднимает раннер на каждую назначенную сервису роль. Совмещённый деплой и
// выделение сервиса — изменение конфигурации, а не кода: тот же образ запускает
// одну или несколько ролей. Шесть прежних имён процессов (api, ingest, sender,
// worker, chains, schedule) сохранены как роли.
//
// Роль владеет только запуском и адаптерами, не доменом: composition не выводит
// правил и не решает допустимость. Единица развёртывания — сервис, но запуск
// задаётся конфигурацией.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/deployment"
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/observability/metriccatalog"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/observability/prommetrics"
	"lead-gen/backend/platform/transport/amqp"
	platformworkflow "lead-gen/backend/platform/workflow"
	"lead-gen/frontend/components"
)

// envOr возвращает значение переменной окружения или значение по умолчанию,
// если переменная пуста. Позволяет задавать адреса консолей и ENV, и флагом:
// ENV становится значением по умолчанию, а явный флаг его перекрывает.
func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// envIntOr — числовой вариант envOr: неразборчивое значение не роняет старт, а
// даёт значение по умолчанию (конфигурация процесса — не место для паники).
func envIntOr(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// envBoolOr — логический вариант envOr: значение читается через strconv.ParseBool,
// неразборчивое значение не роняет старт и даёт значение по умолчанию.
func envBoolOr(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// envDurationOr — длительный вариант envOr: значение читается через
// time.ParseDuration, неразборчивое значение не роняет старт и даёт значение по
// умолчанию (конфигурация процесса — не место для паники).
func envDurationOr(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func main() {
	if handled, err := releaseHelperMode(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "lead-gen:", err)
			os.Exit(1)
		}
		return
	}
	dbPolicy := flag.String("db-policy", envOr("LEADGEN_DB_POLICY", "legacy"), "DB policy приложения: legacy или runtime-only для отдельной intake-группы")
	requestRuntimeDSNFile := flag.String("request-runtime-dsn-file", "", "файл runtime DSN request-fulfillment (ENV LEADGEN_REQUEST_RUNTIME_DSN_FILE)")
	consoleAuthRuntimeDSNFile := flag.String("consoleauth-runtime-dsn-file", "", "файл runtime DSN consoleauth (ENV LEADGEN_CONSOLEAUTH_RUNTIME_DSN_FILE)")
	vectorRuntimeDSNFile := flag.String("vector-runtime-dsn-file", "", "файл vector-only runtime DSN integration-platform (ENV LEADGEN_VECTOR_RUNTIME_DSN_FILE)")
	dbJobMode := flag.String("db-job", "", "одноразовый режим БД/секретов: provision, migrate, verify, stage-secrets, stage-trust-anchor или verify-secrets")
	dbTarget := flag.String("db-target", "", "allowlisted target для одноразового DB job")
	candidateManifestFile := flag.String("candidate-manifest-file", "", "подписанный candidate manifest для DB job; runtime-only intake также принимает LEADGEN_CANDIDATE_MANIFEST_FILE")
	candidatePublicKeyFile := flag.String("candidate-public-key-file", "", "доверенный Ed25519 public key candidate manifest; runtime-only intake также принимает LEADGEN_CANDIDATE_PUBLIC_KEY_FILE")
	stageTarget := flag.String("target", "", "allowlisted secret bundle для stage-secrets")
	stageSourceDir := flag.String("source-dir", "", "только для stage-secrets: read-only каталог источников")
	stageVolumeDir := flag.String("volume-dir", stagedSecretRoot, "только фиксированный target directory named volume")
	stageGeneration := flag.String("generation", "", "несекретный идентификатор поколения staged bundle")
	stageSourceName := flag.String("source-name", "", "имя allowlisted файла для stage-trust-anchor")
	stageKeyID := flag.String("key-id", "", "несекретный ID внешнего trust anchor")
	bootstrapDSNFile := flag.String("bootstrap-dsn-file", "", "путь к файлу bootstrap DSN (только provision job)")
	migrationDSNFile := flag.String("migration-dsn-file", "", "путь к файлу migration DSN (только migrate job)")
	runtimeDSNFile := flag.String("runtime-dsn-file", "", "путь к файлу runtime DSN (только verify job)")
	migratorPasswordFile := flag.String("migrator-password-file", "", "путь к файлу пароля migrator (только provision job)")
	runtimePasswordFile := flag.String("runtime-password-file", "", "путь к файлу пароля runtime role (только provision job)")
	additionalRuntimePasswordFile := flag.String("additional-runtime-password-file", "", "путь к дополнительному runtime password file integration-platform")
	configPath := flag.String("config", "deploy/services.yaml", "путь к конфигурации развёртывания")
	processFilter := flag.String("process", envOr("LEADGEN_PROCESS", ""), "группы процессов через запятую (пусто — все)")
	serviceFilter := flag.String("service", "", "сервисы через запятую (пусто — все выбранных групп)")
	apiAddr := flag.String("addr", ":8080", "адрес внутреннего HTTP-сервера роли api")
	apiBindHost := flag.String("api-bind-host", "", "адрес привязки API при совмещённом запуске с -api-port-base (пусто — все интерфейсы)")
	ingestAddr := flag.String("ingest-addr", envOr("LEADGEN_INGEST_ADDR", ":8081"), "адрес публичного HTTP-сервера роли ingest")
	apiPortBase := flag.Int("api-port-base", envIntOr("LEADGEN_API_PORT_BASE", 0), "базовый порт для API-сервисов совмещённого запуска (0 — общий -addr, один сервис на процесс)")
	requestIntentURL := flag.String("request-intent-url", envOr("LEADGEN_REQUEST_INTENT_URL", ""), "базовый адрес сервиса заявок request-fulfillment (пусто — in-proc адаптер)")
	leadSearchURL := flag.String("lead-search-url", envOr("LEADGEN_LEAD_SEARCH_URL", ""), "базовый адрес сервиса поиска lead-management (пусто — прежняя заглушка поиска)")
	emailGenerationURL := flag.String("email-generation-url", envOr("LEADGEN_EMAIL_GENERATION_URL", ""), "базовый адрес сервиса генерации писем lead-management (пусто — шаг генерации пропускается)")
	contentRequestURL := flag.String("content-request-url", envOr("LEADGEN_CONTENT_REQUEST_URL", ""), "базовый адрес контракта content-request у content-production (пусто — материал письма недоступен)")
	outreachHandoffURL := flag.String("outreach-handoff-url", envOr("LEADGEN_OUTREACH_HANDOFF_URL", ""), "базовый адрес контракта outreach-handoff у outreach-orchestration (пусто — передача писем недоступна)")
	campaignCommandURL := flag.String("campaign-command-url", envOr("LEADGEN_CAMPAIGN_COMMAND_URL", ""), "базовый адрес контракта campaign-command у outreach-orchestration (пусто — in-proc подача черновика на утверждение)")
	draftDecisionURL := flag.String("draft-decision-url", envOr("LEADGEN_DRAFT_DECISION_URL", ""), "базовый адрес read-контракта draftdecision у outreach-orchestration (пусто — in-proc чтение решения)")
	approvedActionURL := flag.String("approved-action-url", envOr("LEADGEN_APPROVED_ACTION_URL", ""), "базовый адрес приёма approved-action у message-delivery (пусто — in-proc мост)")
	approvedActionIntakeAddr := flag.String("approved-action-intake-addr", envOr("LEADGEN_APPROVED_ACTION_INTAKE_ADDR", ""), "адрес слушателя приёма approved-action (роль worker message-delivery; пусто — приём не поднимается)")
	deliveryTargetURL := flag.String("delivery-target-url", envOr("LEADGEN_DELIVERY_TARGET_URL", ""), "базовый адрес контракта delivery-target у lead-management (пусто — in-proc резолвер)")
	contentVersionURL := flag.String("content-version-url", envOr("LEADGEN_CONTENT_VERSION_URL", ""), "базовый адрес контракта content-version у content-production (пусто — in-proc источник)")
	integrationPlatformURL := flag.String("integration-platform-url", envOr("LEADGEN_INTEGRATION_PLATFORM_URL", ""), "базовый адрес HTTP-поверхности integration-platform (пусто — in-proc источник состояния адаптеров)")
	accessDecisionURL := flag.String("access-decision-url", envOr("LEADGEN_ACCESS_DECISION_URL", ""), "базовый адрес HTTP-поверхности решения доступа platform-governance (пусто — in-proc связывание владельца)")
	agentOrchestrationURL := flag.String("agent-orchestration-url", envOr("LEADGEN_AGENT_ORCHESTRATION_URL", ""), "базовый адрес HTTP-поверхности agent-orchestration (контракт agent-run-state; пусто — in-proc источник экрана операций)")
	analyticsURL := flag.String("analytics-url", envOr("LEADGEN_ANALYTICS_URL", ""), "базовый адрес HTTP-поверхности analytics (контракт campaign-report; пусто — in-proc источник агрегатов экрана операций)")
	requestExecutionsURL := flag.String("request-executions-url", envOr("LEADGEN_REQUEST_EXECUTIONS_URL", ""), "базовый адрес read-only API request-fulfillment для сведений о заявках и workflow")
	brokerURL := flag.String("broker-url", envOr("LEADGEN_BROKER_URL", ""), "адрес AMQP-брокера RabbitMQ, amqp://... (пусто — брокер в памяти)")
	requestDeliveryQueue := flag.String("request-delivery-queue", envOr("LEADGEN_REQUEST_DELIVERY_QUEUE", ""), "очередь подписки request-fulfillment на события доставки (пусто — очередь по умолчанию request-fulfillment.delivery-event)")
	analyticsDeliveryQueue := flag.String("analytics-delivery-queue", envOr("LEADGEN_ANALYTICS_DELIVERY_QUEUE", ""), "очередь подписки analytics на исходы доставки (пусто — очередь по умолчанию analytics.delivery-event)")
	actor := flag.String("actor", "", "оператор, которому выдано право сборки")
	tenant := flag.String("tenant", "", "тенант, в котором выдано право сборки")
	brokerAttempts := flag.Int("broker-attempts", 3, "число попыток доставки в брокер до DLQ")
	publishInterval := flag.Duration("publish-interval", time.Second, "интервал публикации approved-action")
	logLevel := flag.String("log-level", envOr("LEADGEN_LOG_LEVEL", "info"), "уровень логирования (debug/info/warn/error)")

	// Секреты и конфигурация сервиса lead-management. Секреты задаются вне
	// репозитория (env/флаги на деплое), значения по умолчанию — пустые.
	leadGreenDSN := flag.String("lead-green-dsn", envOr("LEADGEN_LEAD_GREEN_DSN", ""), "DSN зелёной зоны lead-management (пусто — in-memory адаптеры)")
	leadRedDSN := flag.String("lead-red-dsn", envOr("LEADGEN_LEAD_RED_DSN", ""), "DSN красной зоны lead-management (пусто — DSN зелёной зоны)")
	requestGreenDSN := flag.String("request-green-dsn", envOr("LEADGEN_REQUEST_GREEN_DSN", ""), "DSN зелёной зоны request-fulfillment (пусто — in-memory адаптеры)")
	supportGreenDSN := flag.String("support-green-dsn", envOr("LEADGEN_SUPPORT_GREEN_DSN", ""), "DSN зелёной зоны supporting-контекстов (пусто — in-memory адаптеры)")
	firecrawlKey := flag.String("firecrawl-key", envOr("FIRECRAWL_API_KEY", ""), "ключ доступа Firecrawl (поиск и обход)")
	firecrawlURL := flag.String("firecrawl-url", envOr("FIRECRAWL_BASE_URL", ""), "базовый адрес Firecrawl API (пусто — адрес по умолчанию)")
	llmBaseURL := flag.String("llm-base-url", envOr("LLM_BASE_URL", ""), "базовый адрес LLM API (DeepSeek; пусто — адрес по умолчанию)")
	llmModel := flag.String("llm-model", envOr("LLM_MODEL", ""), "модель LLM (DeepSeek; пусто — модель по умолчанию)")
	llmKey := flag.String("llm-key", envOr("LLM_API_KEY", ""), "ключ доступа LLM (пусто — LLM-планирование отключено)")
	embeddingBaseURL := flag.String("embedding-base-url", envOr("EMBEDDING_BASE_URL", ""), "базовый адрес OpenAI-совместимого API эмбеддингов (пусто — адрес по умолчанию эмбеддера)")
	embeddingModel := flag.String("embedding-model", envOr("EMBEDDING_MODEL", ""), "модель эмбеддингов (пусто — модель по умолчанию)")
	embeddingKey := flag.String("embedding-key", envOr("EMBEDDING_KEY", ""), "ключ провайдера эмбеддингов (пусто — векторный recall недоступен)")
	consoleUser := flag.String("console-user", envOr("CONSOLE_USER", ""), "логин оператора консоли (пусто — консоль без аутентификации, только dev)")
	consolePasswordFile := flag.String("console-password-file", envOr("CONSOLE_PASSWORD_FILE", ""), "путь к файлу пароля оператора консоли; содержимое пароля не принимается через argv/ENV")

	// Базовые адреса сервисов консоли для общей навигации по разделам и внешние
	// префиксы путей под единым origin (admin-proxy). Каждый задаётся ENV или
	// флагом (флаг перекрывает ENV). Это префиксы путей одного хоста, а не
	// разные origins: навигация остаётся в пределах origin, а ссылки сервиса
	// переписываются под префикс общим middleware (frontend/components).
	consoleLeadsBase := flag.String("console-leads-base", envOr("CONSOLE_LEADS_BASE_PATH", "/lead-console"), "внешний префикс консоли lead-management под единым origin (раздел «Лиды»)")
	consoleRequestsBase := flag.String("console-requests-base", envOr("CONSOLE_REQUESTS_BASE_PATH", "/intake"), "внешний префикс консоли request-fulfillment под единым origin (разделы «Заявки» и «Исполнение»)")
	consoleReportsBase := flag.String("console-reports-base", envOr("CONSOLE_REPORTS_BASE_PATH", "/reporting"), "внешний префикс консоли analytics под единым origin (раздел «Отчёты»)")
	consoleOutreachBase := flag.String("console-outreach-base", envOr("CONSOLE_OUTREACH_BASE_PATH", "/mailing"), "внешний префикс консоли outreach-orchestration под единым origin (раздел «Рассылки»)")
	consolePlatformBase := flag.String("console-platform-base", envOr("CONSOLE_PLATFORM_BASE_PATH", "/admin"), "внешний префикс консоли platform-governance под единым origin (платформенные разделы)")
	leadMaxPages := flag.Int("lead-max-pages", 50, "верхний предел страниц обхода на один сайт (внутренний предохранитель; 0 — без предела)")
	leadMaxDepth := flag.Int("lead-max-depth", 3, "максимальная глубина обхода lead-management")
	leadMaxTimeSeconds := flag.Int("lead-max-time-seconds", 120, "предельное время обхода lead-management, с")
	leadSearchMaxResults := flag.Int("lead-search-max-results", 0, "верхний предел кандидатов синхронного поиска под заявку; 0 — значение по умолчанию")
	leadSearchDeadline := flag.Duration("lead-search-deadline", 0, "предельное время синхронного поиска под заявку; 0 — значение по умолчанию")
	dialogueMaxTurns := flag.Int("dialogue-max-turns", 8, "предел числа ходов диалога ассистента (0 — значение по умолчанию)")
	firecrawlMaxCredits := flag.Int("firecrawl-max-credits", 0, "верхний предел кредитов Firecrawl на задачу; 0 — без per-task cap (account admission остаётся обязательным)")
	firecrawlCrawlInterval := flag.Duration("firecrawl-crawl-interval", 0, "минимальная пауза между запусками crawl; 0 — консервативное значение по умолчанию")
	smtpHost := flag.String("smtp-host", "localhost", "адрес SMTP-сервера отправки писем")
	smtpPort := flag.Int("smtp-port", 25, "порт SMTP-сервера отправки писем")
	smtpUsername := flag.String("smtp-username", "", "имя учётной записи SMTP (пусто — без аутентификации)")
	smtpPassword := flag.String("smtp-password", "", "пароль учётной записи SMTP")
	smtpFrom := flag.String("smtp-from", "leads@lead-gen.local", "адрес отправителя писем")
	addressVerifierURL := flag.String("address-verifier-url", envOr("LEADGEN_ADDRESS_VERIFIER_URL", ""), "базовый адрес внешнего сервиса верификации адресов (пусто — проверка честно недоступна)")
	addressVerifierKey := flag.String("address-verifier-key", envOr("LEADGEN_ADDRESS_VERIFIER_KEY", ""), "ключ доступа к сервису верификации адресов (пусто — без ключа)")
	addressVerificationURL := flag.String("address-verification-url", envOr("LEADGEN_ADDRESS_VERIFICATION_URL", ""), "базовый адрес контракта address-verification у lead-management (пусто — in-proc владелец при co-location)")
	topologyCatalog := flag.String("topology-catalog", "architecture/domain-catalog.yaml", "путь к каталогу для консоли топологии (пусто — граф не подключён)")
	topologyServices := flag.String("topology-services", "deploy/services.yaml", "путь к deploy/services.yaml для консоли топологии")
	topologyInfrastructure := flag.String("topology-infrastructure", "deploy/infrastructure.yaml", "путь к deploy/infrastructure.yaml для консоли топологии")
	prometheusURL := flag.String("prometheus-url", "", "базовый адрес Prometheus для консоли топологии (пусто — метрики недоступны)")
	deliveryRetryInterval := flag.Duration("delivery-retry-interval", time.Minute, "интервал повторных окон доставки message-delivery")
	deliveryMaxAttempts := flag.Int("delivery-max-attempts", 3, "предельное число попыток доставки message-delivery (включая первую)")

	// Конфигурация реального движка рендеринга content-production (headless
	// Chromium по CDP). Пусто — контекст остаётся на детерминированном dev-двойнике
	// рендера и честно предупреждает об этом: подмены продуктового движка нет.
	rendererExecPath := flag.String("renderer-exec-path", envOr("RENDERER_EXEC_PATH", ""), "путь к исполняемому Chromium/Chrome для реального рендера (пусто — браузер не задан)")
	rendererWSURL := flag.String("renderer-ws-url", envOr("RENDERER_WS_URL", ""), "адрес CDP-эндпоинта запущенного Chromium (пусто — браузер не задан)")
	rendererTimeout := flag.Duration("renderer-timeout", 30*time.Second, "таймаут одной операции реального рендера Chromium")

	// Рантайм исполнения (Temporal). Пустой адрес отключает рантайм: воркер
	// workflow не поднимается, заявка остаётся в текущем статусе.
	temporalAddress := flag.String("temporal-address", "", "адрес Temporal, host:port (пусто — рантайм исполнения отключён)")
	temporalNamespace := flag.String("temporal-namespace", "default", "пространство имён Temporal")
	temporalAPIKey := flag.String("temporal-api-key", "", "ключ доступа Temporal Cloud (пусто — без TLS и ключа)")
	temporalTaskQueue := flag.String("temporal-task-queue", "request-fulfillment", "очередь задач воркера workflow")
	temporalConnectAttempts := flag.Int("temporal-connect-attempts", platformworkflow.DefaultConnectAttempts, "число попыток подключения к Temporal при старте")
	temporalConnectBackoff := flag.Duration("temporal-connect-backoff", platformworkflow.DefaultConnectInitialBackoff, "начальная задержка между попытками подключения к Temporal")
	searchStubLeads := flag.Int("workflow-search-stub-leads", 0, "заглушка шага поиска: сколько лидов «находит» (0 — поиск не подключён)")

	// Политики исполнения заявки: сроки паузы утверждения, таймауты и heartbeat
	// длинных activities, устойчивость воркера.
	workflowApprovalTTL := flag.Duration("workflow-approval-ttl", 168*time.Hour, "срок паузы утверждения результата (по умолчанию 7 дней)")
	workflowExecutionTimeout := flag.Duration("workflow-execution-timeout", 720*time.Hour, "предельное время исполнения заявки (по умолчанию 30 дней)")
	workflowTransitionTimeout := flag.Duration("workflow-transition-timeout", 30*time.Second, "StartToCloseTimeout коротких activities-переходов")
	workflowTransitionAttempts := flag.Int("workflow-transition-attempts", 3, "число попыток коротких activities-переходов")
	workflowLongTimeout := flag.Duration("workflow-long-timeout", 10*time.Minute, "StartToCloseTimeout длинных activities (поиск, письма)")
	workflowHeartbeatTimeout := flag.Duration("workflow-heartbeat-timeout", time.Minute, "HeartbeatTimeout длинных activities")
	workflowLongAttempts := flag.Int("workflow-long-attempts", 2, "число попыток длинных внешних шагов")
	workerStopTimeout := flag.Duration("worker-stop-timeout", 30*time.Second, "время graceful drain воркера workflow при остановке")
	workerMaxActivities := flag.Int("worker-max-concurrent-activities", 10, "предел одновременных activities воркера")
	workerMaxWorkflows := flag.Int("worker-max-concurrent-workflows", 10, "предел одновременных задач workflow воркера")
	shutdownTimeout := flag.Duration("shutdown-timeout", 60*time.Second, "предельное время ожидания остановки ролей (graceful drain)")

	// Бюджет перезапуска роли (design.md, Решение 2): по исчерпании любого из
	// лимитов супервизор возвращает ошибку, и процесс завершается неуспешно, чтобы
	// оркестратор (`restart: unless-stopped`) поднял свежий контейнер. По умолчанию
	// конечны — вечный backoff внутри процесса не предусмотрен.
	restartMaxAttempts := flag.Int("role-restart-max-attempts", roleRestartMaxAttempts, "предельное число неудачных запусков роли до передачи управления оркестратору")
	restartBudget := flag.Duration("role-restart-budget", roleRestartBudget, "накопленный бюджет времени перезапусков роли до передачи управления оркестратору")

	// Liveness уровня процесса (design.md, Решение 3): отдельный слушатель, не
	// зависящий от ролей, и режим пробы `-healthcheck` тем же статическим
	// бинарником (distroless не даёт shell/curl). Проба не проверяет здоровье
	// отдельных ролей и не использует /readyz.
	livenessAddr := flag.String("liveness-addr", envOr("LEADGEN_LIVENESS_ADDR", "127.0.0.1:8091"), "адрес liveness-слушателя процесса (проба «процесс жив и отвечает»; по умолчанию loopback)")
	metricsAddr := flag.String("metrics-addr", envOr("LEADGEN_METRICS_ADDR", ":8092"), "адрес процесс-уровневого слушателя /metrics (Prometheus exposition; пусто — экспорт метрик отключён)")
	healthcheck := flag.Bool("healthcheck", false, "режим пробы живости: HTTP-запрос к liveness-слушателю и выход кодом 0/1")
	healthcheckURL := flag.String("healthcheck-url", "", "URL liveness-пробы (режим -healthcheck)")
	healthcheckTimeout := flag.Duration("healthcheck-timeout", 3*time.Second, "таймаут liveness-пробы (режим -healthcheck)")

	// Процессный сторож (design.md, Вариант C): самопроверка собственной
	// liveness-пробы. При устойчивой потере (threshold подряд неудач) процесс
	// самозавершается неуспешно, чтобы Docker (`restart: unless-stopped`) поднял
	// свежий контейнер — без новых привилегий и зависимостей (docker.sock/autoheal).
	// Значение 0 у интервала или порога отключает сторожа; стартовый период даёт
	// допуск на медленный старт процесса.
	selfHealInterval := flag.Duration("self-heal-interval", envDurationOr("LEADGEN_SELF_HEAL_INTERVAL", selfHealDefaultInterval), "интервал самопроверки собственной liveness-пробы (0 — сторож отключён)")
	selfHealTimeout := flag.Duration("self-heal-timeout", envDurationOr("LEADGEN_SELF_HEAL_TIMEOUT", selfHealDefaultTimeout), "таймаут одной самопроверки liveness (неположительное значение клампится к дефолту)")
	selfHealThreshold := flag.Int("self-heal-threshold", envIntOr("LEADGEN_SELF_HEAL_THRESHOLD", selfHealDefaultThreshold), "число подряд неудач liveness до самозавершения (0 — сторож отключён)")
	selfHealStartPeriod := flag.Duration("self-heal-start-period", envDurationOr("LEADGEN_SELF_HEAL_START_PERIOD", selfHealDefaultStartPeriod), "стартовый период без самопроверок (допуск на медленный старт)")

	// Сервис agent-orchestration (change intelligent-lead-operations): отдельная
	// очередь задач агентского workflow (нельзя путать с очередью заявок, иначе
	// воркер чужого сервиса получит задачу неизвестного типа), предел поиска и
	// параллелизма, тенанты расписания. Пустые тенанты оставляют роль schedule
	// без работы и честно предупреждают об этом, а не выдумывают очередь.
	agentWorkflowTaskQueue := flag.String("agent-workflow-task-queue", envOr("LEADGEN_AGENT_WORKFLOW_TASK_QUEUE", "agent-orchestration"), "очередь задач воркера workflow агентов (отдельная от request-fulfillment)")
	agentSearchLimit := flag.Int("agent-search-limit", envIntOr("LEADGEN_AGENT_SEARCH_LIMIT", 0), "сколько компаний просит блок поиска агента; 0 — одна")
	agentMaxConcurrent := flag.Int("agent-max-concurrent", envIntOr("LEADGEN_AGENT_MAX_CONCURRENT", 0), "предел одновременной обработки компаний агентом; 0 — значение по умолчанию")
	agentScheduleTenants := flag.String("agent-schedule-tenants", envOr("LEADGEN_AGENT_SCHEDULE_TENANTS", ""), "тенанты расписания роли schedule агентов (через запятую; пусто — расписание никого не запускает)")
	agentBootstrapPipeline := flag.Bool("agent-bootstrap-pipeline", envBoolOr("LEADGEN_AGENT_BOOTSTRAP_PIPELINE", false), "разово опубликовать первый пайплайн агента для тенантов расписания при сборке (выкл — публикация не выполняется)")
	agentContentLanguage := flag.String("agent-content-language", envOr("LEADGEN_AGENT_CONTENT_LANGUAGE", ""), "язык материала письма блока агента (пусто — значение контекста по умолчанию)")
	leadRelevanceURL := flag.String("lead-relevance-url", envOr("LEADGEN_LEAD_RELEVANCE_URL", ""), "базовый адрес контракта lead-relevance у lead-management (пусто — in-proc владелец при co-location)")
	recommendationApplyURL := flag.String("recommendation-apply-url", envOr("LEADGEN_RECOMMENDATION_APPLY_URL", ""), "базовый адрес контракта recommendation-apply у lead-management (пусто — in-proc владелец при co-location)")
	cockpitMinGroup := flag.Int("cockpit-min-group", envIntOr("LEADGEN_COCKPIT_MIN_GROUP", 0), "порог минимальной группы полноэкранного дашборда операций; 0 — значение по умолчанию")
	flag.Parse()
	if _, supplied := os.LookupEnv("CONSOLE_PASSWORD"); supplied {
		fmt.Fprintln(os.Stderr, "lead-gen: CONSOLE_PASSWORD запрещён; используйте внешний console password file")
		os.Exit(1)
	}
	if *dbJobMode != "" && *dbPolicy != "legacy" {
		fmt.Fprintln(os.Stderr, "lead-gen: db-job несовместим с runtime app DB policy")
		os.Exit(1)
	}

	// DB jobs завершаются до observability, слушателей, Temporal, RabbitMQ и
	// composition. Это одноразовая административная команда, не роль деплоя.
	dbFlags := dbJobFlags{
		job: *dbJobMode, target: *dbTarget,
		candidateManifestFile: *candidateManifestFile, candidatePublicKeyFile: *candidatePublicKeyFile,
		stageTarget: *stageTarget, stageSourceDir: *stageSourceDir,
		stageVolumeDir: *stageVolumeDir, stageGeneration: *stageGeneration,
		stageSourceName: *stageSourceName, stageKeyID: *stageKeyID,
		bootstrapDSNFile: *bootstrapDSNFile, migrationDSNFile: *migrationDSNFile,
		runtimeDSNFile: *runtimeDSNFile, migratorPasswordFile: *migratorPasswordFile,
		runtimePasswordFile: *runtimePasswordFile, additionalRuntimePasswordFile: *additionalRuntimePasswordFile,
	}
	if *dbJobMode != string(dbJobStage) && *dbJobMode != string(dbJobStageTrustAnchor) && *dbJobMode != string(dbJobVerifyBundle) {
		paths := []string{*consolePasswordFile, *requestRuntimeDSNFile, *consoleAuthRuntimeDSNFile, *vectorRuntimeDSNFile}
		paths = append(paths, *bootstrapDSNFile, *migrationDSNFile, *runtimeDSNFile, *migratorPasswordFile, *runtimePasswordFile, *additionalRuntimePasswordFile)
		if err := verifyConfiguredSecretGeneration(os.Getenv("LEADGEN_SECRET_GENERATION"), paths...); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if *dbJobMode != "" || *dbTarget != "" || *stageTarget != "" || *stageSourceDir != "" || *stageGeneration != "" || *stageSourceName != "" || *stageKeyID != "" || anyDBCredentialFile(dbFlags) || anyDBCredentialEnvironment(os.LookupEnv) {
		if err := runDBJob(context.Background(), dbFlags); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if *dbJobMode != "" {
			return
		}
	}

	// Режим пробы живости: кратковременный HTTP-запрос к liveness-слушателю
	// процесса и выход кодом 0/1 — без чтения конфигурации и подъёма ролей.
	if *healthcheck {
		os.Exit(runHealthcheck(*healthcheckURL, *healthcheckTimeout))
	}
	consolePassword, err := resolveConsolePassword(*consoleUser, *consolePasswordFile, os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	data, err := os.ReadFile(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lead-gen: не удалось прочитать deployment config")
		os.Exit(1)
	}
	cfg, err := deployment.ParseDeployConfig(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lead-gen: некорректный deployment config")
		os.Exit(1)
	}
	selected, err := selectServices(cfg, *processFilter, *serviceFilter)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lead-gen: не удалось выбрать сервисы")
		os.Exit(1)
	}
	policy, intakeDSNs, err := resolveIntakeDBPolicy(*dbPolicy, selected, intakeRuntimeFileFlags{
		requestDSNFile: *requestRuntimeDSNFile, consoleAuthDSNFile: *consoleAuthRuntimeDSNFile, vectorDSNFile: *vectorRuntimeDSNFile,
		candidateManifestFile: *candidateManifestFile, candidatePublicKeyFile: *candidatePublicKeyFile,
	}, []string{*leadGreenDSN, *leadRedDSN, *requestGreenDSN, *supportGreenDSN}, os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	runtimePools := newRuntimePoolRegistry()

	// Наблюдаемость строится один раз на процесс: логгер и метрики общие для
	// сервисов (потокобезопасны), трейсер — на сервис (несёт service.name).
	level, err := parseLogLevel(*logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lead-gen:", err)
		os.Exit(1)
	}
	logger := observ.NewLoggerWithLevel(level)
	logger.Debug("lead-gen: наблюдаемость собрана", observ.Fields{}, "log_level", level.String())
	metricsCatalog := metriccatalog.Default()
	metrics := prommetrics.NewWithCatalog(metricsCatalog)

	// Процесс-уровневый экспорт метрик (design.md, Решение 2): один /metrics на
	// группу, метрики всех сервисов различимы по метке service. Пустой адрес
	// отключает экспорт — метрики копятся в адаптере, но наружу не отдаются.
	metricsServer, err := startMetricsListener(*metricsAddr, metrics.Handler(), logger)
	if err != nil {
		logger.Error("lead-gen: слушатель метрик", err, observ.Fields{}, "addr", *metricsAddr)
		os.Exit(1)
	}
	if metricsServer != nil {
		defer func() { _ = metricsServer.Close() }()
	}

	// Liveness уровня процесса поднимается до чтения конфигурации: проба «процесс
	// жив и отвечает» доступна всем группам, включая те, у которых нет роли api
	// (например, delivery). /readyz сюда не подмешивается (design.md, Решение 3).
	// Адрес пробы — часть контракта запуска: невозможность привязать слушатель
	// фатальна, а не тихий запуск без пробы.
	liveness, err := startLivenessListener(*livenessAddr, logger)
	if err != nil {
		logger.Error("lead-gen: liveness-слушатель", err, observ.Fields{}, "addr", *livenessAddr)
		os.Exit(1)
	}
	if liveness != nil {
		defer func() { _ = liveness.Close() }()
	}

	// Совмещённый запуск: каждому сервису с ролью api — свой порт от базового,
	// иначе несколько api-сервисов в одном процессе конфликтуют за -addr.
	apiResolver := buildAPIResolver(*apiPortBase, selected, *apiAddr, *apiBindHost)

	// Брокер событий. При заданном -broker-url используется реальный RabbitMQ: и
	// Publisher, и Consumer поверх amqp091-go, поэтому публикация одного сервиса
	// доходит до подписки другого через сеть. Без адреса — прежний брокер в
	// памяти (совмещённый процесс без внешней сети).
	var publisher amqp.Publisher
	var consumer amqp.Consumer
	if strings.TrimSpace(*brokerURL) != "" {
		rabbit, err := amqp.NewRabbitMQBroker(*brokerURL, *brokerAttempts)
		if err != nil {
			logBrokerConnectionFailure(logger, err, *brokerURL)
			os.Exit(1)
		}
		defer func() { _ = rabbit.Close() }()
		publisher, consumer = rabbit, rabbit
		logger.Info("lead-gen: брокер RabbitMQ подключён", observ.Fields{}, "exchange", amqp.PlatformExchange)
	} else {
		memory := amqp.NewInMemoryBroker(*brokerAttempts)
		publisher, consumer = memory, memory
	}
	stub := newPublicIngestStub(*ingestAddr, logger)

	// Рантайм исполнения подключается один раз на процесс. Подключение ретраится
	// с backoff: транзиентный сбой не помечает рантайм выключенным навсегда, а
	// после исчерпания попыток сервис работает в режиме деградации с неготовой
	// пробой /readyz. Существующие ручные потоки смены статуса продолжают работать.
	workflowConfig := platformworkflow.Config{
		Address:   *temporalAddress,
		Namespace: *temporalNamespace,
		APIKey:    *temporalAPIKey,
		TaskQueue: *temporalTaskQueue,
	}
	availability := platformworkflow.NewAvailability()
	var workflowClient *platformworkflow.Client
	if workflowConfig.Enabled() {
		client, err := platformworkflow.DialWithRetry(context.Background(), workflowConfig, metrics, platformworkflow.RetryConfig{
			Attempts:       *temporalConnectAttempts,
			InitialBackoff: *temporalConnectBackoff,
		})
		if err != nil {
			logger.Warn("lead-gen: рантайм исполнения недоступен, режим деградации", observ.Fields{}, "error", err.Error())
		} else {
			workflowClient = client
			defer workflowClient.Close()
			availability.MarkReady()
			logger.Info("lead-gen: рантайм исполнения подключён", observ.Fields{}, "namespace", workflowClient.Namespace(), "task_queue", *temporalTaskQueue)
		}
	} else {
		// Рантайм отключён конфигурацией: зависимости нет — сервис готов.
		availability.MarkReady()
	}

	opts := buildOptions{
		logger:             logger,
		metrics:            metrics,
		failureNormalizer:  buildFailureNormalizer(),
		apiAddr:            *apiAddr,
		apiAddrResolver:    apiResolver,
		ingestAddr:         *ingestAddr,
		requestIntentURL:   *requestIntentURL,
		leadSearchURL:      *leadSearchURL,
		emailGenerationURL: *emailGenerationURL,
		contentRequestURL:  *contentRequestURL,
		outreachHandoffURL: *outreachHandoffURL,

		campaignCommandURL: *campaignCommandURL,
		draftDecisionURL:   *draftDecisionURL,

		approvedActionURL:        *approvedActionURL,
		approvedActionIntakeAddr: *approvedActionIntakeAddr,
		deliveryTargetURL:        *deliveryTargetURL,
		contentVersionURL:        *contentVersionURL,
		integrationPlatformURL:   *integrationPlatformURL,
		accessDecisionURL:        *accessDecisionURL,
		agentOrchestrationURL:    *agentOrchestrationURL,
		analyticsURL:             *analyticsURL,
		requestExecutionsURL:     *requestExecutionsURL,

		consumer:               consumer,
		requestDeliveryQueue:   *requestDeliveryQueue,
		analyticsDeliveryQueue: *analyticsDeliveryQueue,
		publishInterval:        *publishInterval,
		actor:                  *actor,
		tenant:                 *tenant,
		leadGreenDSN:           *leadGreenDSN,
		leadRedDSN:             *leadRedDSN,
		requestGreenDSN:        *requestGreenDSN,
		supportGreenDSN:        *supportGreenDSN,
		dbPolicy:               policy,
		intakeRuntimeDSNs:      intakeDSNs,
		intakeRuntimeDBOps:     defaultIntakeRuntimeDBOps(),
		intakeRuntimeFactory:   defaultIntakeRuntimeConstructors(),
		runtimeDBResources:     runtimePools,
		firecrawlKey:           *firecrawlKey,
		firecrawlURL:           *firecrawlURL,
		llmBaseURL:             *llmBaseURL,
		llmModel:               *llmModel,
		llmKey:                 *llmKey,
		embeddingBaseURL:       *embeddingBaseURL,
		embeddingModel:         *embeddingModel,
		embeddingKey:           *embeddingKey,
		consoleUser:            *consoleUser,
		consolePassword:        consolePassword,
		console: components.ConsoleBases{
			components.ConsoleServiceLeads:    *consoleLeadsBase,
			components.ConsoleServiceRequests: *consoleRequestsBase,
			components.ConsoleServiceReports:  *consoleReportsBase,
			components.ConsoleServiceOutreach: *consoleOutreachBase,
			components.ConsoleServicePlatform: *consolePlatformBase,
		},
		consoleLeadsBase:    *consoleLeadsBase,
		consoleRequestsBase: *consoleRequestsBase,
		consoleReportsBase:  *consoleReportsBase,
		consoleOutreachBase: *consoleOutreachBase,
		consolePlatformBase: *consolePlatformBase,
		consolePrefixes: []string{
			*consoleLeadsBase,
			*consoleRequestsBase,
			*consoleReportsBase,
			*consoleOutreachBase,
			*consolePlatformBase,
		},
		leadMaxPages:           *leadMaxPages,
		leadMaxDepth:           *leadMaxDepth,
		leadMaxTimeSeconds:     *leadMaxTimeSeconds,
		dialogueMaxTurns:       *dialogueMaxTurns,
		firecrawlMaxCredits:    *firecrawlMaxCredits,
		firecrawlCrawlInterval: *firecrawlCrawlInterval,
		leadSearchMaxResults:   *leadSearchMaxResults,
		leadSearchDeadline:     *leadSearchDeadline,
		smtpHost:               *smtpHost,
		smtpPort:               *smtpPort,
		smtpUsername:           *smtpUsername,
		smtpPassword:           *smtpPassword,
		smtpFrom:               *smtpFrom,
		addressVerifierURL:     *addressVerifierURL,
		addressVerifierKey:     *addressVerifierKey,
		addressVerificationURL: *addressVerificationURL,
		deliveryRetryInterval:  *deliveryRetryInterval,
		deliveryMaxAttempts:    *deliveryMaxAttempts,
		// Реальный движок рендера content-production.
		rendererExecPath: *rendererExecPath,
		rendererWSURL:    *rendererWSURL,
		rendererTimeout:  *rendererTimeout,

		// Консоль топологии.
		topologyCatalogPath:        *topologyCatalog,
		topologyServicesPath:       *topologyServices,
		topologyInfrastructurePath: *topologyInfrastructure,
		prometheusURL:              *prometheusURL,
		workflowClient:             workflowClient,
		workflowTaskQueue:          *temporalTaskQueue,
		runtime:                    availability,
		searchStubLeads:            *searchStubLeads,
		approvalTTL:                *workflowApprovalTTL,
		executionTimeout:           *workflowExecutionTimeout,
		transitionTimeout:          *workflowTransitionTimeout,
		transitionAttempts:         *workflowTransitionAttempts,
		longTimeout:                *workflowLongTimeout,
		heartbeatTimeout:           *workflowHeartbeatTimeout,
		longAttempts:               *workflowLongAttempts,
		workerStopTimeout:          *workerStopTimeout,
		workerMaxActivities:        *workerMaxActivities,
		workerMaxWorkflows:         *workerMaxWorkflows,

		// Сервис agent-orchestration.
		agentWorkflowTaskQueue: *agentWorkflowTaskQueue,
		agentSearchLimit:       *agentSearchLimit,
		agentMaxConcurrent:     *agentMaxConcurrent,
		agentScheduleTenants:   splitList(*agentScheduleTenants),
		agentBootstrapPipeline: *agentBootstrapPipeline,

		// Порты владельцев для блоков агента и контура обратной связи.
		agentContentLanguage:   *agentContentLanguage,
		leadRelevanceURL:       *leadRelevanceURL,
		recommendationApplyURL: *recommendationApplyURL,
		leadStore:              newLeadManagementStore(),

		// Порог минимальной группы экрана операций.
		cockpitMinGroup: *cockpitMinGroup,
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	// Процессный сторож автовосстановления (design.md, Вариант C): самопроверка
	// собственной liveness-пробы. При устойчивой потере сторож доводит причину до
	// selfTerminate, и ниже процесс завершается неуспешно. nil — сторож отключён
	// (пустой -liveness-addr либо нулевой интервал/порог).
	selfTerminate := startSelfHealing(ctx, logger, metrics, *livenessAddr,
		*selfHealInterval, *selfHealTimeout, *selfHealThreshold, *selfHealStartPeriod)

	// Сборка сервисов кэшируется: мост approved-action → delivery-command требует
	// резолвер получателя из lead-management ДО сборки message-delivery и
	// outreach-orchestration, поэтому lead-management собирается первым, а его
	// результат переиспользуется в основном цикле (повторной сборки нет). Мьютекс
	// защищает кэш от параллельного разрешения ленивым источником экрана операций.
	var builtMu sync.Mutex
	built := make(map[string]composition.Service, len(selected))
	buildOnce := func(sel selectedService) (composition.Service, error) {
		builtMu.Lock()
		defer builtMu.Unlock()
		if service, ok := built[sel.ID]; ok {
			return service, nil
		}
		service, err := buildService(sel.ID, sel.Mechanism, opts, publisher, stub)
		if err != nil {
			runtimePools.closeAll()
			return nil, err
		}
		built[sel.ID] = service
		return service, nil
	}

	// Транспорт связывания approved-action → delivery-command выбирается по
	// топологии процесса: сетевой клиент при разделении (задан адрес приёма),
	// in-proc мост при co-location (live-e2e-contour, Фаза 2).
	sink, bridge, err := assembleApprovedActionBridge(selected, opts, buildOnce, logger)
	if err != nil {
		logger.Error("lead-gen: сборка транспорта approved-action", err, observ.Fields{})
		runtimePools.closeAll()
		os.Exit(1)
	}
	opts.approvedActionSink = sink
	opts.bridge = bridge
	if sink != nil {
		logger.Info("lead-gen: транспорт approved-action собран", observ.Fields{},
			"network", strings.TrimSpace(opts.approvedActionURL) != "")
	}

	// Порты владельцев для блоков агента и контура обратной связи: собираются до
	// запуска сервисов. Co-location разрешается лениво при первом обращении, чтобы
	// сборка потребителя не тянула сборку владельца (как у ленты и агрегатов экрана
	// операций). Незаданный порт оставляет блок/контур честно недоступными
	// (AGENTS.md §3).
	assembleRecommendationApply(selected, &opts, buildOnce, logger)
	assembleAgentOwnerPorts(selected, &opts, buildOnce, logger)

	// Источник экрана операций (владельцы agent-orchestration и analytics)
	// собирается ДО решения о доступе: при co-location он ленивый, но
	// platform-governance собирается вместе с решением доступа и должен видеть
	// заданный источник. При разделении — сетевым клиентом agent-run-state.
	assembleOperationsSource(selected, &opts, buildOnce, logger)

	// Решение о доступе для потребителей контракта access-decision собирается до
	// запуска сервисов: владелец platform-governance (если выбран) связывается с
	// контрактом in-process адаптером; иначе потребители честно отвечают отказом.
	assembleAccessDecision(selected, &opts, buildOnce, logger)

	// Сборщик признаков риска пакетного пути (risk-screening) собирается до запуска
	// сервисов: он держит контракты владельцев (delivery-target, suppression-policy,
	// address-verification). Неполный контур оставляет пакетное утверждение честно
	// недоступным, а не идёт «без проверки» (AGENTS.md §3).
	assembleRiskFacts(selected, &opts, buildOnce, logger)

	// Роли запускаются под супервизором: ошибка исполнения (в частности
	// транзиентный сбой БД) перезапускает роль с backoff, а не роняет процесс и
	// соседние роли. Ошибка сборки сервиса остаётся фатальной — супервизор
	// оборачивает исполнение роли, не её сборку (design.md, Решение 1).
	var wg sync.WaitGroup
	supervisorFailures := make(chan error, 1)
	limits := restartLimits{maxAttempts: *restartMaxAttempts, budget: *restartBudget}
	if err := startSelectedServices(ctx, &wg, selected, buildOnce, logger, metrics, limits, supervisorFailures); err != nil {
		logger.Error("lead-gen: сборка сервиса", err, observ.Fields{})
		runtimePools.closeAll()
		os.Exit(1)
	}

	logger.Info("lead-gen: процессы запущены", observ.Fields{},
		"processes", len(cfg.Processes), "services", len(selected))

	// Процесс жив до сигнала остановки, до исчерпания бюджета перезапуска
	// какой-либо роли либо до самозавершения сторожем по потере собственной
	// liveness-пробы: тогда управление передаётся оркестратору (неуспешный выход),
	// а не крутится вечный backoff внутри (design.md, Решения 2 и 6).
	select {
	case <-ctx.Done():
		logger.Info("lead-gen: останов", observ.Fields{})
	case err := <-supervisorFailures:
		logger.Error("lead-gen: супервизор роли завершился с ошибкой", err, observ.Fields{})
		cancel()
		drainRoles(&wg, *shutdownTimeout, logger)
		runtimePools.closeAll()
		os.Exit(1)
	case err := <-selfTerminate:
		logger.Error("lead-gen: процесс завершён сторожем по потере liveness", err, observ.Fields{})
		// Самозавершение инициирует graceful-останов с ограниченным drain (spec,
		// «Самозавершение процесса при потере собственного liveness-контракта»):
		// роли получают отмену контекста и штатно завершаются, но ожидание
		// ограничено -shutdown-timeout — процесс уже нездоров, бесконечно ждать
		// нельзя. Затем ненулевой код, чтобы restart: unless-stopped поднял свежий
		// контейнер.
		cancel()
		drainRoles(&wg, *shutdownTimeout, logger)
		runtimePools.closeAll()
		os.Exit(1)
	}

	drainRoles(&wg, *shutdownTimeout, logger)
	runtimePools.closeAll()
}

// buildFailureNormalizer собирает конечный безопасный каталог кодов владельцев
// заданий. Техническая диагностика здесь — только фиксированные категории, не
// текст исходной ошибки; расширение каталога требует изменения кода.
func buildFailureNormalizer() jobs.FailureNormalizer {
	return jobs.NewCatalogFailureNormalizer(map[string]jobs.FailureRule{
		"payload.invalid":            {Message: "Некорректный запрос", Diagnostic: "invalid_payload"},
		"budget.exhausted":           {Message: "Закончились кредиты", Diagnostic: "budget_exhausted"},
		"search.failed":              {Message: "Не удалось выполнить поиск", Diagnostic: "search_provider_failure"},
		"crawl.failed":               {Message: "Не удалось обойти сайт", Diagnostic: "crawl_provider_failure"},
		"parse.failed":               {Message: "Страница не распознана", Diagnostic: "parse_failure"},
		"lifecycle.interrupted":      {Message: "Задание прервано перезапуском", Diagnostic: "process_interrupted"},
		"export.payload.invalid":     {Message: "Некорректный запрос выгрузки", Diagnostic: "invalid_payload"},
		"export.report.unavailable":  {Message: "Отчёт недоступен: контур клиента не отвечает", Diagnostic: "report_unavailable"},
		"export.render.failed":       {Message: "Не удалось собрать файл выгрузки", Diagnostic: "render_failure"},
		"export.result.store.failed": {Message: "Не удалось сохранить результат выгрузки", Diagnostic: "result_store_failure"},
		"export.interrupted":         {Message: "Выгрузка прервана перезапуском", Diagnostic: "process_interrupted"},
	}, jobs.Failure{Code: "job.failed", Message: "Ошибка задания", Diagnostic: "unknown"})
}

// drainRoles ждёт завершения всех ролей процесса, но не дольше timeout: по
// истечении времени ожидание прекращается и логируется предупреждение. Путь
// общий для штатного останова по сигналу и для самозавершения сторожем — разница
// лишь в том, что самозавершение после drain выходит ненулевым кодом.
func drainRoles(wg *sync.WaitGroup, timeout time.Duration, logger observ.Logger) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		logger.Warn("lead-gen: роли не завершились за отведённое время, выход", observ.Fields{}, "timeout", timeout)
	}
}

// startLivenessListener поднимает отдельный HTTP-слушатель уровня процесса:
// liveness-проба «процесс жив и отвечает», независимая от ролей и доступная всем
// группам, включая те, у которых нет роли api (например, delivery). Проба не
// проверяет здоровье отдельных ролей и не использует /readyz (design.md, Решение 3).
// Пустой адрес отключает слушатель (возвращает nil, nil). Привязка выполняется
// синхронно: ошибка bind возвращается вызывающему (в main — фатальна), потому что
// адрес пробы — часть контракта запуска, и тихо жить без пробы нельзя.
func startLivenessListener(addr string, logger observ.Logger) (*http.Server, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("привязка %s: %w", addr, err)
	}
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("lead-gen: liveness-слушатель запущен", observ.Fields{}, "addr", ln.Addr().String())
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("lead-gen: liveness-слушатель", err, observ.Fields{}, "addr", ln.Addr().String())
		}
	}()
	return server, nil
}

// startMetricsListener поднимает процесс-уровневый слушатель /metrics поверх
// адаптера Prometheus (design.md, Решение 2). Один адрес на группу процессов,
// метрики всех сервисов различимы по метке service. Пустой адрес отключает
// слушатель (возвращает nil, nil); привязка выполняется синхронно — ошибка bind
// возвращается вызывающему и фатальна, потому что адрес метрик — часть контракта.
func startMetricsListener(addr string, handler http.Handler, logger observ.Logger) (*http.Server, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, nil
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", handler)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("привязка %s: %w", addr, err)
	}
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("lead-gen: слушатель метрик запущен", observ.Fields{}, "addr", ln.Addr().String())
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("lead-gen: слушатель метрик", err, observ.Fields{}, "addr", ln.Addr().String())
		}
	}()
	return server, nil
}

// runHealthcheck выполняет liveness-пробу: HTTP GET к liveness-слушателю процесса
// с таймаутом. Возвращает код выхода: 0 — процесс жив и отвечает, 1 — иначе
// (недоступен, таймаут или не-2xx ответ). Диагностика — в stderr, без полного
// логгера: режим пробы короткоживущий и поднимается в distroless без shell/curl.
func runHealthcheck(url string, timeout time.Duration) int {
	if strings.TrimSpace(url) == "" {
		fmt.Fprintln(os.Stderr, "healthcheck: не задан URL пробы (-healthcheck-url)")
		return 1
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: запрос %s: %v\n", url, err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		fmt.Fprintf(os.Stderr, "healthcheck: %s ответил %d\n", url, resp.StatusCode)
		return 1
	}
	return 0
}

// findSelected возвращает выбранный к запуску сервис по идентификатору.
func findSelected(selected []selectedService, id string) (selectedService, bool) {
	for _, sel := range selected {
		if sel.ID == id {
			return sel, true
		}
	}
	return selectedService{}, false
}

// senderDomainFromAddr выводит отправляющий домен из адреса отправителя
// (например, "Name <noreply@example.com>" → "example.com"). Пусто, если домен не
// выводится: тогда мост не собирается, а выдуманный домен не подставляется.
func senderDomainFromAddr(from string) string {
	value := strings.TrimSpace(from)
	value = strings.Trim(value, "<>")
	if at := strings.IndexByte(value, '@'); at >= 0 {
		value = value[at+1:]
	}
	if space := strings.IndexAny(value, "> "); space >= 0 {
		value = value[:space]
	}
	return strings.TrimSpace(strings.Trim(value, "<>"))
}

// selectedService — выбранный к запуску сервис вместе с группой, механизмом
// группы и ролями.
type selectedService struct {
	Process   string
	Mechanism string
	ID        string
	Roles     []composition.Role
}

// buildAPIResolver строит резолвер адреса API для совмещённого запуска: каждому
// выбранному сервису с ролью api назначается свой порт от basePort
// (детерминированно по порядку выбора). basePort<=0 — резолвер не задаётся
// (у всех сервисов общий -addr, один сервис на процесс).
func buildAPIResolver(basePort int, selected []selectedService, fallback, bindHost string) func(string) string {
	if basePort <= 0 {
		return nil
	}
	addrs := make(map[string]string)
	port := basePort
	for _, sel := range selected {
		if !hasRole(sel.Roles, composition.RoleAPI) {
			continue
		}
		addrs[sel.ID] = net.JoinHostPort(bindHost, strconv.Itoa(port))
		port++
	}
	return func(id string) string {
		if addr, ok := addrs[id]; ok {
			return addr
		}
		return fallback
	}
}

// hasRole сообщает, выбрана ли роль.
func hasRole(roles []composition.Role, want composition.Role) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

// selectServices выбирает сервисы по фильтрам групп и идентификаторов. Пустой
// фильтр означает «все». Ошибка неизвестной роли — отказ старта: расхождение
// конфигурации с таблицей ролей не должно проходить молча.
func selectServices(cfg deployment.DeployConfig, processFilter, serviceFilter string) ([]selectedService, error) {
	wantProcesses := splitList(processFilter)
	wantServices := splitList(serviceFilter)

	var out []selectedService
	for _, process := range cfg.Processes {
		if !matchesFilter(wantProcesses, process.Name) {
			continue
		}
		for _, service := range process.Services {
			if !matchesFilter(wantServices, service.ID) {
				continue
			}
			roles := make([]composition.Role, 0, len(service.Roles))
			for _, raw := range service.Roles {
				role, err := composition.ParseRole(raw)
				if err != nil {
					return nil, err
				}
				roles = append(roles, role)
			}
			out = append(out, selectedService{
				Process:   process.Name,
				Mechanism: process.Mechanism,
				ID:        service.ID,
				Roles:     roles,
			})
		}
	}
	if len(out) == 0 {
		return nil, &noServicesError{processFilter: processFilter, serviceFilter: serviceFilter}
	}
	return out, nil
}

// noServicesError называет фильтры, не выбравшие ни одного сервиса.
type noServicesError struct {
	processFilter string
	serviceFilter string
}

func (e *noServicesError) Error() string {
	return "не выбран ни один сервис: process=" + e.processFilter + ", service=" + e.serviceFilter
}

// splitList разбирает значение флага «через запятую» в список непустых элементов.
func splitList(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// matchesFilter сообщает, проходит ли значение фильтр; пустой фильтр пропускает всё.
func matchesFilter(filter []string, value string) bool {
	if len(filter) == 0 {
		return true
	}
	for _, want := range filter {
		if want == value {
			return true
		}
	}
	return false
}
