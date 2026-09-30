package httpapi_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestViewsContainNoDomainRules проверяет, что разметка представления и клиентский
// JavaScript не содержат доменных правил: ни импорта домена, ни inline-скриптов,
// ни текста доменных ошибок (например, запрета необъявленного плейсхолдера).
// Домен и прикладной слой остаются единственным местом проверки инвариантов.
func TestViewsContainNoDomainRules(t *testing.T) {
	forbidden := []struct {
		needle string
		reason string
	}{
		{"internal/domain", "представление не должно ссылаться на доменный пакет"},
		{"не объявлен", "текст доменной ошибки не должен дублироваться в представлении"},
		{"fallback объявленной переменной", "текст доменной ошибки не должен дублироваться в представлении"},
	}

	for _, glob := range []string{
		filepath.Join("templates", "*.html"),
		filepath.Join("static", "console.js"),
	} {
		files, err := filepath.Glob(glob)
		if err != nil {
			t.Fatalf("glob %s: %v", glob, err)
		}
		if len(files) == 0 {
			t.Fatalf("не найдено файлов по шаблону %s", glob)
		}
		for _, file := range files {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("чтение %s: %v", file, err)
			}
			content := string(data)
			for _, rule := range forbidden {
				if strings.Contains(strings.ToLower(content), strings.ToLower(rule.needle)) {
					t.Fatalf("%s: %s (найдено %q)", file, rule.reason, rule.needle)
				}
			}
			// leads-export.html — автономная выгрузка: скачивается и открывается вне
			// консоли, где внешние /static не разрешаются, поэтому она намеренно несёт
			// встроенные стили и скрипт. Остальные шаблоны рендерятся под CSP консоли.
			if strings.HasSuffix(file, ".html") && filepath.Base(file) != "leads-export.html" && strings.Contains(content, "<script") {
				t.Fatalf("%s: разметка не должна содержать inline-скриптов", file)
			}
		}
	}
}
