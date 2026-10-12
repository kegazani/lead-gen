package topology

import (
	"sort"

	"lead-gen/backend/platform/graph"
)

// Шаги раскладки берутся из общего примитива backend/platform/graph: одинаковая
// модель даёт одинаковую картинку, а групповая часть алгоритма не дублируется.
const (
	layoutColumnStep = graph.ColumnStep
	layoutRowStep    = graph.RowStep
	layoutBandGap    = graph.BandGap
)

// Layout возвращает копию графа с детерминированными координатами X/Y. Узлы-сервисы
// раскладываются по колонкам-группам deploy (общий примитив graph.Place: колонки в
// лексикографическом порядке, внутри — по id), инфраструктурные узлы — отдельной
// полосой ниже (по id), узлы unknown — ещё ниже (по id). Случайности нет:
// одинаковый граф даёт одинаковые координаты.
func (g Graph) Layout() Graph {
	var services, infrastructure, unknown []Node
	for _, node := range g.Nodes {
		switch node.Kind {
		case NodeInfrastructure:
			infrastructure = append(infrastructure, node)
		case NodeUnknown:
			unknown = append(unknown, node)
		default:
			services = append(services, node)
		}
	}

	serviceNodes := make([]graph.Node, 0, len(services))
	for _, node := range services {
		serviceNodes = append(serviceNodes, graph.Node{ID: node.ID, Column: node.PrimaryGroup()})
	}
	placement := graph.Place(serviceNodes)
	coordinates := placement.Pos
	maxRows := placement.Rows

	bandY := float64(maxRows)*layoutRowStep + layoutBandGap
	sort.SliceStable(infrastructure, func(i, j int) bool { return infrastructure[i].ID < infrastructure[j].ID })
	for column, node := range infrastructure {
		coordinates[node.ID] = [2]float64{float64(column) * layoutColumnStep, bandY}
	}
	unknownY := bandY
	if len(infrastructure) > 0 {
		unknownY = bandY + layoutRowStep
	}
	sort.SliceStable(unknown, func(i, j int) bool { return unknown[i].ID < unknown[j].ID })
	for column, node := range unknown {
		coordinates[node.ID] = [2]float64{float64(column) * layoutColumnStep, unknownY}
	}

	nodes := make([]Node, len(g.Nodes))
	copy(nodes, g.Nodes)
	for index := range nodes {
		if point, ok := coordinates[nodes[index].ID]; ok {
			nodes[index].X = point[0]
			nodes[index].Y = point[1]
		}
	}
	return Graph{Nodes: nodes, Edges: g.Edges, GeneratedAt: g.GeneratedAt, Warnings: g.Warnings}
}
