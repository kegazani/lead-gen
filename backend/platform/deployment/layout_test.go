package deployment

import (
	"reflect"
	"testing"
)

// TestLayoutDeterministic: одинаковый граф даёт одинаковую раскладку.
func TestLayoutDeterministic(t *testing.T) {
	graph, err := Load(realCatalogPath, realServicesPath, realInfrastructurePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	first := graph.Layout()
	second := graph.Layout()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("раскладка недетерминирована:\nfirst:  %v\nsecond: %v", first, second)
	}

	reloaded, err := Load(realCatalogPath, realServicesPath, realInfrastructurePath)
	if err != nil {
		t.Fatalf("повторный Load: %v", err)
	}
	if !reflect.DeepEqual(first, reloaded.Layout()) {
		t.Fatal("раскладка зависит от порядка сборки, а не только от данных")
	}
}

// TestLayoutCoversAllNodes проверяет, что раскладка содержит каждый узел ровно
// один раз.
func TestLayoutCoversAllNodes(t *testing.T) {
	graph, err := Load(realCatalogPath, realServicesPath, realInfrastructurePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	placements := graph.Layout()
	if len(placements) != len(graph.Nodes) {
		t.Fatalf("точек: %d, узлов: %d", len(placements), len(graph.Nodes))
	}
	seen := map[string]bool{}
	for _, placement := range placements {
		if seen[placement.ID] {
			t.Errorf("узел %q размещён дважды", placement.ID)
		}
		seen[placement.ID] = true
	}
}

// TestLayoutGroupsInColumnsAndStableOrder: сервисы одной группы стоят в одной
// колонке, порядок вывода — по группе и ID, инфраструктура — после сервисов.
func TestLayoutGroupsInColumnsAndStableOrder(t *testing.T) {
	graph := Graph{Nodes: []Node{
		{ID: "z", Kind: NodeService, Groups: []string{"revenue"}},
		{ID: "a", Kind: NodeService, Groups: []string{"core"}},
		{ID: "b", Kind: NodeService, Groups: []string{"core"}},
		{ID: "m", Kind: NodeInfra},
	}}
	placements := graph.Layout()

	ids := make([]string, 0, len(placements))
	positions := map[string]Placement{}
	for _, placement := range placements {
		ids = append(ids, placement.ID)
		positions[placement.ID] = placement
	}
	// core (a, b) идёт раньше revenue (z) лексикографически; инфраструктура — в конце.
	want := []string{"a", "b", "z", "m"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("порядок раскладки = %v, ожидался %v", ids, want)
	}
	if positions["a"].X != positions["b"].X {
		t.Errorf("узлы одной группы core должны стоять в одной колонке: %v и %v", positions["a"].X, positions["b"].X)
	}
	if positions["a"].X == positions["z"].X {
		t.Errorf("группы core и revenue должны быть в разных колонках")
	}
	if positions["m"].Y <= positions["a"].Y {
		t.Errorf("инфраструктура должна быть ниже полосы сервисов")
	}
}

// TestLayoutMultiGroupNodeKeepsOnePlacement: сервис, объявленный в нескольких
// процессах, остаётся одной точкой и размещается в колонке своей основной
// (первой лексикографически) группы, а не в каждой из групп.
func TestLayoutMultiGroupNodeKeepsOnePlacement(t *testing.T) {
	graph := Graph{Nodes: []Node{
		{ID: "multi", Kind: NodeService, Groups: []string{"delivery", "platform"}},
		{ID: "sender", Kind: NodeService, Groups: []string{"delivery"}},
		{ID: "api", Kind: NodeService, Groups: []string{"platform"}},
	}}
	placements := graph.Layout()
	if len(placements) != 3 {
		t.Fatalf("точек: %d, узлов: %d — узел должен быть размещён ровно один раз",
			len(placements), len(graph.Nodes))
	}

	ids := make([]string, 0, len(placements))
	positions := map[string]Placement{}
	for _, placement := range placements {
		ids = append(ids, placement.ID)
		positions[placement.ID] = placement
	}
	// Колонки лексикографически: delivery (multi, sender), затем platform (api).
	want := []string{"multi", "sender", "api"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("порядок раскладки = %v, ожидался %v", ids, want)
	}
	if positions["multi"].X != positions["sender"].X {
		t.Errorf("multi с основной группой delivery должен стоять в колонке delivery: %v и %v",
			positions["multi"].X, positions["sender"].X)
	}
	if positions["multi"].X == positions["api"].X {
		t.Errorf("multi и api должны быть в разных колонках (delivery и platform)")
	}
}
