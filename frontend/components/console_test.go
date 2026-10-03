package components

import (
	"bytes"
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
	}

	nav := ConsoleNav(bases, ConsoleSectionLeads)

	want := []NavItem{
		{Label: "Заявки", Href: "http://localhost:8081/validation"},
		{Label: "Исполнение", Href: "http://localhost:8081/approvals"},
		{Label: "Лиды", Href: "/", Active: true},
		{Label: "Отчёты", Href: "http://localhost:8083/dashboard"},
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
	}

	nav := ConsoleNav(bases, "unknown")

	want := []NavItem{
		{Label: "Заявки", Href: "http://requests:8080/validation"},
		{Label: "Исполнение", Href: "http://requests:8080/approvals"},
		{Label: "Лиды", Href: "http://leads:8080/"},
		{Label: "Отчёты", Href: "http://reports:8080/dashboard"},
	}
	assertNavItems(t, nav.Items, want)
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

// TestRenderNavGroups проверяет разметку вторичной группы под общим разделом.
func TestRenderNavGroups(t *testing.T) {
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
	want := `<nav class="sidebar">
  <ul class="sidebar-list">
    <li><a href="/validation">Заявки</a></li>
    <li><a href="/" class="is-active" aria-current="page">Лиды</a></li>
  </ul>
  <p class="sidebar-group-label">Экраны лидов</p>
  <ul class="sidebar-list sidebar-list-group">
    <li><a href="/" class="is-active" aria-current="page">Главная</a></li>
    <li><a href="/cards">Карточки</a></li>
  </ul>
  <button class="theme-toggle" id="theme-toggle" type="button" aria-label="Тема оформления">
    <span class="theme-toggle-label">Тема</span>
    <b class="theme-toggle-value" data-theme-name>Как в системе</b>
  </button>
</nav>`
	if got != want {
		t.Fatalf("разметка групп навигации:\n got: %q\nwant: %q", got, want)
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
