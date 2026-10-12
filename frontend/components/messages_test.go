package components

import (
	"regexp"
	"strings"
	"testing"
)

// TestMessagesResolveEveryDeclaredKey проверяет, что каждый объявленный ключ
// каталога имеет непустое сообщение и не маскируется собственным ключом.
func TestMessagesResolveEveryDeclaredKey(t *testing.T) {
	messages := DefaultMessages()
	if messages.Locale() != LocaleRu {
		t.Fatalf("локаль по умолчанию = %q, ожидалась %q", messages.Locale(), LocaleRu)
	}
	for key, value := range ruCatalog {
		if strings.TrimSpace(value) == "" {
			t.Errorf("ключ %q имеет пустое сообщение", key)
		}
		if got := messages.Text(key); got != value {
			t.Errorf("Text(%q) = %q, ожидалось %q", key, got, value)
		}
	}
}

// TestMessagesUnknownKeyIsVisible проверяет, что неизвестный ключ возвращается
// самим ключом: пропущенное сообщение видно в разметке, а не маскируется пустой
// строкой.
func TestMessagesUnknownKeyIsVisible(t *testing.T) {
	key := MessageKey("missing.key")
	if got := DefaultMessages().Text(key); got != string(key) {
		t.Fatalf("Text(missing) = %q, ожидался сам ключ", got)
	}
	if DefaultMessages().Has(key) {
		t.Fatalf("Has(missing) = true, ожидалось false")
	}
}

// TestMessagesNilFallsBackToDefault проверяет, что nil-каталог безопасен и
// откатывается к локали по умолчанию.
func TestMessagesNilFallsBackToDefault(t *testing.T) {
	var messages *Messages
	if got := messages.Text(msgNavLabel); got != ruCatalog[msgNavLabel] {
		t.Fatalf("nil-каталог вернул %q, ожидалось значение по умолчанию", got)
	}
}

// TestTemplateTextKeysAreDeclared проверяет, что каждый ключ, на который
// ссылаются шаблоны через функцию text, объявлен в каталоге: единый каталог и
// шаблоны не расходятся.
func TestTemplateTextKeysAreDeclared(t *testing.T) {
	pattern := regexp.MustCompile(`text\s+"([^"]+)"`)
	messages := DefaultMessages()
	for _, file := range templateNames(t) {
		content := readEmbedded(t, "templates/"+file)
		for _, match := range pattern.FindAllStringSubmatch(content, -1) {
			key := MessageKey(match[1])
			if !messages.Has(key) {
				t.Errorf("шаблон %s ссылается на незаданный ключ %q", file, key)
			}
		}
	}
}

// TestConsoleNavMessagesUsesCatalog проверяет, что подписи разделов берутся из
// каталога, а не из разметки.
func TestConsoleNavMessagesUsesCatalog(t *testing.T) {
	nav := ConsoleNavMessages(DefaultMessages(), nil, ConsoleSectionLeads)
	if len(nav.Items) == 0 {
		t.Fatal("навигация пуста")
	}
	if nav.Items[0].Label != ruCatalog[msgSectionRequests] {
		t.Fatalf("подпись первого раздела = %q, ожидалась из каталога", nav.Items[0].Label)
	}
}

// TestWithMessagesMergesConsumerKeys проверяет, что потребитель может дополнить
// каталог своими текстами: базовые ключи сохраняются, новые появляются, а
// совпадающие перекрываются.
func TestWithMessagesMergesConsumerKeys(t *testing.T) {
	custom := MessageKey("screen.title")
	messages := WithMessages(DefaultMessages(), map[MessageKey]string{
		custom:             "Экран",
		msgSectionRequests: "Заявки клиента",
	})
	if got := messages.Text(custom); got != "Экран" {
		t.Fatalf("Text(custom) = %q", got)
	}
	if got := messages.Text(msgSectionLeads); got != ruCatalog[msgSectionLeads] {
		t.Fatalf("базовый ключ потерян: %q", got)
	}
	if got := messages.Text(msgSectionRequests); got != "Заявки клиента" {
		t.Fatalf("совпадающий ключ не перекрыт: %q", got)
	}
}

// TestMessagesLabel проверяет, что машинный код получает читаемую подпись по
// каталогу независимо от регистра и разделителей, а неизвестный код остаётся
// видимым как есть (change console-liquid-glass).
func TestMessagesLabel(t *testing.T) {
	messages := WithMessages(DefaultMessages(), map[MessageKey]string{
		"demo.phase.awaiting-approval": "Ждёт решения",
		"demo.phase.not-warmed":        "Не прогрет",
		"demo.execution.running":       "Выполняется",
	})
	cases := []struct{ prefix, code, want string }{
		{"demo.phase", "awaiting-approval", "Ждёт решения"},
		{"demo.phase", "not warmed", "Не прогрет"},
		{"demo.execution", "RUNNING", "Выполняется"},
		{"demo.phase", "unknown-phase", "unknown-phase"},
		{"demo.phase", "", ""},
	}
	for _, c := range cases {
		if got := messages.Label(c.prefix, c.code); got != c.want {
			t.Errorf("Label(%q, %q) = %q, ожидалось %q", c.prefix, c.code, got, c.want)
		}
	}
}
