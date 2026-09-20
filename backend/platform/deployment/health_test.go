package deployment

import (
	"context"
	"errors"
	"testing"
)

// mapSource — тестовый источник состояний по ID узла.
type mapSource map[string]Health

func (m mapSource) Health(_ context.Context, nodeID string) (Health, error) {
	return m[nodeID], nil
}

// errorSource всегда возвращает ошибку.
type errorSource struct{}

func (errorSource) Health(context.Context, string) (Health, error) {
	return HealthUp, errors.New("источник недоступен")
}

// sampleGraph собирает небольшой граф с двумя узлами и одним ребром.
func sampleGraph() Graph {
	weight := 3.0
	return Graph{
		Nodes: []Node{
			{ID: "a", Kind: NodeService, Health: HealthUnknown},
			{ID: "b", Kind: NodeService, Health: HealthUnknown},
		},
		Edges: []Edge{{ID: "r", Upstream: "a", Downstream: "b", Weight: &weight, Health: HealthUnknown}},
	}
}

// TestWithHealthAppliesStates проверяет наложение состояний.
func TestWithHealthAppliesStates(t *testing.T) {
	graph := sampleGraph()
	result := graph.WithHealth(context.Background(), mapSource{"a": HealthUp, "b": HealthDown})
	if result.Nodes[0].Health != HealthUp {
		t.Errorf("узел a: health = %q, ожидался up", result.Nodes[0].Health)
	}
	if result.Nodes[1].Health != HealthDown {
		t.Errorf("узел b: health = %q, ожидался down", result.Nodes[1].Health)
	}
}

// TestWithHealthDoesNotMutateSource проверяет, что исходный граф не меняется.
func TestWithHealthDoesNotMutateSource(t *testing.T) {
	graph := sampleGraph()
	_ = graph.WithHealth(context.Background(), mapSource{"a": HealthUp, "b": HealthDown})
	for _, node := range graph.Nodes {
		if node.Health != HealthUnknown {
			t.Errorf("исходный узел %q изменён: health = %q", node.ID, node.Health)
		}
	}
	if graph.Edges[0].Weight == nil || *graph.Edges[0].Weight != 3.0 {
		t.Errorf("исходное ребро изменено: weight = %v", graph.Edges[0].Weight)
	}
}

// TestWithHealthErrorIsUnknown: ошибка источника — unknown, а не up.
func TestWithHealthErrorIsUnknown(t *testing.T) {
	graph := sampleGraph()
	result := graph.WithHealth(context.Background(), errorSource{})
	for _, node := range result.Nodes {
		if node.Health != HealthUnknown {
			t.Errorf("узел %q при ошибке источника: health = %q, ожидался unknown", node.ID, node.Health)
		}
	}
}

// TestWithHealthNormalizesUnknownValue: незнакомое значение состояния — unknown.
func TestWithHealthNormalizesUnknownValue(t *testing.T) {
	graph := sampleGraph()
	result := graph.WithHealth(context.Background(), mapSource{"a": Health("broken")})
	if result.Nodes[0].Health != HealthUnknown {
		t.Errorf("незнакомое состояние: %q, ожидался unknown", result.Nodes[0].Health)
	}
	if result.Nodes[1].Health != HealthUnknown {
		t.Errorf("отсутствующее состояние: %q, ожидался unknown", result.Nodes[1].Health)
	}
}

// TestWithHealthNilSource: nil-источник возвращает равную копию.
func TestWithHealthNilSource(t *testing.T) {
	graph := sampleGraph()
	result := graph.WithHealth(context.Background(), nil)
	if len(result.Nodes) != len(graph.Nodes) || len(result.Edges) != len(graph.Edges) {
		t.Fatalf("размер копии не совпал")
	}
	for index := range result.Nodes {
		if result.Nodes[index].Health != graph.Nodes[index].Health {
			t.Errorf("состояние узла %q изменено", result.Nodes[index].ID)
		}
	}
}
