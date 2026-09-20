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
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/deployment"
	"lead-gen/backend/platform/transport/amqp"
)

func main() {
	configPath := flag.String("config", "deploy/services.yaml", "путь к конфигурации развёртывания")
	processFilter := flag.String("process", "", "группы процессов через запятую (пусто — все)")
	serviceFilter := flag.String("service", "", "сервисы через запятую (пусто — все выбранных групп)")
	apiAddr := flag.String("addr", ":8080", "адрес внутреннего HTTP-сервера роли api")
	ingestAddr := flag.String("ingest-addr", ":8081", "адрес публичного HTTP-сервера роли ingest")
	actor := flag.String("actor", "", "оператор, которому выдано право сборки")
	tenant := flag.String("tenant", "", "тенант, в котором выдано право сборки")
	brokerAttempts := flag.Int("broker-attempts", 3, "число попыток доставки в брокер до DLQ")
	publishInterval := flag.Duration("publish-interval", time.Second, "интервал публикации approved-action")
	flag.Parse()

	data, err := os.ReadFile(*configPath)
	if err != nil {
		slog.Error("lead-gen: чтение конфигурации развёртывания", "path", *configPath, "error", err)
		os.Exit(1)
	}
	cfg, err := deployment.ParseDeployConfig(data)
	if err != nil {
		slog.Error("lead-gen: разбор конфигурации развёртывания", "error", err)
		os.Exit(1)
	}
	selected, err := selectServices(cfg, *processFilter, *serviceFilter)
	if err != nil {
		slog.Error("lead-gen: выбор сервисов", "error", err)
		os.Exit(1)
	}

	// В совмещённом деплое сервисы процесса делят брокер в памяти: он реализует
	// и Publisher, и Consumer, поэтому публикация одного сервиса доходит до
	// очередей другого без внешней сети.
	broker := amqp.NewInMemoryBroker(*brokerAttempts)
	stub := newPublicIngestStub(*ingestAddr)
	opts := buildOptions{
		apiAddr:         *apiAddr,
		publishInterval: *publishInterval,
		actor:           *actor,
		tenant:          *tenant,
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	var wg sync.WaitGroup
	for _, sel := range selected {
		service, err := buildService(sel.ID, sel.Mechanism, opts, broker, stub)
		if err != nil {
			slog.Error("lead-gen: сборка сервиса", "service", sel.ID, "error", err)
			os.Exit(1)
		}
		startRoles(ctx, &wg, cancel, sel, service)
	}

	slog.Info("lead-gen: процессы запущены",
		"processes", len(cfg.Processes), "services", len(selected))

	<-ctx.Done()
	slog.Info("lead-gen: останов")

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		slog.Warn("lead-gen: роли не завершились за 15с, выход")
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
) {
	for _, role := range sel.Roles {
		runner, ok := service.RoleRunner(role)
		if !ok {
			slog.Warn("роль не зарегистрирована у сервиса",
				"process", sel.Process, "service", sel.ID, "role", role)
			continue
		}
		wg.Add(1)
		go func(role composition.Role, runner composition.RoleRunner) {
			defer wg.Done()
			slog.Info("роль запущена",
				"process", sel.Process, "service", sel.ID, "role", role)
			if err := runner.Run(ctx); err != nil {
				slog.Error("роль остановлена с ошибкой",
					"process", sel.Process, "service", sel.ID, "role", role, "error", err)
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
