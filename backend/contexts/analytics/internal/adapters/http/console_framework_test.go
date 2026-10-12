package httpapi

import (
	"io/fs"
	"regexp"
	"testing"

	"lead-gen/frontend/components"
)

// templateCommentRe — комментарий шаблона. Комментарий не попадает в вывод, поэтому
// при проверке текста шаблонов он снимается: критерий задачи 7.1 — про строковые
// литералы пользовательского текста в разметке, а не про пояснения.
var templateCommentRe = regexp.MustCompile(`(?s)\{\{/\*.*?\*/\}\}`)

// cyrillicRe — кириллическая последовательность. Имена ключей, классов и атрибутов
// машинные (ASCII), поэтому кириллица в шаблоне — признак вынесенного мимо каталога
// текста.
var cyrillicRe = regexp.MustCompile(`[\x{0400}-\x{04FF}\x{0500}-\x{052F}]+`)

// textKeyRe — ссылка шаблона на ключ каталога через функцию text.
var textKeyRe = regexp.MustCompile(`text "([^"]+)"`)

// TestConsoleTemplatesContainNoUserTextLiterals проверяет критерий задачи 7.1: в
// шаблонах консоли аналитики нет кириллицы — весь пользовательский текст приходит
// из каталога сообщений через функцию text.
func TestConsoleTemplatesContainNoUserTextLiterals(t *testing.T) {
	for _, content := range consoleTemplateContents(t) {
		withoutComments := templateCommentRe.ReplaceAllString(content, "")
		if match := cyrillicRe.FindString(withoutComments); match != "" {
			t.Errorf("шаблон консоли содержит пользовательский текст %q", match)
		}
	}
}

// TestConsoleMessagesCoverTemplates проверяет, что каталог сообщений покрывает
// каждый ключ, на который ссылаются шаблоны: неизвестный ключ вернул бы сам ключ и
// просочился бы в интерфейс.
func TestConsoleMessagesCoverTemplates(t *testing.T) {
	messages := consoleMessages()
	for _, content := range consoleTemplateContents(t) {
		for _, match := range textKeyRe.FindAllStringSubmatch(content, -1) {
			if !messages.Has(components.MessageKey(match[1])) {
				t.Errorf("шаблон ссылается на ключ %q без значения в каталоге", match[1])
			}
		}
	}
}

// consoleTemplateContents возвращает содержимое всех встроенных шаблонов консоли.
func consoleTemplateContents(t *testing.T) []string {
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
