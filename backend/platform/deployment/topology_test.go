package deployment

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Пути к реальным каноническим источникам относительно каталога пакета.
const (
	realCatalogPath        = "../../../architecture/domain-catalog.yaml"
	realServicesPath       = "../../../deploy/services.yaml"
	realInfrastructurePath = "../../../deploy/infrastructure.yaml"
)

// writeTempFile кладёт содержимое во временный файл и возвращает его путь.
func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("запись фикстуры %s: %v", name, err)
	}
	return path
}

// TestLoadRealSources проверяет сборку графа из настоящих источников:
// двенадцать узлов-сервисов, сорок девять рёбер с транспортом и контрактом,
// группы deploy и инфраструктурные узлы.
func TestLoadRealSources(t *testing.T) {
	graph, err := Load(realCatalogPath, realServicesPath, realInfrastructurePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var services, infrastructure int
	groups := map[string][]string{}
	for _, node := range graph.Nodes {
		switch node.Kind {
		case NodeService:
			services++
			if len(node.Groups) == 0 {
				t.Errorf("сервис %q без группы deploy", node.ID)
			}
			groups[node.ID] = node.Groups
			if len(node.Roles) == 0 {
				t.Errorf("сервис %q без ролей", node.ID)
			}
			if node.Health != HealthUnknown {
				t.Errorf("сервис %q: health = %q, ожидался unknown", node.ID, node.Health)
			}
		case NodeInfra:
			infrastructure++
			if node.Health != HealthUnknown {
				t.Errorf("инфраструктура %q: health = %q, ожидался unknown", node.ID, node.Health)
			}
		default:
			t.Errorf("узел %q: неизвестный kind %q", node.ID, node.Kind)
		}
	}
	if services != 12 {
		t.Errorf("узлов-сервисов: %d, ожидалось 12", services)
	}
	if infrastructure != 8 {
		t.Errorf("инфраструктурных узлов: %d, ожидалось 8", infrastructure)
	}
	if len(graph.Edges) != 51 {
		t.Errorf("рёбер: %d, ожидалось 51", len(graph.Edges))
	}
	if !containsString(groups["outreach-orchestration"], "core") {
		t.Errorf("группы outreach-orchestration = %v, ожидалась core", groups["outreach-orchestration"])
	}
	// integration-platform объявлена только в группе platform: роль sender снята
	// как нереализованная (вернётся при реализации контура доставки).
	if !containsString(groups["integration-platform"], "platform") {
		t.Errorf("группы integration-platform = %v, ожидалась platform", groups["integration-platform"])
	}
	if !containsString(groups["request-fulfillment"], "intake") {
		t.Errorf("группы request-fulfillment = %v, ожидалась intake", groups["request-fulfillment"])
	}

	nodeIDs := map[string]bool{}
	for _, node := range graph.Nodes {
		nodeIDs[node.ID] = true
	}
	for _, edge := range graph.Edges {
		if edge.Transport == "" {
			t.Errorf("ребро %q без транспорта", edge.ID)
		}
		if edge.Contract == "" {
			t.Errorf("ребро %q без контракта", edge.ID)
		}
		if edge.Mechanism == "" {
			t.Errorf("ребро %q без механизма", edge.ID)
		}
		if edge.Weight != nil {
			t.Errorf("ребро %q: weight = %v, ожидался nil («нет данных»)", edge.ID, *edge.Weight)
		}
		if edge.Health != HealthUnknown {
			t.Errorf("ребро %q: health = %q, ожидался unknown", edge.ID, edge.Health)
		}
		if !nodeIDs[edge.Upstream] {
			t.Errorf("ребро %q ссылается на неизвестный upstream %q", edge.ID, edge.Upstream)
		}
		if !nodeIDs[edge.Downstream] {
			t.Errorf("ребро %q ссылается на неизвестный downstream %q", edge.ID, edge.Downstream)
		}
	}
}

// TestLoadRealInfrastructure проверяет разбор объявленной инфраструктуры: зоны,
// адреса и отдельный узел красной зоны.
func TestLoadRealInfrastructure(t *testing.T) {
	nodes, err := readInfrastructure(realInfrastructurePath)
	if err != nil {
		t.Fatalf("readInfrastructure: %v", err)
	}
	byID := map[string]Node{}
	for _, node := range nodes {
		byID[node.ID] = node
	}
	green, ok := byID["postgres-green"]
	if !ok {
		t.Fatal("нет узла postgres-green")
	}
	if green.Zone != "green" {
		t.Errorf("postgres-green.zone = %q, ожидалось green", green.Zone)
	}
	if !strings.Contains(green.Meta["addresses"], "postgres-green:5432") {
		t.Errorf("postgres-green без внутреннего адреса: %q", green.Meta["addresses"])
	}
	red, ok := byID["postgres-red"]
	if !ok {
		t.Fatal("нет отдельного узла postgres-red (красная зона)")
	}
	if red.Zone != "red" {
		t.Errorf("postgres-red.zone = %q, ожидалось red", red.Zone)
	}
}

// TestLoadRejectsContextWithoutService: контекст без service — ошибка.
func TestLoadRejectsContextWithoutService(t *testing.T) {
	catalog := writeTempFile(t, "catalog.json",
		`{"contexts":[{"id":"a","name":"A"}],"relationships":[]}`)
	if _, err := Load(catalog, "", ""); err == nil {
		t.Fatal("контекст без service не отвергнут")
	}
}

// TestLoadRejectsRelationshipWithoutTransport: связь без transport — ошибка.
func TestLoadRejectsRelationshipWithoutTransport(t *testing.T) {
	catalog := writeTempFile(t, "catalog.json",
		`{"contexts":[{"id":"a","service":"a"},{"id":"b","service":"b"}],`+
			`"relationships":[{"id":"r","upstream":"a","downstream":"b",`+
			`"contract":"c","mechanism":"http-json"}]}`)
	if _, err := Load(catalog, "", ""); err == nil {
		t.Fatal("связь без transport не отвергнута")
	}
}

// TestLoadRejectsRelationshipWithUnknownContext: связь на неизвестный контекст —
// ошибка, а не молча пропущенное ребро.
func TestLoadRejectsRelationshipWithUnknownContext(t *testing.T) {
	catalog := writeTempFile(t, "catalog.json",
		`{"contexts":[{"id":"a","service":"a"}],`+
			`"relationships":[{"id":"r","upstream":"a","downstream":"ghost",`+
			`"contract":"c","transport":"sync","mechanism":"http-json"}]}`)
	if _, err := Load(catalog, "", ""); err == nil {
		t.Fatal("связь на неизвестный контекст не отвергнута")
	}
}

// TestLoadRejectsUnknownServiceInGroup: сервис группы, которого нет в каталоге,
// — ошибка.
func TestLoadRejectsUnknownServiceInGroup(t *testing.T) {
	catalog := writeTempFile(t, "catalog.json",
		`{"contexts":[{"id":"a","service":"known"}],"relationships":[]}`)
	services := writeTempFile(t, "services.yaml",
		"processes:\n  - name: core\n    services:\n      - id: ghost\n        roles: [api]\n")
	if _, err := Load(catalog, services, ""); err == nil {
		t.Fatal("неизвестный сервис группы не отвергнут")
	}
}

// TestLoadAssignsGroup проверяет, что группы deploy назначаются узлу сервиса.
func TestLoadAssignsGroup(t *testing.T) {
	catalog := writeTempFile(t, "catalog.json",
		`{"contexts":[{"id":"a","name":"A","service":"alpha","roles":["api"]}],"relationships":[]}`)
	services := writeTempFile(t, "services.yaml",
		"processes:\n  - name: revenue\n    mechanism: in-process\n    services:\n"+
			"      - id: alpha\n        roles: [api]\n")
	graph, err := Load(catalog, services, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(graph.Nodes) != 1 {
		t.Fatalf("узлов: %d, ожидался 1", len(graph.Nodes))
	}
	if !reflect.DeepEqual(graph.Nodes[0].Groups, []string{"revenue"}) {
		t.Errorf("группы = %v, ожидались [revenue]", graph.Nodes[0].Groups)
	}
}

// TestLoadAssignsMultipleGroups: сервис, объявленный в двух процессах, остаётся
// одним узлом и получает обе группы в детерминированном (лексикографическом)
// порядке, а его основная группа — первая из них.
func TestLoadAssignsMultipleGroups(t *testing.T) {
	catalog := writeTempFile(t, "catalog.json",
		`{"contexts":[{"id":"a","name":"A","service":"alpha","roles":["api","sender"]}],"relationships":[]}`)
	services := writeTempFile(t, "services.yaml",
		"processes:\n"+
			"  - name: platform\n    mechanism: in-process\n    services:\n"+
			"      - id: alpha\n        roles: [api]\n"+
			"  - name: delivery\n    mechanism: in-process\n    services:\n"+
			"      - id: alpha\n        roles: [sender]\n")
	graph, err := Load(catalog, services, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(graph.Nodes) != 1 {
		t.Fatalf("узлов: %d, ожидался 1 (сервис — один узел)", len(graph.Nodes))
	}
	want := []string{"delivery", "platform"}
	if !reflect.DeepEqual(graph.Nodes[0].Groups, want) {
		t.Errorf("группы = %v, ожидались %v", graph.Nodes[0].Groups, want)
	}
	if got := graph.Nodes[0].PrimaryGroup(); got != "delivery" {
		t.Errorf("основная группа = %q, ожидалась delivery", got)
	}
}

// TestReadInfrastructureRejectsUnknownKey: неизвестный ключ узла — ошибка.
func TestReadInfrastructureRejectsUnknownKey(t *testing.T) {
	path := writeTempFile(t, "infrastructure.yaml",
		"nodes:\n  - id: x\n    kind: broker\n    label: \"X\"\n    bogus: 1\n")
	if _, err := readInfrastructure(path); err == nil {
		t.Fatal("неизвестный ключ не отвергнут")
	}
}

// TestReadInfrastructureRejectsUnknownKind: неизвестный вид узла — ошибка.
func TestReadInfrastructureRejectsUnknownKind(t *testing.T) {
	path := writeTempFile(t, "infrastructure.yaml",
		"nodes:\n  - id: x\n    kind: quantum\n    label: \"X\"\n")
	if _, err := readInfrastructure(path); err == nil {
		t.Fatal("неизвестный вид не отвергнут")
	}
}

// TestReadServicesRejectsUnknownKey: неизвестное поле сервиса — ошибка.
func TestReadServicesRejectsUnknownKey(t *testing.T) {
	path := writeTempFile(t, "services.yaml",
		"processes:\n  - name: core\n    services:\n      - id: a\n        roles: [api]\n        bogus: 1\n")
	if _, err := readServices(path); err == nil {
		t.Fatal("неизвестное поле сервиса не отвергнуто")
	}
}
