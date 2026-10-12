package httpapi

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/frontend/components"
)

// templateCommentRe — комментарий шаблона (в вывод не попадает, поэтому снимается).
var templateCommentRe = regexp.MustCompile(`(?s)\{\{/\*.*?\*/\}\}`)

// cyrillicRe — кириллическая последовательность в шаблоне: признак вынесенного мимо
// каталога текста.
var cyrillicRe = regexp.MustCompile(`[\x{0400}-\x{04FF}\x{0500}-\x{052F}]+`)

// textKeyRe — ссылка шаблона на ключ каталога через функцию text.
var textKeyRe = regexp.MustCompile(`text "([^"]+)"`)

// TestConsoleTemplatesContainNoUserTextLiterals проверяет критерий задачи 7.1: в
// шаблонах консоли лидов (включая автономную выгрузку) нет пользовательского
// текста — он приходит из каталога сообщений.
func TestConsoleTemplatesContainNoUserTextLiterals(t *testing.T) {
	for name, content := range leadTemplateContents(t) {
		withoutComments := templateCommentRe.ReplaceAllString(content, "")
		if match := cyrillicRe.FindString(withoutComments); match != "" {
			t.Errorf("шаблон %s содержит пользовательский текст %q", name, match)
		}
	}
}

// TestConsoleMessagesCoverTemplates проверяет, что каталог покрывает каждый ключ
// шаблонов: неизвестный ключ вернул бы сам ключ и просочился бы в интерфейс.
func TestConsoleMessagesCoverTemplates(t *testing.T) {
	messages := consoleMessages()
	for name, content := range leadTemplateContents(t) {
		for _, match := range textKeyRe.FindAllStringSubmatch(content, -1) {
			if !messages.Has(components.MessageKey(match[1])) {
				t.Errorf("шаблон %s ссылается на ключ %q без значения в каталоге", name, match[1])
			}
		}
	}
}

// TestVerificationDictionaryBinding проверяет критерий задачи 7.2: показ исхода
// верификации сведён к общему словарю, статусы различимы, а запрещённой
// формулировки «адрес существует» нет ни в каталоге, ни в шаблонах.
func TestVerificationDictionaryBinding(t *testing.T) {
	labels := map[components.AddressStatus]string{}
	for _, status := range []domain.VerificationStatus{
		domain.StatusDeliverable, domain.StatusRisky, domain.StatusUnknown, domain.StatusInvalid,
		domain.StatusDisposable, domain.StatusRole, domain.StatusAbuse,
	} {
		mapped := addressStatus(status)
		if !mapped.Valid() {
			t.Errorf("статус %q сведён к недействительному словарю %q", status, mapped)
			continue
		}
		labels[mapped] = mapped.Label()
	}
	// Все четыре исхода словаря достижимы и различимы.
	for _, want := range []components.AddressStatus{
		components.AddressDeliverable, components.AddressRisky, components.AddressUnknown, components.AddressInvalid,
	} {
		if _, ok := labels[want]; !ok {
			t.Errorf("словарь не покрывает статус %q", want)
		}
	}

	forbidden := []string{"адрес существует", "почта существует", "адрес не существует"}
	for key, value := range leadCatalog {
		lowered := strings.ToLower(value)
		for _, phrase := range forbidden {
			if strings.Contains(lowered, phrase) {
				t.Errorf("каталог %q содержит запрещённую формулировку %q", key, phrase)
			}
		}
	}
	for name, content := range leadTemplateContents(t) {
		lowered := strings.ToLower(content)
		for _, phrase := range forbidden {
			if strings.Contains(lowered, phrase) {
				t.Errorf("шаблон %s содержит запрещённую формулировку %q", name, phrase)
			}
		}
	}
}

// TestVerificationFromResult проверяет, что исход проверки домена размечается общим
// словарём: подпись статуса приходит из общего слоя.
func TestVerificationFromResult(t *testing.T) {
	checked, err := domain.NewCheckedEmail(
		"sales@example.com", domain.ContactAddress{}, domain.StatusDeliverable,
		"сервер принял получателя", "provider", time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC), false,
	)
	if err != nil {
		t.Fatalf("NewCheckedEmail: %v", err)
	}
	badge := string(verificationBadge(verificationFromResult(checked)))
	if !strings.Contains(badge, components.AddressDeliverable.Label()) {
		t.Fatalf("бейдж верификации не несёт подпись общего словаря: %q", badge)
	}
	if !strings.Contains(badge, "verify-badge") && !strings.Contains(badge, "verify-deliverable") {
		t.Fatalf("бейдж верификации не размечен общим компонентом: %q", badge)
	}
}

// leadTemplateContents возвращает содержимое встроенных шаблонов консоли.
func leadTemplateContents(t *testing.T) map[string]string {
	t.Helper()
	entries, err := fs.ReadDir(templateFS, "templates")
	if err != nil {
		t.Fatalf("чтение каталога шаблонов: %v", err)
	}
	contents := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(templateFS, "templates/"+entry.Name())
		if err != nil {
			t.Fatalf("чтение шаблона %s: %v", entry.Name(), err)
		}
		contents[entry.Name()] = string(data)
	}
	return contents
}
