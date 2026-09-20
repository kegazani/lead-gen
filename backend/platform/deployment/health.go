package deployment

import "context"

// HealthSource накладывает актуальное состояние на статический граф. Источник
// может быть удалённым (опрос, метрики) и может возвращать ошибку для отдельного
// узла — тогда состояние этого узла остаётся `unknown`.
type HealthSource interface {
	Health(ctx context.Context, nodeID string) (Health, error)
}

// WithHealth возвращает копию графа с состояниями узлов, полученными из src.
// Исходный граф не изменяется. nil-источник возвращает копию без изменений.
//
// Ошибка источника и неизвестное значение состояния трактуются одинаково —
// `HealthUnknown`: консоль обязана показывать «нет данных», а не подставлять
// `up`. Рёбра не обновляются: источник адресуется узлами, а интенсивность и
// состояние рёбер берутся из слоя наблюдения отдельно.
func (g Graph) WithHealth(ctx context.Context, src HealthSource) Graph {
	nodes := make([]Node, len(g.Nodes))
	copy(nodes, g.Nodes)
	result := Graph{Nodes: nodes, Edges: g.Edges}
	if src == nil {
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for index := range result.Nodes {
		state, err := src.Health(ctx, result.Nodes[index].ID)
		if err != nil {
			result.Nodes[index].Health = HealthUnknown
			continue
		}
		result.Nodes[index].Health = normalizeHealth(state)
	}
	return result
}

// normalizeHealth оставляет только фиксированный набор состояний; любое другое
// значение — `unknown`.
func normalizeHealth(state Health) Health {
	switch state {
	case HealthUp, HealthDegraded, HealthDown, HealthUnknown:
		return state
	default:
		return HealthUnknown
	}
}
