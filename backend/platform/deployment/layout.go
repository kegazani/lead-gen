package deployment

import "sort"

// Placement — координаты одного узла в детерминированной раскладке по умолчанию.
//
// Формат координат: абстрактные единицы сетки, а не пиксели; начало — левый
// верхний угол, X растёт вправо, Y — вниз. Выбор абстрактных единиц делает
// результат независимым от размера экрана и масштаба, поэтому одинаковая модель
// даёт одинаковую картинку.
type Placement struct {
	ID string
	X  float64
	Y  float64
}

// Шаги раскладки в единицах сетки.
const (
	// layoutColumnStep — расстояние между соседними колонками (группами deploy
	// и инфраструктурными узлами).
	layoutColumnStep = 240.0
	// layoutRowStep — расстояние между узлами внутри колонки.
	layoutRowStep = 96.0
	// layoutInfraGap — вертикальный отступ полосы инфраструктуры от полосы
	// сервисов.
	layoutInfraGap = 160.0
)

// Layout вычисляет детерминированную раскладку: узлы-сервисы раскладываются по
// колонкам-группам deploy (группы в лексикографическом порядке, внутри группы —
// по ID), инфраструктурные узлы — отдельной полосой ниже, по ID. Случайности
// нет: одинаковый граф даёт одинаковые координаты.
//
// Узел сервиса, объявленного в нескольких процессах, остаётся одной вершиной и
// размещается в колонке своей основной группы (Node.PrimaryGroup); остальные
// группы видны в модели узла и карточке, но не дублируют вершину на графе.
//
// Возвращаемый порядок стабилен: сервисы (по группе, затем по ID), затем
// инфраструктура по ID.
func (g Graph) Layout() []Placement {
	services := make([]Node, 0, len(g.Nodes))
	infrastructure := make([]Node, 0)
	for _, node := range g.Nodes {
		if node.Kind == NodeInfra {
			infrastructure = append(infrastructure, node)
			continue
		}
		services = append(services, node)
	}

	byGroup := map[string][]Node{}
	groupNames := make([]string, 0)
	for _, node := range services {
		group := node.PrimaryGroup()
		if _, ok := byGroup[group]; !ok {
			groupNames = append(groupNames, group)
		}
		byGroup[group] = append(byGroup[group], node)
	}
	sort.Strings(groupNames)

	placements := make([]Placement, 0, len(g.Nodes))
	maxRows := 0
	for column, group := range groupNames {
		items := byGroup[group]
		sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		for row, node := range items {
			placements = append(placements, Placement{
				ID: node.ID,
				X:  float64(column) * layoutColumnStep,
				Y:  float64(row) * layoutRowStep,
			})
		}
		if len(items) > maxRows {
			maxRows = len(items)
		}
	}

	sort.SliceStable(infrastructure, func(i, j int) bool { return infrastructure[i].ID < infrastructure[j].ID })
	infrastructureY := 0.0
	if maxRows > 0 {
		infrastructureY = float64(maxRows)*layoutRowStep + layoutInfraGap
	}
	for column, node := range infrastructure {
		placements = append(placements, Placement{
			ID: node.ID,
			X:  float64(column) * layoutColumnStep,
			Y:  infrastructureY,
		})
	}
	return placements
}
