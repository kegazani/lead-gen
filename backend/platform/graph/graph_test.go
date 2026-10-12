package graph

import (
	"reflect"
	"testing"
	"time"
)

// TestPlaceIsDeterministic проверяет, что раскладка не зависит от порядка
// объявления узлов и повторный вызов даёт те же координаты.
func TestPlaceIsDeterministic(t *testing.T) {
	nodes := []Node{
		{ID: "b", Column: "beta"},
		{ID: "a", Column: "alpha"},
		{ID: "c", Column: "alpha"},
	}
	first := Place(nodes)
	second := Place([]Node{{ID: "c", Column: "alpha"}, {ID: "b", Column: "beta"}, {ID: "a", Column: "alpha"}})
	if !reflect.DeepEqual(first.Pos, second.Pos) {
		t.Fatalf("раскладка не детерминирована:\n%v\n%v", first.Pos, second.Pos)
	}

	// Колонки отсортированы, внутри — по (Row, ID).
	if len(first.Columns) != 2 || first.Columns[0] != "alpha" || first.Columns[1] != "beta" {
		t.Fatalf("колонки = %v, ожидалось [alpha beta]", first.Columns)
	}
	if first.Rows != 2 {
		t.Fatalf("рядов = %d, ожидалось 2", first.Rows)
	}
	if first.Pos["a"] != [2]float64{0, 0} || first.Pos["c"] != [2]float64{0, RowStep} {
		t.Fatalf("координаты колонки alpha = %v", first.Pos)
	}
	if first.Pos["b"] != [2]float64{ColumnStep, 0} {
		t.Fatalf("координата beta = %v, ожидалась x=%v", first.Pos["b"], ColumnStep)
	}
}

// TestPlaceHonorsRow проверяет явный ряд как первичный ключ сортировки.
func TestPlaceHonorsRow(t *testing.T) {
	placement := Place([]Node{
		{ID: "z", Column: "c", Row: 0},
		{ID: "a", Column: "c", Row: 1},
	})
	if placement.Pos["z"] != [2]float64{0, 0} {
		t.Fatalf("узел с меньшим Row не на первом ряду: %v", placement.Pos["z"])
	}
	if placement.Pos["a"] != [2]float64{0, RowStep} {
		t.Fatalf("узел со старшим Row не на втором ряду: %v", placement.Pos["a"])
	}
}

// TestLayoutFillsCoordinates проверяет, что Layout накладывает координаты, не
// теряя узлы, рёбра и метаданные времени.
func TestLayoutFillsCoordinates(t *testing.T) {
	now := time.Now().UTC()
	laid := Graph{
		Nodes:       []Node{{ID: "a", Column: "x"}, {ID: "b", Column: "x"}},
		Edges:       []Edge{{ID: "e", From: "a", To: "b"}},
		GeneratedAt: now,
	}.Layout()
	if len(laid.Nodes) != 2 || len(laid.Edges) != 1 {
		t.Fatalf("Layout потерял узлы или рёбра: %+v", laid)
	}
	if laid.Nodes[0].Y != 0 || laid.Nodes[1].Y != RowStep {
		t.Fatalf("координаты не заполнены: %+v", laid.Nodes)
	}
	if !laid.GeneratedAt.Equal(now) {
		t.Fatalf("Layout потерял время построения")
	}
}
