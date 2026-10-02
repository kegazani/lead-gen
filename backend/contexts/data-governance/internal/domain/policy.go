package domain

import "strings"

// Политики контекста data-governance — чистые функции без побочных эффектов: они
// не читают время, не обращаются к хранилищу и не эмитят события. Имена политик
// совпадают с architecture/domain-catalog.yaml.

// Handling — режим обращения с данными категории, выведенный политикой
// data-zone-policy.
type Handling string

const (
	// HandlingPlatformStorage — данные допустимы в платформенном хранилище.
	HandlingPlatformStorage Handling = "platform-storage"

	// HandlingSplitByAttribute — данные разделяются по атрибутам: green-часть в
	// платформе, red-часть на стороне клиента.
	HandlingSplitByAttribute Handling = "split-by-attribute"

	// HandlingClientSide — данные хранятся и обрабатываются только на стороне
	// клиента.
	HandlingClientSide Handling = "client-side"

	// HandlingAggregateOnly — данные допустимы только как обезличенная агрегация.
	HandlingAggregateOnly Handling = "aggregate-only"

	// HandlingReferenceOnly — локально хранится только ссылка на внешний источник.
	HandlingReferenceOnly Handling = "reference-only"
)

// ZoneRule — решение политики data-zone-policy: как обращаться с данными
// указанной цели и категории.
type ZoneRule struct {
	// Category — классифицированная категория данных.
	Category DataCategory

	// Handling — требуемый режим обращения.
	Handling Handling

	// SharedStorageAllowed — допустим ли вынос в общее хранилище.
	SharedStorageAllowed bool

	// Minimize — требуется ли минимизация передаваемых данных.
	Minimize bool

	// Reason — основание правила в терминах домена.
	Reason string
}

// DataZonePolicy — политика data-zone-policy (capability data-handling): по цели
// и категории обработки возвращает правило обращения с данными.
//
// Политика — единственное место, где классифицируется зона: потребители
// (lead-management, audience) применяют решение, но не воспроизводят правило
// локально (docs/domain-model.md, межконтекстные отношения).
func DataZonePolicy(purpose ProcessingPurpose) (ZoneRule, error) {
	if err := purpose.validate(); err != nil {
		return ZoneRule{}, err
	}
	switch purpose.Category() {
	case CategoryGreen:
		return ZoneRule{
			Category:             CategoryGreen,
			Handling:             HandlingPlatformStorage,
			SharedStorageAllowed: true,
			Minimize:             false,
			Reason:               "green: допустимо в платформенном хранилище при соблюдении tenant isolation, retention и audit",
		}, nil
	case CategoryMixed:
		return ZoneRule{
			Category:             CategoryMixed,
			Handling:             HandlingSplitByAttribute,
			SharedStorageAllowed: true,
			Minimize:             true,
			Reason:               "mixed: green-часть в платформе, red-часть подчиняется клиентским правилам",
		}, nil
	case CategoryRed:
		return ZoneRule{
			Category:             CategoryRed,
			Handling:             HandlingClientSide,
			SharedStorageAllowed: false,
			Minimize:             true,
			Reason:               "red: хранится в клиентском хранилище; вынос в общее хранилище, логи, события и аналитику запрещён",
		}, nil
	case CategoryAnalytics:
		return ZoneRule{
			Category:             CategoryAnalytics,
			Handling:             HandlingAggregateOnly,
			SharedStorageAllowed: true,
			Minimize:             true,
			Reason:               "analytics: только обезличенные/агрегированные производные; восстановление operational state запрещено",
		}, nil
	case CategoryExternal:
		return ZoneRule{
			Category:             CategoryExternal,
			Handling:             HandlingReferenceOnly,
			SharedStorageAllowed: true,
			Minimize:             true,
			Reason:               "external: внешняя система остаётся system of record, локально — ссылка и состояние синхронизации",
		}, nil
	default:
		return ZoneRule{}, purpose.validate()
	}
}

// SuppressionAction — вид действия, к которому применяется политика
// suppression-policy.
type SuppressionAction string

const (
	// ActionCampaign — действие исходящей кампании.
	ActionCampaign SuppressionAction = "campaign"

	// ActionOperatorDecision — положительное решение оператора.
	ActionOperatorDecision SuppressionAction = "operator-decision"

	// ActionProvider — действие, предложенное внешним поставщиком.
	ActionProvider SuppressionAction = "provider"
)

// SuppressionDecision — решение политики suppression-policy.
type SuppressionDecision struct {
	// Blocked — действие блокируется стоп-листом.
	Blocked bool

	// Reason — основание решения в терминах домена.
	Reason string
}

// SuppressionPolicy — политика suppression-policy (capability
// contact-suppression): активная запись стоп-листа имеет приоритет над
// кампанией, решением оператора и поставщиком (инвариант I2).
//
// Политика не различает источник действия: при активной записи любое действие
// блокируется, а вид действия лишь уточняет причину отказа.
func SuppressionPolicy(suppressed bool, action SuppressionAction) SuppressionDecision {
	if !suppressed {
		return SuppressionDecision{Blocked: false, Reason: "активная запись стоп-листа отсутствует"}
	}
	kind := strings.TrimSpace(string(action))
	if kind == "" {
		kind = "любым источником"
	}
	return SuppressionDecision{
		Blocked: true,
		Reason:  "активная запись стоп-листа имеет приоритет над " + kind,
	}
}
