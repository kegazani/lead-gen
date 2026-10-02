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
	"flag"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/deployment"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	platformworkflow "lead-gen/backend/platform/workflow"
)

func main() {
	configPath := flag.String("config", "deploy/services.yaml", "путь к конфигурации развёртывания")
	processFilter := flag.String("process", "", "группы процессов через запятую (пусто — все)")
	serviceFilter := flag.String("service", "", "сервисы через запятую (пусто — все выбранных групп)")
	apiAddr := flag.String("addr", ":8080", "адрес внутреннего HTTP-сервера роли api")
	ingestAddr := flag.String("ingest-addr", ":8081", "адрес публичного HTTP-сервера роли ingest")
	requestIntentURL := flag.String("request-intent-url", "", "базовый адрес сервиса заявок request-fulfillment (пусто — in-proc адаптер)")
	brokerURL := flag.String("broker-url", "", "адрес AMQP-брокера RabbitMQ, amqp://... (пусто — брокер в памяти)")
	actor := flag.String("actor", "", "оператор, которому выдано право сборки")
	tenant := flag.String("tenant", "", "тенант, в котором выдано право сборки")
	brokerAttempts := flag.Int("broker-attempts", 3, "число попыток доставки в брокер до DLQ")
	publishInterval := flag.Duration("publish-interval", time.Second, "интервал публикации approved-action")

	// Секреты и конфигурация сервиса lead-management. Секреты задаются вне
	// репозитория (env/флаги на деплое), значения по умолчанию — пустые.
	leadGreenDSN := flag.String("lead-green-dsn", "", "DSN зелёной зоны lead-management (пусто — in-memory адаптеры)")
	leadRedDSN := flag.String("lead-red-dsn", "", "DSN красной зоны lead-management (пусто — DSN зелёной зоны)")
	firecrawlKey := flag.String("firecrawl-key", "", "ключ доступа Firecrawl (поиск и обход)")
	firecrawlURL := flag.String("firecrawl-url", "", "базовый адрес Firecrawl API (пусто — адрес по умолчанию)")
	llmBaseURL := flag.String("llm-base-url", "", "базовый адрес LLM API (DeepSeek; пусто — адрес по умолчанию)")
	llmModel := flag.String("llm-model", "", "модель LLM (DeepSeek; пусто — модель по умолчанию)")
	llmKey := flag.String("llm-key", "", "ключ доступа LLM (пусто — LLM-планирование отключено)")
	consoleUser := flag.String("console-user", "", "логин оператора консоли (пусто — вход с пустыми учётными данными, только dev)")
	consolePassword := flag.String("console-password", "", "пароль оператора консоли (пусто — вход с пустыми учётными данными, только dev)")
	leadMaxPages := flag.Int("lead-max-pages", 50, "верхний предел страниц обхода на один сайт (внутренний предохранитель; 0 — без предела)")
	leadMaxDepth := flag.Int("lead-max-depth", 3, "максимальная глубина обхода lead-management")
	leadMaxTimeSeconds := flag.Int("lead-max-time-seconds", 120, "предельное время обхода lead-management, с")
	dialogueMaxTurns := flag.Int("dialogue-max-turns", 8, "предел числа ходов диалога ассистента (0 — значение по умолчанию)")
	firecrawlMaxCredits := flag.Int("firecrawl-max-credits", 0, "лимит кредитов Firecrawl на задачу; 0 — без бюджета (по умолчанию)")
	smtpHost := flag.String("smtp-host", "localhost", "адрес SMTP-сервера отправки писем")
	smtpPort := flag.Int("smtp-port", 25, "порт SMTP-сервера отправки писем")
	smtpUsername := flag.String("smtp-username", "", "имя учётной записи SMTP (пусто — без аутентификации)")
	smtpPassword := flag.String("smtp-password", "", "пароль учётной записи SMTP")
	smtpFrom := flag.String("smtp-from", "leads@lead-gen.local", "адрес отправителя писем")

	// Рантайм исполнения (Temporal). Пустой адрес отключает рантайм: воркер
	// workflow не поднимается, заявка остаётся в текущем статусе.
	temporalAddress := flag.String("temporal-address", "", "адрес Temporal, host:port (пусто — рантайм исполнения отключён)")
	temporalNamespace := flag.String("temporal-namespace", "default", "пространство имён Temporal")
	temporalAPIKey := flag.String("temporal-api-key", "", "ключ доступа Temporal Cloud (пусто — без TLS и ключа)")
	temporalTaskQueue := flag.String("temporal-task-queue", "request-fulfillment", "очередь задач воркера workflow")
	searchStubLeads := flag.Int("workflow-search-stub-leads", 0, "заглушка шага поиска: сколько лидов «находит» (0 — поиск не подключён)")
	flag.Parse()

	// Наблюдаемость строится один раз на процесс: логгер и метрики общие для
	// сервисов (потокобезопасны), трейсер — на сервис (несёт service.name).
	logger := observ.NewLogger()
	metrics := observ.NewMetrics()

	data, err := os.ReadFile(*configPath)
	if err != nil {
		logger.Error("lead-gen: чтение конфигурации развёртывания", err, observ.Fields{}, "path", *configPath)
		os.Exit(1)
	}
	cfg, err := deployment.ParseDeployConfig(data)
	if err != nil {
		logger.Error("lead-gen: разбор конфигурации развёртывания", err, observ.Fields{})
		os.Exit(1)
	}
	selected, err := selectServices(cfg, *processFilter, *serviceFilter)
	if err != nil {
		logger.Error("lead-gen: выбор сервисов", err, observ.Fields{})
		os.Exit(1)
	}

	// Брокер событий. При заданном -broker-url используется реальный RabbitMQ: и
	// Publisher, и Consumer поверх amqp091-go, поэтому публикация одного сервиса
	// доходит до подписки другого через сеть. Без адреса — прежний брокер в
	// памяти (совмещённый процесс без внешней сети).
	var publisher amqp.Publisher
	var consumer amqp.Consumer
	if strings.TrimSpace(*brokerURL) != "" {
		rabbit, err := amqp.NewRabbitMQBroker(*brokerURL, *brokerAttempts)
		if err != nil {
			logger.Error("lead-gen: подключение к RabbitMQ", err, observ.Fields{}, "url", *brokerURL)
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

	// Рантайм исполнения подключается один раз на процесс. Недоступность
	// Temporal не валит процесс: рантайм честно помечается выключенным, а
	// существующие ручные потоки смены статуса продолжают работать.
	workflowConfig := platformworkflow.Config{
		Address:   *temporalAddress,
		Namespace: *temporalNamespace,
		APIKey:    *temporalAPIKey,
		TaskQueue: *temporalTaskQueue,
	}
	var workflowClient *platformworkflow.Client
	if workflowConfig.Enabled() {
		client, err := platformworkflow.Dial(context.Background(), workflowConfig, metrics)
		if err != nil {
			logger.Warn("lead-gen: рантайм исполнения недоступен, работаем без workflow", observ.Fields{}, "error", err.Error())
		} else {
			workflowClient = client
			defer workflowClient.Close()
			logger.Info("lead-gen: рантайм исполнения подключён", observ.Fields{}, "namespace", workflowClient.Namespace(), "task_queue", *temporalTaskQueue)
		}
	}

	opts := buildOptions{
		logger:              logger,
		metrics:             metrics,
		apiAddr:             *apiAddr,
		requestIntentURL:    *requestIntentURL,
		consumer:            consumer,
		publishInterval:     *publishInterval,
		actor:               *actor,
		tenant:              *tenant,
		leadGreenDSN:        *leadGreenDSN,
		leadRedDSN:          *leadRedDSN,
		firecrawlKey:        *firecrawlKey,
		firecrawlURL:        *firecrawlURL,
		llmBaseURL:          *llmBaseURL,
		llmModel:            *llmModel,
		llmKey:              *llmKey,
		consoleUser:         *consoleUser,
		consolePassword:     *consolePassword,
		leadMaxPages:        *leadMaxPages,
		leadMaxDepth:        *leadMaxDepth,
		leadMaxTimeSeconds:  *leadMaxTimeSeconds,
		dialogueMaxTurns:    *dialogueMaxTurns,
		firecrawlMaxCredits: *firecrawlMaxCredits,
		smtpHost:            *smtpHost,
		smtpPort:            *smtpPort,
		smtpUsername:        *smtpUsername,
		smtpPassword:        *smtpPassword,
		smtpFrom:            *smtpFrom,
		workflowClient:      workflowClient,
		workflowTaskQueue:   *temporalTaskQueue,
		searchStubLeads:     *searchStubLeads,
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	var wg sync.WaitGroup
	for _, sel := range selected {
		service, err := buildService(sel.ID, sel.Mechanism, opts, publisher, stub)
		if err != nil {
			logger.Error("lead-gen: сборка сервиса", err, observ.Fields{}, "service", sel.ID)
			os.Exit(1)
		}
		startRoles(ctx, &wg, cancel, sel, service, logger)
	}

	logger.Info("lead-gen: процессы запущены", observ.Fields{},
		"processes", len(cfg.Processes), "services", len(selected))

	<-ctx.Done()
	logger.Info("lead-gen: останов", observ.Fields{})

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		logger.Warn("lead-gen: роли не завершились за 15с, выход", observ.Fields{})
	}
}

// selectedService — выбранный к запуску сервис вместе с группой, механизмом
// группы и ролями.
type selectedService struct {
	Process   string
	Mechanism string
	ID        string
	Roles     []composition.Role
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

// startRoles запускает раннер на каждую выбранную роль выбранного сервиса.
// Незарегистрированная роль не пропускается молча: о ней логируется честное
// предупреждение. Ошибка раннера отменяет весь процесс — единица отказа и
// единица запуска совпадают.
func startRoles(
	ctx context.Context,
	wg *sync.WaitGroup,
	cancel context.CancelFunc,
	sel selectedService,
	service composition.Service,
	logger observ.Logger,
) {
	for _, role := range sel.Roles {
		runner, ok := service.RoleRunner(role)
		if !ok {
			logger.Warn("роль не зарегистрирована у сервиса", observ.Fields{},
				"process", sel.Process, "service", sel.ID, "role", role)
			continue
		}
		wg.Add(1)
		go func(role composition.Role, runner composition.RoleRunner) {
			defer wg.Done()
			logger.Info("роль запущена", observ.Fields{},
				"process", sel.Process, "service", sel.ID, "role", role)
			if err := runner.Run(ctx); err != nil {
				logger.Error("роль остановлена с ошибкой", err, observ.Fields{},
					"process", sel.Process, "service", sel.ID, "role", role)
				cancel()
			}
		}(role, runner)
	}
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
