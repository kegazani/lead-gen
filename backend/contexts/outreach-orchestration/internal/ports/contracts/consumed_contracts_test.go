package contracts

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// validAudienceSnapshot собирает полный контракт audience-snapshot.
func validAudienceSnapshot() AudienceSnapshot {
	return AudienceSnapshot{
		Envelope:    validEnvelope(ContractAudienceSnapshot),
		SnapshotID:  "snap-1",
		SegmentID:   "segment-1",
		Version:     3,
		MemberCount: 120,
		GeneratedAt: testInstant,
	}
}

// Round-trip audience-snapshot: сериализация и обратная десериализация совпадают.
func TestAudienceSnapshot_RoundTrip(t *testing.T) {
	orig := validAudienceSnapshot()
	if err := orig.Validate(); err != nil {
		t.Fatalf("исходный контракт должен быть валиден: %v", err)
	}
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored AudienceSnapshot
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, restored) {
		t.Fatalf("round-trip не совпал:\norig: %+v\n got: %+v", orig, restored)
	}
}

// Валидатор audience-snapshot сообщает все отсутствующие поля сразу.
func TestAudienceSnapshot_RequiredFields(t *testing.T) {
	if err := validAudienceSnapshot().Validate(); err != nil {
		t.Fatalf("полный контракт должен быть валиден: %v", err)
	}

	empty := AudienceSnapshot{Envelope: validEnvelope(ContractAudienceSnapshot)}
	err := empty.Validate()
	if err == nil {
		t.Fatal("ожидалась ошибка об обязательных полях audience-snapshot")
	}
	for _, field := range []string{"snapshot_id", "segment_id", "version", "generated_at"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
	}

	// Пустой сегмент-снимок валиден: member_count = 0 не ошибка.
	zeroMembers := validAudienceSnapshot()
	zeroMembers.MemberCount = 0
	if err := zeroMembers.Validate(); err != nil {
		t.Fatalf("снимок пустого сегмента должен быть валиден: %v", err)
	}
}

// validEngagementEvent собирает полный контракт engagement-event.
func validEngagementEvent() EngagementEvent {
	return EngagementEvent{
		Envelope:     validEnvelope(ContractEngagementEvent),
		EventType:    EngagementEventOpened,
		CampaignID:   "camp-1",
		DraftID:      "draft-1",
		RecipientRef: "recipient-ref-1",
	}
}

// Round-trip engagement-event: сериализация и обратная десериализация совпадают.
func TestEngagementEvent_RoundTrip(t *testing.T) {
	orig := validEngagementEvent()
	if err := orig.Validate(); err != nil {
		t.Fatalf("исходный контракт должен быть валиден: %v", err)
	}
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored EngagementEvent
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, restored) {
		t.Fatalf("round-trip не совпал:\norig: %+v\n got: %+v", orig, restored)
	}
}

// Неизвестный тип реакции отвергается: набор события закрыт.
func TestEngagementEvent_RejectsUnknownType(t *testing.T) {
	if err := validEngagementEvent().Validate(); err != nil {
		t.Fatalf("известный тип должен быть валиден: %v", err)
	}
	unknown := validEngagementEvent()
	unknown.EventType = "forwarded"
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), "event_type") {
		t.Fatalf("неизвестный event_type должен отвергаться, got %v", err)
	}
}

// validProcessingAuthority собирает полный контракт processing-authority.
func validProcessingAuthority() ProcessingAuthority {
	return ProcessingAuthority{
		Envelope:     validEnvelope(ContractProcessingAuthority),
		PolicyID:     "policy-1",
		Scope:        "tenant:tenant-1",
		AllowedZones: []string{"green"},
		LegalBasis:   "consent",
		EffectiveAt:  testInstant,
	}
}

// Round-trip processing-authority: сериализация и обратная десериализация совпадают.
func TestProcessingAuthority_RoundTrip(t *testing.T) {
	orig := validProcessingAuthority()
	if err := orig.Validate(); err != nil {
		t.Fatalf("исходный контракт должен быть валиден: %v", err)
	}
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored ProcessingAuthority
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, restored) {
		t.Fatalf("round-trip не совпал:\norig: %+v\n got: %+v", orig, restored)
	}
}

// Политика без разрешённых зон — не решение и отвергается.
func TestProcessingAuthority_RequiresAllowedZones(t *testing.T) {
	if err := validProcessingAuthority().Validate(); err != nil {
		t.Fatalf("полный контракт должен быть валиден: %v", err)
	}
	noZones := validProcessingAuthority()
	noZones.AllowedZones = []string{"  "}
	if err := noZones.Validate(); err == nil || !strings.Contains(err.Error(), "allowed_zones") {
		t.Fatalf("политика без разрешённых зон должна отвергаться, got %v", err)
	}

	noBasis := validProcessingAuthority()
	noBasis.LegalBasis = ""
	noBasis.EffectiveAt = time.Time{}
	err := noBasis.Validate()
	if err == nil {
		t.Fatal("политика без основания и момента вступления должна отвергаться")
	}
	for _, field := range []string{"legal_basis", "effective_at"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
	}
}
