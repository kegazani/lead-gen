package httpapi

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"lead-gen/frontend/components"
)

// templateKeyPattern извлекает ключи каталога из вызовов text "...".
var templateKeyPattern = regexp.MustCompile(`text\s+"([^"]+)"`)

// TestTemplateMessageKeysExist проверяет, что каждый ключ text "..." из шаблонов
// объявлен в каталоге контекста: опечатка не должна показываться оператору как
// машинный ключ вместо текста (срез 7, 7.1).
func TestTemplateMessageKeysExist(t *testing.T) {
	entries, err := os.ReadDir("templates")
	if err != nil {
		t.Fatalf("чтение каталога шаблонов: %v", err)
	}
	messages := consoleMessages()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("templates", entry.Name()))
		if err != nil {
			t.Fatalf("чтение шаблона %s: %v", entry.Name(), err)
		}
		for _, match := range templateKeyPattern.FindAllStringSubmatch(string(data), -1) {
			if !messages.Has(components.MessageKey(match[1])) {
				t.Errorf("шаблон %s ссылается на неизвестный ключ каталога %q", entry.Name(), match[1])
			}
		}
	}
}
