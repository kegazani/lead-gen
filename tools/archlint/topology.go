package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Роли запуска внутри сервиса (Решение 1 дизайна).
const (
	roleAPI      = "api"
	roleIngest   = "ingest"
	roleSender   = "sender"
	roleWorker   = "worker"
	roleChains   = "chains"
	roleSchedule = "schedule"
)

// Допустимый транспорт и механизм связи (Решение 7 дизайна).
const (
	transportSync  = "sync"
	transportAsync = "async"

	mechanismHTTPJSON  = "http-json"
	mechanismAMQP      = "amqp"
	mechanismInProcess = "in-process"
)

// allowedMechanisms — допустимые значения mechanism группы деплоя:
// `in-process` — совмещённый деплой (несколько сервисов в одном процессе
// образа, Решение 5) и не налагает ограничений на роль; `http-json`/`amqp` —
// транспортный режим запуска, совместимый только с ролями своего класса.
var allowedMechanisms = map[string]bool{
	"in-process":      true,
	mechanismHTTPJSON: true,
	mechanismAMQP:     true,
}

// allowedRoles — полное множество допустимых значений поля roles контекста.
var allowedRoles = map[string]bool{
	roleAPI:      true,
	roleIngest:   true,
	roleSender:   true,
	roleWorker:   true,
	roleChains:   true,
	roleSchedule: true,
}

// roleMechanisms сопоставляет роль запуска её режиму (механизму) работы:
// api/ingest — синхронные HTTP-точки входа, остальные роли — асинхронные
// потребители очередей и циклы планировщика. Используется для проверки
// «mechanism группы совпадает с режимом роли» (задача 2.4).
var roleMechanisms = map[string]string{
	roleAPI:      mechanismHTTPJSON,
	roleIngest:   mechanismHTTPJSON,
	roleSender:   mechanismAMQP,
	roleWorker:   mechanismAMQP,
	roleChains:   mechanismAMQP,
	roleSchedule: mechanismAMQP,
}

// senderContexts — явный список контекстов-источников отправки, которым
// каталог разрешает объявлять роль sender. message-delivery владеет
// агрегатом message и является единственным входом в отправку
// (утверждённое действие); integration-platform исполняет отправку как
// транспортный адаптер. Список намеренно явный: расширение контура отправки
// требует осознанной правки каталога и этого правила, а не «размывания» роли.
var senderContexts = map[string]bool{
	"message-delivery":     true,
	"integration-platform": true,
}

type DeployConfig struct {
	Processes []DeployProcess
}

type DeployProcess struct {
	Name      string
	Mechanism string
	Services  []DeployService
}

type DeployService struct {
	ID        string
	Roles     []string
	Mechanism string
}

// validateServicePerContext — правило 2.1 «один сервис на контекст»:
// service обязателен, равен id контекста и уникален; сервисов ровно столько,
// сколько контекстов.
func validateServicePerContext(catalog Catalog) []string {
	var problems []string
	services := make(map[string]string)
	for _, context := range catalog.Contexts {
		if context.Service == "" {
			problems = append(problems, fmt.Sprintf("contexts.%s: не объявлен service", context.ID))
			continue
		}
		if context.Service != context.ID {
			problems = append(problems, fmt.Sprintf("contexts.%s: service %q должен совпадать с id контекста", context.ID, context.Service))
		}
		if previous, exists := services[context.Service]; exists {
			problems = append(problems, fmt.Sprintf("contexts.%s: service %q уже принадлежит контексту %q; сервис обязан быть уникален", context.ID, context.Service, previous))
			continue
		}
		services[context.Service] = context.ID
	}
	if len(services) != len(catalog.Contexts) {
		problems = append(problems, fmt.Sprintf("contexts: сервисов %d, а контекстов %d; должен быть ровно один сервис на контекст", len(services), len(catalog.Contexts)))
	}
	return problems
}

// validateContextRoles — правило 2.2 «роли корректны»: roles непусты, содержат
// только допустимые значения и не дублируются; роль sender разрешена только
// контекстам-источникам отправки.
func validateContextRoles(catalog Catalog) []string {
	var problems []string
	for _, context := range catalog.Contexts {
		if len(context.Roles) == 0 {
			problems = append(problems, fmt.Sprintf("contexts.%s: roles пусты; нужна хотя бы одна роль запуска", context.ID))
		}
		seen := make(map[string]bool)
		for _, role := range context.Roles {
			if !allowedRoles[role] {
				problems = append(problems, fmt.Sprintf("contexts.%s: недопустимая роль %q", context.ID, role))
				continue
			}
			if seen[role] {
				problems = append(problems, fmt.Sprintf("contexts.%s: роль %q объявлена повторно", context.ID, role))
			}
			seen[role] = true
			if role == roleSender && !senderContexts[context.ID] {
				problems = append(problems, fmt.Sprintf("contexts.%s: роль sender запрещена; каталог разрешает исполнение отправки только %s", context.ID, sortedKeys(senderContexts)))
			}
		}
	}
	return problems
}

// validateRelationshipTransports — правило 2.3 «транспорт объявлен и
// согласован»: у каждой связи есть transport и mechanism; command/query →
// sync + http-json либо явно описанный co-location, event/policy → async + amqp.
// Проверка самого co-location выполняется validateInProcessBindings.
func validateRelationshipTransports(catalog Catalog) []string {
	var problems []string
	for _, relationship := range catalog.Relationships {
		expectedTransport, expectedMechanism, known := expectedTransportFor(relationship.Integration)
		if !known {
			problems = append(problems, fmt.Sprintf("relationships.%s: неизвестный тип интеграции %q", relationship.ID, relationship.Integration))
			continue
		}
		if relationship.Transport == "" {
			problems = append(problems, fmt.Sprintf("relationships.%s: не объявлен transport", relationship.ID))
		} else if relationship.Transport != expectedTransport {
			problems = append(problems, fmt.Sprintf("relationships.%s: transport %q несовместим с интеграцией %q (ожидался %q)", relationship.ID, relationship.Transport, relationship.Integration, expectedTransport))
		}
		if relationship.Mechanism == "" {
			problems = append(problems, fmt.Sprintf("relationships.%s: не объявлен mechanism", relationship.ID))
		} else if relationship.Mechanism != expectedMechanism && !(expectedTransport == transportSync && relationship.Mechanism == mechanismInProcess) {
			problems = append(problems, fmt.Sprintf("relationships.%s: mechanism %q несовместим с интеграцией %q (ожидался %q)", relationship.ID, relationship.Mechanism, relationship.Integration, expectedMechanism))
		}
	}
	return problems
}

func expectedTransportFor(integration string) (string, string, bool) {
	switch integration {
	case "command", "query":
		return transportSync, mechanismHTTPJSON, true
	case "event", "policy":
		return transportAsync, mechanismAMQP, true
	default:
		return "", "", false
	}
}

// validateDeploy — правило «деплой не спорит с моделью»: каждый сервис
// deploy/services.yaml существует в каталоге, роли процесса ⊆ ролей сервиса,
// mechanism процесса совпадает с режимом роли, и одна роль сервиса не
// разносится более чем по одному процессу (задача 4.5: сервис MAY присутствовать
// в нескольких процессах, но каждая его роль встречается ровно один раз).
//
// Отдельно проверяется изоляция опасных ролей (задача 2.2): ни один процесс не
// совмещает роль sender и роль api. Публичный edge-образ отделён структурно
// (отдельный модуль/образ) и в deploy/services.yaml сервисом каталога не
// объявляется, поэтому правила «edge в процессе» здесь нет.
func validateDeploy(catalog Catalog, deploy DeployConfig) []string {
	var problems []string
	contextByService := make(map[string]Context)
	for _, context := range catalog.Contexts {
		contextByService[context.ID] = context
		if context.Service != "" {
			contextByService[context.Service] = context
		}
	}

	deployed := make(map[string]bool)
	roleProcesses := make(map[string]map[string]string) // сервис → роль → имя процесса
	for _, process := range deploy.Processes {
		if process.Name == "" {
			problems = append(problems, "deploy: группа без имени")
		}
		if process.Mechanism != "" && !allowedMechanisms[process.Mechanism] {
			problems = append(problems, fmt.Sprintf("deploy.%s: недопустимый mechanism %q", process.Name, process.Mechanism))
		}
		processRoles := make(map[string]bool)
		for _, entry := range process.Services {
			if entry.ID == "" {
				problems = append(problems, fmt.Sprintf("deploy.%s: сервис без id", process.Name))
				continue
			}
			context, known := contextByService[entry.ID]
			if !known {
				problems = append(problems, fmt.Sprintf("deploy.%s: неизвестный сервис %q", process.Name, entry.ID))
				continue
			}
			deployed[entry.ID] = true

			// mechanism может быть объявлен и на группе, и на сервисе;
			// сервисный уровень уточняет групповой.
			mechanism := entry.Mechanism
			if mechanism == "" {
				mechanism = process.Mechanism
			}
			if mechanism == "" {
				problems = append(problems, fmt.Sprintf("deploy.%s.%s: не объявлен mechanism", process.Name, entry.ID))
			} else if !allowedMechanisms[mechanism] {
				problems = append(problems, fmt.Sprintf("deploy.%s.%s: недопустимый mechanism %q", process.Name, entry.ID, mechanism))
			}

			serviceRoles := stringSet(context.Roles)
			roleOwners := roleProcesses[entry.ID]
			if roleOwners == nil {
				roleOwners = make(map[string]string)
				roleProcesses[entry.ID] = roleOwners
			}
			for _, role := range entry.Roles {
				if !allowedRoles[role] {
					problems = append(problems, fmt.Sprintf("deploy.%s.%s: недопустимая роль %q", process.Name, entry.ID, role))
					continue
				}
				if !serviceRoles[role] {
					problems = append(problems, fmt.Sprintf("deploy.%s.%s: роль %q не объявлена сервисом", process.Name, entry.ID, role))
				}
				if mechanism != "" && !mechanismSupportsRole(mechanism, role) {
					problems = append(problems, fmt.Sprintf("deploy.%s.%s: mechanism %q не совпадает с режимом роли %q (%q)", process.Name, entry.ID, mechanism, role, roleMechanisms[role]))
				}
				if previous, exists := roleOwners[role]; exists {
					if previous == process.Name {
						problems = append(problems, fmt.Sprintf("deploy.%s.%s: роль %q объявлена дважды", process.Name, entry.ID, role))
					} else {
						problems = append(problems, fmt.Sprintf("deploy: роль %q сервиса %q объявлена в нескольких процессах (%q и %q)", role, entry.ID, previous, process.Name))
					}
					continue
				}
				roleOwners[role] = process.Name
				processRoles[role] = true
			}
		}
		// Задача 2.2: опасная роль sender не делит процесс с точкой входа api.
		if processRoles[roleSender] && processRoles[roleAPI] {
			problems = append(problems, fmt.Sprintf("deploy.%s: процесс совмещает роли %q и %q; опасная роль sender должна быть изолирована от точки входа api", process.Name, roleSender, roleAPI))
		}
	}

	for _, context := range catalog.Contexts {
		if deployed[context.Service] || deployed[context.ID] {
			continue
		}
		problems = append(problems, fmt.Sprintf("deploy: сервис %q отсутствует в deploy/services.yaml", context.Service))
	}
	sort.Strings(problems)
	return problems
}

// validateServiceModules — правило 2.1 «один модуль на сервис»: если каталог
// backend/contexts/<id>/ существует, он обязан соответствовать service-идентификатору
// контекста каталога, а его go.mod — объявлять module lead-gen/backend/contexts/<id>.
// Каталог модуля, не соответствующий ни одному сервису, — ошибка. Наличие
// модуля для ещё не реализованного сервиса не требуется: проверяются только
// существующие каталоги. Область ограничена backend/contexts/ — contracts/,
// frontend/, tools/, backend/platform/ и backend/cmd/ сервисами не являются.
func validateServiceModules(root string, catalog Catalog) []string {
	var problems []string
	services := make(map[string]bool)
	for _, context := range catalog.Contexts {
		if context.Service != "" {
			services[context.Service] = true
		}
	}

	contextsDir := filepath.Join(root, "backend", "contexts")
	entries, err := os.ReadDir(contextsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return []string{fmt.Sprintf("contexts: %v", err)}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if !services[id] {
			problems = append(problems, fmt.Sprintf("backend/contexts/%s: каталог модуля не соответствует ни одному сервису каталога", id))
			continue
		}
		expected := "lead-gen/backend/contexts/" + id
		data, err := os.ReadFile(filepath.Join(contextsDir, id, "go.mod"))
		if err != nil {
			problems = append(problems, fmt.Sprintf("backend/contexts/%s: отсутствует go.mod", id))
			continue
		}
		if actual := modulePath(data); actual != expected {
			problems = append(problems, fmt.Sprintf("backend/contexts/%s/go.mod: объявляет module %q, ожидалось %q", id, actual, expected))
		}
	}
	sort.Strings(problems)
	return problems
}

// modulePath извлекает путь из директивы module в go.mod; пустая строка, если
// директива отсутствует.
func modulePath(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "module" {
			return unquoteScalar(fields[1])
		}
	}
	return ""
}

// mechanismSupportsRole проверяет, что режим запуска соответствует роли:
// `in-process` — совмещённый деплой, совместим с любой ролью; http-json —
// только с синхронными точками входа api/ingest; amqp — только с
// асинхронными ролями sender/worker/chains/schedule.
func mechanismSupportsRole(mechanism, role string) bool {
	switch mechanism {
	case "in-process":
		return true
	case mechanismHTTPJSON:
		return role == roleAPI || role == roleIngest
	case mechanismAMQP:
		return role != roleAPI && role != roleIngest
	default:
		return false
	}
}

// loadDeploy читает и разбирает deploy/services.yaml минимальным парсером.
func loadDeploy(path string) (DeployConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DeployConfig{}, fmt.Errorf("deploy/services.yaml: файл отсутствует")
		}
		return DeployConfig{}, fmt.Errorf("deploy/services.yaml: %w", err)
	}
	config, err := parseDeployConfig(data)
	if err != nil {
		return DeployConfig{}, fmt.Errorf("deploy/services.yaml: %w", err)
	}
	return config, nil
}

type yamlLine struct {
	indent int
	text   string
}

// parseDeployConfig разбирает минимальный YAML-подмножество, достаточное для
// deploy/services.yaml: вложенные блочные списки и скаляры, flow-списки вида
// `[a, b]`, комментарии. Внешние зависимости не используются.
func parseDeployConfig(data []byte) (DeployConfig, error) {
	lines := yamlLines(data)
	var config DeployConfig
	index := 0
	for index < len(lines) && lines[index].text != "processes:" {
		index++
	}
	if index == len(lines) {
		return config, fmt.Errorf("не найдена секция processes")
	}
	index++

	for index < len(lines) {
		line := lines[index]
		if !strings.HasPrefix(line.text, "-") {
			break
		}
		process := DeployProcess{}
		baseIndent := line.indent
		rest := strings.TrimSpace(strings.TrimPrefix(line.text, "-"))
		index++
		if rest != "" {
			key, value, ok := splitKeyValue(rest)
			if !ok {
				return config, fmt.Errorf("некорректный элемент processes: %q", rest)
			}
			if key != "name" {
				return config, fmt.Errorf("неожиданное поле %q у группы", key)
			}
			process.Name = unquoteScalar(value)
		}

		for index < len(lines) && lines[index].indent > baseIndent {
			current := lines[index]
			key, value, ok := splitKeyValue(current.text)
			if !ok {
				return config, fmt.Errorf("некорректная строка группы: %q", current.text)
			}
			switch key {
			case "name":
				process.Name = unquoteScalar(value)
				index++
			case "mechanism":
				process.Mechanism = unquoteScalar(value)
				index++
			case "services":
				if strings.TrimSpace(value) != "" {
					return config, fmt.Errorf("services должен быть блочным списком")
				}
				index++
				services, next, err := parseDeployServices(lines, index, current.indent)
				if err != nil {
					return config, err
				}
				process.Services = services
				index = next
			default:
				index++
			}
		}
		config.Processes = append(config.Processes, process)
	}
	return config, nil
}

func parseDeployServices(lines []yamlLine, index, parentIndent int) ([]DeployService, int, error) {
	var services []DeployService
	for index < len(lines) && lines[index].indent > parentIndent {
		line := lines[index]
		if !strings.HasPrefix(line.text, "-") {
			break
		}
		itemIndent := line.indent
		rest := strings.TrimSpace(strings.TrimPrefix(line.text, "-"))
		index++
		service := DeployService{}
		if rest != "" {
			key, value, ok := splitKeyValue(rest)
			if !ok {
				return nil, index, fmt.Errorf("некорректный элемент services: %q", rest)
			}
			// Индекс уже указывает на следующую строку, поэтому ключ лежит строкой выше.
			next, err := applyDeployServiceKey(&service, key, value, lines, index-1, itemIndent)
			if err != nil {
				return nil, index, err
			}
			index = next
		}
		for index < len(lines) && lines[index].indent > itemIndent {
			current := lines[index]
			if strings.HasPrefix(current.text, "-") {
				break
			}
			key, value, ok := splitKeyValue(current.text)
			if !ok {
				return nil, index, fmt.Errorf("некорректная строка сервиса: %q", current.text)
			}
			next, err := applyDeployServiceKey(&service, key, value, lines, index, current.indent)
			if err != nil {
				return nil, index, err
			}
			index = next
		}
		services = append(services, service)
	}
	return services, index, nil
}

// applyDeployServiceKey применяет один ключ сервиса и возвращает индекс
// следующей неразобранной строки. keyLineIndex — строка самого ключа.
func applyDeployServiceKey(service *DeployService, key, value string, lines []yamlLine, keyLineIndex, keyIndent int) (int, error) {
	switch key {
	case "id":
		service.ID = unquoteScalar(value)
		return keyLineIndex + 1, nil
	case "mechanism":
		service.Mechanism = unquoteScalar(value)
		return keyLineIndex + 1, nil
	case "roles":
		roles, next, err := parseRolesValue(value, lines, keyLineIndex+1, keyIndent)
		if err != nil {
			return keyLineIndex + 1, err
		}
		service.Roles = roles
		return next, nil
	default:
		return keyLineIndex + 1, fmt.Errorf("неизвестное поле %q у сервиса", key)
	}
}

// parseRolesValue разбирает значение ролей: flow-список `[a, b]` или блочный
// список строк глубже по отступу.
func parseRolesValue(value string, lines []yamlLine, index, keyIndent int) ([]string, int, error) {
	inline := strings.TrimSpace(value)
	if inline != "" {
		roles, err := parseFlowList(inline)
		if err != nil {
			return nil, index, err
		}
		return roles, index, nil
	}
	var roles []string
	for index < len(lines) && lines[index].indent > keyIndent {
		item := lines[index]
		text := strings.TrimSpace(strings.TrimPrefix(item.text, "-"))
		if !strings.HasPrefix(item.text, "-") || text == "" {
			break
		}
		roles = append(roles, unquoteScalar(text))
		index++
	}
	return roles, index, nil
}

func parseFlowList(value string) ([]string, error) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return nil, fmt.Errorf("ожидался список вида [a, b], получено %q", value)
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "["), "]"))
	if inner == "" {
		return nil, nil
	}
	var items []string
	for _, part := range strings.Split(inner, ",") {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		items = append(items, unquoteScalar(item))
	}
	return items, nil
}

func yamlLines(data []byte) []yamlLine {
	var lines []yamlLine
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r\t ")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if marker := strings.Index(trimmed, " #"); marker >= 0 {
			trimmed = strings.TrimSpace(trimmed[:marker])
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		lines = append(lines, yamlLine{indent: indent, text: trimmed})
	}
	return lines
}

func splitKeyValue(text string) (string, string, bool) {
	marker := strings.Index(text, ":")
	if marker < 0 {
		return "", "", false
	}
	return strings.TrimSpace(text[:marker]), strings.TrimSpace(text[marker+1:]), true
}

func unquoteScalar(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) >= 2 {
		if (trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"') || (trimmed[0] == '\'' && trimmed[len(trimmed)-1] == '\'') {
			return trimmed[1 : len(trimmed)-1]
		}
	}
	return trimmed
}

func sortedKeys(set map[string]bool) string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
