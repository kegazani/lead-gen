package deployment

import (
	"fmt"
	"strings"
)

// DeployConfig — конфигурация развёртывания из deploy/services.yaml в объёме,
// нужном точке входа. Формат файла:
//
//	processes:
//	  - name: core
//	    mechanism: in-process
//	    services:
//	      - id: outreach-orchestration
//	        roles: [api, worker, chains, schedule]
//
// Группа процессов (process) — способ запуска: один образ поднимает один или
// несколько сервисов, каждый со своим подмножеством ролей. Каталог
// (architecture/domain-catalog.yaml) при этом всегда объявляет один сервис на
// контекст; группировка модель не меняет (design.md, Решение 5).
type DeployConfig struct {
	Processes []ProcessConfig
}

// ProcessConfig — группа процессов.
type ProcessConfig struct {
	Name      string
	Mechanism string
	Services  []ServiceConfig
}

// ServiceConfig — сервис в группе и назначенные ему роли.
type ServiceConfig struct {
	ID    string
	Roles []string
}

// ParseDeployConfig разбирает deploy/services.yaml минимальным парсером на
// стандартной библиотеке. Полноценный YAML не нужен: формат файла фиксирован и
// узок, а лишняя зависимость в точке входа недопустима.
//
// Парсер осознанно строг: нераспознанная строка — ошибка с номером, а не
// молчаливый пропуск. Расхождение конфигурации с ожидаемой формой должно быть
// видно на старте, а не проявляться отсутствием роли.
func ParseDeployConfig(data []byte) (DeployConfig, error) {
	var cfg DeployConfig
	processIdx, serviceIdx := -1, -1
	inServices := false

	lines := strings.Split(string(data), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}

		switch {
		case line == "processes:":
			inServices = false
			processIdx, serviceIdx = -1, -1

		case strings.HasPrefix(line, "- name:"):
			name, err := scalar(line, "- name:")
			if err != nil {
				return cfg, lineError(i, err)
			}
			if name == "" {
				return cfg, lineError(i, fmt.Errorf("пустое имя группы процессов"))
			}
			cfg.Processes = append(cfg.Processes, ProcessConfig{Name: name})
			processIdx = len(cfg.Processes) - 1
			serviceIdx = -1
			inServices = false

		case strings.HasPrefix(line, "mechanism:"):
			if processIdx < 0 {
				return cfg, lineError(i, fmt.Errorf("mechanism вне группы процессов"))
			}
			value, err := scalar(line, "mechanism:")
			if err != nil {
				return cfg, lineError(i, err)
			}
			cfg.Processes[processIdx].Mechanism = value

		case line == "services:":
			if processIdx < 0 {
				return cfg, lineError(i, fmt.Errorf("services вне группы процессов"))
			}
			inServices = true
			serviceIdx = -1

		case inServices && strings.HasPrefix(line, "- id:"):
			id, err := scalar(line, "- id:")
			if err != nil {
				return cfg, lineError(i, err)
			}
			if id == "" {
				return cfg, lineError(i, fmt.Errorf("пустой идентификатор сервиса"))
			}
			cfg.Processes[processIdx].Services = append(
				cfg.Processes[processIdx].Services, ServiceConfig{ID: id},
			)
			serviceIdx = len(cfg.Processes[processIdx].Services) - 1

		case inServices && strings.HasPrefix(line, "roles:"):
			if serviceIdx < 0 {
				return cfg, lineError(i, fmt.Errorf("roles вне сервиса"))
			}
			roles, err := parseRoles(line, "roles:")
			if err != nil {
				return cfg, lineError(i, err)
			}
			cfg.Processes[processIdx].Services[serviceIdx].Roles = roles

		default:
			return cfg, lineError(i, fmt.Errorf("нераспознанная строка %q", line))
		}
	}

	if len(cfg.Processes) == 0 {
		return cfg, fmt.Errorf("deploy/services.yaml: не объявлено ни одной группы процессов")
	}
	for _, process := range cfg.Processes {
		if len(process.Services) == 0 {
			return cfg, fmt.Errorf("deploy/services.yaml: группа %q без сервисов", process.Name)
		}
		for _, service := range process.Services {
			if len(service.Roles) == 0 {
				return cfg, fmt.Errorf(
					"deploy/services.yaml: сервис %q без ролей", service.ID)
			}
		}
	}
	return cfg, nil
}

// stripComment отбрасывает комментарий от символа # до конца строки.
func stripComment(line string) string {
	if idx := strings.IndexByte(line, '#'); idx >= 0 {
		return line[:idx]
	}
	return line
}

// scalar извлекает значение после префикса, снимая необязательные кавычки.
func scalar(line, prefix string) (string, error) {
	if !strings.HasPrefix(line, prefix) {
		return "", fmt.Errorf("ожидался префикс %q в строке %q", prefix, line)
	}
	value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
	}
	return value, nil
}

// parseRoles разбирает поток вида "roles: [api, worker, chains]".
func parseRoles(line, prefix string) ([]string, error) {
	value, err := scalar(line, prefix)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil, fmt.Errorf("роли должны быть списком в скобках: %q", value)
	}
	inner := strings.TrimSpace(value[1 : len(value)-1])
	if inner == "" {
		return nil, nil
	}
	parts := strings.Split(inner, ",")
	roles := make([]string, 0, len(parts))
	for _, part := range parts {
		role := strings.TrimSpace(part)
		if role == "" {
			continue
		}
		roles = append(roles, role)
	}
	return roles, nil
}

// lineError добавляет номер строки (1-based) к ошибке разбора.
func lineError(index int, err error) error {
	return fmt.Errorf("deploy/services.yaml:%d: %w", index+1, err)
}
