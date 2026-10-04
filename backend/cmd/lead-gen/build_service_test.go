package main

import (
	"testing"

	analytics "lead-gen/backend/contexts/analytics"
	audience "lead-gen/backend/contexts/audience"
	contentproduction "lead-gen/backend/contexts/content-production"
	datagovernance "lead-gen/backend/contexts/data-governance"
	engagementobservation "lead-gen/backend/contexts/engagement-observation"
	integrationplatform "lead-gen/backend/contexts/integration-platform"
	leadmanagement "lead-gen/backend/contexts/lead-management"
	messagedelivery "lead-gen/backend/contexts/message-delivery"
	platformgovernance "lead-gen/backend/contexts/platform-governance"
	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
)

// TestBuildLeadManagementService проверяет сборку сервиса lead-management в
// dev-режиме: пустые DSN берут in-memory адаптеры, фиктивный ключ Firecrawl
// допускает сборку без обращения к движку, а пустой LLM-ключ оставляет
// планирование отключённым. Сервис обязан честно зарегистрировать обе роли.
func TestBuildLeadManagementService(t *testing.T) {
	opts := buildOptions{
		apiAddr:      ":0",
		firecrawlKey: "dummy-key",
		logger:       observ.NewLogger(),
		metrics:      observ.NewMetrics(),
	}
	broker := amqp.NewInMemoryBroker(3)
	stub := newPublicIngestStub(":0", observ.NewLogger())

	service, err := buildService(leadmanagement.ServiceID, "in-process", opts, broker, stub)
	if err != nil {
		t.Fatalf("сборка сервиса lead-management: %v", err)
	}
	if got := service.ID(); got != leadmanagement.ServiceID {
		t.Fatalf("ID() = %q, ожидалось %q", got, leadmanagement.ServiceID)
	}

	apiRunner, ok := service.RoleRunner(composition.RoleAPI)
	if !ok || apiRunner == nil {
		t.Fatalf("роль api не зарегистрирована: runner=%v, ok=%v", apiRunner, ok)
	}
	workerRunner, ok := service.RoleRunner(composition.RoleWorker)
	if !ok || workerRunner == nil {
		t.Fatalf("роль worker не зарегистрирована: runner=%v, ok=%v", workerRunner, ok)
	}
}

// TestBuildRequestFulfillmentServiceDev проверяет выбор адаптера хранилища
// сервиса request-fulfillment: пустой DSN берёт in-memory адаптеры (dev), и
// сервис честно регистрирует обе роли. Путь с DSN покрыт контрактными suite
// PostgreSQL-адаптера (make test-postgres).
func TestBuildRequestFulfillmentServiceDev(t *testing.T) {
	opts := buildOptions{
		apiAddr:         ":0",
		requestGreenDSN: "",
		logger:          observ.NewLogger(),
		metrics:         observ.NewMetrics(),
	}
	broker := amqp.NewInMemoryBroker(3)
	stub := newPublicIngestStub(":0", observ.NewLogger())

	service, err := buildService(requestfulfillment.ServiceID, "in-process", opts, broker, stub)
	if err != nil {
		t.Fatalf("сборка сервиса request-fulfillment: %v", err)
	}
	if got := service.ID(); got != requestfulfillment.ServiceID {
		t.Fatalf("ID() = %q, ожидалось %q", got, requestfulfillment.ServiceID)
	}

	apiRunner, ok := service.RoleRunner(composition.RoleAPI)
	if !ok || apiRunner == nil {
		t.Fatalf("роль api не зарегистрирована: runner=%v, ok=%v", apiRunner, ok)
	}
	workerRunner, ok := service.RoleRunner(composition.RoleWorker)
	if !ok || workerRunner == nil {
		t.Fatalf("роль worker не зарегистрирована: runner=%v, ok=%v", workerRunner, ok)
	}
}

// TestBuildIntegrationPlatformService проверяет, что ветка заглушки для
// integration-platform заменена реальной сборкой контекста: сервис собирается
// без внешних зависимостей и честно регистрирует роли среза.
func TestBuildIntegrationPlatformService(t *testing.T) {
	opts := buildOptions{
		apiAddr: ":0",
		logger:  observ.NewLogger(),
		metrics: observ.NewMetrics(),
	}
	broker := amqp.NewInMemoryBroker(3)
	stub := newPublicIngestStub(":0", observ.NewLogger())

	service, err := buildService(integrationplatform.ServiceID, "in-process", opts, broker, stub)
	if err != nil {
		t.Fatalf("сборка сервиса integration-platform: %v", err)
	}
	if got := service.ID(); got != integrationplatform.ServiceID {
		t.Fatalf("ID() = %q, ожидалось %q", got, integrationplatform.ServiceID)
	}

	apiRunner, ok := service.RoleRunner(composition.RoleAPI)
	if !ok || apiRunner == nil {
		t.Fatalf("роль api не зарегистрирована: runner=%v, ok=%v", apiRunner, ok)
	}
	workerRunner, ok := service.RoleRunner(composition.RoleWorker)
	if !ok || workerRunner == nil {
		t.Fatalf("роль worker не зарегистрирована: runner=%v, ok=%v", workerRunner, ok)
	}
}

// TestBuildSupportContextsDev проверяет, что все supporting-контексты зелёной
// зоны собираются в dev-режиме при пустом DSN: ветки не сломаны, in-memory
// адаптеры берутся по умолчанию, заглушка на место сборки не встаёт.
func TestBuildSupportContextsDev(t *testing.T) {
	ids := []string{
		analytics.ServiceID,
		audience.ServiceID,
		contentproduction.ServiceID,
		datagovernance.ServiceID,
		engagementobservation.ServiceID,
		integrationplatform.ServiceID,
		platformgovernance.ServiceID,
		messagedelivery.ServiceID,
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			opts := buildOptions{
				apiAddr:    ":0",
				ingestAddr: ":0",
				logger:     observ.NewLogger(),
				metrics:    observ.NewMetrics(),
				smtpHost:   "localhost",
				smtpPort:   25,
				smtpFrom:   "leads@lead-gen.local",
			}
			broker := amqp.NewInMemoryBroker(3)
			stub := newPublicIngestStub(":0", observ.NewLogger())

			service, err := buildService(id, "in-process", opts, broker, stub)
			if err != nil {
				t.Fatalf("сборка сервиса %s (dev): %v", id, err)
			}
			if got := service.ID(); got != id {
				t.Fatalf("ID() = %q, ожидалось %q", got, id)
			}
		})
	}
}

// TestBuildMessageDeliveryDev проверяет сборку сервиса message-delivery без DSN:
// контекст честно остаётся на in-memory персистентности, но регистрирует все три
// роли (sender, worker, schedule) и собирает in-memory журнал outbox.
func TestBuildMessageDeliveryDev(t *testing.T) {
	opts := buildOptions{
		apiAddr:  ":0",
		logger:   observ.NewLogger(),
		metrics:  observ.NewMetrics(),
		smtpHost: "localhost",
		smtpPort: 25,
		smtpFrom: "leads@lead-gen.local",
	}
	broker := amqp.NewInMemoryBroker(3)
	stub := newPublicIngestStub(":0", observ.NewLogger())

	service, err := buildService(messagedelivery.ServiceID, "in-process", opts, broker, stub)
	if err != nil {
		t.Fatalf("сборка сервиса message-delivery: %v", err)
	}
	if got := service.ID(); got != messagedelivery.ServiceID {
		t.Fatalf("ID() = %q, ожидалось %q", got, messagedelivery.ServiceID)
	}
	for _, role := range []composition.Role{composition.RoleSender, composition.RoleWorker, composition.RoleSchedule} {
		runner, ok := service.RoleRunner(role)
		if !ok || runner == nil {
			t.Fatalf("роль %q не зарегистрирована: runner=%v, ok=%v", role, runner, ok)
		}
	}
	delivery, ok := service.(*messagedelivery.Service)
	if !ok {
		t.Fatalf("ожидался *messagedelivery.Service, got %T", service)
	}
	if delivery.Journal() == nil {
		t.Fatal("dev-режим должен собрать in-memory журнал outbox")
	}
}
