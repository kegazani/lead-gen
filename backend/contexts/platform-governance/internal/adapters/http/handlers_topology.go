package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	app "lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/platform/observability/prometheus"
	"lead-gen/backend/platform/topology"
	"lead-gen/frontend/components"
)

// NodePosition — позиция узла сохранённой раскладки.
type NodePosition struct{ X, Y float64 }

// TopologyLayoutSource возвращает сохранённые позиции раскладки пользователя.
// Ошибка или отсутствие записи означает детерминированную раскладку по умолчанию
// (service-topology-console, задача 5.4): недоступность раскладки не роняет экран.
type TopologyLayoutSource func(ctx context.Context, tenantID, userID string) (map[string]NodePosition, error)

// MetricsQuerier — часть клиента Prometheus, нужная серверному прокси. Браузер к
// Prometheus не ходит: запрос выполняет серверная часть (задача 3.3).
type MetricsQuerier interface {
	Query(ctx context.Context, query string) (prometheus.Vector, []string, error)
	QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (prometheus.Matrix, []string, error)
}

// Пределы прокси метрик: число точек на ответ, максимальный диапазон и
// минимальный шаг. Превышение диапазона усекается, а шаг увеличивается с
// пометкой — предсказуемый отказ вместо перегрузки.
const (
	topologyMaxPoints = 300
	topologyMaxRange  = 24 * time.Hour
	topologyMinStep   = 15 * time.Second
	// topologyMetricsTimeout — жёсткий таймаут запроса к Prometheus, меньший
	// бюджета HTTP-запроса пользователя: превышение — предсказуемый отказ, а не
	// зависание (Решение 10).
	topologyMetricsTimeout = 5 * time.Second
)

// handleTopology обслуживает GET /platform/topology: полная страница вкладки —
// серверный SVG-граф и доступная текстовая таблица узлов и связей.
func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	actor := consoleActor(r)
	tenant, ok := s.requireTenant(w, r, actor)
	if !ok {
		return
	}
	if status, message := s.decideSection(r, tenant, app.SectionTopology, app.ActionView); status != 0 {
		s.renderConsoleDenied(w, status, message)
		return
	}
	graph := s.topologyGraph(r.Context(), tenant, actor)
	view := s.buildTopologyView(graph, tenant, actor)
	view.Unavailable = s.deps.Topology == nil
	s.render(w, http.StatusOK, pageTopology, components.Page{
		Layout:  components.Layout{Title: s.messages.Text(msgTopologyTitle)},
		Sidebar: s.consoleNav("/platform/topology", tenant, actor),
		View:    view,
	})
}

// handleTopologyGraph обслуживает GET /platform/topology/graph: JSON-модель
// графа для «острова» и опроса. Ответ — детерминированный (порядок узлов и
// рёбер задаёт построитель) и не содержит внутренних адресов.
func (s *Server) handleTopologyGraph(w http.ResponseWriter, r *http.Request) {
	tenant := s.activeTenant(r)
	if status, message := s.decideSection(r, tenant, app.SectionTopology, app.ActionView); status != 0 {
		s.writeJSONError(w, status, message)
		return
	}
	graph := s.topologyGraph(r.Context(), tenant, consoleActor(r))
	s.writeJSON(w, http.StatusOK, buildGraphJSON(graph))
}

// handleTopologyNode обслуживает GET /platform/topology/node/{id}: серверную
// карточку узла. При HTMX-запросе отдаётся фрагмент, иначе — полная страница;
// данные берутся из каталога и deploy/services.yaml.
func (s *Server) handleTopologyNode(w http.ResponseWriter, r *http.Request) {
	tenant := s.activeTenant(r)
	actor := consoleActor(r)
	if status, message := s.decideSection(r, tenant, app.SectionTopology, app.ActionView); status != 0 {
		s.renderConsoleDenied(w, status, message)
		return
	}
	id := r.PathValue("id")
	graph := s.topologyGraph(r.Context(), tenant, actor)
	card, found := s.buildNodeCard(graph, id, tenant, actor)
	if !found {
		// Даже у ненайденного узла ссылка «к топологии» ведёт на рабочий экран.
		card.BackURL = topologyURL(tenant, actor)
	}
	model := topologyNodePage{
		Found:    found,
		Card:     card,
		Missing:  s.messages.Text(msgTopologyNodeMissing),
		Live:     components.Live{ID: "topology-live"},
		TenantID: tenant,
		Actor:    actor,
	}
	status := http.StatusOK
	if !found {
		status = http.StatusNotFound
	}
	if r.Header.Get("HX-Request") == "true" {
		s.renderFragment(w, status, pageTopologyNode, "node-card", model)
		return
	}
	s.render(w, status, pageTopologyNode, components.Page{
		Layout:  components.Layout{Title: s.messages.Text(msgTopologyNodeTitle)},
		Sidebar: s.consoleNav("/platform/topology", tenant, actor),
		View:    model,
	})
}

// handleTopologyMetrics обслуживает GET /platform/topology/metrics — серверный
// прокси Prometheus. Принимается только объявленная панель (не произвольный
// PromQL). Недоступность Prometheus — честный исход available:false с watermark,
// без нулевых точек (задачи 3.3, 3.6).
func (s *Server) handleTopologyMetrics(w http.ResponseWriter, r *http.Request) {
	tenant := s.activeTenant(r)
	if status, message := s.decideSection(r, tenant, app.SectionTopology, app.ActionView); status != 0 {
		s.writeJSONError(w, status, message)
		return
	}
	node := strings.TrimSpace(r.URL.Query().Get("node"))
	panel, ok := s.topologyPanel(strings.TrimSpace(r.URL.Query().Get("metric")))
	if !ok {
		s.writeJSONError(w, http.StatusBadRequest, "неизвестная панель метрик")
		return
	}
	at := time.Now().UTC()
	if s.deps.TopologyMetrics == nil {
		s.writeTopologyMetricsUnavailable(w, panel, node, at)
		return
	}
	window, step, warnings, err := resolveMetricsWindow(panel, r.URL.Query().Get("range"), r.URL.Query().Get("step"))
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	end := at
	start := end.Add(-window)
	queryCtx, cancel := context.WithTimeout(r.Context(), topologyMetricsTimeout)
	defer cancel()
	matrix, promWarnings, err := s.deps.TopologyMetrics.QueryRange(queryCtx, renderPanelQuery(panel.Query, node), start, end, step)
	if err != nil {
		// Ошибка транспорта — не ряд с нулями: экран покажет watermark.
		s.writeTopologyMetricsUnavailable(w, panel, node, at)
		return
	}
	s.writeJSON(w, http.StatusOK, topologyMetricsResponse{
		Available:    true,
		Metric:       panel.Key,
		PanelLabel:   s.messages.Text(panel.LabelKey),
		Node:         node,
		Unit:         panel.Unit,
		GeneratedAt:  at,
		RangeSeconds: window.Seconds(),
		StepSeconds:  step.Seconds(),
		Series:       metricsSeries(matrix),
		Warnings:     append(warnings, promWarnings...),
	})
}

// topologyGraph возвращает модель графа. Если сохранённая раскладка недоступна
// или пуста, возвращается детерминированная раскладка по умолчанию (задача 5.4).
func (s *Server) topologyGraph(ctx context.Context, tenant, actor string) topology.Graph {
	if s.deps.Topology == nil {
		return topology.Graph{}
	}
	graph := s.deps.Topology.Load()
	if s.deps.TopologyLayout == nil {
		return graph
	}
	positions, err := s.deps.TopologyLayout(ctx, tenant, actor)
	if err != nil || len(positions) == 0 {
		return graph
	}
	return applyPositions(graph, positions)
}

// topologyPanels возвращает объявленный перечень панелей; пустой — встроенный.
func (s *Server) topologyPanels() []TopologyPanel {
	if len(s.deps.TopologyPanels) > 0 {
		return s.deps.TopologyPanels
	}
	return DefaultTopologyPanels()
}

// topologyPanel ищет объявленную панель по ключу. Неизвестный ключ — отказ, а не
// попытка выполнить произвольный запрос.
func (s *Server) topologyPanel(key string) (TopologyPanel, bool) {
	if key == "" {
		return TopologyPanel{}, false
	}
	for _, panel := range s.topologyPanels() {
		if panel.Key == key {
			return panel, true
		}
	}
	return TopologyPanel{}, false
}

// resolveMetricsWindow проверяет диапазон и шаг, ограничивает число точек и
// возвращает пометки о применённых ограничениях.
func resolveMetricsWindow(panel TopologyPanel, rangeParam, stepParam string) (time.Duration, time.Duration, []string, error) {
	window := panel.Range
	if window <= 0 {
		window = time.Hour
	}
	if rangeParam != "" {
		parsed, err := time.ParseDuration(rangeParam)
		if err != nil || parsed <= 0 {
			return 0, 0, nil, fmt.Errorf("некорректный диапазон")
		}
		if parsed > topologyMaxRange {
			parsed = topologyMaxRange
		}
		window = parsed
	}

	step := panel.Step
	if step <= 0 {
		step = time.Minute
	}
	if stepParam != "" {
		parsed, err := time.ParseDuration(stepParam)
		if err != nil || parsed <= 0 {
			return 0, 0, nil, fmt.Errorf("некорректный шаг")
		}
		step = parsed
	}
	if step < topologyMinStep {
		step = topologyMinStep
	}

	var warnings []string
	if points := int(window/step) + 1; points > topologyMaxPoints {
		step = time.Duration(int64(window) / int64(topologyMaxPoints))
		if step < topologyMinStep {
			step = topologyMinStep
		}
		warnings = append(warnings, "шаг увеличен до предела точек на ответ")
	}
	if step > window {
		step = window
	}
	return window, step, warnings, nil
}

// metricsSeries переводит матрицу Prometheus в точки ответа, ограничивая число
// точек на серию. Пустая матрица даёт пустой список, а не нулевую серию.
func metricsSeries(matrix prometheus.Matrix) []topologyMetricSeries {
	out := make([]topologyMetricSeries, 0, len(matrix))
	for _, series := range matrix {
		points := make([]topologyMetricPoint, 0, len(series.Samples))
		for index, sample := range series.Samples {
			if index >= topologyMaxPoints {
				break
			}
			points = append(points, topologyMetricPoint{Time: sample.Time, Value: sample.Value})
		}
		labels := series.Metric
		out = append(out, topologyMetricSeries{Labels: labels, Points: points})
	}
	return out
}

// writeTopologyMetricsUnavailable отдаёт честный исход недоступности: без точек и
// с watermark, а не ряд с нулями.
func (s *Server) writeTopologyMetricsUnavailable(w http.ResponseWriter, panel TopologyPanel, node string, at time.Time) {
	s.writeJSON(w, http.StatusOK, topologyMetricsResponse{
		Available:   false,
		Metric:      panel.Key,
		PanelLabel:  s.messages.Text(panel.LabelKey),
		Node:        node,
		Unit:        panel.Unit,
		GeneratedAt: at,
		Series:      []topologyMetricSeries{},
		Watermark:   s.messages.Text(msgTopologyMetricsUnavail),
	})
}

// renderFragment рисует именованный фрагмент в буфер и отдаёт его без каркаса
// страницы (гипермедиа-частичный ответ). Ответ с персональными данными не
// кэшируется.
func (s *Server) renderFragment(w http.ResponseWriter, status int, page, fragment string, model any) {
	if s.pages == nil {
		s.renderPlainError(w, http.StatusInternalServerError, "шаблоны консоли не собраны")
		return
	}
	var buf bytes.Buffer
	if err := s.pages.RenderFragment(&buf, page, fragment, model); err != nil {
		s.renderPlainError(w, http.StatusInternalServerError, "ошибка рендеринга фрагмента")
		return
	}
	components.SetNoStore(w)
	w.Header().Set("Content-Type", contentTypeHTML)
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// writeJSONError отдаёт отказ JSON-эндпоинта.
func (s *Server) writeJSONError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]string{"error": message})
}

// topologyURL собирает адрес страницы топологии.
func topologyURL(tenant, actor string) string {
	return consoleURL("/platform/topology", tenant, actor, "")
}

// nodeCardURL собирает адрес карточки узла.
func nodeCardURL(id, tenant, actor string) string {
	base := "/platform/topology/node/" + url.PathEscape(id)
	values := url.Values{}
	if tenant = strings.TrimSpace(tenant); tenant != "" {
		values.Set("tenant_id", tenant)
	}
	if actor = strings.TrimSpace(actor); actor != "" {
		values.Set("actor", actor)
	}
	if encoded := values.Encode(); encoded != "" {
		return base + "?" + encoded
	}
	return base
}

// buildGraphJSON строит JSON-представление модели графа. Внутренние адреса узлов
// намеренно не включаются: JSON для «острова» их не требует, а наружу они не
// публикуются (задача 7.5).
func buildGraphJSON(graph topology.Graph) topologyGraphResponse {
	resp := topologyGraphResponse{
		GeneratedAt: graph.GeneratedAt,
		Warnings:    append([]string(nil), graph.Warnings...),
		Nodes:       make([]topologyNodeJSON, 0, len(graph.Nodes)),
		Edges:       make([]topologyEdgeJSON, 0, len(graph.Edges)),
	}
	for _, node := range graph.Nodes {
		health := topologyHealthJSON{State: string(node.Health.State), Detail: node.Health.Detail}
		if !node.Health.Since.IsZero() {
			since := node.Health.Since
			health.Since = &since
		}
		resp.Nodes = append(resp.Nodes, topologyNodeJSON{
			ID:        node.ID,
			Kind:      string(node.Kind),
			Label:     node.Label,
			Zone:      node.Zone,
			Roles:     sortedCopy(node.Roles),
			Groups:    sortedCopy(node.Groups),
			Contracts: sortedCopy(node.Contracts),
			Health:    health,
			X:         node.X,
			Y:         node.Y,
		})
	}
	for _, edge := range graph.Edges {
		resp.Edges = append(resp.Edges, topologyEdgeJSON{
			ID:          edge.ID,
			From:        edge.From,
			To:          edge.To,
			Integration: edge.Integration,
			Transport:   edge.Transport,
			Mechanism:   edge.Mechanism,
			Contract:    edge.Contract,
			Intensity: topologyIntensityJSON{
				Available:  edge.Intensity.Available,
				RPS:        edge.Intensity.RPS,
				ErrorRatio: edge.Intensity.ErrorRatio,
				Latency:    edge.Intensity.Latency,
				State:      string(edge.Intensity.State),
			},
		})
	}
	return resp
}

// topologyNodePage — модель карточки узла для страницы и фрагмента.
type topologyNodePage struct {
	Found    bool
	Card     topologyNodeCard
	Missing  string
	Live     components.Live
	TenantID string
	Actor    string
}

// JSON-представления. Поля названы в camelCase; пустые списки опускаются, чтобы
// ответ оставался компактным.

type topologyGraphResponse struct {
	GeneratedAt time.Time          `json:"generatedAt"`
	Nodes       []topologyNodeJSON `json:"nodes"`
	Edges       []topologyEdgeJSON `json:"edges"`
	Warnings    []string           `json:"warnings,omitempty"`
}

type topologyNodeJSON struct {
	ID        string             `json:"id"`
	Kind      string             `json:"kind"`
	Label     string             `json:"label"`
	Zone      string             `json:"zone,omitempty"`
	Roles     []string           `json:"roles,omitempty"`
	Groups    []string           `json:"groups,omitempty"`
	Contracts []string           `json:"contracts,omitempty"`
	Health    topologyHealthJSON `json:"health"`
	X         float64            `json:"x"`
	Y         float64            `json:"y"`
}

type topologyHealthJSON struct {
	State  string     `json:"state"`
	Since  *time.Time `json:"since,omitempty"`
	Detail string     `json:"detail,omitempty"`
}

type topologyEdgeJSON struct {
	ID          string                `json:"id"`
	From        string                `json:"from"`
	To          string                `json:"to"`
	Integration string                `json:"integration,omitempty"`
	Transport   string                `json:"transport,omitempty"`
	Mechanism   string                `json:"mechanism,omitempty"`
	Contract    string                `json:"contract,omitempty"`
	Intensity   topologyIntensityJSON `json:"intensity"`
}

type topologyIntensityJSON struct {
	Available  bool    `json:"available"`
	RPS        float64 `json:"rps,omitempty"`
	ErrorRatio float64 `json:"errorRatio,omitempty"`
	Latency    float64 `json:"latency,omitempty"`
	State      string  `json:"state"`
}

type topologyMetricsResponse struct {
	Available    bool                   `json:"available"`
	Metric       string                 `json:"metric"`
	PanelLabel   string                 `json:"panelLabel,omitempty"`
	Node         string                 `json:"node,omitempty"`
	Unit         string                 `json:"unit,omitempty"`
	GeneratedAt  time.Time              `json:"generatedAt"`
	RangeSeconds float64                `json:"rangeSeconds,omitempty"`
	StepSeconds  float64                `json:"stepSeconds,omitempty"`
	Series       []topologyMetricSeries `json:"series"`
	Watermark    string                 `json:"watermark,omitempty"`
	Warnings     []string               `json:"warnings,omitempty"`
}

type topologyMetricSeries struct {
	Labels map[string]string     `json:"labels,omitempty"`
	Points []topologyMetricPoint `json:"points"`
}

type topologyMetricPoint struct {
	Time  time.Time `json:"t"`
	Value float64   `json:"v"`
}
