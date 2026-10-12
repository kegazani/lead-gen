package components

import (
	"io/fs"
	"strings"
	"testing"
)

// templateNames возвращает имена встроенных шаблонов компонентов.
func templateNames(t *testing.T) []string {
	t.Helper()
	entries, err := fs.ReadDir(baseFS, "templates")
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

// readEmbedded читает встроенный файл и падает, если он недоступен.
func readEmbedded(t *testing.T, path string) string {
	t.Helper()
	data, err := fs.ReadFile(baseFS, path)
	if err != nil {
		t.Fatalf("чтение встроенного файла %s: %v", path, err)
	}
	return string(data)
}

// TestTemplatesContainNoUserTextLiterals проверяет критерий задачи 7.1: в
// шаблонах компонентов нет строковых литералов пользовательского текста — весь
// текст приходит из каталога сообщений через функцию text, поэтому поиск по
// кириллице в шаблонах не находит совпадений.
func TestTemplatesContainNoUserTextLiterals(t *testing.T) {
	for _, file := range templateNames(t) {
		content := readEmbedded(t, "templates/"+file)
		if position, found := firstCyrillic(content); found {
			t.Errorf("шаблон %s содержит пользовательский текст на позиции %d: %q",
				file, position, excerpt(content, position))
		}
	}
}

// firstCyrillic возвращает позицию и признак первой кириллической буквы. Имена
// ключей, классов и атрибутов машинные (ASCII), поэтому кириллица в шаблоне —
// признак вынесенного мимо каталога текста.
func firstCyrillic(content string) (int, bool) {
	for index, r := range content {
		if isCyrillic(r) {
			return index, true
		}
	}
	return 0, false
}

// isCyrillic сообщает, относится ли руна к кириллическим блокам.
func isCyrillic(r rune) bool {
	return (r >= 0x0400 && r <= 0x04FF) || (r >= 0x0500 && r <= 0x052F)
}

// excerpt возвращает короткий фрагмент вокруг позиции для сообщения об ошибке.
func excerpt(content string, position int) string {
	start := position - 20
	if start < 0 {
		start = 0
	}
	end := position + 20
	if end > len(content) {
		end = len(content)
	}
	return strings.TrimSpace(content[start:end])
}
