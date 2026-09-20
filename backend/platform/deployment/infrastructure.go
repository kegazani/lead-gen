package deployment

import (
	"fmt"
)

// infrastructureSource — человекочитаемое имя источника для сообщений об ошибках.
const infrastructureSource = "deploy/infrastructure.yaml"

// infrastructureKinds — допустимые виды инфраструктурных узлов. Вид задаёт
// подпись и группировку на графе; `postgres` используется и для зелёной, и для
// красной зоны — зоны различает поле zone, а не вид.
var infrastructureKinds = map[string]bool{
	"postgres":       true,
	"broker":         true,
	"cache":          true,
	"object-storage": true,
	"workflow":       true,
	"metrics":        true,
	"external":       true,
}

// infrastructureZones — допустимые зоны данных инфраструктурного узла.
var infrastructureZones = map[string]bool{
	"green": true,
	"red":   true,
	"mixed": true,
	"infra": true,
}

// readInfrastructure читает `deploy/infrastructure.yaml` и возвращает
// инфраструктурные узлы. Пустой путь означает, что источник не участвует в
// сборке. Формат:
//
//	nodes:
//	  - id: rabbitmq
//	    kind: broker
//	    label: "RabbitMQ"
//	    zone: green
//	    addresses: ["rabbitmq:5672"]
//
// Обязательны id и kind; label, zone и addresses необязательны.
func readInfrastructure(path string) ([]Node, error) {
	if path == "" {
		return nil, nil
	}
	data, err := readSource(path, infrastructureSource)
	if err != nil {
		return nil, err
	}
	root, err := parseYAML(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", infrastructureSource, err)
	}
	document, err := asMapping(root)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", infrastructureSource, err)
	}
	if err := rejectUnknownKeys(document, "nodes"); err != nil {
		return nil, fmt.Errorf("%s: %w", infrastructureSource, err)
	}
	rawNodes, ok := document["nodes"]
	if !ok {
		return nil, fmt.Errorf("%s: не найдена секция nodes", infrastructureSource)
	}
	items, err := asSequence(rawNodes)
	if err != nil {
		return nil, fmt.Errorf("%s: узлы: %w", infrastructureSource, err)
	}

	nodes := make([]Node, 0, len(items))
	seen := make(map[string]bool, len(items))
	for index, rawNode := range items {
		mapping, err := asMapping(rawNode)
		if err != nil {
			return nil, fmt.Errorf("%s: узел %d: %w", infrastructureSource, index, err)
		}
		if err := rejectUnknownKeys(mapping, "id", "kind", "label", "zone", "addresses"); err != nil {
			return nil, fmt.Errorf("%s: узел %d: %w", infrastructureSource, index, err)
		}
		id, err := asString(mapping["id"])
		if err != nil {
			return nil, fmt.Errorf("%s: узел %d: id: %w", infrastructureSource, index, err)
		}
		if id == "" {
			return nil, fmt.Errorf("%s: узел %d без id", infrastructureSource, index)
		}
		if seen[id] {
			return nil, fmt.Errorf("%s: узел %q объявлен более одного раза", infrastructureSource, id)
		}
		seen[id] = true

		kind, err := asString(mapping["kind"])
		if err != nil {
			return nil, fmt.Errorf("%s: узел %q: kind: %w", infrastructureSource, id, err)
		}
		if !infrastructureKinds[kind] {
			return nil, fmt.Errorf("%s: узел %q: неизвестный вид %q", infrastructureSource, id, kind)
		}
		label, err := asString(mapping["label"])
		if err != nil {
			return nil, fmt.Errorf("%s: узел %q: label: %w", infrastructureSource, id, err)
		}
		if label == "" {
			return nil, fmt.Errorf("%s: узел %q без label", infrastructureSource, id)
		}
		zone, err := asString(mapping["zone"])
		if err != nil {
			return nil, fmt.Errorf("%s: узел %q: zone: %w", infrastructureSource, id, err)
		}
		if zone != "" && !infrastructureZones[zone] {
			return nil, fmt.Errorf("%s: узел %q: неизвестная зона %q", infrastructureSource, id, zone)
		}
		addresses, err := asStringSequence(mapping["addresses"])
		if err != nil {
			return nil, fmt.Errorf("%s: узел %q: addresses: %w", infrastructureSource, id, err)
		}

		meta := map[string]string{"kind": kind}
		if len(addresses) > 0 {
			meta["addresses"] = joinList(addresses)
		}
		nodes = append(nodes, Node{
			ID:     id,
			Kind:   NodeInfra,
			Label:  label,
			Zone:   zone,
			Health: HealthUnknown,
			Meta:   meta,
		})
	}
	return nodes, nil
}
