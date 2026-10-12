package topology

import (
	"fmt"
	"strings"
)

// infrastructureSource — человекочитаемое имя источника для сообщений об ошибках.
const infrastructureSource = "deploy/infrastructure.yaml"

// infrastructureKinds — допустимые виды инфраструктурных узлов. `postgres`
// используется и для зелёной, и для красной зоны — зоны различает поле zone, а
// не вид. `service` означает ссылку на сервис каталога (например, для явного
// сопоставления узла инфраструктуры сервису); такой узел обязан существовать.
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

// infrastructureAddressRequired — виды, для которых адрес обязателен: сетевой
// узел без адреса бесполезен. Объектное хранилище и внешние провайдеры могут
// задаваться без адреса (в источнике адрес недоступен по замыслу), поэтому у них
// адрес необязателен.
var infrastructureAddressRequired = map[string]bool{
	"postgres": true,
	"broker":   true,
	"cache":    true,
	"workflow": true,
	"metrics":  true,
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
// Обязательны id и kind; label, zone и addresses необязательны. Неизвестный ключ,
// вид или зона — ошибка: молчаливый пропуск исказил бы граф, а несоответствие
// конфигурации должно быть видно.
func readInfrastructure(path string) ([]Node, error) {
	if path == "" {
		return nil, nil
	}
	data, err := readSourceFile(path, infrastructureSource)
	if err != nil {
		return nil, err
	}
	return parseInfrastructure(data)
}

// parseInfrastructure разбирает содержимое `deploy/infrastructure.yaml`.
func parseInfrastructure(data []byte) ([]Node, error) {
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
		if kind == "service" {
			// Узел-ссылка не описывает инфраструктуру; id обязан указывать на
			// сервис каталога, иначе это опечатка, а не узел.
			return nil, fmt.Errorf("%s: узел %q: вид service зарезервирован и не может быть объявлен в источнике инфраструктуры", infrastructureSource, id)
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
		if infrastructureAddressRequired[kind] && len(addresses) == 0 {
			return nil, fmt.Errorf("%s: узел %q вида %q без адреса", infrastructureSource, id, kind)
		}

		endpoints := make([]Endpoint, 0, len(addresses))
		for _, address := range addresses {
			if strings.TrimSpace(address) == "" {
				return nil, fmt.Errorf("%s: узел %q: пустой адрес", infrastructureSource, id)
			}
			endpoints = append(endpoints, parseEndpoint(address))
		}

		nodes = append(nodes, Node{
			ID:        id,
			Kind:      NodeInfrastructure,
			Label:     label,
			Zone:      zone,
			Addresses: endpoints,
			Health:    Health{State: HealthUnknown},
			Meta:      map[string]string{"kind": kind},
		})
	}
	return nodes, nil
}

// parseEndpoint разбирает внутренний адрес на хост и порт. Порт выделяется,
// только если он числовой; адрес-подстановка (например, «<внутренний адрес
// контура клиента>») сохраняется целиком без домысла.
func parseEndpoint(address string) Endpoint {
	endpoint := Endpoint{Address: strings.TrimSpace(address)}
	marker := strings.LastIndex(endpoint.Address, ":")
	if marker <= 0 || marker == len(endpoint.Address)-1 {
		return endpoint
	}
	candidate := endpoint.Address[marker+1:]
	if !isDigits(candidate) {
		return endpoint
	}
	endpoint.Host = endpoint.Address[:marker]
	endpoint.Port = candidate
	return endpoint
}

// isDigits сообщает, что строка непуста и состоит только из цифр.
func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
