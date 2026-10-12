// Package topology — техническая основа графа сервисной топологии: модель
// (узлы, рёбра, состояние, интенсивность, адрес), сборка графа из канонических
// источников (каталог доменной архитектуры, `deploy/services.yaml`,
// `deploy/infrastructure.yaml`), детерминированная раскладка и короткий кэш.
//
// Пакет не содержит доменных терминов и не импортирует `contexts/**`: граф —
// эксплуатационное представление уже объявленной проекции «контекст → сервис», а
// не второй владелец доменной модели. Состояния узлов и интенсивность рёбер
// сюда не «выдумываются»: статический граф всегда начинается с `unknown`, а
// измеренные значения накладывает слой наблюдения.
//
// Fail-closed: недоступный источник или ссылка на неизвестный узел не
// подменяются догадкой — узел остаётся `unknown`, а причина попадает в
// `Graph.Warnings`; сборка не падает.
package topology

import "time"

// NodeKind различает узел-сервис, инфраструктурный узел и узел, сослались на
// который, но объявленного определения не нашли (fail-closed). Узел вида
// unknown не владеет моделью и не трактуется как сервис.
type NodeKind string

const (
	// NodeService — узел-сервис, соответствующий сервису каталога.
	NodeService NodeKind = "service"
	// NodeInfrastructure — инфраструктурный узел (хранилище, брокер, кэш и т.д.).
	NodeInfrastructure NodeKind = "infrastructure"
	// NodeUnknown — узел, на который сослалась связь, но объявления нет.
	NodeUnknown NodeKind = "unknown"
)

// HealthState — состояние узла. Набор фиксирован; отсутствие данных выражается
// HealthUnknown, а не подстановкой HealthUp («нет данных» ≠ «ноль»).
type HealthState string

const (
	// HealthUp — узел отвечает штатно.
	HealthUp HealthState = "up"
	// HealthDegraded — узел работает с деградацией.
	HealthDegraded HealthState = "degraded"
	// HealthDown — узел недоступен (при наличии данных, подтверждающих это).
	HealthDown HealthState = "down"
	// HealthUnknown — данных нет. Статический граф всегда начинается с этого
	// состояния.
	HealthUnknown HealthState = "unknown"
)

// Health — состояние узла: значение, момент и пояснение. Пояснение отделяет
// «нет ряда» от «ошибка транспорта», не подменяя одно другим.
type Health struct {
	State  HealthState
	Since  time.Time
	Detail string
}

// IntensityState — категория интенсивности ребра.
type IntensityState string

const (
	// IntensityUnknown — данных по выбранной метрике нет (не ноль).
	IntensityUnknown IntensityState = "unknown"
	// IntensityNormal — интенсивность в пределах обычного режима.
	IntensityNormal IntensityState = "normal"
	// IntensityHot — интенсивность выше объявленного порога.
	IntensityHot IntensityState = "hot"
)

// Intensity — производная интенсивности ребра от сигналов наблюдения. Available
// отделяет «измерения нет» от честного нуля: недоступные данные дают
// IntensityUnknown, а не нулевую толщину.
type Intensity struct {
	Available  bool
	RPS        float64
	ErrorRatio float64
	Latency    float64
	State      IntensityState
}

// Endpoint — внутренний адрес узла. Address хранит объявленное значение
// (host:port или URL), Host и Port — разобранные части, когда адрес их содержит.
// Наружу адреса не публикуются и в логах не печатаются целиком.
type Endpoint struct {
	Address string
	Host    string
	Port    string
}

// Node — вершина графа. Для сервиса ID равен `service` каталога; для
// инфраструктуры — идентификатор из `deploy/infrastructure.yaml`.
type Node struct {
	ID        string
	Kind      NodeKind
	Label     string
	Zone      string
	Roles     []string
	Groups    []string
	Contracts []string
	Addresses []Endpoint
	Health    Health
	X         float64
	Y         float64
	Meta      map[string]string
}

// PrimaryGroup — группа deploy, в колонке которой детерминированная раскладка
// размещает узел: лексикографически первая из его групп. Выбор не зависит от
// порядка объявления в источнике и потому не ломает детерминизм. Пусто — у узла
// без групп (инфраструктура, unknown или сервис вне `deploy/services.yaml`).
func (n Node) PrimaryGroup() string {
	if len(n.Groups) == 0 {
		return ""
	}
	return n.Groups[0]
}

// Edge — ребро графа. Рёбра каталога сохраняют id связи; выведенные рёбра
// «сервис → инфраструктура» получают префикс DerivedEdgePrefix и не дублируют
// межконтекстные связи.
type Edge struct {
	ID          string
	From        string
	To          string
	Integration string
	Transport   string
	Mechanism   string
	Contract    string
	Intensity   Intensity
}

// DerivedEdgePrefix — префикс идентификатора выведенного ребра. Позволяет
// отличить выведенные рёбра от объявленных связей каталога.
const DerivedEdgePrefix = "derived:"

// Graph — модель графа. Порядок узлов детерминирован: сервисы (по ID),
// инфраструктура (по ID), затем unknown (по ID); рёбра отсортированы по ID.
type Graph struct {
	Nodes       []Node
	Edges       []Edge
	GeneratedAt time.Time
	Warnings    []string
}
