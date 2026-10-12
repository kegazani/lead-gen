package httpapi

import (
	"io/fs"
	"regexp"
	"testing"

	"lead-gen/frontend/components"
)

// templateCommentRe — комментарий шаблона (в вывод не попадает, поэтому при
// проверке текста снимается).
var templateCommentRe = regexp.MustCompile(`(?s)\{\{/\*.*?\*/\}\}`)

// cyrillicRe — кириллическая последовательность в шаблоне: признак вынесенного
// мимо каталога текста.
var cyrillicRe = regexp.MustCompile(`[\x{0400}-\x{04FF}\x{0500}-\x{052F}]+`)

// textKeyRe — ссылка шаблона на ключ каталога через функцию text.
var textKeyRe = regexp.MustCompile(`text "([^"]+)"`)

// TestConsoleTemplatesContainNoUserTextLiterals проверяет критерий задачи 7.1: в
// шаблонах консоли рассылок нет пользовательского текста — он приходит из каталога.
func TestConsoleTemplatesContainNoUserTextLiterals(t *testing.T) {
	for _, content := range outreachTemplateContents(t) {
		withoutComments := templateCommentRe.ReplaceAllString(content, "")
		if match := cyrillicRe.FindString(withoutComments); match != "" {
			t.Errorf("шаблон консоли содержит пользовательский текст %q", match)
		}
	}
}

// TestConsoleMessagesCoverTemplates проверяет, что каталог покрывает каждый ключ
// шаблонов: неизвестный ключ вернул бы сам ключ и просочился бы в интерфейс.
func TestConsoleMessagesCoverTemplates(t *testing.T) {
	messages := consoleMessages()
	for _, content := range outreachTemplateContents(t) {
		for _, match := range textKeyRe.FindAllStringSubmatch(content, -1) {
			if !messages.Has(components.MessageKey(match[1])) {
				t.Errorf("шаблон ссылается на ключ %q без значения в каталоге", match[1])
			}
		}
	}
}

// outreachTemplateContents возвращает содержимое встроенных шаблонов консоли.
func outreachTemplateContents(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(templateFS, "templates")
	if err != nil {
		t.Fatalf("чтение каталога шаблонов: %v", err)
	}
	contents := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(templateFS, "templates/"+entry.Name())
		if err != nil {
			t.Fatalf("чтение шаблона %s: %v", entry.Name(), err)
		}
		contents = append(contents, string(data))
	}
	return contents
}
