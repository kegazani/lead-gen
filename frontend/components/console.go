package components

import (
	"net/url"
	"strings"
)

// ConsoleServiceKey — ключ сервиса-владельца раздела консоли. Совпадает с
// идентификатором сервиса развёртывания, но компонент по нему только сопоставляет
// раздел с базовым адресом: обращаться к API сервиса он не умеет.
type ConsoleServiceKey string

// Ключи сервисов, экраны которых входят в общую навигацию консоли.
const (
	ConsoleServiceRequests ConsoleServiceKey = "request-fulfillment"
	ConsoleServiceLeads    ConsoleServiceKey = "lead-management"
	ConsoleServiceReports  ConsoleServiceKey = "analytics"
	ConsoleServiceOutreach ConsoleServiceKey = "outreach-orchestration"
	ConsoleServicePlatform ConsoleServiceKey = "platform-governance"
)

// Ключи разделов общей навигации консоли. Совпадают с машинными именами,
// которыми владелец экрана помечает активный раздел.
const (
	ConsoleSectionRequests      = "requests"
	ConsoleSectionExecution     = "execution"
	ConsoleSectionLeads         = "leads"
	ConsoleSectionReports       = "reports"
	ConsoleSectionOutreach      = "outreach"
	ConsoleSectionTenant        = "tenant"
	ConsoleSectionUsers         = "users"
	ConsoleSectionRegistrations = "registrations"
	ConsoleSectionIntegrations  = "integrations"
	ConsoleSectionAudit         = "audit"
	ConsoleSectionTopology      = "topology"
	ConsoleSectionOperations    = "operations"
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
// «Лиды» — в lead-management, «Отчёты» — в analytics, «Рассылки» — в
// outreach-orchestration; платформенные разделы («Тенант», «Пользователи»,
// «Регистрации», «Интеграции», «Аудит», «Топология», «Операции») принадлежат
// platform-governance. Один и тот же список показывается на всех консолях —
// операторских и платформенной, поэтому сайдбар не сужается при переходах.
// Подписи берутся из каталога сообщений (Label — снимок локали по умолчанию для
// потребителей, которые читают поле напрямую).
var ConsoleSections = []ConsoleSection{
	{Key: ConsoleSectionRequests, Label: ruCatalog[msgSectionRequests], Path: "/validation", Owner: ConsoleServiceRequests},
	{Key: ConsoleSectionExecution, Label: ruCatalog[msgSectionExecution], Path: "/approvals", Owner: ConsoleServiceRequests},
	{Key: ConsoleSectionLeads, Label: ruCatalog[msgSectionLeads], Path: "/", Owner: ConsoleServiceLeads},
	{Key: ConsoleSectionReports, Label: ruCatalog[msgSectionReports], Path: "/dashboard", Owner: ConsoleServiceReports},
	{Key: ConsoleSectionOutreach, Label: ruCatalog[msgSectionOutreach], Path: "/outreach/drafts", Owner: ConsoleServiceOutreach},
	{Key: ConsoleSectionTenant, Label: ruCatalog[msgSectionTenant], Path: "/console/tenant", Owner: ConsoleServicePlatform},
	{Key: ConsoleSectionUsers, Label: ruCatalog[msgSectionUsers], Path: "/console/users", Owner: ConsoleServicePlatform},
	{Key: ConsoleSectionRegistrations, Label: ruCatalog[msgSectionRegistrations], Path: "/console/registrations", Owner: ConsoleServicePlatform},
	{Key: ConsoleSectionIntegrations, Label: ruCatalog[msgSectionIntegrations], Path: "/console/integrations", Owner: ConsoleServicePlatform},
	{Key: ConsoleSectionAudit, Label: ruCatalog[msgSectionAudit], Path: "/console/audit", Owner: ConsoleServicePlatform},
	{Key: ConsoleSectionTopology, Label: ruCatalog[msgSectionTopology], Path: "/platform/topology", Owner: ConsoleServicePlatform},
	{Key: ConsoleSectionOperations, Label: ruCatalog[msgSectionOperations], Path: "/platform/operations", Owner: ConsoleServicePlatform},
}

// ConsoleBases — базовые адреса сервисов консоли по ключу сервиса, задаваемые
// конфигурацией точки входа (флаги/ENV). Под единым origin (admin-proxy) адрес —
// префикс пути сервиса (например "/leads"), а не отдельный origin: ссылки
// навигации склеиваются в пределах одного хоста. Пустой адрес означает незнание
// адреса: пункт сервиса выводится относительным путём, а не выдуманным адресом.
type ConsoleBases map[ConsoleServiceKey]string

// ConsoleContext — контекст активного тенанта и актора, переносимый по ссылкам
// общей навигации консоли. Пустые значения означают, что контекст не задан:
// соответствующий параметр в ссылку не добавляется, и поведение остаётся прежним.
// Экспортирован, потому что потребители передают своё представление о текущем
// тенанте и акторе (из запроса/сессии), а общий слой лишь переносит их в ссылки.
type ConsoleContext struct {
	TenantID string
	Actor    string
}

// ConsoleNav собирает общую навигацию консоли без контекста тенанта: прежнее
// поведение. Канонические разделы (ConsoleSections) с выделенным активным; пункты
// сервиса, которому принадлежит активный раздел, остаются относительными, пункты
// остальных сервисов — абсолютные (base + path). Подписи — из каталога по умолчанию.
func ConsoleNav(bases ConsoleBases, active string) Nav {
	return ConsoleNavWithContext(bases, active, ConsoleContext{})
}

// ConsoleNavWithContext собирает общую навигацию консоли с контекстом активного
// тенанта и актора: ссылки разделов несут выбранный тенант, поэтому при переходе
// между консолями разных сервисов он не теряется. Пустой контекст даёт прежние
// ссылки без параметров.
func ConsoleNavWithContext(bases ConsoleBases, active string, ctx ConsoleContext) Nav {
	return ConsoleNavMessagesWithContext(DefaultMessages(), bases, active, ctx)
}

// ConsoleNavMessages собирает навигацию с подписями заданного каталога без
// контекста тенанта: прежнее поведение.
func ConsoleNavMessages(messages *Messages, bases ConsoleBases, active string) Nav {
	return ConsoleNavMessagesWithContext(messages, bases, active, ConsoleContext{})
}

// ConsoleNavMessagesWithContext собирает навигацию с подписями заданного каталога:
// локаль определяет текст пунктов, структура разделов остаётся общей. Контекст
// тенанта и актора подставляется в ссылки разделов; пустой контекст не меняет
// адреса.
func ConsoleNavMessagesWithContext(messages *Messages, bases ConsoleBases, active string, ctx ConsoleContext) Nav {
	if messages == nil {
		messages = DefaultMessages()
	}
	current := consoleOwner(active)
	items := make([]NavItem, 0, len(ConsoleSections))
	for _, section := range ConsoleSections {
		href := section.Path
		if section.Owner != current {
			href = consoleHref(bases[section.Owner], section.Path)
		}
		raw := href
		href = ConsoleHrefWithContext(href, ctx)
		items = append(items, NavItem{
			Label:  messages.Text(sectionLabelKey(section.Key)),
			Href:   href,
			Active: consoleSectionActive(section, raw, active),
		})
	}
	return Nav{Items: items}
}

// consoleSectionActive сообщает, открыт ли раздел. Активность отмечается и по
// ключу раздела (операторские консоли передают ключ), и по префиксу адреса
// (платформенная консоль передаёт путь раздела, а не ключ) — так один общий слой
// выделяет активный раздел в обеих группах консолей.
func consoleSectionActive(section ConsoleSection, href, active string) bool {
	if section.Key == active {
		return true
	}
	if active == "" {
		return false
	}
	return strings.HasPrefix(href, active) || strings.HasPrefix(active, href)
}

// ConsoleHrefWithContext добавляет к адресу непустые tenant_id и actor. Экспорт
// нужен потребителям, которые строят собственные пункты навигации (вторичные
// группы экранов сервиса): их ссылки тоже должны сохранять выбранный тенант.
// Параметры кодируются так же, как в консольных адресах platform-governance.
func ConsoleHrefWithContext(href string, ctx ConsoleContext) string {
	values := url.Values{}
	if tenant := strings.TrimSpace(ctx.TenantID); tenant != "" {
		values.Set("tenant_id", tenant)
	}
	if actor := strings.TrimSpace(ctx.Actor); actor != "" {
		values.Set("actor", actor)
	}
	encoded := values.Encode()
	if encoded == "" {
		return href
	}
	separator := "?"
	if strings.Contains(href, "?") {
		separator = "&"
	}
	return href + separator + encoded
}

// sectionLabelKey сопоставляет ключ раздела ключу каталога сообщений.
func sectionLabelKey(section string) MessageKey {
	switch section {
	case ConsoleSectionRequests:
		return msgSectionRequests
	case ConsoleSectionExecution:
		return msgSectionExecution
	case ConsoleSectionLeads:
		return msgSectionLeads
	case ConsoleSectionReports:
		return msgSectionReports
	case ConsoleSectionOutreach:
		return msgSectionOutreach
	case ConsoleSectionTenant:
		return msgSectionTenant
	case ConsoleSectionUsers:
		return msgSectionUsers
	case ConsoleSectionRegistrations:
		return msgSectionRegistrations
	case ConsoleSectionIntegrations:
		return msgSectionIntegrations
	case ConsoleSectionAudit:
		return msgSectionAudit
	case ConsoleSectionTopology:
		return msgSectionTopology
	case ConsoleSectionOperations:
		return msgSectionOperations
	default:
		// Неизвестный раздел не маскируется: каталог вернёт сам ключ.
		return MessageKey(section)
	}
}

// consoleOwner возвращает ключ сервиса-владельца раздела; сопоставляет и по ключу
// раздела (операторские консоли передают ключ), и по пути раздела (платформенная
// консоль передаёт путь). Пустая строка — раздел неизвестен, и тогда все пункты
// строятся по базовым адресам.
func consoleOwner(section string) ConsoleServiceKey {
	for _, candidate := range ConsoleSections {
		if candidate.Key == section || candidate.Path == section {
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
