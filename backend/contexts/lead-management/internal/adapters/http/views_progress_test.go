package httpapi

import (
	"strings"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// TestProgressTextHasNoCredentials проверяет приёмку «прогресс без секретов и
// персональных данных»: строка прогресса несёт фазу, адрес, счётчики и кредиты, но
// не содержит признаков ключей/токенов.
func TestProgressTextHasNoCredentials(t *testing.T) {
	p := &ports.SearchJobProgress{
		Phase:            "crawling",
		CurrentURL:       "https://example.com/contacts",
		PagesCrawled:     3,
		CompaniesFound:   1,
		CreditsSpent:     2,
		CreditsAvailable: 18,
	}
	got := progressText(p)
	lower := strings.ToLower(got)
	for _, secret := range []string{"bearer", "authorization", "apikey", "api_key", "token", "password"} {
		if strings.Contains(lower, secret) {
			t.Fatalf("прогресс содержит чувствительное поле %q: %q", secret, got)
		}
	}
	for _, want := range []string{"обход", "https://example.com/contacts", "страниц 3", "компаний 1", "кредитов 2/20"} {
		if !strings.Contains(got, want) {
			t.Fatalf("прогресс не содержит %q: %q", want, got)
		}
	}
}

// TestProgressTextShowsTargetRatio проверяет, что при заданной цели прогресс
// показывает «найдено X из N» и причину остановки терминального задания.
func TestProgressTextShowsTargetRatio(t *testing.T) {
	got := progressText(&ports.SearchJobProgress{
		Phase:           "finalizing",
		CompaniesFound:  3,
		TargetCompanies: 10,
		StopReason:      "бюджет исчерпан",
	})
	if !strings.Contains(got, "компаний 3 из 10") {
		t.Fatalf("прогресс не содержит отношение «X из N»: %q", got)
	}
	if !strings.Contains(got, "бюджет исчерпан") {
		t.Fatalf("прогресс не содержит причину остановки: %q", got)
	}
}

// TestProgressTextPhaseLabels проверяет подпись фазы добора целей.
func TestProgressTextPhaseLabels(t *testing.T) {
	if got := phaseLabel(app.PhaseExpanding); got != "добор целей" {
		t.Fatalf("подпись фазы добора = %q, ожидалась %q", got, "добор целей")
	}
}

// TestProgressTextShowsAssessmentErrors проверяет, что ошибки оценки видны в строке
// прогресса отдельно от ошибок разбора, когда они есть, и не показываются при нуле.
func TestProgressTextShowsAssessmentErrors(t *testing.T) {
	withErrors := progressText(&ports.SearchJobProgress{Phase: "extracting", AssessmentErrors: 2})
	if !strings.Contains(withErrors, "ошибок оценки 2") {
		t.Fatalf("прогресс не содержит счётчик ошибок оценки: %q", withErrors)
	}
	clean := progressText(&ports.SearchJobProgress{Phase: "extracting"})
	if strings.Contains(clean, "ошибок оценки") {
		t.Fatalf("нулевые ошибки оценки не должны показываться: %q", clean)
	}
}
