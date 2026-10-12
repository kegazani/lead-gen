package components

import (
	"io/fs"
	"strings"
	"testing"
)

// TestStaticFSServesConsoleStylesheet проверяет, что общее оформление встроено и
// доступно как файл console.css: каркас и раздача используют один источник.
func TestStaticFSServesConsoleStylesheet(t *testing.T) {
	data, err := fs.ReadFile(StaticFS(), "console.css")
	if err != nil {
		t.Fatalf("чтение оформления: %v", err)
	}
	if !strings.Contains(string(data), ".page") {
		t.Fatalf("оформление не содержит базовых правил:\n%s", data)
	}
}

// TestStaticFSServesCockpitScreenStyles проверяет критерий задачи 6.6: общий слой
// раздаёт оформление экрана операций — все классы cockpit-* и выделение среза
// определены в том же встроенном console.css, что отдаёт каркас (разметку
// экрана общий слой не трогает, только оформляет её классы).
func TestStaticFSServesCockpitScreenStyles(t *testing.T) {
	data, err := fs.ReadFile(StaticFS(), "console.css")
	if err != nil {
		t.Fatalf("чтение оформления: %v", err)
	}
	css := string(data)
	for _, selector := range []string{
		".cockpit-panels",
		".cockpit-panel",
		".cockpit-agents-list",
		".cockpit-agent-row",
		".cockpit-figure",
		".cockpit-graph",
		".cockpit-timeline",
		".cockpit-edge",
		".cockpit-node-box",
		".cockpit-node-label",
		".cockpit-node-state",
		".cockpit-dashboard",
		".table tbody tr.is-active",
	} {
		if !strings.Contains(css, selector) {
			t.Errorf("оформление экрана операций не содержит правило %q", selector)
		}
	}
}

// TestConsoleStylesheetsPath проверяет, что объявленные адреса таблиц стилей и
// скриптов совпадают с файлами в StaticFS: каркас и раздача не расходятся.
func TestConsoleStylesheetsPath(t *testing.T) {
	if len(ConsoleStylesheets) != 1 || ConsoleStylesheets[0] != "/static/console.css" {
		t.Fatalf("ConsoleStylesheets = %v", ConsoleStylesheets)
	}
	if _, err := fs.Stat(StaticFS(), "console.css"); err != nil {
		t.Fatalf("файл оформления недоступен: %v", err)
	}
	if len(ConsoleBootstrapScripts) != 1 || ConsoleBootstrapScripts[0] != "/static/theme.js" {
		t.Fatalf("ConsoleBootstrapScripts = %v", ConsoleBootstrapScripts)
	}
	if _, err := fs.Stat(StaticFS(), "theme.js"); err != nil {
		t.Fatalf("скрипт темы недоступен: %v", err)
	}
}

// TestConsoleScriptsServeFocusManagement проверяет, что скрипт управления фокусом
// и живыми объявлениями (задача 7.3) встроен и объявлен для подключения.
func TestConsoleScriptsServeFocusManagement(t *testing.T) {
	if len(ConsoleScripts) != 1 || ConsoleScripts[0] != "/static/console-a11y.js" {
		t.Fatalf("ConsoleScripts = %v", ConsoleScripts)
	}
	data, err := fs.ReadFile(StaticFS(), "console-a11y.js")
	if err != nil {
		t.Fatalf("скрипт консоли недоступен: %v", err)
	}
	for _, want := range []string{"htmx:afterSwap", "data-focus-after-swap", "data-live-announce"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("console-a11y.js не обрабатывает %q", want)
		}
	}
}
