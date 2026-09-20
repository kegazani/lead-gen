package deployment

import (
	"fmt"
	"sort"
)

// servicesSource — человекочитаемое имя источника для сообщений об ошибках.
const servicesSource = "deploy/services.yaml"

// readServices читает `deploy/services.yaml` и возвращает проекцию
// «сервис → имена групп процессов, в которых он объявлен». Сервис MAY
// присутствовать в нескольких процессах: единица размещения — роль, а не
// сервис, поэтому membership множественный. Значение — множество имён
// (без повторов), отсортированное для детерминизма. Пустой путь означает, что
// источник не участвует в сборке (например, в узком тесте каталога).
func readServices(path string) (map[string][]string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := readSource(path, servicesSource)
	if err != nil {
		return nil, err
	}
	root, err := parseYAML(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", servicesSource, err)
	}
	document, err := asMapping(root)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", servicesSource, err)
	}
	if err := rejectUnknownKeys(document, "processes"); err != nil {
		return nil, fmt.Errorf("%s: %w", servicesSource, err)
	}
	rawProcesses, ok := document["processes"]
	if !ok {
		return nil, fmt.Errorf("%s: не найдена секция processes", servicesSource)
	}
	processes, err := asSequence(rawProcesses)
	if err != nil {
		return nil, fmt.Errorf("%s: процессы: %w", servicesSource, err)
	}
	if len(processes) == 0 {
		return nil, fmt.Errorf("%s: не объявлено ни одной группы процессов", servicesSource)
	}

	groups := map[string][]string{}
	for processIndex, rawProcess := range processes {
		process, err := asMapping(rawProcess)
		if err != nil {
			return nil, fmt.Errorf("%s: группа %d: %w", servicesSource, processIndex, err)
		}
		if err := rejectUnknownKeys(process, "name", "mechanism", "services"); err != nil {
			return nil, fmt.Errorf("%s: группа %d: %w", servicesSource, processIndex, err)
		}
		name, err := asString(process["name"])
		if err != nil {
			return nil, fmt.Errorf("%s: группа %d: name: %w", servicesSource, processIndex, err)
		}
		if name == "" {
			return nil, fmt.Errorf("%s: группа %d без name", servicesSource, processIndex)
		}
		rawServicesValue, ok := process["services"]
		if !ok {
			return nil, fmt.Errorf("%s: группа %q без сервисов", servicesSource, name)
		}
		services, err := asSequence(rawServicesValue)
		if err != nil {
			return nil, fmt.Errorf("%s: группа %q: %w", servicesSource, name, err)
		}
		if len(services) == 0 {
			return nil, fmt.Errorf("%s: группа %q без сервисов", servicesSource, name)
		}
		for serviceIndex, rawService := range services {
			service, err := asMapping(rawService)
			if err != nil {
				return nil, fmt.Errorf("%s: группа %q, сервис %d: %w", servicesSource, name, serviceIndex, err)
			}
			if err := rejectUnknownKeys(service, "id", "roles", "mechanism"); err != nil {
				return nil, fmt.Errorf("%s: группа %q, сервис %d: %w", servicesSource, name, serviceIndex, err)
			}
			id, err := asString(service["id"])
			if err != nil {
				return nil, fmt.Errorf("%s: группа %q: id: %w", servicesSource, name, err)
			}
			if id == "" {
				return nil, fmt.Errorf("%s: группа %q: сервис без id", servicesSource, name)
			}
			// Сервис может быть объявлен в нескольких процессах; повтор в одной
			// группе — не второй факт, а дублирование строки.
			if !containsString(groups[id], name) {
				groups[id] = append(groups[id], name)
			}
		}
	}
	for id := range groups {
		sort.Strings(groups[id])
	}
	return groups, nil
}

// containsString сообщает, содержит ли список значение.
func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
