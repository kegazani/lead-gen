package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/grants"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/observability/prometheus"
	"lead-gen/backend/platform/topology"
	"lead-gen/backend/platform/topology/catalog"
)

// Пути к каноническим источникам относительно каталога пакета
// (backend/contexts/platform-governance/internal/adapters/http).
const (
	realCatalogPath        = "../../../../../../architecture/domain-catalog.yaml"
	realServicesPath       = "../../../../../../deploy/services.yaml"
	realInfrastructurePath = "../../../../../../deploy/infrastructure.yaml"
)

// realTopologyLoader собирает загрузчик модели из реальных источников без кэша.
func realTopologyLoader() *topology.Loader {
	return topology.NewLoader(topology.Sources{
		CatalogPath:        realCatalogPath,
		ServicesPath:       realServicesPath,
		InfrastructurePath: realInfrastructurePath,
	}, 0)
}

// newTopologyServer собирает сервер консоли с администратором user-1 в тенанте
// tenant-1 и клиентом user-3 в tenant-2, как и остальные тесты консоли. Дополняет
// переданную конфигурацию правами доступа.
func newTopologyServer(t *testing.T, cfg platformgovernance.Config) *httptest.Server {
	t.Helper()
	grantsTable, err := grants.New(map[string][]string{
		"admin-1": {string(domain.RoleAdmin)},
		"user-1":  {string(domain.RoleAdmin)},
	})
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	cfg.Grants = grantsTable
	slice, err := platformgovernance.Build(cfg)
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	createTenantForConsole(t, slice, "cmd-1", "tenant-1", "ООО Ромашка", "user-1", []string{string(domain.RoleAdmin)})
	createTenantForConsole(t, slice, "cmd-2", "tenant-2", "ООО Василёк", "user-3", []string{string(domain.RoleClient)})
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// fakeMetrics — управляемый двойник клиента Prometheus для тестов прокси.
type fakeMetrics struct {
	matrix prometheus.Matrix
	err    error
}

func (f fakeMetrics) Query(context.Context, string) (prometheus.Vector, []string, error) {
	return nil, nil, f.err
}

func (f fakeMetrics) QueryRange(context.Context, string, time.Time, time.Time, time.Duration) (prometheus.Matrix, []string, error) {
	return f.matrix, nil, f.err
}

// graphJSON — разобранный ответ /platform/topology/graph.
type graphJSON struct {
	GeneratedAt time.Time `json:"generatedAt"`
	Nodes       []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	} `json:"nodes"`
	Edges []struct {
		ID string `json:"id"`
	} `json:"edges"`
}

// metricsJSON — разобранный ответ /platform/topology/metrics.
type metricsJSON struct {
	Available bool   `json:"available"`
	Metric    string `json:"metric"`
	Watermark string `json:"watermark"`
	Series    []struct {
		Points []struct {
			Time  time.Time `json:"t"`
			Value float64   `json:"v"`
		} `json:"points"`
	} `json:"series"`
}

// getJSON выполняет GET и разбирает JSON в target.
func getJSON(t *testing.T, server *httptest.Server, path string, target any) *http.Response {
	t.Helper()
	resp := get(t, server, path)
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("разбор JSON %s: %v", path, err)
	}
	return resp
}

// TestTemplatesContainNoUserTextLiterals проверяет критерий задачи 7.1 в контексте:
// в шаблонах консоли нет кириллицы — весь пользовательский текст приходит из
// каталога сообщений через функцию text. Там же проверяется отсутствие inline
// скриптов и стилей (строгая CSP).
func TestTemplatesContainNoUserTextLiterals(t *testing.T) {
	entries, err := os.ReadDir("templates")
	if err != nil {
		t.Fatalf("чтение каталога шаблонов: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("templates", entry.Name()))
		if err != nil {
			t.Fatalf("чтение шаблона %s: %v", entry.Name(), err)
		}
		content := string(data)
		if index, found := firstCyrillic(content); found {
			t.Errorf("шаблон %s содержит пользовательский текст на позиции %d", entry.Name(), index)
		}
		if strings.Contains(content, "<script") && !strings.Contains(content, "src=") {
			t.Errorf("шаблон %s содержит inline-скрипт", entry.Name())
		}
		if strings.Contains(content, ` style="`) {
			t.Errorf("шаблон %s содержит inline-стиль", entry.Name())
		}
	}
}

// firstCyrillic возвращает позицию первой кириллической буквы.
func firstCyrillic(content string) (int, bool) {
	for index, r := range content {
		if (r >= 0x0400 && r <= 0x04FF) || (r >= 0x0500 && r <= 0x052F) {
			return index, true
		}
	}
	return 0, false
}

// TestTopologyGraphJSON (6.2) проверяет JSON-модель графа: 12 сервисов каталога и
// все связи, детерминированный порядок узлов и отсутствие внутренних адресов.
func TestTopologyGraphJSON(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Topology: realTopologyLoader()})

	var graph graphJSON
	resp := getJSON(t, server, "/platform/topology/graph?tenant_id=tenant-1&actor=user-1", &graph)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET graph = %d, ожидался 200", resp.StatusCode)
	}

	services := 0
	for _, node := range graph.Nodes {
		if node.Kind == "service" {
			services++
		}
	}
	if services != 12 {
		t.Fatalf("узлов-сервисов: %d, ожидалось 12", services)
	}

	// Все связи каталога присутствуют в ответе.
	document, err := catalog.Read(realCatalogPath)
	if err != nil {
		t.Fatalf("чтение каталога: %v", err)
	}
	present := make(map[string]bool, len(graph.Edges))
	for _, edge := range graph.Edges {
		present[edge.ID] = true
	}
	for _, relationship := range document.Relationships {
		if !present[relationship.ID] {
			t.Errorf("в графе нет связи каталога %q", relationship.ID)
		}
	}

	// Внутренние адреса наружу не отдаются.
	raw := body(t, get(t, server, "/platform/topology/graph?tenant_id=tenant-1&actor=user-1"))
	if strings.Contains(raw, ":5432") || strings.Contains(raw, "rabbitmq:5672") {
		t.Fatalf("внутренние адреса утекли в JSON графа:\n%s", raw)
	}

	// Порядок узлов детерминирован: два ответа дают одинаковую последовательность.
	var again graphJSON
	getJSON(t, server, "/platform/topology/graph?tenant_id=tenant-1&actor=user-1", &again)
	if len(again.Nodes) != len(graph.Nodes) {
		t.Fatalf("детерминизм нарушен: %d и %d узлов", len(graph.Nodes), len(again.Nodes))
	}
	for i := range graph.Nodes {
		if graph.Nodes[i].ID != again.Nodes[i].ID {
			t.Fatalf("порядок узлов не детерминирован: %q и %q", graph.Nodes[i].ID, again.Nodes[i].ID)
		}
	}
}

// TestTopologyPageSvgAndTable (6.1) проверяет серверный SVG-граф, доступную
// таблицу узлов и связей, область aria-live и подключение скрипта доступности.
func TestTopologyPageSvgAndTable(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Topology: realTopologyLoader()})
	resp := get(t, server, "/platform/topology?tenant_id=tenant-1&actor=user-1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET topology = %d, ожидался 200", resp.StatusCode)
	}
	if cacheControl := resp.Header.Get("Cache-Control"); !strings.Contains(cacheControl, "no-store") {
		t.Fatalf("Cache-Control = %q, ожидался no-store", cacheControl)
	}
	page := body(t, resp)
	for _, want := range []string{
		"<svg", "role=\"img\"", "data-topology-graph",
		"data-node-id=", "data-edge-id=",
		"/platform/topology/node/",
		"data-live-region", "/static/console-a11y.js",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("страница топологии не содержит %q", want)
		}
	}
}

// TestTopologyNodeCard (6.3) проверяет карточку узла: роли и группа deploy берутся
// из источников, а изменение группы в deploy/services.yaml меняет карточку без
// правки шаблона. HTMX-запрос получает фрагмент без каркаса страницы.
func TestTopologyNodeCard(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json")
	servicesPath := filepath.Join(dir, "services.yaml")
	writeFile(t, catalogPath, `{"contexts":[{"id":"ctx-a","name":"Сервис А","service":"svc-a","roles":["api","worker"],"contracts":["contract-a"]}],"relationships":[]}`)
	writeFile(t, servicesPath, "processes:\n  - name: alpha\n    services:\n      - id: svc-a\n        roles: [api, worker]\n")

	loader := topology.NewLoader(topology.Sources{CatalogPath: catalogPath, ServicesPath: servicesPath}, 0)
	server := newTopologyServer(t, platformgovernance.Config{Topology: loader})

	page := body(t, get(t, server, "/platform/topology/node/svc-a?tenant_id=tenant-1&actor=user-1"))
	for _, want := range []string{"Сервис А", "svc-a", "api", "worker", "alpha", "contract-a"} {
		if !strings.Contains(page, want) {
			t.Fatalf("карточка узла не показала %q:\n%s", want, page)
		}
	}

	// Изменение группы в deploy/services.yaml меняет карточку без правки шаблона.
	writeFile(t, servicesPath, "processes:\n  - name: beta\n    services:\n      - id: svc-a\n        roles: [api, worker]\n")
	changed := body(t, get(t, server, "/platform/topology/node/svc-a?tenant_id=tenant-1&actor=user-1"))
	if !strings.Contains(changed, "beta") {
		t.Fatalf("карточка не отразила смену группы deploy:\n%s", changed)
	}

	// Фрагмент для HTMX не содержит каркаса страницы.
	fragment := body(t, htmxGet(t, server, "/platform/topology/node/svc-a?tenant_id=tenant-1&actor=user-1"))
	if strings.Contains(fragment, "<!DOCTYPE") {
		t.Fatalf("HTMX-фрагмент содержит каркас всей страницы:\n%s", fragment)
	}
	if !strings.Contains(fragment, "topology-card") {
		t.Fatalf("HTMX-фрагмент не является карточкой узла:\n%s", fragment)
	}
}

// TestTopologyMetricsUnavailable (3.3, 3.6) проверяет честный исход недоступности
// Prometheus: available:false, watermark, без нулевых точек. Неизвестная панель
// отвергается, а рабочий клиент отдаёт точки.
func TestTopologyMetricsUnavailable(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Topology: realTopologyLoader()})

	var unavailable metricsJSON
	resp := getJSON(t, server, "/platform/topology/metrics?tenant_id=tenant-1&actor=user-1&metric=node_request_rate&node=svc-a", &unavailable)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET metrics = %d, ожидался 200", resp.StatusCode)
	}
	if unavailable.Available {
		t.Fatal("неподключённые метрики выданы за доступные")
	}
	if unavailable.Watermark == "" {
		t.Fatal("нет watermark при недоступности метрик")
	}
	if len(unavailable.Series) != 0 {
		t.Fatalf("при недоступности подставлены точки: %+v", unavailable.Series)
	}
	raw := body(t, get(t, server, "/platform/topology/metrics?tenant_id=tenant-1&actor=user-1&metric=node_request_rate&node=svc-a"))
	if strings.Contains(raw, `"v":0`) {
		t.Fatalf("при недоступности появились нулевые точки:\n%s", raw)
	}

	// Ошибка транспорта от подключённого клиента — тот же честный исход.
	broken := newTopologyServer(t, platformgovernance.Config{Topology: realTopologyLoader(), TopologyMetrics: fakeMetrics{err: context.DeadlineExceeded}})
	var transport metricsJSON
	getJSON(t, broken, "/platform/topology/metrics?tenant_id=tenant-1&actor=user-1&metric=node_request_rate&node=svc-a", &transport)
	if transport.Available || transport.Watermark == "" {
		t.Fatalf("ошибка транспорта не дала available:false с watermark: %+v", transport)
	}

	// Неизвестная панель — отказ, а не произвольный PromQL.
	bad := get(t, server, "/platform/topology/metrics?tenant_id=tenant-1&actor=user-1&metric=free&node=svc-a")
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Fatalf("неизвестная панель = %d, ожидался 400", bad.StatusCode)
	}

	// Рабочий клиент отдаёт точки: прокси не является заглушкой.
	working := newTopologyServer(t, platformgovernance.Config{Topology: realTopologyLoader(), TopologyMetrics: fakeMetrics{matrix: prometheus.Matrix{
		{Metric: map[string]string{"service": "svc-a"}, Samples: []prometheus.Sample{{Time: time.Now().UTC(), Value: 3.5}}},
	}}})
	var ok metricsJSON
	getJSON(t, working, "/platform/topology/metrics?tenant_id=tenant-1&actor=user-1&metric=node_request_rate&node=svc-a", &ok)
	if !ok.Available || len(ok.Series) != 1 || len(ok.Series[0].Points) != 1 || ok.Series[0].Points[0].Value != 3.5 {
		t.Fatalf("прокси не вернул точки рабочего клиента: %+v", ok)
	}
}

// TestTopologyDeniedForNonAdmin (7.1, 7.2) проверяет, что отказ не зависит от
// навигации: не-администратор получает отказ на каждом эндпоинте напрямую.
func TestTopologyDeniedForNonAdmin(t *testing.T) {
	server := newTopologyServer(t, platformgovernance.Config{Topology: realTopologyLoader()})
	paths := []string{
		"/platform/topology?tenant_id=tenant-2&actor=user-3",
		"/platform/topology/graph?tenant_id=tenant-2&actor=user-3",
		"/platform/topology/node/svc-a?tenant_id=tenant-2&actor=user-3",
		"/platform/topology/metrics?tenant_id=tenant-2&actor=user-3&metric=node_request_rate",
	}
	for _, path := range paths {
		resp := get(t, server, path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s = %d, ожидался 403", path, resp.StatusCode)
		}
	}

	// Администратор получает страницу.
	admin := get(t, server, "/platform/topology?tenant_id=tenant-1&actor=user-1")
	admin.Body.Close()
	if admin.StatusCode != http.StatusOK {
		t.Fatalf("администратор = %d, ожидался 200", admin.StatusCode)
	}
}

// TestTopologyLayoutFallback (5.4) проверяет, что недоступность сохранённой
// раскладки не роняет экран: отдаётся детерминированная раскладка по умолчанию.
func TestTopologyLayoutFallback(t *testing.T) {
	layout := func(context.Context, string, string) (map[string]platformgovernance.NodePosition, error) {
		return nil, context.DeadlineExceeded
	}
	server := newTopologyServer(t, platformgovernance.Config{Topology: realTopologyLoader(), TopologyLayout: layout})
	resp := get(t, server, "/platform/topology?tenant_id=tenant-1&actor=user-1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("экран при недоступной раскладке = %d, ожидался 200", resp.StatusCode)
	}
	page := body(t, resp)
	if !strings.Contains(page, "data-topology-graph") || !strings.Contains(page, "viewBox=") {
		t.Fatalf("экран без раскладки по умолчанию:\n%s", page)
	}
}

// TestSensitivePostRateLimited (7.5) проверяет, что превышение порога чувствительных
// POST блокируется и фиксируется структурированным логом.
func TestSensitivePostRateLimited(t *testing.T) {
	var logBuffer bytes.Buffer
	server := newTopologyServer(t, platformgovernance.Config{
		Topology: realTopologyLoader(),
		Logger:   observ.NewLoggerWithWriter(&logBuffer),
	})

	blocked := false
	for i := 0; i < 40; i++ {
		resp := postForm(t, server, "/register", map[string][]string{"company_name": {""}})
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("превышение порога входов не было заблокировано")
	}
	if !strings.Contains(logBuffer.String(), "предел частоты") {
		t.Fatalf("блокировка не зафиксирована в логе:\n%s", logBuffer.String())
	}
}

// htmxGet выполняет GET с заголовком HTMX, чтобы получить фрагмент.
func htmxGet(t *testing.T, server *httptest.Server, path string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
	if err != nil {
		t.Fatalf("сборка запроса %s: %v", path, err)
	}
	request.Header.Set("HX-Request", "true")
	resp, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// writeFile пишет фикстуру и падает при ошибке.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("запись фикстуры %s: %v", path, err)
	}
}
