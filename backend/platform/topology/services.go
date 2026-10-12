package topology

import (
	"fmt"
	"sort"
)

// servicesSource — человекочитаемое имя источника для сообщений об ошибках.
const servicesSource = "deploy/services.yaml"

// deployEntry — что `deploy/services.yaml` сообщает о сервисе: группы
// развёртывания, в которых он объявлен, и объединённые роли запуска по этим
// группам. Роли каталога остаются каноническими; роли здесь нужны для
// согласования деплоя с моделью и не подменяют каталог.
type deployEntry struct {
	Groups []string
	Roles  []string
}

// readDeploy читает `deploy/services.yaml` и возвращает проекцию
// «сервис → группы и роли». Пустой путь означает, что источник не участвует в
// сборке. Сервис MAY присутствовать в нескольких процессах: единица размещения —
// роль, а не сервис, поэтому membership множественный. Значения отсортированы
// для детерминизма.
func readDeploy(path string) (map[string]deployEntry, error) {
	if path == "" {
		return nil, nil
	}
	data, err := readSourceFile(path, servicesSource)
	if err != nil {
		return nil, err
	}
	return parseDeploy(data)
}

// parseDeploy разбирает `deploy/services.yaml`.
func parseDeploy(data []byte) (map[string]deployEntry, error) {
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

	entries := map[string]deployEntry{}
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
		rawServices, ok := process["services"]
		if !ok {
			return nil, fmt.Errorf("%s: группа %q без сервисов", servicesSource, name)
		}
		services, err := asSequence(rawServices)
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
			roles, err := asStringSequence(service["roles"])
			if err != nil {
				return nil, fmt.Errorf("%s: группа %q, сервис %q: roles: %w", servicesSource, name, id, err)
			}
			if len(roles) == 0 {
				return nil, fmt.Errorf("%s: группа %q: сервис %q без ролей", servicesSource, name, id)
			}

			entry := entries[id]
			if !containsString(entry.Groups, name) {
				entry.Groups = append(entry.Groups, name)
			}
			for _, role := range roles {
				if !containsString(entry.Roles, role) {
					entry.Roles = append(entry.Roles, role)
				}
			}
			entries[id] = entry
		}
	}
	for id, entry := range entries {
		sort.Strings(entry.Groups)
		sort.Strings(entry.Roles)
		entries[id] = entry
	}
	return entries, nil
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
