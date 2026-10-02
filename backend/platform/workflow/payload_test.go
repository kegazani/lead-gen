package workflow

import (
	"strings"
	"testing"
	"time"
)

// validPayload — корректный payload: только идентификаторы, коды и время.
type validPayload struct {
	RequestID     string
	TenantID      string
	CorrelationID string
	LeadTarget    int
	Approved      bool
	OccurredAt    time.Time
	Tags          []string
	Nested        validNested
}

type validNested struct {
	WorkflowID string
	Round      int
}

// piiPayload — payload с персональными данными: должен быть отвергнут.
type piiPayload struct {
	RequestID string
	Email     string
}

// secretPayload — payload с секретом: должен быть отвергнут.
type secretPayload struct {
	RequestID string
	APIKey    string
}

// opaquePayload — payload с непрозрачным типом: должен быть отвергнут.
type opaquePayload struct {
	RequestID string
	Extra     map[string]string
}

// TestCheckIdentifiersOnlyAcceptsIdentifiers проверяет, что payload из
// идентификаторов, кодов и времени проходит проверку без нарушений.
func TestCheckIdentifiersOnlyAcceptsIdentifiers(t *testing.T) {
	if violations := CheckIdentifiersOnly(validPayload{}); len(violations) != 0 {
		t.Fatalf("корректный payload отвергнут: %+v", violations)
	}
}

// TestCheckIdentifiersOnlyAcceptsPointers проверяет разыменование указателя.
func TestCheckIdentifiersOnlyAcceptsPointers(t *testing.T) {
	if violations := CheckIdentifiersOnly(&validPayload{}); len(violations) != 0 {
		t.Fatalf("указатель на корректный payload отвергнут: %+v", violations)
	}
}

// TestCheckIdentifiersOnlyRejectsPII проверяет, что поле с PII отвергается.
func TestCheckIdentifiersOnlyRejectsPII(t *testing.T) {
	violations := CheckIdentifiersOnly(piiPayload{})
	if len(violations) != 1 {
		t.Fatalf("ожидалось одно нарушение, получено %+v", violations)
	}
	if !strings.Contains(violations[0].Field, "Email") {
		t.Fatalf("нарушение должно указывать на поле Email: %+v", violations[0])
	}
}

// TestCheckIdentifiersOnlyRejectsSecret проверяет, что поле с секретом отвергается.
func TestCheckIdentifiersOnlyRejectsSecret(t *testing.T) {
	violations := CheckIdentifiersOnly(secretPayload{})
	if len(violations) != 1 || !strings.Contains(violations[0].Field, "APIKey") {
		t.Fatalf("ожидалось нарушение на APIKey, получено %+v", violations)
	}
}

// TestCheckIdentifiersOnlyRejectsOpaque проверяет, что непрозрачный тип отвергается.
func TestCheckIdentifiersOnlyRejectsOpaque(t *testing.T) {
	violations := CheckIdentifiersOnly(opaquePayload{})
	if len(violations) != 1 {
		t.Fatalf("ожидалось одно нарушение, получено %+v", violations)
	}
}

// TestCheckIdentifiersOnlyRejectsNil проверяет отказ на пустом payload.
func TestCheckIdentifiersOnlyRejectsNil(t *testing.T) {
	if violations := CheckIdentifiersOnly(nil); len(violations) != 1 {
		t.Fatalf("nil payload должен быть отвергнут: %+v", violations)
	}
}
