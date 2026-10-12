package components

import (
	"bytes"
	"strings"
	"testing"
)

// TestConsoleNavBuildsSharedSections проверяет общую навигацию: пункты текущего
// сервиса относительны, пункты остальных сервисов абсолютны, активный раздел
// выделен.
func TestConsoleNavBuildsSharedSections(t *testing.T) {
	bases := ConsoleBases{
		ConsoleServiceRequests: "http://localhost:8081",
		ConsoleServiceLeads:    "http://localhost:8080",
		ConsoleServiceReports:  "http://localhost:8083",
		ConsoleServiceOutreach: "http://localhost:8084",
		ConsoleServicePlatform: "http://localhost:8085",
	}

	nav := ConsoleNav(bases, ConsoleSectionLeads)

	want := []NavItem{
		{Label: "Заявки", Href: "http://localhost:8081/validation"},
		{Label: "Исполнение", Href: "http://localhost:8081/approvals"},
		{Label: "Лиды", Href: "/", Active: true},
		{Label: "Отчёты", Href: "http://localhost:8083/dashboard"},
		{Label: "Рассылки", Href: "http://localhost:8084/outreach/drafts"},
		{Label: "Тенант", Href: "http://localhost:8085/console/tenant"},
		{Label: "Пользователи", Href: "http://localhost:8085/console/users"},
		{Label: "Регистрации", Href: "http://localhost:8085/console/registrations"},
		{Label: "Интеграции", Href: "http://localhost:8085/console/integrations"},
		{Label: "Аудит", Href: "http://localhost:8085/console/audit"},
		{Label: "Топология", Href: "http://localhost:8085/platform/topology"},
		{Label: "Операции", Href: "http://localhost:8085/platform/operations"},
	}
	assertNavItems(t, nav.Items, want)
}

// TestConsoleNavWithoutBasesKeepsPathsRelative проверяет, что незнание адреса
// сервиса не выдумывает адрес: пункт остаётся относительным путём.
func TestConsoleNavWithoutBasesKeepsPathsRelative(t *testing.T) {
	nav := ConsoleNav(nil, ConsoleSectionRequests)

	want := []NavItem{
		{Label: "Заявки", Href: "/validation", Active: true},
		{Label: "Исполнение", Href: "/approvals"},
		{Label: "Лиды", Href: "/"},
		{Label: "Отчёты", Href: "/dashboard"},
		{Label: "Рассылки", Href: "/outreach/drafts"},
		{Label: "Тенант", Href: "/console/tenant"},
		{Label: "Пользователи", Href: "/console/users"},
		{Label: "Регистрации", Href: "/console/registrations"},
		{Label: "Интеграции", Href: "/console/integrations"},
		{Label: "Аудит", Href: "/console/audit"},
		{Label: "Топология", Href: "/platform/topology"},
		{Label: "Операции", Href: "/platform/operations"},
	}
	assertNavItems(t, nav.Items, want)
}

// TestConsoleNavUnknownSectionKeepsEverySectionAbsolute проверяет, что при
// неизвестном активном разделе текущий сервис не определяется и все настроенные
// адреса абсолютны.
func TestConsoleNavUnknownSectionKeepsEverySectionAbsolute(t *testing.T) {
	bases := ConsoleBases{
		ConsoleServiceRequests: "http://requests:8080/",
		ConsoleServiceLeads:    "http://leads:8080",
		ConsoleServiceReports:  "http://reports:8080",
		ConsoleServiceOutreach: "http://outreach:8080",
		ConsoleServicePlatform: "http://platform:8080",
	}

	nav := ConsoleNav(bases, "unknown")

	want := []NavItem{
		{Label: "Заявки", Href: "http://requests:8080/validation"},
		{Label: "Исполнение", Href: "http://requests:8080/approvals"},
		{Label: "Лиды", Href: "http://leads:8080/"},
		{Label: "Отчёты", Href: "http://reports:8080/dashboard"},
		{Label: "Рассылки", Href: "http://outreach:8080/outreach/drafts"},
		{Label: "Тенант", Href: "http://platform:8080/console/tenant"},
		{Label: "Пользователи", Href: "http://platform:8080/console/users"},
		{Label: "Регистрации", Href: "http://platform:8080/console/registrations"},
		{Label: "Интеграции", Href: "http://platform:8080/console/integrations"},
		{Label: "Аудит", Href: "http://platform:8080/console/audit"},
		{Label: "Топология", Href: "http://platform:8080/platform/topology"},
		{Label: "Операции", Href: "http://platform:8080/platform/operations"},
	}
	assertNavItems(t, nav.Items, want)
}

// TestConsoleNavOutreachSection проверяет раздел «Рассылки»: он есть в общем слое,
// открывается на очереди черновиков и активен при активном разделе рассылок.
func TestConsoleNavOutreachSection(t *testing.T) {
	bases := ConsoleBases{ConsoleServiceOutreach: "http://outreach:8080"}

	nav := ConsoleNav(bases, ConsoleSectionOutreach)

	var outreach NavItem
	found := false
	for _, item := range nav.Items {
		if item.Label == "Рассылки" {
			outreach, found = item, true
		}
	}
	if !found {
		t.Fatalf("в навигации нет раздела «Рассылки»: %+v", nav.Items)
	}
	// Активный раздел принадлежит сервису-владельцу, поэтому ссылка относительна.
	if outreach.Href != "/outreach/drafts" {
		t.Fatalf("адрес раздела «Рассылки» = %q, ожидался /outreach/drafts", outreach.Href)
	}
	if !outreach.Active {
		t.Fatalf("раздел «Рассылки» не отмечен активным: %+v", outreach)
	}
}

// TestConsoleNavShowsBothGroupsOnEveryConsole проверяет двустороннюю единую
// навигацию: на каждой консоли — операторской и платформенной — видны обе группы
// разделов в каноническом порядке, без дублей, и ровно один раздел активен.
func TestConsoleNavShowsBothGroupsOnEveryConsole(t *testing.T) {
	bases := ConsoleBases{
		ConsoleServiceRequests: "http://requests:8080",
		ConsoleServiceLeads:    "http://leads:8080",
		ConsoleServiceReports:  "http://reports:8080",
		ConsoleServiceOutreach: "http://outreach:8080",
		ConsoleServicePlatform: "http://platform:8080",
	}
	operatorLabels := []string{"Заявки", "Исполнение", "Лиды", "Отчёты", "Рассылки"}
	platformLabels := []string{"Тенант", "Пользователи", "Регистрации", "Интеграции", "Аудит", "Топология", "Операции"}
	allLabels := append(append([]string{}, operatorLabels...), platformLabels...)

	// Активные разделы покрывают обе группы: операторские по ключу, платформенные
	// по пути (так их передаёт платформенная консоль).
	actives := []string{
		ConsoleSectionRequests, ConsoleSectionExecution, ConsoleSectionLeads,
		ConsoleSectionReports, ConsoleSectionOutreach,
		"/console/tenant", "/console/users", "/console/registrations",
		"/console/integrations", "/console/audit", "/platform/topology", "/platform/operations",
	}
	for _, active := range actives {
		nav := ConsoleNav(bases, active)
		if len(nav.Items) != len(allLabels) {
			t.Fatalf("на консоли %q разделов %d, ожидалось %d: %+v", active, len(nav.Items), len(allLabels), nav.Items)
		}
		for i, want := range allLabels {
			if nav.Items[i].Label != want {
				t.Fatalf("на консоли %q раздел %d = %q, ожидался %q", active, i, nav.Items[i].Label, want)
			}
		}
		activeCount := 0
		for _, item := range nav.Items {
			if item.Active {
				activeCount++
			}
		}
		if activeCount != 1 {
			t.Fatalf("на консоли %q активных разделов %d, ожидался 1: %+v", active, activeCount, nav.Items)
		}
	}
}

// TestConsoleNavMarksActiveByPath проверяет активность по пути раздела: общий слой
// отмечает активным раздел «Тенант», когда консоль передаёт путь /console/tenant,
// а не ключ, и его ссылка остаётся относительной (владелец — текущий сервис).
func TestConsoleNavMarksActiveByPath(t *testing.T) {
	bases := ConsoleBases{ConsoleServicePlatform: "http://platform:8080"}

	nav := ConsoleNav(bases, "/console/tenant")

	var tenant NavItem
	for _, item := range nav.Items {
		if item.Label == "Тенант" {
			tenant = item
		}
	}
	if !tenant.Active {
		t.Fatalf("раздел «Тенант» не отмечен активным по пути: %+v", nav.Items)
	}
	if tenant.Href != "/console/tenant" {
		t.Fatalf("адрес раздела «Тенант» = %q, ожидался относительный /console/tenant", tenant.Href)
	}
}

// TestConsoleNavWithContextCarriesTenantAndActor проверяет перенос контекста
// (задача 3.1): ссылки общей навигации несут активные tenant_id и actor, поэтому
// выбранный тенант не теряется при переходе между разделами консолей разных
// сервисов. Относительные и абсолютные ссылки получают одинаковый контекст.
func TestConsoleNavWithContextCarriesTenantAndActor(t *testing.T) {
	bases := ConsoleBases{
		ConsoleServiceRequests: "http://requests:8080",
		ConsoleServiceLeads:    "http://leads:8080",
		ConsoleServiceReports:  "http://reports:8080",
		ConsoleServiceOutreach: "http://outreach:8080",
		ConsoleServicePlatform: "http://platform:8080",
	}

	nav := ConsoleNavWithContext(bases, ConsoleSectionLeads, ConsoleContext{TenantID: "tenant-1", Actor: "user-1"})

	want := []NavItem{
		{Label: "Заявки", Href: "http://requests:8080/validation?actor=user-1&tenant_id=tenant-1"},
		{Label: "Исполнение", Href: "http://requests:8080/approvals?actor=user-1&tenant_id=tenant-1"},
		{Label: "Лиды", Href: "/?actor=user-1&tenant_id=tenant-1", Active: true},
		{Label: "Отчёты", Href: "http://reports:8080/dashboard?actor=user-1&tenant_id=tenant-1"},
		{Label: "Рассылки", Href: "http://outreach:8080/outreach/drafts?actor=user-1&tenant_id=tenant-1"},
		{Label: "Тенант", Href: "http://platform:8080/console/tenant?actor=user-1&tenant_id=tenant-1"},
		{Label: "Пользователи", Href: "http://platform:8080/console/users?actor=user-1&tenant_id=tenant-1"},
		{Label: "Регистрации", Href: "http://platform:8080/console/registrations?actor=user-1&tenant_id=tenant-1"},
		{Label: "Интеграции", Href: "http://platform:8080/console/integrations?actor=user-1&tenant_id=tenant-1"},
		{Label: "Аудит", Href: "http://platform:8080/console/audit?actor=user-1&tenant_id=tenant-1"},
		{Label: "Топология", Href: "http://platform:8080/platform/topology?actor=user-1&tenant_id=tenant-1"},
		{Label: "Операции", Href: "http://platform:8080/platform/operations?actor=user-1&tenant_id=tenant-1"},
	}
	assertNavItems(t, nav.Items, want)
}

// TestConsoleNavEmptyContextKeepsLinks проверяет обратную совместимость: пустой
// контекст не добавляет параметров, и ссылки остаются прежними.
func TestConsoleNavEmptyContextKeepsLinks(t *testing.T) {
	bases := ConsoleBases{
		ConsoleServiceRequests: "http://requests:8080",
		ConsoleServiceLeads:    "http://leads:8080",
	}

	withEmpty := ConsoleNavWithContext(bases, ConsoleSectionLeads, ConsoleContext{})
	withoutContext := ConsoleNav(bases, ConsoleSectionLeads)
	assertNavItems(t, withEmpty.Items, withoutContext.Items)

	for _, item := range withEmpty.Items {
		if strings.Contains(item.Href, "tenant_id") || strings.Contains(item.Href, "actor") {
			t.Fatalf("пустой контекст добавил параметры в ссылку %q", item.Href)
		}
	}
}

// TestConsoleHrefWithContextAppendsToExistingQuery проверяет, что контекст
// добавляется к адресу, уже несущему строку запроса, без потери прежних параметров.
func TestConsoleHrefWithContextAppendsToExistingQuery(t *testing.T) {
	got := ConsoleHrefWithContext("/cards?job=job-1", ConsoleContext{TenantID: "tenant-1", Actor: "user-1"})
	want := "/cards?job=job-1&actor=user-1&tenant_id=tenant-1"
	if got != want {
		t.Fatalf("адрес с прежними параметрами = %q, ожидался %q", got, want)
	}

	if bare := ConsoleHrefWithContext("/cards", ConsoleContext{}); bare != "/cards" {
		t.Fatalf("пустой контекст изменил адрес: %q", bare)
	}
}

// assertNavItems сравнивает пункты навигации по подписи, адресу и активности.
func assertNavItems(t *testing.T, got, want []NavItem) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("пунктов навигации %d, ожидалось %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("пункт %d: получено %+v, ожидалось %+v", i, got[i], want[i])
		}
	}
}

// TestRenderNavIgnoresGroups проверяет, что каркас НЕ выводит вторичные группы в
// сайдбаре: вторичные переходы раздела живут в Page.Subnav, а общий сайдбар
// остаётся одинаковым на всех консолях (change console-shell-navigation).
func TestRenderNavIgnoresGroups(t *testing.T) {
	nav := Nav{
		Items: []NavItem{
			{Label: "Заявки", Href: "/validation"},
			{Label: "Лиды", Href: "/", Active: true},
		},
		Groups: []NavGroup{{
			Label: "Экраны лидов",
			Items: []NavItem{
				{Label: "Главная", Href: "/", Active: true},
				{Label: "Карточки", Href: "/cards"},
			},
		}},
	}

	got := renderNav(t, nav)
	want := `<nav class="sidebar" aria-label="Основная навигация">
  <ul class="sidebar-list">
    <li><a href="/validation">Заявки</a></li>
    <li><a href="/" class="is-active" aria-current="page">Лиды</a></li>
  </ul>
  <button class="theme-toggle" id="theme-toggle" type="button"
    aria-label="Тема оформления"
    data-theme-label-system="Как в системе"
    data-theme-label-light="Светлая"
    data-theme-label-dark="Тёмная"
    data-theme-aria="Тема оформления: %s. Нажмите, чтобы сменить.">
    <span class="theme-toggle-label">Тема</span>
    <b class="theme-toggle-value" data-theme-name>Как в системе</b>
  </button>
</nav>`
	if got != want {
		t.Fatalf("разметка навигации с группами:\n got: %q\nwant: %q", got, want)
	}
}

// renderNav размечает навигацию в строку через общий компонент.
func renderNav(t *testing.T, nav Nav) string {
	t.Helper()
	var buffer bytes.Buffer
	if err := RenderNav(&buffer, nav); err != nil {
		t.Fatalf("RenderNav: %v", err)
	}
	return buffer.String()
}
