package httpapi_test

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// templateCommentRe — комментарий шаблона (в вывод не попадает, поэтому снимается).
var templateCommentRe = regexp.MustCompile(`(?s)\{\{/\*.*?\*/\}\}`)

// cyrillicRe — кириллическая последовательность в шаблоне: признак вынесенного мимо
// каталога текста.
var cyrillicRe = regexp.MustCompile(`[\x{0400}-\x{04FF}\x{0500}-\x{052F}]+`)

// TestConsoleTemplatesContainNoUserTextLiterals проверяет критерий задачи 7.1: в
// шаблонах экранов заявок нет пользовательского текста — он приходит из каталога.
func TestConsoleTemplatesContainNoUserTextLiterals(t *testing.T) {
	for _, entry := range templateEntries(t) {
		data, err := os.ReadFile(filepath.Join("templates", entry))
		if err != nil {
			t.Fatalf("чтение шаблона %s: %v", entry, err)
		}
		withoutComments := templateCommentRe.ReplaceAllString(string(data), "")
		if match := cyrillicRe.FindString(withoutComments); match != "" {
			t.Errorf("шаблон %s содержит пользовательский текст %q", entry, match)
		}
	}
}

// templateEntries возвращает имена встроенных шаблонов экранов заявок.
func templateEntries(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("templates")
	if err != nil {
		t.Fatalf("чтение каталога шаблонов: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	return names
}
