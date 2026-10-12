package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
)

// infrastructureSource — человекочитаемое имя источника для сообщений об ошибках.
const infrastructureSource = "deploy/infrastructure.yaml"

// infrastructureKinds — объявленный перечень видов инфраструктурных узлов.
// `postgres` используется и для зелёной, и для красной зоны (зоны различает поле
// zone). `service` — ссылка на сервис каталога: такой узел обязан существовать.
var infrastructureKinds = map[string]bool{
	"postgres":       true,
	"broker":         true,
	"cache":          true,
	"object-storage": true,
	"workflow":       true,
	"metrics":        true,
	"external":       true,
	"service":        true,
}

// infrastructureZones — допустимые зоны данных инфраструктурного узла.
var infrastructureZones = map[string]bool{
	"green": true,
	"red":   true,
	"mixed": true,
	"infra": true,
}

// infrastructureAddressRequired — виды, для которых адрес обязателен: без адреса
// сетевой узел бесполезен. Объектное хранилище и внешние провайдеры задаются без
// адреса по замыслу, поэтому у них адрес необязателен.
var infrastructureAddressRequired = map[string]bool{
	"postgres": true,
	"broker":   true,
	"cache":    true,
	"workflow": true,
	"metrics":  true,
}

// InfrastructureNode — инфраструктурный узел, объявленный в
// `deploy/infrastructure.yaml`.
type InfrastructureNode struct {
	ID        string
	Kind      string
	Label     string
	Zone      string
	Addresses []string
}

// InfrastructureConfig — объявленный перечень инфраструктурных узлов.
type InfrastructureConfig struct {
	Nodes []InfrastructureNode
}

// loadInfrastructure читает и разбирает `deploy/infrastructure.yaml`.
func loadInfrastructure(path string) (InfrastructureConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return InfrastructureConfig{}, fmt.Errorf("%s: файл отсутствует", infrastructureSource)
		}
		return InfrastructureConfig{}, fmt.Errorf("%s: %w", infrastructureSource, err)
	}
	config, err := parseInfrastructure(data)
	if err != nil {
		return InfrastructureConfig{}, fmt.Errorf("%s: %w", infrastructureSource, err)
	}
	return config, nil
}

// parseInfrastructure разбирает минимальное YAML-подмножество
// `deploy/infrastructure.yaml`: блочный список узлов, flow- и блочные списки
// адресов, скаляры, комментарии. Внешние зависимости не используются.
func parseInfrastructure(data []byte) (InfrastructureConfig, error) {
	lines := yamlLines(data)
	var config InfrastructureConfig
	index := 0
	for index < len(lines) && lines[index].text != "nodes:" {
		index++
	}
	if index == len(lines) {
		return config, fmt.Errorf("не найдена секция nodes")
	}
	index++

	for index < len(lines) {
		line := lines[index]
		if !strings.HasPrefix(line.text, "-") {
			break
		}
		itemIndent := line.indent
		rest := strings.TrimSpace(strings.TrimPrefix(line.text, "-"))
		index++
		node := InfrastructureNode{}
		if rest != "" {
			key, value, ok := splitKeyValue(rest)
			if !ok {
				return config, fmt.Errorf("некорректный элемент nodes: %q", rest)
			}
			next, err := applyInfrastructureKey(&node, key, value, lines, index-1, itemIndent)
			if err != nil {
				return config, err
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
				return config, fmt.Errorf("некорректная строка узла: %q", current.text)
			}
			next, err := applyInfrastructureKey(&node, key, value, lines, index, current.indent)
			if err != nil {
				return config, err
			}
			index = next
		}
		config.Nodes = append(config.Nodes, node)
	}
	return config, nil
}

// applyInfrastructureKey применяет один ключ узла и возвращает индекс следующей
// неразобранной строки. keyLineIndex — строка самого ключа.
func applyInfrastructureKey(node *InfrastructureNode, key, value string, lines []yamlLine, keyLineIndex, keyIndent int) (int, error) {
	switch key {
	case "id":
		node.ID = unquoteScalar(value)
		return keyLineIndex + 1, nil
	case "kind":
		node.Kind = unquoteScalar(value)
		return keyLineIndex + 1, nil
	case "label":
		node.Label = unquoteScalar(value)
		return keyLineIndex + 1, nil
	case "zone":
		node.Zone = unquoteScalar(value)
		return keyLineIndex + 1, nil
	case "addresses":
		addresses, next, err := parseInfrastructureList(value, lines, keyLineIndex+1, keyIndent)
		if err != nil {
			return keyLineIndex + 1, err
		}
		node.Addresses = addresses
		return next, nil
	default:
		return keyLineIndex + 1, fmt.Errorf("неизвестное поле %q у узла", key)
	}
}

// parseInfrastructureList разбирает значение адресов: flow-список `[a, b]` или
// блочный список строк глубже по отступу.
func parseInfrastructureList(value string, lines []yamlLine, index, keyIndent int) ([]string, int, error) {
	inline := strings.TrimSpace(value)
	if inline != "" {
		items, err := parseFlowList(inline)
		if err != nil {
			return nil, index, err
		}
		return items, index, nil
	}
	var items []string
	for index < len(lines) && lines[index].indent > keyIndent {
		item := lines[index]
		if !strings.HasPrefix(item.text, "-") {
			break
		}
		items = append(items, unquoteScalar(strings.TrimSpace(strings.TrimPrefix(item.text, "-"))))
		index++
	}
	return items, index, nil
}

// validateInfrastructure проверяет `deploy/infrastructure.yaml`: вид узла — из
// объявленного перечня, адрес непуст (где применимо), значения не противоречат
// каталогу и `deploy/services.yaml`. Нарушение — ошибка, а не предупреждение.
func validateInfrastructure(catalog Catalog, deploy DeployConfig, config InfrastructureConfig) []string {
	var problems []string

	catalogServices := make(map[string]bool)
	for _, context := range catalog.Contexts {
		if context.ID != "" {
			catalogServices[context.ID] = true
		}
		if context.Service != "" {
			catalogServices[context.Service] = true
		}
	}
	deployed := make(map[string]bool)
	for _, process := range deploy.Processes {
		for _, service := range process.Services {
			if service.ID != "" {
				deployed[service.ID] = true
			}
		}
	}
	haveDeploy := len(deploy.Processes) > 0

	seen := make(map[string]bool)
	for _, node := range config.Nodes {
		if node.ID == "" {
			problems = append(problems, infrastructureSource+": узел без id")
			continue
		}
		label := fmt.Sprintf("%s: узел %q", infrastructureSource, node.ID)
		if seen[node.ID] {
			problems = append(problems, label+": объявлен более одного раза")
		}
		seen[node.ID] = true

		if node.Kind == "" {
			problems = append(problems, label+": не объявлен kind")
		} else if !infrastructureKinds[node.Kind] {
			problems = append(problems, label+fmt.Sprintf(": неизвестный вид %q", node.Kind))
		}
		if node.Label == "" {
			problems = append(problems, label+": не объявлен label")
		}
		if node.Zone != "" && !infrastructureZones[node.Zone] {
			problems = append(problems, label+fmt.Sprintf(": неизвестная зона %q", node.Zone))
		}

		emptyAddress := false
		for _, address := range node.Addresses {
			if strings.TrimSpace(address) == "" {
				emptyAddress = true
				break
			}
		}
		if emptyAddress {
			problems = append(problems, label+": пустой адрес")
		}
		if node.Kind != "" && infrastructureAddressRequired[node.Kind] && len(node.Addresses) == 0 {
			problems = append(problems, label+fmt.Sprintf(": вид %q требует адрес", node.Kind))
		}

		if node.Kind == "service" {
			if !catalogServices[node.ID] {
				problems = append(problems, label+": узел вида service не найден в каталоге")
			} else if haveDeploy && !deployed[node.ID] {
				problems = append(problems, label+": сервис не объявлен в deploy/services.yaml")
			}
		} else if node.Kind != "" && catalogServices[node.ID] {
			problems = append(problems, label+": идентификатор совпадает с сервисом каталога")
		}
	}
	sort.Strings(problems)
	return problems
}
