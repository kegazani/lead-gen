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
