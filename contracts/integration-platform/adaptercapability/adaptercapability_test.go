package adaptercapability

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

var testInstant = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func validEnvelope() Envelope {
	return Envelope{
		EventID:       "evt-1",
		OccurredAt:    testInstant,
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
		Name:          ContractName,
		SchemaVersion: SchemaVersion,
	}
}

func validDeclaration() Declaration {
	d := NewDeclaration(validEnvelope())
	d.AdapterName = "firecrawl"
	d.Capabilities = []Capability{
		{Name: "search", SetVersion: "1.2.0"},
		{Name: "crawl", SetVersion: "1.0.0"},
	}
	return d
}

// NewDeclaration проставляет имя и версию схемы контракта.
func TestNewDeclarationSetsNameAndVersion(t *testing.T) {
	d := NewDeclaration(Envelope{})
	if d.Name != ContractName || d.SchemaVersion != SchemaVersion {
		t.Fatalf("имя/версия: got (%q, %q)", d.Name, d.SchemaVersion)
	}
}

// Полное объявление валидно и переживает round-trip сериализации.
func TestDeclaration_RoundTrip(t *testing.T) {
	orig := validDeclaration()
	if err := orig.Validate(); err != nil {
		t.Fatalf("исходное объявление должно быть валидно: %v", err)
	}
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored Declaration
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, restored) {
		t.Fatalf("round-trip не совпал:\norig: %+v\n got: %+v", orig, restored)
	}
}

// Валидация называет все обязательные поля объявления одним сообщением.
func TestDeclaration_ValidateReportsAllMissingFields(t *testing.T) {
	err := NewDeclaration(validEnvelope()).Validate()
	if err == nil {
		t.Fatal("пустое объявление должно быть невалидно")
	}
	for _, field := range []string{"adapter_name", "capabilities"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
	}
}

// Возможность без имени и без semver-версии набора отвергается.
func TestCapability_Validate(t *testing.T) {
	invalid := validDeclaration()
	invalid.Capabilities = []Capability{{Name: "", SetVersion: "1.0"}}
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "capabilities[0]") {
		t.Fatalf("некорректная возможность должна отвергаться, got %v", err)
	}
}

// Версия схемы конверта проверяется как semver.
func TestDeclaration_InvalidSchemaVersion(t *testing.T) {
	invalid := validDeclaration()
	invalid.SchemaVersion = "1.0"
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("невалидная версия схемы должна отвергаться, got %v", err)
	}
}

func TestIsValidSemver(t *testing.T) {
	for _, v := range []string{"0.0.0", "1.0.0", "2.13.4", "1.0.0-rc.1", "1.2.3-rc.1+build.5"} {
		if !IsValidSemver(v) {
			t.Errorf("%q должен быть валидным semver", v)
		}
	}
	for _, v := range []string{"", "1", "1.0", "v1.0.0", "01.0.0", "1.0.0.0"} {
		if IsValidSemver(v) {
			t.Errorf("%q не должен быть валидным semver", v)
		}
	}
}
