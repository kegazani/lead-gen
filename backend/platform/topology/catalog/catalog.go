// Package catalog — только читающий загрузчик каталога доменной архитектуры
// (`architecture/domain-catalog.yaml`). Пакет разбирает артефакт и возвращает
// нейтральную проекцию: сервисы (узлы каталога) и объявленные связи между ними.
//
// Доменных терминов и импорта контекстов здесь нет: это технический разбор
// объявленного источника, а не модель домена. Значения концов связи остаются
// идентификаторами контекстов; перевод в идентификаторы узлов-сервисов делает
// построитель графа и не прячет несоответствие — ссылку на неизвестный контекст
// он помечает, а не подставляет догадкой.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Source — человекочитаемое имя источника для сообщений об ошибках.
const Source = "architecture/domain-catalog.yaml"

// Service — сервис каталога: элемент contexts, объявляющий service. Service.ID —
// это идентификатор сервиса (совпадает с id контекста по правилу «один сервис на
// контекст»); ContextID хранит id контекста, чтобы связать концы связей.
type Service struct {
	ContextID      string
	ID             string
	Name           string
	Classification string
	Roles          []string
	Contracts      []string
}

// Relationship — объявленная связь каталога. Концы остаются идентификаторами
// контекстов (UpstreamContext/DownstreamContext).
type Relationship struct {
	ID                string
	UpstreamContext   string
	DownstreamContext string
	Integration       string
	Contract          string
	Transport         string
	Mechanism         string
}

// Document — проекция каталога, нужная построителю графа.
type Document struct {
	Services      []Service
	Relationships []Relationship
}

// document — форма разбираемого артефакта. Каталог хранит JSON (несмотря на
// расширение .yaml), поэтому используется encoding/json, а не мини-парсер YAML.
type document struct {
	Contexts []struct {
		ID             string   `json:"id"`
		Name           string   `json:"name"`
		Classification string   `json:"classification"`
		Service        string   `json:"service"`
		Roles          []string `json:"roles"`
		Contracts      []string `json:"contracts"`
	} `json:"contexts"`
	Relationships []struct {
		ID          string `json:"id"`
		Upstream    string `json:"upstream"`
		Downstream  string `json:"downstream"`
		Integration string `json:"integration"`
		Contract    string `json:"contract"`
		Transport   string `json:"transport"`
		Mechanism   string `json:"mechanism"`
	} `json:"relationships"`
}

// Read читает каталог по пути и разбирает его.
func Read(path string) (Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Document{}, fmt.Errorf("%s: %w", Source, err)
	}
	return Parse(data)
}

// Parse разбирает содержимое каталога. Ошибка возвращается на структурные
// дефекты самого артефакта (нет контекстов, контекст без service, повтор
// сервиса, связь без id); ссылки на неизвестные контексты здесь не проверяются —
// это ответственность построителя графа, который переводит их в предупреждение.
func Parse(data []byte) (Document, error) {
	var raw document
	if err := json.Unmarshal(data, &raw); err != nil {
		return Document{}, fmt.Errorf("%s: разбор JSON: %w", Source, err)
	}
	if len(raw.Contexts) == 0 {
		return Document{}, fmt.Errorf("%s: не объявлено ни одного контекста", Source)
	}

	seenServices := make(map[string]bool, len(raw.Contexts))
	services := make([]Service, 0, len(raw.Contexts))
	for _, context := range raw.Contexts {
		if context.ID == "" {
			return Document{}, fmt.Errorf("%s: контекст без id", Source)
		}
		if context.Service == "" {
			return Document{}, fmt.Errorf("%s: контекст %q не объявляет service", Source, context.ID)
		}
		if seenServices[context.Service] {
			return Document{}, fmt.Errorf("%s: сервис %q объявлен более одного раза", Source, context.Service)
		}
		seenServices[context.Service] = true
		services = append(services, Service{
			ContextID:      context.ID,
			ID:             context.Service,
			Name:           context.Name,
			Classification: context.Classification,
			Roles:          append([]string(nil), context.Roles...),
			Contracts:      append([]string(nil), context.Contracts...),
		})
	}

	seenEdges := make(map[string]bool, len(raw.Relationships))
	relationships := make([]Relationship, 0, len(raw.Relationships))
	for _, relationship := range raw.Relationships {
		if relationship.ID == "" {
			return Document{}, fmt.Errorf("%s: связь без id", Source)
		}
		if seenEdges[relationship.ID] {
			return Document{}, fmt.Errorf("%s: связь %q объявлена более одного раза", Source, relationship.ID)
		}
		seenEdges[relationship.ID] = true
		relationships = append(relationships, Relationship{
			ID:                relationship.ID,
			UpstreamContext:   relationship.Upstream,
			DownstreamContext: relationship.Downstream,
			Integration:       relationship.Integration,
			Contract:          relationship.Contract,
			Transport:         relationship.Transport,
			Mechanism:         relationship.Mechanism,
		})
	}

	sort.SliceStable(services, func(i, j int) bool { return services[i].ID < services[j].ID })
	sort.SliceStable(relationships, func(i, j int) bool { return relationships[i].ID < relationships[j].ID })
	return Document{Services: services, Relationships: relationships}, nil
}
