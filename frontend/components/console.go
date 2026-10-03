package components

import "strings"

// ConsoleServiceKey — ключ сервиса-владельца раздела консоли. Совпадает с
// идентификатором сервиса развёртывания, но компонент по нему только сопоставляет
// раздел с базовым адресом: обращаться к API сервиса он не умеет.
type ConsoleServiceKey string

// Ключи сервисов, экраны которых входят в общую навигацию консоли.
const (
	ConsoleServiceRequests ConsoleServiceKey = "request-fulfillment"
	ConsoleServiceLeads    ConsoleServiceKey = "lead-management"
	ConsoleServiceReports  ConsoleServiceKey = "analytics"
)

// Ключи разделов общей навигации консоли. Совпадают с машинными именами,
// которыми владелец экрана помечает активный раздел.
const (
	ConsoleSectionRequests  = "requests"
	ConsoleSectionExecution = "execution"
	ConsoleSectionLeads     = "leads"
	ConsoleSectionReports   = "reports"
)

// ConsoleSection — раздел общей навигации консоли: машинный ключ, подпись и путь
// внутри сервиса-владельца (без базового адреса). Подпись — задача оператора, а
// не имя контекста: контексты остаются внутренней границей.
type ConsoleSection struct {
	Key   string
	Label string
	Path  string
	Owner ConsoleServiceKey
}

// ConsoleSections — канонический список разделов консоли и порядок их показа
// (web-frontend-architecture, design.md, Решение 5). Навигация строится вокруг
// работы оператора: «Заявки» и «Исполнение» ведут в request-fulfillment,
// «Лиды» — в lead-management, «Отчёты» — в analytics.
var ConsoleSections = []ConsoleSection{
	{Key: ConsoleSectionRequests, Label: "Заявки", Path: "/validation", Owner: ConsoleServiceRequests},
	{Key: ConsoleSectionExecution, Label: "Исполнение", Path: "/approvals", Owner: ConsoleServiceRequests},
	{Key: ConsoleSectionLeads, Label: "Лиды", Path: "/", Owner: ConsoleServiceLeads},
	{Key: ConsoleSectionReports, Label: "Отчёты", Path: "/dashboard", Owner: ConsoleServiceReports},
}

// ConsoleBases — базовые адреса сервисов консоли по ключу сервиса, задаваемые
// конфигурацией точки входа (флаги/ENV). Пустой адрес означает незнание адреса:
// пункт сервиса выводится относительным путём, а не выдуманным адресом.
type ConsoleBases map[ConsoleServiceKey]string

// ConsoleNav собирает общую навигацию консоли: канонические разделы
// (ConsoleSections) с выделенным активным. Пункты сервиса, которому принадлежит
// активный раздел, остаются относительными — переход внутри текущего origin;
// пункты остальных сервисов — абсолютные (base + path). Активный раздел
// отмечается флагом Active.
func ConsoleNav(bases ConsoleBases, active string) Nav {
	current := consoleOwner(active)
	items := make([]NavItem, 0, len(ConsoleSections))
	for _, section := range ConsoleSections {
		href := section.Path
		if section.Owner != current {
			href = consoleHref(bases[section.Owner], section.Path)
		}
		items = append(items, NavItem{
			Label:  section.Label,
			Href:   href,
			Active: section.Key == active,
		})
	}
	return Nav{Items: items}
}

// consoleOwner возвращает ключ сервиса-владельца раздела по ключу раздела; пустая
// строка — раздел неизвестен, и тогда все пункты строятся по базовым адресам.
func consoleOwner(section string) ConsoleServiceKey {
	for _, candidate := range ConsoleSections {
		if candidate.Key == section {
			return candidate.Owner
		}
	}
	return ""
}

// consoleHref склеивает базовый адрес сервиса с путём раздела. Пустая база даёт
// относительный путь: адрес сервиса не выдумывается.
func consoleHref(base, path string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(base), "/")
	if trimmed == "" {
		return path
	}
	if path == "/" {
		return trimmed + "/"
	}
	return trimmed + path
}
