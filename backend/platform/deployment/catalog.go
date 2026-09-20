package deployment

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// catalogSource — человекочитаемое имя источника для сообщений об ошибках.
const catalogSource = "architecture/domain-catalog.yaml"

// catalogDocument — минимальная проекция каталога: только то, что нужно графу.
// Каталог хранит JSON (несмотря на расширение), поэтому разбирается
// encoding/json, а не мини-парсером YAML.
type catalogDocument struct {
	Contexts []struct {
		ID             string   `json:"id"`
		Name           string   `json:"name"`
		Classification string   `json:"classification"`
		Service        string   `json:"service"`
		Roles          []string `json:"roles"`
	} `json:"contexts"`
	Relationships []struct {
		ID         string `json:"id"`
		Upstream   string `json:"upstream"`
		Downstream string `json:"downstream"`
		Contract   string `json:"contract"`
		Transport  string `json:"transport"`
		Mechanism  string `json:"mechanism"`
	} `json:"relationships"`
}

// readCatalog читает каталог, строит узлы-сервисы и рёбра. Возвращает также
// проекцию «id контекста → сервис», чтобы перевести концы связей в ID узлов.
func readCatalog(path string) ([]Node, []Edge, map[string]string, error) {
	data, err := readSource(path, catalogSource)
	if err != nil {
		return nil, nil, nil, err
	}
	var document catalogDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, nil, nil, fmt.Errorf("%s: разбор JSON: %w", catalogSource, err)
	}
	if len(document.Contexts) == 0 {
		return nil, nil, nil, fmt.Errorf("%s: не объявлено ни одного контекста", catalogSource)
	}

	contextToService := make(map[string]string, len(document.Contexts))
	nodes := make([]Node, 0, len(document.Contexts))
	seenServices := make(map[string]bool, len(document.Contexts))
	for _, context := range document.Contexts {
		if context.ID == "" {
			return nil, nil, nil, fmt.Errorf("%s: контекст без id", catalogSource)
		}
		if context.Service == "" {
			return nil, nil, nil, fmt.Errorf("%s: контекст %q не объявляет service", catalogSource, context.ID)
		}
		if seenServices[context.Service] {
			return nil, nil, nil, fmt.Errorf("%s: сервис %q объявлен более одного раза", catalogSource, context.Service)
		}
		seenServices[context.Service] = true
		contextToService[context.ID] = context.Service

		meta := map[string]string{"context": context.ID}
		if context.Classification != "" {
			meta["classification"] = context.Classification
		}
		nodes = append(nodes, Node{
			ID:     context.Service,
			Kind:   NodeService,
			Label:  context.Name,
			Roles:  append([]string(nil), context.Roles...),
			Health: HealthUnknown,
			Meta:   meta,
		})
	}

	edges := make([]Edge, 0, len(document.Relationships))
	seenEdges := make(map[string]bool, len(document.Relationships))
	for _, relationship := range document.Relationships {
		if relationship.ID == "" {
			return nil, nil, nil, fmt.Errorf("%s: связь без id", catalogSource)
		}
		if seenEdges[relationship.ID] {
			return nil, nil, nil, fmt.Errorf("%s: связь %q объявлена более одного раза", catalogSource, relationship.ID)
		}
		seenEdges[relationship.ID] = true

		upstream, err := resolveEndpoint(contextToService, relationship.Upstream, relationship.ID, "upstream")
		if err != nil {
			return nil, nil, nil, err
		}
		downstream, err := resolveEndpoint(contextToService, relationship.Downstream, relationship.ID, "downstream")
		if err != nil {
			return nil, nil, nil, err
		}
		if relationship.Transport == "" {
			return nil, nil, nil, fmt.Errorf("%s: связь %q не объявляет transport", catalogSource, relationship.ID)
		}
		if relationship.Mechanism == "" {
			return nil, nil, nil, fmt.Errorf("%s: связь %q не объявляет mechanism", catalogSource, relationship.ID)
		}
		if relationship.Contract == "" {
			return nil, nil, nil, fmt.Errorf("%s: связь %q не объявляет contract", catalogSource, relationship.ID)
		}

		edges = append(edges, Edge{
			ID:         relationship.ID,
			Upstream:   upstream,
			Downstream: downstream,
			Contract:   relationship.Contract,
			Transport:  relationship.Transport,
			Mechanism:  relationship.Mechanism,
			Weight:     nil,
			Health:     HealthUnknown,
		})
	}

	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.SliceStable(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	return nodes, edges, contextToService, nil
}

// resolveEndpoint переводит id контекста в id узла-сервиса. Ссылка на
// неизвестный контекст — ошибка, а не молчаливо пропущенное ребро: догадка о
// узле исказила бы модель.
func resolveEndpoint(contextToService map[string]string, contextID, edgeID, side string) (string, error) {
	if contextID == "" {
		return "", fmt.Errorf("%s: связь %q не объявляет %s", catalogSource, edgeID, side)
	}
	service, ok := contextToService[contextID]
	if !ok {
		return "", fmt.Errorf("%s: связь %q ссылается на неизвестный контекст %q (%s)",
			catalogSource, edgeID, contextID, side)
	}
	return service, nil
}

// joinList склеивает список в строку для Meta.
func joinList(items []string) string {
	return strings.Join(items, ", ")
}
