package main

import (
	"testing"

	integrationplatform "lead-gen/backend/contexts/integration-platform"
	leadmanagement "lead-gen/backend/contexts/lead-management"
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
