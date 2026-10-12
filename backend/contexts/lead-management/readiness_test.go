package leadmanagement

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/platform/readiness"
)

func TestReadinessProfilesAreRoleSpecificAndFailClosed(t *testing.T) {
	up := func(context.Context) (readiness.ProbeResult, error) {
		return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
	}
	service := &Service{
		readinessChecks: readinessChecks{
			persistence: up,
			contacts:    up,
		},
		readinessEvaluator: readiness.New(readiness.Config{Timeout: 100 * time.Millisecond, TTL: time.Second}),
	}
	api := service.Readiness(context.Background(), "api")
	if !api.Ready || api.Status != readiness.StatusDegraded {
		t.Fatalf("API core должен быть готов, а неизвестные capability — видимы как degraded: %+v", api)
	}
	if len(api.Profiles) != 2 {
		t.Fatalf("ожидались отдельные core и paid-search профили: %+v", api.Profiles)
	}
	paidSearch := api.Profiles[1]
	if paidSearch.Capability != "paid-search" || paidSearch.Status != readiness.StatusUnknown || paidSearch.Ready {
		t.Fatalf("платный поиск должен быть отдельным unknown profile: %+v", api.Profiles)
	}
	if paidSearch.Checks[0].Status != readiness.StatusUnknown || !paidSearch.Checks[0].Required ||
		paidSearch.Checks[1].Status != readiness.StatusUnknown || !paidSearch.Checks[1].Required {
		t.Fatalf("состояния и обязательность проверок paid-search должны быть доступны клиенту: %+v", paidSearch.Checks)
	}
	if api.Ready && paidSearch.Ready {
		t.Fatal("готовность API не должна подразумевать доступность paid-search")
	}

	// Отсутствующая provider probe остаётся unknown и закрывает только
	// paid-search capability. После передачи успешных сигналов профиль становится
	// ready; сами тестовые probes не выполняют сетевых или платных операций.
	service.readinessChecks.firecrawlSearch = up
	service.readinessChecks.searchProvider = up
	service.readinessEvaluator = readiness.New(readiness.Config{Timeout: 100 * time.Millisecond, TTL: time.Second})
	api = service.Readiness(context.Background(), "api")
	paidSearch = api.Profiles[1]
	if !api.Ready || !paidSearch.Ready || paidSearch.Status != readiness.StatusUp {
		t.Fatalf("при успешных проверках core и paid-search должны быть готовы раздельно: %+v", api)
	}
	for _, dependency := range paidSearch.Checks {
		if !dependency.Required || dependency.Status != readiness.StatusUp {
			t.Fatalf("paid-search должен сообщать успешное состояние обязательных проверок: %+v", paidSearch.Checks)
		}
	}

	service.readinessChecks.firecrawlSearch = nil
	service.readinessChecks.searchProvider = nil
	service.readinessEvaluator = readiness.New(readiness.Config{Timeout: 100 * time.Millisecond, TTL: time.Second})
	worker := service.Readiness(context.Background(), "worker")
	if worker.Ready || worker.Status != readiness.StatusUnknown {
		t.Fatalf("worker без требуемых probes не должен быть готов: %+v", worker)
	}
	if got := service.Readiness(context.Background(), "schedule"); got.Status != readiness.StatusUnknown {
		t.Fatalf("неизвестная роль должна быть unknown: %+v", got)
	}
	service.readinessChecks.contacts = func(context.Context) (readiness.ProbeResult, error) {
		return readiness.ProbeResult{Status: readiness.StatusDown, Reason: readiness.ReasonProbeFailed}, nil
	}
	service.readinessEvaluator = readiness.New(readiness.Config{Timeout: 100 * time.Millisecond, TTL: time.Second})
	if report := service.Readiness(context.Background(), "api"); report.Ready || report.Status != readiness.StatusDown {
		t.Fatalf("недоступность обязательного локального хранилища должна блокировать core readiness: %+v", report)
	}
}
