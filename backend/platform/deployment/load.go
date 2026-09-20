package deployment

import (
	"errors"
	"fmt"
	"os"
	"sort"
)

// Load строит граф из трёх канонических источников:
//
//   - catalogPath — каталог `architecture/domain-catalog.yaml` (JSON): узлы-сервисы
//     из contexts (поле service, при его отсутствии — ошибка) и рёбра из
//     relationships (upstream, downstream, contract, transport, mechanism);
//   - servicesPath — `deploy/services.yaml`: группы развёртывания и роли; узел
//     сервиса получает все процессы, в которых он объявлен (сервис MAY входить
//     в несколько);
//   - infrastructurePath — `deploy/infrastructure.yaml`: инфраструктурные узлы и
//     их внутренние адреса.
//
// Пустой путь источника означает, что источник пропускается (нужно для узких
// проверок); это не ошибка.
//
// Порядок детерминирован: сначала узлы-сервисы, затем инфраструктурные, внутри
// каждой части — по ID; рёбра — по ID. Состояние всех узлов и рёбер —
// HealthUnknown, интенсивность рёбер — nil: статический граф не выдумывает
// измерения.
func Load(catalogPath, servicesPath, infrastructurePath string) (Graph, error) {
	nodes, edges, _, err := readCatalog(catalogPath)
	if err != nil {
		return Graph{}, err
	}

	serviceIDs := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		serviceIDs[node.ID] = true
	}

	groups, err := readServices(servicesPath)
	if err != nil {
		return Graph{}, err
	}
	if groups != nil {
		for id := range groups {
			if !serviceIDs[id] {
				return Graph{}, fmt.Errorf("%s: сервис %q отсутствует в каталоге", servicesSource, id)
			}
		}
		for index := range nodes {
			if serviceGroups, ok := groups[nodes[index].ID]; ok {
				nodes[index].Groups = append([]string(nil), serviceGroups...)
			}
		}
	}

	infrastructure, err := readInfrastructure(infrastructurePath)
	if err != nil {
		return Graph{}, err
	}

	for _, node := range infrastructure {
		if serviceIDs[node.ID] {
			return Graph{}, fmt.Errorf("%s: узел %q совпадает с сервисом каталога",
				infrastructureSource, node.ID)
		}
		serviceIDs[node.ID] = true
	}

	sort.SliceStable(infrastructure, func(i, j int) bool { return infrastructure[i].ID < infrastructure[j].ID })
	nodes = append(nodes, infrastructure...)
	return Graph{Nodes: nodes, Edges: edges}, nil
}

// readSource читает файл источника и оборачивает ошибку именем этого источника.
func readSource(path, name string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s: файл отсутствует: %s", name, path)
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return data, nil
}
