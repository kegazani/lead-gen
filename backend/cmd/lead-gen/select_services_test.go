package main

import (
	"os"
	"testing"

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

	declarations := map[string][]string{}
	order := make([]string, 0)
	for _, process := range cfg.Processes {
		for _, service := range process.Services {
			if _, seen := declarations[service.ID]; !seen {
				order = append(order, service.ID)
			}
			declarations[service.ID] = append(declarations[service.ID], process.Name)
		}
	}
	multi := ""
	for _, id := range order {
		if len(declarations[id]) > 1 {
			multi = id
			break
		}
	}
	if multi == "" {
		t.Fatal("нет сервиса, объявленного более чем в одном процессе: тест не проверяет модель")
	}

	selected, err = selectServices(cfg, "", multi)
	if err != nil {
		t.Fatalf("выбор сервиса %q: %v", multi, err)
	}
	if len(selected) != len(declarations[multi]) {
		t.Fatalf("сервис %q выбран %d раз, ожидалось %d (по объявлению на процесс)",
			multi, len(selected), len(declarations[multi]))
	}
	groups := map[string]bool{}
	for _, service := range selected {
		if service.ID != multi {
			t.Errorf("выбран сервис %q вместо %q", service.ID, multi)
		}
		groups[service.Process] = true
	}
	if len(groups) != len(declarations[multi]) {
		t.Errorf("сервис %q выбран из групп %v, ожидалось по одной на процесс %v",
			multi, groups, declarations[multi])
	}
}
