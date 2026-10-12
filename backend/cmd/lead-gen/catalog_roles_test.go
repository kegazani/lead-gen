package main

import (
	"os"
	"testing"

	analytics "lead-gen/backend/contexts/analytics"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/deployment"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
)

// TestAnalyticsCatalogRolesMatchRegistration проверяет гигиену каталога
// (design.md, Решение 5): роли, объявленные у analytics в deploy/services.yaml,
// обязаны совпадать с зарегистрированными RoleRunner. Нереализованная роль
// schedule снята, поэтому старт группы не логирует «роль не зарегистрирована».
func TestAnalyticsCatalogRolesMatchRegistration(t *testing.T) {
	data, err := os.ReadFile("../../../deploy/services.yaml")
	if err != nil {
		t.Fatalf("чтение deploy/services.yaml: %v", err)
	}
	cfg, err := deployment.ParseDeployConfig(data)
	if err != nil {
		t.Fatalf("разбор deploy/services.yaml: %v", err)
	}

	declared := map[string]bool{}
	for _, process := range cfg.Processes {
		for _, service := range process.Services {
			if service.ID != analytics.ServiceID {
				continue
			}
			for _, role := range service.Roles {
				declared[role] = true
			}
		}
	}
	if len(declared) == 0 {
		t.Fatalf("сервис %q не объявлен в deploy/services.yaml", analytics.ServiceID)
	}
	if declared[string(composition.RoleSchedule)] {
		t.Errorf("у %q в каталоге осталась нереализованная роль schedule", analytics.ServiceID)
	}

	opts := buildOptions{
		apiAddr: ":0",
		logger:  observ.NewLogger(),
		metrics: observ.NewMetrics(),
	}
	broker := amqp.NewInMemoryBroker(3)
	stub := newPublicIngestStub(":0", observ.NewLogger())
	service, err := buildService(analytics.ServiceID, "in-process", opts, broker, stub)
	if err != nil {
		t.Fatalf("сборка сервиса %q: %v", analytics.ServiceID, err)
	}

	for raw := range declared {
		role, err := composition.ParseRole(raw)
		if err != nil {
			t.Fatalf("каталог объявляет неизвестную роль %q: %v", raw, err)
		}
		if runner, ok := service.RoleRunner(role); !ok || runner == nil {
			t.Errorf("роль %q объявлена в каталоге, но не зарегистрирована у %q", raw, analytics.ServiceID)
		}
	}
}
