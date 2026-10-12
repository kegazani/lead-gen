package topology

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Пути к реальным каноническим источникам относительно каталога пакета.
const (
	realCatalogPath        = "../../../architecture/domain-catalog.yaml"
	realServicesPath       = "../../../deploy/services.yaml"
	realInfrastructurePath = "../../../deploy/infrastructure.yaml"
)

// realSources возвращает источники реального репозитория.
func realSources() Sources {
	return Sources{
		CatalogPath:        realCatalogPath,
		ServicesPath:       realServicesPath,
		InfrastructurePath: realInfrastructurePath,
	}
}

// writeTemp кладёт фикстуру во временный файл и возвращает путь.
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("запись фикстуры %s: %v", name, err)
	}
	return path
}

// positions собирает координаты узлов графа по id.
func positions(graph Graph) map[string][2]float64 {
	result := make(map[string][2]float64, len(graph.Nodes))
	for _, node := range graph.Nodes {
		result[node.ID] = [2]float64{node.X, node.Y}
	}
	return result
}

// derivedEdges возвращает множество выведенных рёбер «откуда → куда».
func derivedEdges(graph Graph) map[string]string {
	result := make(map[string]string)
	for _, edge := range graph.Edges {
		if strings.HasPrefix(edge.ID, DerivedEdgePrefix) {
			result[edge.ID] = edge.From + "->" + edge.To
		}
	}
	return result
}

// TestEndpointParsing (2.1) фиксирует разбор внутреннего адреса: числовой порт
// отделяется от хоста, адрес-подстановка сохраняется целиком.
func TestEndpointParsing(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port string
	}{
		{"postgres-green:5432", "postgres-green", "5432"},
		{"temporal:7233", "temporal", "7233"},
		{"<внутренний адрес контура клиента>", "", ""},
		{"http://prometheus:9090/metrics", "", ""},
	}
	for _, tc := range cases {
		got := parseEndpoint(tc.in)
		if got.Address != tc.in || got.Host != tc.host || got.Port != tc.port {
			t.Errorf("parseEndpoint(%q) = %+v, ожидалось host=%q port=%q", tc.in, got, tc.host, tc.port)
		}
	}
}

// TestBuildRealSources (2.2, 2.3) проверяет сборку из настоящих источников:
// 12 узлов-сервисов, 8 инфраструктурных узлов, связи каталога с согласованным
// транспортом, группы deploy и адреса инфраструктуры.
func TestBuildRealSources(t *testing.T) {
	graph := Build(realSources())
	if len(graph.Warnings) != 0 {
		t.Errorf("несогласованные источники дали предупреждения: %v", graph.Warnings)
	}

	byID := make(map[string]Node, len(graph.Nodes))
	var services, infrastructure, unknown int
	for _, node := range graph.Nodes {
		byID[node.ID] = node
		switch node.Kind {
		case NodeService:
			services++
			if node.Health.State != HealthUnknown {
				t.Errorf("сервис %q: health = %q, ожидался unknown", node.ID, node.Health.State)
			}
		case NodeInfrastructure:
			infrastructure++
			if node.Health.State != HealthUnknown {
				t.Errorf("инфраструктура %q: health = %q, ожидался unknown", node.ID, node.Health.State)
			}
		case NodeUnknown:
			unknown++
		default:
			t.Errorf("узел %q: неизвестный вид %q", node.ID, node.Kind)
		}
	}
	if services != 12 {
		t.Errorf("узлов-сервисов: %d, ожидалось 12", services)
	}
	if infrastructure != 8 {
		t.Errorf("инфраструктурных узлов: %d, ожидалось 8", infrastructure)
	}
	if unknown != 0 {
		t.Errorf("узлов unknown: %d, ожидалось 0", unknown)
	}

	var catalogEdges, derived int
	nodeIDs := make(map[string]bool, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodeIDs[node.ID] = true
	}
	for _, edge := range graph.Edges {
		if strings.HasPrefix(edge.ID, DerivedEdgePrefix) {
			derived++
		} else {
			catalogEdges++
			if edge.Contract == "" {
				t.Errorf("связь %q без контракта", edge.ID)
			}
			switch edge.Integration {
			case "command", "query":
				if edge.Transport != "sync" || edge.Mechanism != "http-json" {
					t.Errorf("связь %q: %s/%s, ожидалось sync/http-json", edge.ID, edge.Transport, edge.Mechanism)
				}
			case "event", "policy":
				if edge.Transport != "async" || edge.Mechanism != "amqp" {
					t.Errorf("связь %q: %s/%s, ожидалось async/amqp", edge.ID, edge.Transport, edge.Mechanism)
				}
			default:
				t.Errorf("связь %q: неизвестный тип интеграции %q", edge.ID, edge.Integration)
			}
		}
		if !nodeIDs[edge.From] {
			t.Errorf("ребро %q ссылается на неизвестный источник %q", edge.ID, edge.From)
		}
		if !nodeIDs[edge.To] {
			t.Errorf("ребро %q ссылается на неизвестный приёмник %q", edge.ID, edge.To)
		}
		if edge.Intensity.State != IntensityUnknown || edge.Intensity.Available {
			t.Errorf("ребро %q: интенсивность %+v, ожидался unknown без измерения", edge.ID, edge.Intensity)
		}
	}
	if catalogEdges != 51 {
		t.Errorf("связей каталога: %d, ожидалось 51", catalogEdges)
	}
	if derived == 0 {
		t.Error("не выведено ни одного ребра «сервис → инфраструктура»")
	}

	orchestration, ok := byID["outreach-orchestration"]
	if !ok {
		t.Fatal("нет узла outreach-orchestration")
	}
	if len(orchestration.Roles) == 0 {
		t.Error("у узла-сервиса не заполнены роли")
	}
	if !containsString(orchestration.Groups, "core") {
		t.Errorf("группы outreach-orchestration = %v, ожидалась core", orchestration.Groups)
	}

	green, ok := byID["postgres-green"]
	if !ok {
		t.Fatal("нет узла postgres-green")
	}
	if green.Kind != NodeInfrastructure || green.Zone != "green" {
		t.Errorf("postgres-green: kind=%q zone=%q, ожидались infrastructure/green", green.Kind, green.Zone)
	}
	if len(green.Addresses) == 0 {
		t.Fatal("postgres-green без внутреннего адреса")
	}
	if green.Addresses[0].Host != "postgres-green" || green.Addresses[0].Port != "5432" {
		t.Errorf("postgres-green адрес = %+v, ожидался postgres-green:5432", green.Addresses[0])
	}
	red, ok := byID["postgres-red"]
	if !ok || red.Zone != "red" {
		t.Errorf("postgres-red: %+v, ожидалась зона red", red)
	}
}

// TestDerivedEdgesAreDeterministic (2.4) проверяет, что рёбра
// «сервис → инфраструктура» выводятся детерминированно и не дублируют
// межконтекстные связи каталога.
func TestDerivedEdgesAreDeterministic(t *testing.T) {
	first := derivedEdges(Build(realSources()))
	second := derivedEdges(Build(realSources()))
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("выведенные рёбра не детерминированы:\n%v\n%v", first, second)
	}
	if _, ok := first[DerivedEdgePrefix+"integration-platform->temporal"]; !ok {
		t.Errorf("нет выведенного ребра chains-сервиса к workflow-узлу temporal: %v", first)
	}

	graph := Build(realSources())
	catalogPairs := make(map[string]bool)
	for _, edge := range graph.Edges {
		if !strings.HasPrefix(edge.ID, DerivedEdgePrefix) {
			catalogPairs[edge.From+"->"+edge.To] = true
		}
	}
	for _, edge := range graph.Edges {
		if strings.HasPrefix(edge.ID, DerivedEdgePrefix) && catalogPairs[edge.From+"->"+edge.To] {
			t.Errorf("выведенное ребро %q дублирует межконтекстную связь каталога", edge.ID)
		}
	}
}

// TestLayoutIsDeterministic (2.5) проверяет, что два построения дают одинаковые
// координаты узлов.
func TestLayoutIsDeterministic(t *testing.T) {
	first := positions(Build(realSources()))
	second := positions(Build(realSources()))
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("раскладка не детерминирована:\n%v\n%v", first, second)
	}
	if len(first) == 0 {
		t.Fatal("раскладка пуста")
	}
}

// TestBuildFailClosedOnUnknownReference (2.6) проверяет, что связь на неизвестный
// узел даёт unknown и предупреждение, а не падение и не подставленный узел.
func TestBuildFailClosedOnUnknownReference(t *testing.T) {
	catalogPath := writeTemp(t, "catalog.json",
		`{"contexts":[{"id":"a","name":"A","service":"a","roles":["api"]}],`+
			`"relationships":[{"id":"r","upstream":"a","downstream":"ghost",`+
			`"integration":"event","contract":"c","transport":"async","mechanism":"amqp"}]}`)
	graph := Build(Sources{CatalogPath: catalogPath})

	if len(graph.Warnings) == 0 {
		t.Fatal("ожидались предупреждения о неизвестном узле")
	}
	byID := make(map[string]Node, len(graph.Nodes))
	for _, node := range graph.Nodes {
		byID[node.ID] = node
	}
	ghost, ok := byID["ghost"]
	if !ok {
		t.Fatal("нет узла unknown для ссылки на неизвестный контекст")
	}
	if ghost.Kind != NodeUnknown {
		t.Errorf("ghost.kind = %q, ожидался unknown", ghost.Kind)
	}
	if ghost.Health.State != HealthUnknown {
		t.Errorf("ghost.health = %q, ожидался unknown", ghost.Health.State)
	}
	if _, ok := byID["a"]; !ok {
		t.Error("известный узел a потерян")
	}
	found := false
	for _, edge := range graph.Edges {
		if edge.ID == "r" && edge.From == "a" && edge.To == "ghost" {
			found = true
		}
	}
	if !found {
		t.Error("связь r не сохранена с пометкой неизвестного приёмника")
	}
}

// TestBuildFailClosedOnMissingSource (2.6) проверяет, что недоступный источник не
// роняет сборку.
func TestBuildFailClosedOnMissingSource(t *testing.T) {
	graph := Build(Sources{CatalogPath: filepath.Join(t.TempDir(), "absent.yaml")})
	if len(graph.Warnings) == 0 {
		t.Fatal("отсутствующий источник не дал предупреждения")
	}
	if len(graph.Nodes) != 0 {
		t.Errorf("узлов: %d, ожидалось 0 при отсутствующем каталоге", len(graph.Nodes))
	}
}

// TestBuildFailClosedOnInvalidInfrastructure (2.6) проверяет, что некорректный
// вид инфраструктуры не подставляется и не роняет сборку.
func TestBuildFailClosedOnInvalidInfrastructure(t *testing.T) {
	catalogPath := writeTemp(t, "catalog.json",
		`{"contexts":[{"id":"a","name":"A","service":"a","roles":["api"]}],"relationships":[]}`)
	infrastructurePath := writeTemp(t, "infrastructure.yaml",
		"nodes:\n  - id: x\n    kind: quantum\n    label: \"X\"\n    addresses: [\"x:1\"]\n")
	graph := Build(Sources{CatalogPath: catalogPath, InfrastructurePath: infrastructurePath})
	if len(graph.Warnings) == 0 {
		t.Fatal("неизвестный вид инфраструктуры не дал предупреждения")
	}
	for _, node := range graph.Nodes {
		if node.Kind == NodeInfrastructure {
			t.Errorf("некорректный инфраструктурный узел подставлен: %q", node.ID)
		}
	}
}

// TestLoaderCacheAvoidsReread (2.7) проверяет, что повторная сборка в пределах
// интервала не перечитывает файлы, а Invalidate заставляет пересобрать.
func TestLoaderCacheAvoidsReread(t *testing.T) {
	var reads int32
	sources := Sources{
		CatalogPath:        "catalog.json",
		ServicesPath:       "services.yaml",
		InfrastructurePath: "infrastructure.yaml",
		ReadFile: func(path string) ([]byte, error) {
			atomic.AddInt32(&reads, 1)
			switch path {
			case "catalog.json":
				return []byte(`{"contexts":[{"id":"a","name":"A","service":"a","roles":["api"]}],"relationships":[]}`), nil
			case "services.yaml":
				return []byte("processes:\n  - name: core\n    services:\n      - id: a\n        roles: [api]\n"), nil
			case "infrastructure.yaml":
				return []byte("nodes:\n  - id: postgres-green\n    kind: postgres\n    label: \"PG\"\n    zone: green\n    addresses: [\"pg:5432\"]\n"), nil
			}
			return nil, os.ErrNotExist
		},
	}
	loader := NewLoader(sources, time.Hour)
	first := loader.Load()
	afterFirst := atomic.LoadInt32(&reads)
	if afterFirst == 0 {
		t.Fatal("первая сборка не прочитала источники")
	}
	second := loader.Load()
	if got := atomic.LoadInt32(&reads); got != afterFirst {
		t.Errorf("повторная сборка в пределах ttl перечитала файлы: %d → %d", afterFirst, got)
	}
	if !reflect.DeepEqual(positions(first), positions(second)) {
		t.Error("кэшированный граф отличается от исходного")
	}
	loader.Invalidate()
	_ = loader.Load()
	if got := atomic.LoadInt32(&reads); got <= afterFirst {
		t.Errorf("после Invalidate граф не пересобран: чтений %d", got)
	}
}

// TestLoaderConcurrentAccess проверяет потокобезопасность доступа к кэшу.
func TestLoaderConcurrentAccess(t *testing.T) {
	loader := NewLoader(realSources(), time.Millisecond)
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = loader.Load()
		}()
	}
	wait.Wait()
}
