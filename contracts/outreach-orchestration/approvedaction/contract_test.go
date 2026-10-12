package approvedaction

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

func validApprovedAction() ApprovedAction {
	a := NewApprovedAction(validEnvelope())
	a.ApprovedActionID = "act-1"
	a.CampaignID = "camp-1"
	a.DraftID = "draft-1"
	a.DraftVersion = 3
	a.DecisionID = "dec-1"
	a.Channel = ChannelEmailName
	a.RecipientRef = "recipient-ref-1"
	a.MaterialVersionRef = "mat-ref-1"
	a.DecidedAt = testInstant.Add(time.Minute)
	return a
}

// NewApprovedAction проставляет имя и версию схемы контракта.
func TestNewApprovedActionSetsNameAndVersion(t *testing.T) {
	a := NewApprovedAction(Envelope{})
	if a.Name != ContractName || a.SchemaVersion != SchemaVersion {
		t.Fatalf("имя/версия: got (%q, %q)", a.Name, a.SchemaVersion)
	}
}

// Полный контракт валиден, и сериализация переживает round-trip без потерь.
func TestApprovedAction_RoundTrip(t *testing.T) {
	orig := validApprovedAction()
	if err := orig.Validate(); err != nil {
		t.Fatalf("исходный контракт должен быть валиден: %v", err)
	}
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored ApprovedAction
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, restored) {
		t.Fatalf("round-trip не совпал:\norig: %+v\n got: %+v", orig, restored)
	}
}

// Валидатор называет все отсутствующие поля сразу, а не первое из них.
func TestApprovedAction_ValidateReportsAllMissingFields(t *testing.T) {
	err := NewApprovedAction(validEnvelope()).Validate()
	if err == nil {
		t.Fatal("пустой контракт должен быть невалиден")
	}
	for _, field := range []string{
		"approved_action_id", "campaign_id", "draft_id", "draft_version",
		"decision_id", "channel", "recipient_ref", "decided_at",
	} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
	}
}

// Канал сверяется с закрытым набором: неизвестный отвергается, регистр и
// окружающие пробелы не значимы.
func TestApprovedAction_ValidatesChannelAgainstClosedSet(t *testing.T) {
	unknown := validApprovedAction()
	unknown.Channel = "telegram"
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), "channel") {
		t.Fatalf("неизвестный канал должен отвергаться, got %v", err)
	}

	canonical := validApprovedAction()
	canonical.Channel = "  EMAIL "
	if err := canonical.Validate(); err != nil {
		t.Fatalf("канал с другим регистром и пробелами допустим: %v", err)
	}
}

func TestEnvelope_ValidateReportsAllMissingFields(t *testing.T) {
	err := Envelope{}.Validate()
	if err == nil {
		t.Fatal("пустой envelope должен быть невалиден")
	}
	for _, field := range []string{"event_id", "occurred_at", "tenant_id", "correlation_id", "name", "schema_version"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
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

// Ключ идемпотентности — (tenant_id, campaign_id, draft_id, draft_version);
// повторная доставка того же действия даёт тот же ключ, новая версия или другая
// кампания — другой.
func TestApprovedAction_IdempotencyKey(t *testing.T) {
	a := validApprovedAction()
	if got, want := a.IdempotencyKey(), "tenant-1|camp-1|draft-1|3"; got != want {
		t.Fatalf("ключ идемпотентности: got %q, want %q", got, want)
	}
	b := a
	b.EventID = "evt-2"
	if b.IdempotencyKey() != a.IdempotencyKey() {
		t.Fatal("повторная доставка того же действия должна давать тот же ключ")
	}
	b.DraftVersion = 4
	if b.IdempotencyKey() == a.IdempotencyKey() {
		t.Fatal("новая версия черновика должна менять ключ идемпотентности")
	}
	otherCampaign := a
	otherCampaign.CampaignID = "camp-2"
	if otherCampaign.IdempotencyKey() == a.IdempotencyKey() {
		t.Fatal("действие другой кампании должно менять ключ идемпотентности")
	}
}

// Идентификатор утверждённого действия уникален в рамках кампании: тот же
// получатель-черновик в разных кампаниях даёт разные идентификаторы, а повтор
// одного действия — тот же.
func TestApprovedActionIdentityScopedToCampaign(t *testing.T) {
	first := ApprovedActionIdentity("camp-1", "info@norbit.ru", 1)
	second := ApprovedActionIdentity("camp-2", "info@norbit.ru", 1)
	if first == second {
		t.Fatalf("идентификаторы разных кампаний совпали: %q", first)
	}
	if repeat := ApprovedActionIdentity("camp-1", "info@norbit.ru", 1); repeat != first {
		t.Fatalf("повтор действия дал другой идентификатор: %q != %q", repeat, first)
	}
	if next := ApprovedActionIdentity("camp-1", "info@norbit.ru", 2); next == first {
		t.Fatal("новая версия должна давать другой идентификатор")
	}
}

// Разделители внутри компонентов не допускают коллизий идентификатора.
func TestApprovedActionIdentityDoesNotCollideOnSeparator(t *testing.T) {
	a := ApprovedActionIdentity("a:b", "c", 1)
	b := ApprovedActionIdentity("a", "b:c", 1)
	if a == b {
		t.Fatalf("идентификаторы совпали: %q", a)
	}
}

// Разделитель не допускает коллизий между разными парами компонентов.
func TestIdempotencyKeyDoesNotCollideOnSeparator(t *testing.T) {
	a := validApprovedAction()
	a.TenantID = "tenant|1"
	a.DraftID = "2"
	b := validApprovedAction()
	b.TenantID = "tenant"
	b.DraftID = "1|2"
	if a.IdempotencyKey() == b.IdempotencyKey() {
		t.Fatalf("ключи совпали: %q", a.IdempotencyKey())
	}
}
