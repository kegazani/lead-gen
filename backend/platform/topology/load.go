package topology

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"lead-gen/backend/platform/topology/catalog"
)

// Sources — пути канонических источников и точка чтения файлов. Пустой путь
// означает, что источник не участвует в сборке (нужно для узких проверок); это
// не ошибка. ReadFile позволяет подменить чтение в тестах (nil — os.ReadFile).
type Sources struct {
	CatalogPath        string
	ServicesPath       string
	InfrastructurePath string
	ReadFile           func(path string) ([]byte, error)
}

// read читает источник, оборачивая ошибку его именем.
func (s Sources) read(path, name string) ([]byte, error) {
	read := s.ReadFile
	if read == nil {
		read = os.ReadFile
	}
	return readFileSource(read, path, name)
}

// Build собирает граф из канонических источников. Сборка fail-closed: недоступный
// или некорректный источник не роняет её, а даёт запись в Graph.Warnings; связь,
// ссылающаяся на неизвестный узел, не подменяется догадкой — узел получает вид
// unknown, а причина попадает в предупреждения. Порядок узлов детерминирован:
// сервисы (по id), инфраструктура (по id), unknown (по id); рёбра — по id.
// Состояние всех узлов и интенсивность всех рёбер — unknown: статический граф не
// выдумывает измерения.
func Build(sources Sources) Graph {
	var warnings []string

	document := catalog.Document{}
	if sources.CatalogPath == "" {
		warnings = append(warnings, "каталог доменной архитектуры не задан: узлы-сервисы и межконтекстные связи недоступны")
	} else if data, err := sources.read(sources.CatalogPath, catalog.Source); err != nil {
		warnings = append(warnings, err.Error())
	} else if parsed, err := catalog.Parse(data); err != nil {
		warnings = append(warnings, err.Error())
	} else {
		document = parsed
	}

	serviceByContext := make(map[string]string, len(document.Services))
	serviceIDs := make(map[string]bool, len(document.Services))
	nodes := make([]Node, 0, len(document.Services))
	for _, service := range document.Services {
		serviceByContext[service.ContextID] = service.ID
		serviceIDs[service.ID] = true
		meta := map[string]string{"context": service.ContextID}
		if service.Classification != "" {
			meta["classification"] = service.Classification
		}
		nodes = append(nodes, Node{
			ID:        service.ID,
			Kind:      NodeService,
			Label:     service.Name,
			Roles:     append([]string(nil), service.Roles...),
			Contracts: append([]string(nil), service.Contracts...),
			Health:    Health{State: HealthUnknown},
			Meta:      meta,
		})
	}

	if sources.ServicesPath != "" {
		if data, err := sources.read(sources.ServicesPath, servicesSource); err != nil {
			warnings = append(warnings, err.Error())
		} else if parsed, err := parseDeploy(data); err != nil {
			warnings = append(warnings, err.Error())
		} else {
			for index := range nodes {
				if entry, ok := parsed[nodes[index].ID]; ok {
					nodes[index].Groups = append([]string(nil), entry.Groups...)
				}
			}
			for id := range parsed {
				if !serviceIDs[id] {
					warnings = append(warnings, fmt.Sprintf("%s: сервис %q отсутствует в каталоге", servicesSource, id))
				}
			}
		}
	}

	var infrastructure []Node
	if sources.InfrastructurePath != "" {
		if data, err := sources.read(sources.InfrastructurePath, infrastructureSource); err != nil {
			warnings = append(warnings, err.Error())
		} else if parsed, err := parseInfrastructure(data); err != nil {
			warnings = append(warnings, err.Error())
		} else {
			infrastructure = parsed
			for _, node := range infrastructure {
				if serviceIDs[node.ID] {
					warnings = append(warnings, fmt.Sprintf("%s: узел %q совпадает с сервисом каталога", infrastructureSource, node.ID))
				}
			}
		}
	}

	edges := make([]Edge, 0, len(document.Relationships))
	knownNode := func(id string) bool {
		if serviceIDs[id] {
			return true
		}
		for _, node := range infrastructure {
			if node.ID == id {
				return true
			}
		}
		return false
	}
	unknownIDs := make(map[string]bool)
	var unknownOrder []string
	unknownReasons := make(map[string]string)
	registerUnknown := func(contextID, reason string) {
		if contextID == "" || knownNode(contextID) || unknownIDs[contextID] {
			return
		}
		unknownIDs[contextID] = true
		unknownOrder = append(unknownOrder, contextID)
		unknownReasons[contextID] = reason
	}
	for _, relationship := range document.Relationships {
		from := serviceByContext[relationship.UpstreamContext]
		if from == "" {
			registerUnknown(relationship.UpstreamContext,
				fmt.Sprintf("связь %q ссылается на неизвестный контекст %q (upstream)", relationship.ID, relationship.UpstreamContext))
			from = relationship.UpstreamContext
		}
		to := serviceByContext[relationship.DownstreamContext]
		if to == "" {
			registerUnknown(relationship.DownstreamContext,
				fmt.Sprintf("связь %q ссылается на неизвестный контекст %q (downstream)", relationship.ID, relationship.DownstreamContext))
			to = relationship.DownstreamContext
		}
		edges = append(edges, Edge{
			ID:          relationship.ID,
			From:        from,
			To:          to,
			Integration: relationship.Integration,
			Transport:   relationship.Transport,
			Mechanism:   relationship.Mechanism,
			Contract:    relationship.Contract,
			Intensity:   Intensity{State: IntensityUnknown},
		})
	}
	sort.Strings(unknownOrder)
	for _, id := range unknownOrder {
		warnings = append(warnings, unknownReasons[id])
	}

	derived, derivedWarnings := deriveServiceInfrastructureEdges(nodes, infrastructure)
	edges = append(edges, derived...)
	warnings = append(warnings, derivedWarnings...)

	sort.SliceStable(infrastructure, func(i, j int) bool { return infrastructure[i].ID < infrastructure[j].ID })
	allNodes := make([]Node, 0, len(nodes)+len(infrastructure)+len(unknownOrder))
	allNodes = append(allNodes, nodes...)
	allNodes = append(allNodes, infrastructure...)
	for _, id := range unknownOrder {
		allNodes = append(allNodes, Node{
			ID:     id,
			Kind:   NodeUnknown,
			Label:  id,
			Health: Health{State: HealthUnknown},
		})
	}
	sort.SliceStable(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })

	graph := Graph{
		Nodes:       allNodes,
		Edges:       edges,
		GeneratedAt: time.Now().UTC(),
		Warnings:    warnings,
	}
	return graph.Layout()
}

// readFileSource читает файл через переданную функцию и оборачивает ошибку
// именем источника.
func readFileSource(read func(string) ([]byte, error), path, name string) ([]byte, error) {
	data, err := read(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s: файл отсутствует: %s", name, path)
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return data, nil
}

// readSourceFile — readFileSource поверх os.ReadFile для узких загрузчиков.
func readSourceFile(path, name string) ([]byte, error) {
	return readFileSource(os.ReadFile, path, name)
}
