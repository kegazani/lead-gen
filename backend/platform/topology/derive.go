package topology

import (
	"fmt"
	"sort"
)

// Роли запуска и виды инфраструктуры, используемые при выводе рёбер
// «сервис → инфраструктура».
const (
	roleAPI      = "api"
	roleIngest   = "ingest"
	roleSender   = "sender"
	roleWorker   = "worker"
	roleChains   = "chains"
	roleSchedule = "schedule"

	infraKindPostgres      = "postgres"
	infraKindBroker        = "broker"
	infraKindCache         = "cache"
	infraKindObjectStorage = "object-storage"
	infraKindWorkflow      = "workflow"
	infraKindMetrics       = "metrics"
	infraKindExternal      = "external"
)

// storageZone — зона, в которой сервис платформы хранит собственные данные.
// Красная зона принадлежит контуру клиента и не связывается выведенными рёбрами:
// консоль показывает её как объявленный узел, но не домысливает доступ сервиса.
const storageZone = "green"

// roleInfrastructure сопоставляет роль запуска видам инфраструктуры, которые она
// заведомо использует. Это выводимое знание, а не переобъявление связей: каталог
// объявляет роли и межконтекстные связи, а принадлежность роли к инфраструктуре
// выводится здесь детерминированно.
var roleInfrastructure = map[string][]string{
	roleAPI:      {infraKindCache, infraKindMetrics},
	roleIngest:   {infraKindCache, infraKindMetrics},
	roleSender:   {infraKindBroker, infraKindMetrics},
	roleWorker:   {infraKindBroker, infraKindExternal, infraKindMetrics},
	roleChains:   {infraKindWorkflow, infraKindExternal, infraKindMetrics},
	roleSchedule: {infraKindBroker, infraKindMetrics},
}

// deriveServiceInfrastructureEdges выводит рёбра «сервис → инфраструктура» из
// ролей сервисов и зон инфраструктурных узлов. Рёбра детерминированы (порядок по
// id сервисов, ролей и узлов) и не дублируют межконтекстные связи каталога:
// адресат — инфраструктурный узел, а не другой сервис. Если для нужного вида нет
// ни одного объявленного узла, ребро не выдумывается, а появляется запись в
// warnings.
func deriveServiceInfrastructureEdges(services, infrastructure []Node) ([]Edge, []string) {
	byKind := make(map[string][]Node)
	for _, node := range infrastructure {
		kind := node.Meta["kind"]
		byKind[kind] = append(byKind[kind], node)
	}
	for kind := range byKind {
		items := byKind[kind]
		sort.SliceStable(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	}

	var edges []Edge
	var warnings []string
	seenEdge := make(map[string]bool)
	seenWarning := make(map[string]bool)

	addEdge := func(from, to Node, kind string) {
		id := DerivedEdgePrefix + from.ID + "->" + to.ID
		if seenEdge[id] {
			return
		}
		seenEdge[id] = true
		edges = append(edges, Edge{
			ID:        id,
			From:      from.ID,
			To:        to.ID,
			Transport: transportForInfraKind(kind),
			Mechanism: mechanismForInfraKind(kind),
			Intensity: Intensity{State: IntensityUnknown},
		})
	}

	warnOnce := func(key, message string) {
		if seenWarning[key] {
			return
		}
		seenWarning[key] = true
		warnings = append(warnings, message)
	}

	for _, service := range services {
		storageLinked := false
		for _, node := range byKind[infraKindPostgres] {
			if node.Zone != storageZone {
				continue
			}
			addEdge(service, node, infraKindPostgres)
			storageLinked = true
		}
		if !storageLinked && len(byKind[infraKindPostgres]) > 0 {
			warnOnce("storage",
				fmt.Sprintf("нет узла %s зоны %q: рёбра хранилища сервисов не построены", infraKindPostgres, storageZone))
		}

		targets := make(map[string]bool)
		for _, role := range service.Roles {
			for _, kind := range roleInfrastructure[role] {
				targets[kind] = true
			}
		}
		kinds := make([]string, 0, len(targets))
		for kind := range targets {
			kinds = append(kinds, kind)
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			nodes := byKind[kind]
			if len(nodes) == 0 {
				warnOnce("kind:"+kind,
					fmt.Sprintf("нет инфраструктурного узла вида %q: выведенные рёбра этого вида не построены", kind))
				continue
			}
			for _, node := range nodes {
				addEdge(service, node, kind)
			}
		}
	}
	return edges, warnings
}

// mechanismForInfraKind — механизм, выводимый из вида инфраструктурного узла.
func mechanismForInfraKind(kind string) string {
	switch kind {
	case infraKindBroker:
		return "amqp"
	case infraKindPostgres, infraKindCache:
		return "tcp"
	case infraKindWorkflow:
		return "grpc"
	case infraKindObjectStorage, infraKindExternal, infraKindMetrics:
		return "http-json"
	default:
		return ""
	}
}

// transportForInfraKind — транспорт, выводимый из вида инфраструктурного узла:
// брокер — асинхронный, остальные — синхронные.
func transportForInfraKind(kind string) string {
	if kind == infraKindBroker {
		return "async"
	}
	return "sync"
}
