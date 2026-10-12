package components

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// verificationDictionary — ожидаемый словарь верификации (BRD §3.18): статус →
// подпись.
var verificationDictionary = map[AddressStatus]string{
	AddressDeliverable: "доставляемый",
	AddressRisky:       "рискованный",
	AddressUnknown:     "неизвестно",
	AddressInvalid:     "недействительный",
}

// TestVerificationStatusesAreDistinct проверяет критерий задачи 7.2: статусы
// различимы, входят в словарь и имеют иконку, отличную от подписи.
func TestVerificationStatusesAreDistinct(t *testing.T) {
	seenLabels := map[string]AddressStatus{}
	seenIcons := map[string]AddressStatus{}
	for status, wantLabel := range verificationDictionary {
		if !status.Valid() {
			t.Errorf("статус %q не входит в словарь", status)
		}
		if got := status.Label(); got != wantLabel {
			t.Errorf("Label(%q) = %q, ожидалось %q", status, got, wantLabel)
		}
		if previous, ok := seenLabels[status.Label()]; ok {
			t.Errorf("статусы %q и %q имеют одинаковую подпись %q", previous, status, status.Label())
		}
		seenLabels[status.Label()] = status

		icon := status.Icon()
		if strings.TrimSpace(icon) == "" {
			t.Errorf("статус %q без значка", status)
		}
		if previous, ok := seenIcons[icon]; ok {
			t.Errorf("статусы %q и %q имеют одинаковый значок %q", previous, status, icon)
		}
		seenIcons[icon] = status
	}
}

// TestVerificationVocabularyForbidsExistenceClaim — линтер формулировок задачи
// 7.2: запрещённая формулировка «адрес существует» не встречается ни в каталоге
// сообщений, ни в шаблонах.
func TestVerificationVocabularyForbidsExistenceClaim(t *testing.T) {
	forbidden := []string{
		"адрес существует",
		"адрес не существует",
		"почта существует",
	}
	for key, value := range ruCatalog {
		lowered := strings.ToLower(value)
		for _, phrase := range forbidden {
			if strings.Contains(lowered, phrase) {
				t.Errorf("каталог %q содержит запрещённую формулировку %q: %q", key, phrase, value)
			}
		}
	}
	for _, file := range templateNames(t) {
		lowered := strings.ToLower(readEmbedded(t, "templates/"+file))
		for _, phrase := range forbidden {
			if strings.Contains(lowered, phrase) {
				t.Errorf("шаблон %s содержит запрещённую формулировку %q", file, phrase)
			}
		}
	}
}

// TestRenderVerification проверяет разметку бейджа верификации: подпись, значок и
// метаданные проверки приходят из общего слоя.
func TestRenderVerification(t *testing.T) {
	verification := Verification{
		Status:  AddressDeliverable,
		Date:    time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC),
		Source:  "verifier",
		HasDate: true,
	}

	var buffer bytes.Buffer
	if err := RenderVerification(&buffer, verification); err != nil {
		t.Fatalf("RenderVerification: %v", err)
	}

	want := `<span class="verify verify-deliverable" title="доставляемый">` +
		`<span class="verify-icon" aria-hidden="true">✓</span>` +
		`<span class="verify-label">доставляемый</span> ` +
		`<span class="verify-meta">Проверено: 02.01.2026 · verifier</span></span>`
	if got := buffer.String(); got != want {
		t.Fatalf("разметка верификации:\n got: %q\nwant: %q", got, want)
	}
}

// TestRenderVerificationUnknownStatus проверяет, что необъявленный статус честно
// показывается как «неизвестно», а не выдаётся за известный.
func TestRenderVerificationUnknownStatus(t *testing.T) {
	var buffer bytes.Buffer
	if err := RenderVerification(&buffer, Verification{Status: AddressStatus("made-up")}); err != nil {
		t.Fatalf("RenderVerification: %v", err)
	}
	got := buffer.String()
	if !strings.Contains(got, "verify-unknown") || !strings.Contains(got, "неизвестно") {
		t.Fatalf("неизвестный статус не сведён к «неизвестно»: %q", got)
	}
}
