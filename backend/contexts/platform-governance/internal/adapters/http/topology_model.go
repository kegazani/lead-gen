package httpapi

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"lead-gen/backend/platform/topology"
	"lead-gen/frontend/components"
)

// Объявленный перечень панелей метрик. Прокси принимает только ключ панели, а не
// произвольный PromQL: запрос — свойство объявленной конфигурации, а не входных
// данных клиента (service-topology-console, Решение 6). Подстановка значения узла
// идёт по токену {{node}} с экранированием, поэтому идентификатор не может
// изменить запрос.
type TopologyPanel struct {
	// Key — машинный ключ панели (значение параметра metric).
	Key string
	// LabelKey — ключ подписи в каталоге сообщений.
	LabelKey components.MessageKey
	// Unit — единица измерения для подписи значения.
	Unit string
	// Query — PromQL-шаблон с необязательным токеном {{node}}.
	Query string
	// Range — диапазон по умолчанию.
	Range time.Duration
	// Step — шаг по умолчанию.
	Step time.Duration
}

// nodeToken — токен подстановки идентификатора узла в запрос панели.
const nodeToken = "{{node}}"

// DefaultTopologyPanels — встроенный объявленный набор панелей. Расширяется
// передачей TopologyPanels в Dependencies; каноническое место конфигурации
// (рядом с инфраструктурой) — вне границ этого изменения.
func DefaultTopologyPanels() []TopologyPanel {
	return []TopologyPanel{
		{
			Key:      "node_request_rate",
			LabelKey: msgTopologyPanelReqRate,
			Unit:     "rps",
			Query:    `sum(rate(http_requests_total{service="{{node}}"}[5m]))`,
			Range:    time.Hour,
			Step:     time.Minute,
		},
		{
			Key:      "node_error_ratio",
			LabelKey: msgTopologyPanelErrRate,
			Unit:     "ratio",
			Query: `sum(rate(http_requests_total{service="{{node}}",code=~"5.."}[5m]))` +
				` / clamp_min(sum(rate(http_requests_total{service="{{node}}"}[5m])), 1e-9)`,
			Range: time.Hour,
			Step:  time.Minute,
		},
	}
}

// renderPanelQuery подставляет идентификатор узла в запрос панели, экранируя
// значение для строкового литерала PromQL. Пустой узел оставляет токен как есть:
// запрос без узла не выполняется панелью, требующей узел.
func renderPanelQuery(query, node string) string {
	return strings.ReplaceAll(query, nodeToken, promLabelValue(node))
}

// promLabelValue экранирует значение метки PromQL: обратный слеш и кавычку.
func promLabelValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `"`, `\"`)
}

// Геометрия серверного SVG. Координаты детерминированной раскладки — единицы
// сетки, поэтому размеры зависят только от модели, не от экрана.
const (
	topologyMargin    = 40.0
	topologyBoxWidth  = 210.0
	topologyBoxHeight = 64.0
)

// topologyView — модель страницы топологии.
type topologyView struct {
	Unavailable bool
	HasNodes    bool
	GeneratedAt time.Time
	Warnings    []string
	ViewBox     string
	Nodes       []topologyNodeItem
	Edges       []topologyEdgeView
	Live        components.Live
	TenantID    string
	Actor       string
}

// topologyNodeItem — узел для SVG и доступной таблицы.
type topologyNodeItem struct {
	ID          string
	Label       string
	KindLabel   string
	ZoneLabel   string
	StateLabel  string
	StateClass  string
	StateIcon   string
	Roles       []string
	Groups      []string
	Contracts   []string
	Connections []string
	CardURL     string
	BoxX        float64
	BoxY        float64
	LabelX      float64
	LabelY      float64
	StateY      float64
}

// topologyEdgeView — ребро для SVG и доступной таблицы.
type topologyEdgeView struct {
	ID             string
	From           string
	To             string
	FromLabel      string
	ToLabel        string
	Integration    string
	Transport      string
	Mechanism      string
	Contract       string
	IntensityLabel string
	X1             float64
	Y1             float64
	X2             float64
	Y2             float64
}

// topologyNodeCard — модель карточки узла.
type topologyNodeCard struct {
	ID          string
	Label       string
	KindLabel   string
	ZoneLabel   string
	StateLabel  string
	StateClass  string
	StateIcon   string
	Roles       []string
	Groups      []string
	Addresses   []string
	Contracts   []string
	Connections []topologyConnectionView
	BackURL     string
}

// topologyConnectionView — связь в карточке узла.
type topologyConnectionView struct {
	Direction string
	Edge      string
	Peer      string
	Transport string
	Mechanism string
	Contract  string
}

// buildTopologyView собирает модель страницы из графа.
func (s *Server) buildTopologyView(graph topology.Graph, tenant, actor string) topologyView {
	view := topologyView{
		GeneratedAt: graph.GeneratedAt,
		Warnings:    append([]string(nil), graph.Warnings...),
		TenantID:    tenant,
		Actor:       actor,
		Live:        components.Live{ID: "topology-live"},
	}
	view.HasNodes = len(graph.Nodes) > 0

	// Карта центров: ребро рисуется от центра источника к центру приёмника.
	centers := make(map[string][2]float64, len(graph.Nodes))
	labels := make(map[string]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		centers[node.ID] = [2]float64{node.X + topologyBoxWidth/2 + topologyMargin, node.Y + topologyBoxHeight/2 + topologyMargin}
		labels[node.ID] = node.Label
	}

	var maxX, maxY float64
	view.Nodes = make([]topologyNodeItem, 0, len(graph.Nodes))
	for _, node := range graph.Nodes {
		boxX := node.X + topologyMargin
		boxY := node.Y + topologyMargin
		if boxX+topologyBoxWidth > maxX {
			maxX = boxX + topologyBoxWidth
		}
		if boxY+topologyBoxHeight > maxY {
			maxY = boxY + topologyBoxHeight
		}
		view.Nodes = append(view.Nodes, topologyNodeItem{
			ID:          node.ID,
			Label:       node.Label,
			KindLabel:   s.kindLabel(node.Kind),
			ZoneLabel:   s.zoneLabel(node.Zone),
			StateLabel:  s.healthLabel(node.Health.State),
			StateClass:  healthStateClass(node.Health.State),
			StateIcon:   s.healthIcon(node.Health.State),
			Roles:       sortedCopy(node.Roles),
			Groups:      sortedCopy(node.Groups),
			Contracts:   sortedCopy(node.Contracts),
			Connections: connectionLabels(node.ID, graph, labels),
			CardURL:     nodeCardURL(node.ID, tenant, actor),
			BoxX:        boxX,
			BoxY:        boxY,
			LabelX:      boxX + 10,
			LabelY:      boxY + 26,
			StateY:      boxY + 48,
		})
	}

	view.Edges = make([]topologyEdgeView, 0, len(graph.Edges))
	for _, edge := range graph.Edges {
		from, fromOK := centers[edge.From]
		to, toOK := centers[edge.To]
		if !fromOK || !toOK {
			continue
		}
		view.Edges = append(view.Edges, topologyEdgeView{
			ID:             edge.ID,
			From:           edge.From,
			To:             edge.To,
			FromLabel:      labels[edge.From],
			ToLabel:        labels[edge.To],
			Integration:    edge.Integration,
			Transport:      edge.Transport,
			Mechanism:      edge.Mechanism,
			Contract:       edge.Contract,
			IntensityLabel: s.intensityLabel(edge.Intensity.State),
			X1:             from[0],
			Y1:             from[1],
			X2:             to[0],
			Y2:             to[1],
		})
	}

	if view.HasNodes {
		view.ViewBox = fmt.Sprintf("0 0 %.0f %.0f", maxX+topologyMargin, maxY+topologyMargin)
	}
	return view
}

// buildNodeCard собирает карточку узла. Второе значение ложно, если узел не
// объявлен в графе: экран честно отвечает «не найден», а не рисует пустую карточку.
func (s *Server) buildNodeCard(graph topology.Graph, id, tenant, actor string) (topologyNodeCard, bool) {
	var node topology.Node
	found := false
	for _, candidate := range graph.Nodes {
		if candidate.ID == id {
			node = candidate
			found = true
			break
		}
	}
	if !found {
		return topologyNodeCard{}, false
	}

	card := topologyNodeCard{
		ID:         node.ID,
		Label:      node.Label,
		KindLabel:  s.kindLabel(node.Kind),
		ZoneLabel:  s.zoneLabel(node.Zone),
		StateLabel: s.healthLabel(node.Health.State),
		StateClass: healthStateClass(node.Health.State),
		StateIcon:  s.healthIcon(node.Health.State),
		Roles:      sortedCopy(node.Roles),
		Groups:     sortedCopy(node.Groups),
		Contracts:  sortedCopy(node.Contracts),
		BackURL:    topologyURL(tenant, actor),
	}
	for _, address := range node.Addresses {
		card.Addresses = append(card.Addresses, address.Address)
	}
	card.Connections = connectionViews(node.ID, graph)
	return card, true
}

// connectionLabels возвращает подписи связей узла для таблицы и SVG.
func connectionLabels(id string, graph topology.Graph, labels map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, edge := range graph.Edges {
		switch {
		case edge.From == id:
			label := "→ " + labels[edge.To]
			if !seen[label] {
				seen[label] = true
				out = append(out, label)
			}
		case edge.To == id:
			label := "← " + labels[edge.From]
			if !seen[label] {
				seen[label] = true
				out = append(out, label)
			}
		}
	}
	sort.Strings(out)
	return out
}

// connectionViews возвращает связи узла с транспортом и контрактом для карточки.
func connectionViews(id string, graph topology.Graph) []topologyConnectionView {
	labels := make(map[string]string, len(graph.Nodes))
	for _, node := range graph.Nodes {
		labels[node.ID] = node.Label
	}
	var out []topologyConnectionView
	for _, edge := range graph.Edges {
		switch {
		case edge.From == id:
			out = append(out, topologyConnectionView{
				Direction: "→",
				Edge:      edge.ID,
				Peer:      labels[edge.To],
				Transport: edge.Transport,
				Mechanism: edge.Mechanism,
				Contract:  edge.Contract,
			})
		case edge.To == id:
			out = append(out, topologyConnectionView{
				Direction: "←",
				Edge:      edge.ID,
				Peer:      labels[edge.From],
				Transport: edge.Transport,
				Mechanism: edge.Mechanism,
				Contract:  edge.Contract,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Direction != out[j].Direction {
			return out[i].Direction < out[j].Direction
		}
		return out[i].Edge < out[j].Edge
	})
	return out
}

// kindLabel переводит вид узла в подпись каталога.
func (s *Server) kindLabel(kind topology.NodeKind) string {
	switch kind {
	case topology.NodeService:
		return s.messages.Text(msgTopologyKindService)
	case topology.NodeInfrastructure:
		return s.messages.Text(msgTopologyKindInfra)
	default:
		return s.messages.Text(msgTopologyKindUnknown)
	}
}

// zoneLabel переводит зону данных в подпись каталога; пустая зона — «нет».
func (s *Server) zoneLabel(zone string) string {
	switch zone {
	case "green":
		return s.messages.Text(msgTopologyZoneGreen)
	case "red":
		return s.messages.Text(msgTopologyZoneRed)
	case "mixed":
		return s.messages.Text(msgTopologyZoneMixed)
	case "infra":
		return s.messages.Text(msgTopologyZoneInfra)
	default:
		return s.messages.Text(msgTopologyNodeEmpty)
	}
}

// healthLabel — текстовая подпись состояния: смысл не передаётся только цветом.
func (s *Server) healthLabel(state topology.HealthState) string {
	switch state {
	case topology.HealthUp:
		return s.messages.Text(msgTopologyStateUp)
	case topology.HealthDegraded:
		return s.messages.Text(msgTopologyStateDegraded)
	case topology.HealthDown:
		return s.messages.Text(msgTopologyStateDown)
	default:
		return s.messages.Text(msgTopologyStateUnknown)
	}
}

// healthIcon — значок состояния рядом с подписью.
func (s *Server) healthIcon(state topology.HealthState) string {
	switch state {
	case topology.HealthUp:
		return s.messages.Text(msgTopologyStateIconUp)
	case topology.HealthDegraded:
		return s.messages.Text(msgTopologyStateIconDeg)
	case topology.HealthDown:
		return s.messages.Text(msgTopologyStateIconDown)
	default:
		return s.messages.Text(msgTopologyStateIconUnk)
	}
}

// healthStateClass сопоставляет состояние классу общего бейджа.
func healthStateClass(state topology.HealthState) string {
	switch state {
	case topology.HealthUp:
		return "done"
	case topology.HealthDegraded:
		return "running"
	case topology.HealthDown:
		return "failed"
	default:
		return "queued"
	}
}

// intensityLabel переводит категорию интенсивности в подпись.
func (s *Server) intensityLabel(state topology.IntensityState) string {
	switch state {
	case topology.IntensityNormal:
		return s.messages.Text(msgTopologyIntensityNormal)
	case topology.IntensityHot:
		return s.messages.Text(msgTopologyIntensityHot)
	default:
		return s.messages.Text(msgTopologyIntensityUnknown)
	}
}

// sortedCopy возвращает копию списка в детерминированном порядке.
func sortedCopy(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

// applyPositions накладывает сохранённые позиции раскладки на граф, не меняя
// узлы без записи. Детерминированная раскладка остаётся значением по умолчанию.
func applyPositions(graph topology.Graph, positions map[string]NodePosition) topology.Graph {
	if len(positions) == 0 {
		return graph
	}
	nodes := make([]topology.Node, len(graph.Nodes))
	copy(nodes, graph.Nodes)
	for index := range nodes {
		if position, ok := positions[nodes[index].ID]; ok {
			nodes[index].X = position.X
			nodes[index].Y = position.Y
		}
	}
	return topology.Graph{Nodes: nodes, Edges: graph.Edges, GeneratedAt: graph.GeneratedAt, Warnings: graph.Warnings}
}
