package components

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// renderLayout собирает каркас с одной тестовой страницей и возвращает разметку.
func renderLayout(t *testing.T, sidebar Nav) string {
	t.Helper()
	pages := fstest.MapFS{
		"page.html": &fstest.MapFile{Data: []byte(`{{define "content"}}<h1>ok</h1>{{end}}`)},
	}
	set, err := NewWithMessages(DefaultMessages(), pages, "page.html")
	if err != nil {
		t.Fatalf("сборка страниц: %v", err)
	}
	var buffer bytes.Buffer
	if err := set.Render(&buffer, "page.html", Page{Layout: Layout{Title: "Тест"}, Sidebar: sidebar}); err != nil {
		t.Fatalf("рендер каркаса: %v", err)
	}
	return buffer.String()
}

// TestLayoutRendersSubnav проверяет, что вторичные переходы раздела выводятся
// внутриэкранной панелью в области содержимого (change console-shell-navigation),
// а не в общем сайдбаре.
func TestLayoutRendersSubnav(t *testing.T) {
	pages := fstest.MapFS{
		"page.html": &fstest.MapFile{Data: []byte(`{{define "content"}}<h1>ok</h1>{{end}}`)},
	}
	set, err := NewWithMessages(DefaultMessages(), pages, "page.html")
	if err != nil {
		t.Fatalf("сборка страниц: %v", err)
	}
	var buffer bytes.Buffer
	page := Page{
		Layout: Layout{Title: "Тест"},
		Subnav: &Tabs{Items: []Tab{
			{Key: "cards", Label: "Карточки", Href: "/cards"},
			{Key: "leads", Label: "Лиды", Href: "/leads"},
		}, Active: "cards"},
	}
	if err := set.Render(&buffer, "page.html", page); err != nil {
		t.Fatalf("рендер каркаса: %v", err)
	}
	markup := buffer.String()
	if !strings.Contains(markup, `<nav class="tabs">`) || !strings.Contains(markup, `href="/cards"`) {
		t.Fatalf("каркас не вывел внутриэкранную панель:\n%s", markup)
	}
}

// TestLayoutSkipLinkAndLandmark проверяет критерий задачи 7.3 на уровне каркаса:
// клавиатурный обход начинается со скип-ссылки, основное содержимое имеет
// landmark-цель.
func TestLayoutSkipLinkAndLandmark(t *testing.T) {
	markup := renderLayout(t, Nav{Items: []NavItem{{Label: "Главная", Href: "/"}}})

	for _, want := range []string{
		`<html lang="ru">`,
		`<a class="skip-link" href="#main-content">Перейти к содержимому</a>`,
		`<main class="page" id="main-content">`,
	} {
		if !strings.Contains(markup, want) {
			t.Errorf("каркас не содержит %q:\n%s", want, markup)
		}
	}
}

// TestNavLandmarkHasLabel проверяет, что навигация — именованный landmark: экран
// может иметь несколько <nav>, и без подписи они неразличимы ассистентам.
func TestNavLandmarkHasLabel(t *testing.T) {
	var buffer bytes.Buffer
	if err := RenderNav(&buffer, Nav{Items: []NavItem{{Label: "Главная", Href: "/"}}}); err != nil {
		t.Fatalf("RenderNav: %v", err)
	}
	if !strings.Contains(buffer.String(), `<nav class="sidebar" aria-label="Основная навигация">`) {
		t.Fatalf("навигация без подписи landmark:\n%s", buffer.String())
	}
}

// TestLiveRegionAttributes проверяет критерий задачи 7.3: область aria-live
// присутствует, вежлива и атомарна; текст пуст при первой отрисовке.
func TestLiveRegionAttributes(t *testing.T) {
	var buffer bytes.Buffer
	if err := RenderLiveRegion(&buffer, Live{ID: "live"}); err != nil {
		t.Fatalf("RenderLiveRegion: %v", err)
	}
	for _, want := range []string{
		`role="status"`,
		`aria-live="polite"`,
		`aria-atomic="true"`,
		`aria-label="Объявления"`,
		`data-live-region`,
		`id="live"`,
	} {
		if !strings.Contains(buffer.String(), want) {
			t.Errorf("область aria-live не содержит %q:\n%s", want, buffer.String())
		}
	}
}

// TestTemplatesAvoidPositiveTabindex проверяет, что ручное управление порядком
// табуляции не вводится: нативная последовательность без tabindex сохраняет
// фокус предсказуемым.
func TestTemplatesAvoidPositiveTabindex(t *testing.T) {
	for _, file := range templateNames(t) {
		if strings.Contains(readEmbedded(t, "templates/"+file), "tabindex") {
			t.Errorf("шаблон %s задаёт tabindex: порядок табуляции должен оставаться нативным", file)
		}
	}
}

// TestReducedMotionDisablesAnimations проверяет критерий задачи 6.6: при
// prefers-reduced-motion: reduce движение экрана операций отключается. Общий
// слой гасит анимации и переходы глобально и явно — для ленты агентов.
func TestReducedMotionDisablesAnimations(t *testing.T) {
	css := readCSS(t)
	for _, want := range []string{
		"@media (prefers-reduced-motion: reduce)",
		"animation: none !important",
		"transition: none !important",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("оформление не отключает движение: нет %q", want)
		}
	}
	cockpit := regexp.MustCompile(`(?s)@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{\s*\.cockpit-agent-row`)
	if !cockpit.MatchString(css) {
		t.Error("нет правила reduced-motion для ленты агентов экрана операций")
	}
}

// TestCockpitSVGUsesThemeTokens проверяет критерий задачи 6.6 о контрасте в обеих
// темах: цвет узлов и рёбер графа процессов задаётся токенами, а не чёрным по
// умолчанию, — иначе в тёмной теме текст узлов пропадает.
func TestCockpitSVGUsesThemeTokens(t *testing.T) {
	css := readCSS(t)
	checks := []struct {
		selector string
		property string
		token    string
	}{
		{".cockpit-node-label", "fill", "--ink"},
		{".cockpit-node-state", "fill", "--ink-soft"},
		{".cockpit-edge", "stroke", "--line-strong"},
		{".cockpit-node-box", "stroke", "--line-strong"},
	}
	for _, check := range checks {
		value := declarationValue(t, check.selector, cssBlockFor(t, css, check.selector), check.property)
		if value != "var("+check.token+")" {
			t.Errorf("%s: %s = %q, ожидался токен var(%s)", check.selector, check.property, value, check.token)
		}
	}
}
