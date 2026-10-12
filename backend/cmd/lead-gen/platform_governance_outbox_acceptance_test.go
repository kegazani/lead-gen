package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/migrate"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
)

// TestPlatformGovernanceWorkerRunnerPublishesAccessDecisionAcceptance проходит
// от production HTTP-команды через PostgreSQL outbox и production worker до
// RabbitMQ. Все подключения должны указывать на изолированные disposable
// PostgreSQL 17 и RabbitMQ; без обеих явных переменных тест пропускается.
func TestPlatformGovernanceWorkerRunnerPublishesAccessDecisionAcceptance(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LEADGEN_POSTGRES_TEST_DSN"))
	rabbitURL := strings.TrimSpace(os.Getenv("LEADGEN_RABBITMQ_TEST_URL"))
	rabbitContainer := strings.TrimSpace(os.Getenv("LEADGEN_RABBITMQ_TEST_CONTAINER"))
	if dsn == "" || rabbitURL == "" || rabbitContainer == "" {
		t.Skip("нужны disposable PostgreSQL 17 и RabbitMQ в выделенном Docker-контейнере: LEADGEN_POSTGRES_TEST_DSN, LEADGEN_RABBITMQ_TEST_URL и LEADGEN_RABBITMQ_TEST_CONTAINER")
	}

	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(previousDir, "..", "..", ".."))
	if err := os.Chdir(root); err != nil {
		t.Fatalf("переход в корень репозитория для путей миграций: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDir) })

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("подключение к disposable PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.Apply(ctx, pool, "backend/contexts/platform-governance/migrations", migrate.OwnSchema("platform-governance")); err != nil {
		t.Fatalf("миграции platform-governance: %v", err)
	}
	var serverVersion string
	if err := pool.QueryRow(ctx, `SHOW server_version`).Scan(&serverVersion); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(serverVersion, "17.") {
		t.Fatalf("PostgreSQL server_version = %q; требуется PostgreSQL 17", serverVersion)
	}

	logger := observ.NewLogger()
	newService := func(publisher amqp.Publisher) (*platformgovernance.Service, composition.RoleRunner) {
		t.Helper()
		resources := newRuntimePoolRegistry()
		t.Cleanup(resources.closeAll)
		service, buildErr := buildService(platformgovernance.ServiceID, "in-process", buildOptions{
			apiAddr:            "127.0.0.1:0",
			supportGreenDSN:    dsn,
			runtimeDBResources: resources,
			logger:             logger,
			metrics:            observ.NewMetrics(),
			publishInterval:    10 * time.Millisecond,
			consoleUser:        "acceptance-operator",
			consolePassword:    "test-only-not-a-secret",
		}, publisher, newPublicIngestStub(":0", logger))
		if buildErr != nil {
			t.Fatalf("сборка production platform-governance через buildService: %v", buildErr)
		}
		pgService, ok := service.(*platformgovernance.Service)
		if !ok {
			t.Fatalf("тип сервиса = %T, ожидался *platformgovernance.Service", service)
		}
		worker, ok := service.RoleRunner(composition.RoleWorker)
		if !ok || worker == nil {
			t.Fatal("production worker role не зарегистрирована")
		}
		return pgService, worker
	}

	queue := "platform-governance.access-decision"
	deliveries := make(chan amqp.Delivery, 32)
	startConsumer := func() (*amqp.RabbitMQBroker, context.CancelFunc, <-chan error) {
		t.Helper()
		var broker *amqp.RabbitMQBroker
		var connectErr error
		connectDeadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(connectDeadline) {
			broker, connectErr = amqp.NewRabbitMQBroker(rabbitURL, 3)
			if connectErr == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if connectErr != nil {
			t.Fatalf("подключение consumer к disposable RabbitMQ: %v", connectErr)
		}
		t.Cleanup(func() { _ = broker.Close() })
		consumeCtx, stop := context.WithCancel(ctx)
		errCh := make(chan error, 1)
		go func() {
			errCh <- broker.Consume(consumeCtx, queue, func(_ context.Context, delivery amqp.Delivery) error {
				deliveries <- delivery
				return nil
			})
		}()
		// Consumer объявляет durable очередь и binding до вызова команды.
		time.Sleep(150 * time.Millisecond)
		return broker, stop, errCh
	}
	_, stopConsumer, initialConsumerErr := startConsumer()
	defer stopConsumer()
	consumerErr := initialConsumerErr

	publisher, err := amqp.NewRabbitMQBroker(rabbitURL, 3)
	if err != nil {
		t.Fatalf("подключение publisher к disposable RabbitMQ: %v", err)
	}
	t.Cleanup(func() { _ = publisher.Close() })
	service, worker := newService(publisher)
	api := httptest.NewServer(service.Slice().Server)
	t.Cleanup(api.Close)

	tenantID := fmt.Sprintf("acceptance-tenant-%d", time.Now().UnixNano())
	commandID := fmt.Sprintf("acceptance-command-%d", time.Now().UnixNano())
	requestBody := fmt.Sprintf(`{"tenant_id":%q,"company_name":"Acceptance Example","description":"synthetic acceptance data","user_id":"acceptance-user","login":"acceptance-login","roles":["client"]}`, tenantID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, api.URL+"/api/tenants", strings.NewReader(requestBody))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Actor", "acceptance-operator")
	request.Header.Set("X-Command-Id", commandID)
	response, err := api.Client().Do(request)
	if err != nil {
		t.Fatalf("HTTP-команда создания tenant/access decision: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("HTTP-команда вернула %d, ожидался 201", response.StatusCode)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM "platform-governance".tenant WHERE id = $1`, tenantID)
	})

	var eventID string
	var snapshotVersion int64
	if err := pool.QueryRow(ctx, `SELECT event_id, snapshot_version
		FROM "platform-governance".outbox_message
		WHERE tenant_id = $1 AND contract = 'access-decision'`, tenantID).Scan(&eventID, &snapshotVersion); err != nil {
		t.Fatalf("outbox access-decision не создан HTTP/domain командой: %v", err)
	}
	if eventID == "" || snapshotVersion < 1 {
		t.Fatalf("access-decision outbox event_id=%q snapshot_version=%d", eventID, snapshotVersion)
	}

	// Останавливаем именно disposable RabbitMQ-контейнер: его TCP listener и
	// broker недоступны, а не только закрыто клиентское соединение publisher.
	if output, stopErr := exec.CommandContext(ctx, "docker", "stop", rabbitContainer).CombinedOutput(); stopErr != nil {
		t.Fatalf("остановка изолированного RabbitMQ-контейнера %q: %v; %s", rabbitContainer, stopErr, strings.TrimSpace(string(output)))
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_, _ = exec.CommandContext(cleanupCtx, "docker", "start", rabbitContainer).CombinedOutput()
	})
	assertRowPublished(t, ctx, pool, eventID, false)
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerErr := make(chan error, 1)
	go func() { workerErr <- worker.Run(workerCtx) }()
	time.Sleep(100 * time.Millisecond)
	assertRowPublished(t, ctx, pool, eventID, false)
	stopWorkerAndWait(t, stopWorker, workerErr)

	// Broker рестартуется в том же изолированном контейнере; новый production
	// consumer и worker открывают новые настоящие соединения после восстановления.
	if output, startErr := exec.CommandContext(ctx, "docker", "start", rabbitContainer).CombinedOutput(); startErr != nil {
		t.Fatalf("перезапуск изолированного RabbitMQ-контейнера %q: %v; %s", rabbitContainer, startErr, strings.TrimSpace(string(output)))
	}
	stopConsumer()
	select {
	case <-consumerErr:
	case <-time.After(2 * time.Second):
		t.Fatal("старый consumer не завершился после остановки RabbitMQ")
	}
	_, stopConsumer, consumerErr = startConsumer()
	defer stopConsumer()
	recoveredBroker, err := amqp.NewRabbitMQBroker(rabbitURL, 3)
	if err != nil {
		t.Fatalf("повторное подключение к RabbitMQ после восстановления: %v", err)
	}
	t.Cleanup(func() { _ = recoveredBroker.Close() })
	_, recoveredWorker := newService(recoveredBroker)
	workerCtx, stopWorker = context.WithCancel(ctx)
	workerErr = make(chan error, 1)
	go func() { workerErr <- recoveredWorker.Run(workerCtx) }()
	decision := receiveAccessDecision(t, ctx, consumerErr, deliveries, eventID)
	assertAccessDecisionContract(t, decision, eventID, tenantID, "acceptance-user", snapshotVersion, "active")
	waitForOutboxState(t, ctx, pool, eventID, true)
	stopWorkerAndWait(t, stopWorker, workerErr)

	// Production HTTP-команда отключения создаёт отрицательный снимок политики;
	// проверяем wire contract не только для approve/active, но и reject/disabled.
	disabledTenantID := fmt.Sprintf("acceptance-disabled-%d", time.Now().UnixNano())
	disabledCommand := fmt.Sprintf("acceptance-disabled-command-%d", time.Now().UnixNano())
	createTenantOverHTTP(t, ctx, api, disabledTenantID, disabledCommand)
	disableTenantOverHTTP(t, ctx, api, disabledTenantID, disabledCommand+"-disable")
	var disabledEventID string
	var disabledVersion int64
	if err := pool.QueryRow(ctx, `SELECT event_id, snapshot_version FROM "platform-governance".outbox_message
		WHERE tenant_id = $1 AND contract = 'access-decision' ORDER BY snapshot_version DESC LIMIT 1`, disabledTenantID).Scan(&disabledEventID, &disabledVersion); err != nil {
		t.Fatalf("чтение access-decision отключённого тенанта: %v", err)
	}
	disabled := waitForAccessDecision(t, ctx, recoveredWorker, consumerErr, deliveries, pool, disabledEventID)
	assertAccessDecisionContract(t, disabled, disabledEventID, disabledTenantID, disabledTenantID, disabledVersion, "disabled")

	// После confirm искусственно отклоняем только UPDATE published=true. Реальная
	// публикация успевает попасть в очередь, транзакция rollback оставляет строку
	// pending, а следующий проход публикует повторно с тем же event_id.
	secondTenantID := fmt.Sprintf("acceptance-mark-failure-%d", time.Now().UnixNano())
	secondCommand := fmt.Sprintf("acceptance-mark-command-%d", time.Now().UnixNano())
	createTenantOverHTTP(t, ctx, api, secondTenantID, secondCommand)
	var duplicateEventID string
	var duplicateVersion int64
	if err := pool.QueryRow(ctx, `SELECT event_id, snapshot_version
		FROM "platform-governance".outbox_message
		WHERE tenant_id = $1 AND contract = 'access-decision'`, secondTenantID).Scan(&duplicateEventID, &duplicateVersion); err != nil {
		t.Fatalf("чтение второго access-decision: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM "platform-governance".tenant WHERE id = $1`, secondTenantID)
	})
	markFailureFunction := fmt.Sprintf(`CREATE OR REPLACE FUNCTION "platform-governance".reject_acceptance_mark() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN
		IF NEW.published AND OLD.event_id = '%s' THEN RAISE EXCEPTION 'acceptance mark rejected'; END IF;
		RETURN NEW; END $$`, duplicateEventID)
	if _, err := pool.Exec(ctx, markFailureFunction); err != nil {
		t.Fatalf("установка отказа отметки после confirm: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER reject_acceptance_mark BEFORE UPDATE OF published
		ON "platform-governance".outbox_message FOR EACH ROW EXECUTE FUNCTION "platform-governance".reject_acceptance_mark()`); err != nil {
		t.Fatalf("установка тестового триггера отказа отметки: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS reject_acceptance_mark ON "platform-governance".outbox_message`)
		_, _ = pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS "platform-governance".reject_acceptance_mark()`)
	})

	_, markFailureWorker := newService(recoveredBroker)
	workerCtx, stopWorker = context.WithCancel(ctx)
	workerErr = make(chan error, 1)
	go func() { workerErr <- markFailureWorker.Run(workerCtx) }()
	first := receiveAccessDecision(t, ctx, consumerErr, deliveries, duplicateEventID)
	assertAccessDecisionContract(t, first, duplicateEventID, secondTenantID, secondTenantID, duplicateVersion, "active")
	second := receiveAccessDecision(t, ctx, consumerErr, deliveries, duplicateEventID)
	assertAccessDecisionContract(t, second, duplicateEventID, secondTenantID, secondTenantID, duplicateVersion, "active")
	if first.EventID != second.EventID || string(first.Body) != string(second.Body) {
		t.Fatalf("повтор после ошибки mark изменил event_id или wire payload: %q/%s != %q/%s", first.EventID, first.Body, second.EventID, second.Body)
	}
	// Consumer contract dedup: применение ограничивается stable event_id;
	// access-decision дополнительно допускает идемпотентность по tenant/version.
	consumerSeen := map[string]struct{}{}
	consumerApplications := 0
	for _, delivery := range []amqp.Delivery{first, second} {
		if _, duplicate := consumerSeen[delivery.EventID]; duplicate {
			continue
		}
		consumerSeen[delivery.EventID] = struct{}{}
		consumerApplications++
	}
	if consumerApplications != 1 {
		t.Fatalf("дедупликация consumer по event_id применила повтор %d раз, ожидался один раз", consumerApplications)
	}
	assertRowPublished(t, ctx, pool, duplicateEventID, false)
	stopWorkerAndWait(t, stopWorker, workerErr)

	if _, err := pool.Exec(ctx, `DROP TRIGGER reject_acceptance_mark ON "platform-governance".outbox_message`); err != nil {
		t.Fatalf("удаление тестового триггера: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP FUNCTION "platform-governance".reject_acceptance_mark()`); err != nil {
		t.Fatalf("удаление тестовой функции: %v", err)
	}
	_, finalWorker := newService(recoveredBroker)
	workerCtx, stopWorker = context.WithCancel(ctx)
	workerErr = make(chan error, 1)
	go func() { workerErr <- finalWorker.Run(workerCtx) }()
	waitForOutboxState(t, ctx, pool, duplicateEventID, true)
	stopWorkerAndWait(t, stopWorker, workerErr)
	// Доставка at-least-once: после confirm и сбоя фиксации потребитель увидел
	// повтор с тем же event_id и обязан дедуплицировать его. Exactly-once не заявлен.
}

func createTenantOverHTTP(t *testing.T, ctx context.Context, server *httptest.Server, tenantID, commandID string) {
	t.Helper()
	body := fmt.Sprintf(`{"tenant_id":%q,"company_name":"Acceptance Retry","description":"synthetic acceptance data","user_id":%q,"login":%q,"roles":["client"]}`, tenantID, tenantID, tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/tenants", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Actor", "acceptance-operator")
	req.Header.Set("X-Command-Id", commandID)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("создание второго тенанта HTTP-командой: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("создание второго тенанта = %d, ожидался 201", resp.StatusCode)
	}
}

func disableTenantOverHTTP(t *testing.T, ctx context.Context, server *httptest.Server, tenantID, commandID string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/tenants/"+tenantID+"/disable", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Actor", "acceptance-operator")
	req.Header.Set("X-Command-Id", commandID)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("отключение тенанта HTTP-командой: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("отключение тенанта = %d, ожидался 200", resp.StatusCode)
	}
}

func waitForAccessDecision(t *testing.T, ctx context.Context, worker composition.RoleRunner, consumerErr <-chan error, deliveries <-chan amqp.Delivery, pool *pgxpool.Pool, eventID string) amqp.Delivery {
	t.Helper()
	workerCtx, stopWorker := context.WithCancel(ctx)
	errCh := make(chan error, 1)
	go func() { errCh <- worker.Run(workerCtx) }()
	t.Cleanup(func() { stopWorker() })
	delivery := receiveAccessDecision(t, ctx, consumerErr, deliveries, eventID)
	waitForOutboxState(t, ctx, pool, eventID, true)
	stopWorkerAndWait(t, stopWorker, errCh)
	return delivery
}

func receiveAccessDecision(t *testing.T, ctx context.Context, consumerErr <-chan error, deliveries <-chan amqp.Delivery, eventID string) amqp.Delivery {
	t.Helper()
	for {
		select {
		case delivery := <-deliveries:
			if delivery.EventID == eventID {
				return delivery
			}
		case err := <-consumerErr:
			t.Fatalf("RabbitMQ consumer остановился до event_id=%s: %v", eventID, err)
		case <-ctx.Done():
			t.Fatalf("таймаут ожидания access-decision event_id=%s", eventID)
		}
	}
}

func assertAccessDecisionContract(t *testing.T, delivery amqp.Delivery, eventID, tenantID, actorID string, version int64, expectedState string) {
	t.Helper()
	if delivery.EventID != eventID || delivery.Headers["event_id"] != eventID {
		t.Fatalf("RabbitMQ event/header id = %q/%q, ожидался стабильный %q", delivery.EventID, delivery.Headers["event_id"], eventID)
	}
	var wire struct {
		EventID         string `json:"event_id"`
		TenantID        string `json:"tenant_id"`
		Name            string `json:"name"`
		SchemaVersion   string `json:"schema_version"`
		State           string `json:"state"`
		SnapshotVersion int64  `json:"snapshot_version"`
		Assignments     []struct {
			Actor string   `json:"actor"`
			Roles []string `json:"roles"`
		} `json:"assignments"`
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(delivery.Body, &fields); err != nil {
		t.Fatalf("декодирование набора полей access-decision: %v", err)
	}
	if err := json.Unmarshal(delivery.Body, &wire); err != nil {
		t.Fatalf("декодирование опубликованного контракта access-decision: %v", err)
	}
	allowedFields := map[string]bool{
		"event_id": true, "occurred_at": true, "tenant_id": true, "correlation_id": true,
		"causation_id": true, "name": true, "schema_version": true, "state": true,
		"snapshot_version": true, "assignments": true,
	}
	for name := range fields {
		if !allowedFields[name] {
			t.Fatalf("wire contract содержит поле вне published contract: %q", name)
		}
	}
	for _, required := range []string{"event_id", "occurred_at", "tenant_id", "correlation_id", "name", "schema_version", "state", "snapshot_version", "assignments"} {
		if _, exists := fields[required]; !exists {
			t.Fatalf("wire contract не содержит обязательное поле %q", required)
		}
	}
	if wire.EventID != eventID || wire.TenantID != tenantID || wire.Name != "access-decision" || wire.SchemaVersion != "1.0.0" || wire.State != expectedState || wire.SnapshotVersion != version {
		t.Fatalf("неожиданная опубликованная форма access-decision: %+v", wire)
	}
	if expectedState == "active" && (len(wire.Assignments) != 1 || wire.Assignments[0].Actor != actorID || len(wire.Assignments[0].Roles) != 1 || wire.Assignments[0].Roles[0] != "client") {
		t.Fatalf("неожиданные назначения в access-decision: %+v", wire.Assignments)
	}
}

func assertRowPublished(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventID string, expected bool) {
	t.Helper()
	var published bool
	if err := pool.QueryRow(ctx, `SELECT published FROM "platform-governance".outbox_message WHERE event_id = $1`, eventID).Scan(&published); err != nil {
		t.Fatalf("чтение outbox published для %s: %v", eventID, err)
	}
	if published != expected {
		t.Fatalf("outbox event_id=%s published=%v, ожидалось %v", eventID, published, expected)
	}
}

func waitForOutboxState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, eventID string, expected bool) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var published bool
		err := pool.QueryRow(ctx, `SELECT published FROM "platform-governance".outbox_message WHERE event_id = $1`, eventID).Scan(&published)
		if err == nil && published == expected {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("outbox event_id=%s не перешёл в published=%v: %v (последнее значение %v)", eventID, expected, err, published)
		}
	}
}

func stopWorkerAndWait(t *testing.T, stop context.CancelFunc, workerErr <-chan error) {
	t.Helper()
	stop()
	select {
	case err := <-workerErr:
		if err != nil {
			t.Fatalf("остановка production worker: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("production worker не остановился после отмены контекста")
	}
}
