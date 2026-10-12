package main

import (
	"os"
	"testing"

	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/deployment"
)

// TestSelectServicesFilters проверяет выбор по группе и сервису: результат
// фильтра по группе целиком принадлежит ей, а сервис, объявленный в нескольких
// процессах, выбирается по разу на каждое объявление, оставаясь одним сервисом.
func TestSelectServicesFilters(t *testing.T) {
	data, err := os.ReadFile("../../../deploy/services.yaml")
	if err != nil {
		t.Fatalf("чтение deploy/services.yaml: %v", err)
	}
	cfg, err := deployment.ParseDeployConfig(data)
	if err != nil {
		t.Fatalf("разбор deploy/services.yaml: %v", err)
	}

	group := cfg.Processes[0].Name
	selected, err := selectServices(cfg, group, "")
	if err != nil {
		t.Fatalf("выбор группы %q: %v", group, err)
	}
	for _, service := range selected {
		if service.Process != group {
			t.Errorf("фильтр по группе %q вернул сервис %q из группы %q",
				group, service.ID, service.Process)
		}
	}

	// Модель «сервис в нескольких процессах» проверяем на синтетической
	// конфигурации: реальная топология сейчас не содержит сервиса, объявленного
	// более чем в одном процессе (после снятия роли sender у integration-platform).
	multiCfg, err := deployment.ParseDeployConfig([]byte(`
processes:
  - name: p1
    mechanism: in-process
    services:
      - id: shared
        roles: [api]
  - name: p2
    mechanism: in-process
    services:
      - id: shared
        roles: [worker]
`))
	if err != nil {
		t.Fatalf("разбор синтетической конфигурации: %v", err)
	}

	selected, err = selectServices(multiCfg, "", "shared")
	if err != nil {
		t.Fatalf("выбор сервиса shared: %v", err)
	}
	if len(selected) != 2 {
		t.Fatalf("сервис shared выбран %d раз, ожидалось 2 (по объявлению на процесс)", len(selected))
	}
	groups := map[string]bool{}
	for _, service := range selected {
		if service.ID != "shared" {
			t.Errorf("выбран сервис %q вместо shared", service.ID)
		}
		groups[service.Process] = true
	}
	if len(groups) != 2 {
		t.Errorf("сервис shared выбран из групп %v, ожидалось по одной на процесс p1/p2", groups)
	}
}

func TestBuildAPIResolverSupportsLoopbackBinding(t *testing.T) {
	selected := []selectedService{
		{ID: "lead-management", Roles: []composition.Role{composition.RoleAPI}},
		{ID: "request-fulfillment", Roles: []composition.Role{composition.RoleAPI}},
	}
	resolve := buildAPIResolver(29200, selected, ":8080", "127.0.0.1")
	if got := resolve("lead-management"); got != "127.0.0.1:29200" {
		t.Fatalf("адрес lead-management = %q, ожидался loopback 127.0.0.1:29200", got)
	}
	if got := resolve("request-fulfillment"); got != "127.0.0.1:29201" {
		t.Fatalf("адрес request-fulfillment = %q, ожидался loopback 127.0.0.1:29201", got)
	}
	if got := resolve("unknown"); got != ":8080" {
		t.Fatalf("резервный адрес = %q, ожидался :8080", got)
	}
}
