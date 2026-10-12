// Package graph — технический примитив направленного графа и детерминированной
// раскладки. Пакет не содержит доменных терминов и не импортирует `contexts/**`:
// он раскладывает произвольные узлы и рёбра, а смысл узлов задаёт вызывающий.
//
// Раскладка детерминирована: узлы группируются по колонке (`Column`), имена
// колонок сортируются лексикографически, внутри колонки — по явному ряду (`Row`),
// затем по идентификатору. Случайности и зависимости от порядка объявления нет,
// поэтому одинаковый граф даёт одинаковые координаты.
//
// Пакет переиспользуется консолью сервисной топологии (пакет
// backend/platform/topology делегирует сюда групповую раскладку) и операционным
// экраном (граф процессов Temporal).
package graph

import (
	"sort"
	"time"
)

// Шаги раскладки в единицах сетки (не пиксели: координаты независимы от размера
// экрана и масштаба, поэтому одинаковая модель даёт одинаковую картинку).
const (
	// ColumnStep — расстояние между колонками по горизонтали.
	ColumnStep = 240.0
	// RowStep — расстояние между рядами внутри колонки по вертикали.
	RowStep = 96.0
	// BandGap — вертикальный отступ до отдельной полосы (например, инфраструктуры).
	BandGap = 160.0
)

// Node — вершина графа. Column задаёт колонку раскладки (пусто — отдельная
// колонка без имени), Row — предпочтительный ряд внутри колонки (при равенстве
// порядок задаёт ID). X и Y заполняет Layout.
type Node struct {
	ID     string
	Label  string
	Column string
	Row    int
	Status string
	Detail string
	X      float64
	Y      float64
	Meta   map[string]string
}

// Edge — направленное ребро графа. Kind и Label — данные представления.
type Edge struct {
	ID    string
	From  string
	To    string
	Kind  string
	Label string
}

// Graph — модель графа. Порядок узлов и рёбер задаёт построитель; раскладка
// сохраняет порядок узлов и меняет только координаты.
type Graph struct {
	Nodes       []Node
	Edges       []Edge
	GeneratedAt time.Time
	Warnings    []string
}

// Placement — результат групповой раскладки: отсортированные имена колонок,
// координаты узлов и число рядов в самой высокой колонке.
type Placement struct {
	Columns []string
	Pos     map[string][2]float64
	Rows    int
}

// Place раскладывает узлы по колонкам. Имена колонок сортируются, внутри колонки
// узлы упорядочиваются по (Row, ID). Возвращает координаты и число рядов.
func Place(nodes []Node) Placement {
	byColumn := make(map[string][]Node)
	columns := make([]string, 0)
	for _, node := range nodes {
		if _, ok := byColumn[node.Column]; !ok {
			columns = append(columns, node.Column)
		}
		byColumn[node.Column] = append(byColumn[node.Column], node)
	}
	sort.Strings(columns)

	positions := make(map[string][2]float64, len(nodes))
	rows := 0
	for column, name := range columns {
		items := byColumn[name]
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Row != items[j].Row {
				return items[i].Row < items[j].Row
			}
			return items[i].ID < items[j].ID
		})
		for row, node := range items {
			positions[node.ID] = [2]float64{float64(column) * ColumnStep, float64(row) * RowStep}
		}
		if len(items) > rows {
			rows = len(items)
		}
	}
	return Placement{Columns: columns, Pos: positions, Rows: rows}
}

// Layout возвращает копию графа с координатами X/Y. Узлы без записи в раскладке
// остаются с нулевыми координатами: подстановки догадки нет.
func (g Graph) Layout() Graph {
	placement := Place(g.Nodes)
	nodes := make([]Node, len(g.Nodes))
	copy(nodes, g.Nodes)
	for index := range nodes {
		if point, ok := placement.Pos[nodes[index].ID]; ok {
			nodes[index].X = point[0]
			nodes[index].Y = point[1]
		}
	}
	return Graph{Nodes: nodes, Edges: g.Edges, GeneratedAt: g.GeneratedAt, Warnings: g.Warnings}
}
