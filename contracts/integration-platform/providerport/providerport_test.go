package providerport

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

func validRequest() Request {
	return Request{
		Envelope:        validEnvelope(),
		AdapterName:     "firecrawl",
		IntegrationType: "web-research",
		Operation:       "search",
		CommandKey:      "cmd-1",
		Payload:         json.RawMessage(`{"query":"логисты"}`),
	}
}

// Полный запрос валиден, сериализация переживает round-trip без потерь.
func TestRequest_RoundTrip(t *testing.T) {
	orig := validRequest()
	if err := orig.Validate(); err != nil {
		t.Fatalf("исходный запрос должен быть валиден: %v", err)
	}
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored Request
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, restored) {
		t.Fatalf("round-trip не совпал:\norig: %+v\n got: %+v", orig, restored)
	}
}

// Результат переживает round-trip без потерь.
func TestResult_RoundTrip(t *testing.T) {
	orig := Result{
		Operation:  "search",
		ExternalID: "ext-1",
		Payload:    json.RawMessage(`{"id":"ext-1"}`),
	}
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored Result
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, restored) {
		t.Fatalf("round-trip не совпал:\norig: %+v\n got: %+v", orig, restored)
	}
}

// Валидация называет все отсутствующие поля запроса одним сообщением.
func TestRequest_ValidateReportsAllMissingFields(t *testing.T) {
	broken := validRequest()
	broken.AdapterName = ""
	broken.IntegrationType = ""
	broken.Operation = ""
	broken.CommandKey = ""
	err := broken.Validate()
	if err == nil {
		t.Fatal("неполный запрос должен быть невалиден")
	}
	for _, field := range []string{"adapter_name", "integration_type", "operation", "command_key"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
	}
}

// Конверт проверяется вместе с полями контракта.
func TestRequest_ValidateReportsEnvelopeFields(t *testing.T) {
	broken := validRequest()
	broken.EventID = ""
	broken.SchemaVersion = "1.0"
	err := broken.Validate()
	if err == nil {
		t.Fatal("неполный конверт должен быть невалиден")
	}
	for _, field := range []string{"event_id", "schema_version"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
	}
}

// Имя контракта сверяется с константой, а не с произвольной строкой.
func TestRequest_ValidateRequiresContractName(t *testing.T) {
	broken := validRequest()
	broken.Name = "other"
	if err := broken.Validate(); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("чужое имя контракта должно отвергаться, got %v", err)
	}
}

// Ключ идемпотентности — (tenant_id, тип интеграции, операция, ключ команды):
// новая команда даёт другой ключ, а event_id на ключ не влияет.
func TestRequest_IdempotencyKey(t *testing.T) {
	r := validRequest()
	if got, want := r.IdempotencyKey(), "tenant-1|web-research|search|cmd-1"; got != want {
		t.Fatalf("ключ идемпотентности: got %q, want %q", got, want)
	}

	repeat := r
	repeat.EventID = "evt-2"
	if repeat.IdempotencyKey() != r.IdempotencyKey() {
		t.Fatal("смена event_id не должна менять ключ идемпотентности")
	}

	next := r
	next.CommandKey = "cmd-2"
	if next.IdempotencyKey() == r.IdempotencyKey() {
		t.Fatal("новый ключ команды должен менять ключ идемпотентности")
	}
}

// Разделитель не допускает коллизий между разными наборами компонентов.
func TestIdempotencyKeyDoesNotCollideOnSeparator(t *testing.T) {
	a := validRequest()
	a.TenantID = "tenant|1"
	a.IntegrationType = "2"
	b := validRequest()
	b.TenantID = "tenant"
	b.IntegrationType = "1|2"
	if a.IdempotencyKey() == b.IdempotencyKey() {
		t.Fatalf("ключи совпали: %q", a.IdempotencyKey())
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
