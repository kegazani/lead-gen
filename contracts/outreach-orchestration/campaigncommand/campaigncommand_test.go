package campaigncommand

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func validSubmission() Submission {
	return Submission{
		TenantID:     "tenant-1",
		RunID:        "run-1",
		MissionID:    "mission-1",
		CompanyID:    "company-1",
		DraftRef:     "company-1@v2",
		DraftVersion: 2,
		MaterialRef:  "company-1@v2",
	}
}

func TestSubmissionValidateAcceptsCompleteCommand(t *testing.T) {
	if err := validSubmission().Validate(); err != nil {
		t.Fatalf("полная команда отвергнута: %v", err)
	}
}

func TestSubmissionValidateReportsAllMissingFields(t *testing.T) {
	err := Submission{}.Validate()
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("ожидалась ErrRejected, получено %v", err)
	}
	for _, field := range []string{"tenant_id", "run_id", "company_id", "draft_ref", "draft_version", "material_ref"} {
		if !strings.Contains(err.Error(), field) {
			t.Fatalf("ошибка не называет поле %q: %v", field, err)
		}
	}
}

func TestSubmissionValidateRejectsNonPositiveVersion(t *testing.T) {
	submission := validSubmission()
	submission.DraftVersion = 0
	if err := submission.Validate(); !errors.Is(err, ErrRejected) {
		t.Fatalf("ожидалась ErrRejected, получено %v", err)
	}
}

func TestSubmissionIdempotencyKeyIsStableAndDistinct(t *testing.T) {
	first := validSubmission()
	second := validSubmission()
	if first.IdempotencyKey() != second.IdempotencyKey() {
		t.Fatal("одинаковые команды дали разные ключи идемпотентности")
	}

	// Смена любого компонента ключа (тенант, запуск, ссылка черновика) меняет ключ.
	mutations := map[string]func(*Submission){
		"tenant":    func(s *Submission) { s.TenantID = "tenant-2" },
		"run":       func(s *Submission) { s.RunID = "run-2" },
		"draft_ref": func(s *Submission) { s.DraftRef = "company-1@v3" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			changed := validSubmission()
			mutate(&changed)
			if changed.IdempotencyKey() == first.IdempotencyKey() {
				t.Fatal("изменение компонента не изменило ключ идемпотентности")
			}
		})
	}
}

func TestSubmissionIdempotencyKeyEncodesSeparator(t *testing.T) {
	// Значение с разделителем "|" внутри не должно склеивать разные команды.
	a := validSubmission()
	a.TenantID, a.RunID, a.DraftRef = "t|r", "x", "d"
	b := validSubmission()
	b.TenantID, b.RunID, b.DraftRef = "t", "r|x", "d"
	if a.IdempotencyKey() == b.IdempotencyKey() {
		t.Fatal("разные команды с разделителем в значении дали один ключ")
	}
}

func TestSubmissionJSONRoundTrip(t *testing.T) {
	original := validSubmission()
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded Submission
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded != original {
		t.Fatalf("round-trip изменил команду: %+v != %+v", decoded, original)
	}
	if decoded.IdempotencyKey() != original.IdempotencyKey() {
		t.Fatal("round-trip изменил ключ идемпотентности")
	}
}

func TestResultJSONRoundTrip(t *testing.T) {
	original := Result{CampaignID: "campaign-1", DraftRef: "company-1@v2", DraftVersion: 2, Repeated: true}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded Result
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded != original {
		t.Fatalf("round-trip изменил результат: %+v != %+v", decoded, original)
	}
}
