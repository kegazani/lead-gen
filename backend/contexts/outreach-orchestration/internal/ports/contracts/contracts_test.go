package contracts

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

var testInstant = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func validEnvelope(name string) Envelope {
	return Envelope{
		EventID:       "evt-1",
		OccurredAt:    testInstant,
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
		Name:          name,
		SchemaVersion: "1.0.0",
	}
}

// Обязательные поля envelope присутствуют и проверяются.
func TestEnvelope_RequiredFields(t *testing.T) {
	if err := validEnvelope(ContractApprovedAction).Validate(); err != nil {
		t.Fatalf("полный envelope должен быть валиден: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Envelope)
		field  string
	}{
		{"event_id", func(e *Envelope) { e.EventID = "" }, "event_id"},
		{"occurred_at", func(e *Envelope) { e.OccurredAt = time.Time{} }, "occurred_at"},
		{"tenant_id", func(e *Envelope) { e.TenantID = "" }, "tenant_id"},
		{"correlation_id", func(e *Envelope) { e.CorrelationID = "" }, "correlation_id"},
		{"name", func(e *Envelope) { e.Name = "" }, "name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnvelope(ContractApprovedAction)
			tc.mutate(&env)
			err := env.Validate()
			if err == nil {
				t.Fatalf("ожидалась ошибка об отсутствии %s", tc.field)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("ошибка %q не упоминает поле %s", err, tc.field)
			}
		})
	}
}

// causation_id допускается пустым у корневого сообщения.
func TestEnvelope_CausationIDOptional(t *testing.T) {
	env := validEnvelope(ContractApprovedAction)
	env.CausationID = ""
	if err := env.Validate(); err != nil {
		t.Fatalf("пустой causation_id допустим: %v", err)
	}
}

// Версия схемы должна быть semver.
func TestIsValidSemver(t *testing.T) {
	valid := []string{"0.0.0", "1.0.0", "2.13.4", "1.0.0-rc.1", "1.0.0+build.5", "1.2.3-rc.1+build.5"}
	for _, v := range valid {
		if !IsValidSemver(v) {
			t.Errorf("%q должен быть валидным semver", v)
		}
	}
	invalid := []string{"", "1", "1.0", "v1.0.0", "01.0.0", "1.0.0.0", "1.0.x", " 1.0.0"}
	for _, v := range invalid {
		if IsValidSemver(v) {
			t.Errorf("%q не должен быть валидным semver", v)
		}
	}
}

func TestEnvelope_InvalidSchemaVersion(t *testing.T) {
	env := validEnvelope(ContractApprovedAction)
	env.SchemaVersion = "1.0"
	if err := env.Validate(); err == nil {
		t.Fatal("ожидалась ошибка о невалидном schema_version")
	}
}

// Валидатор сообщает все отсутствующие поля, а не первое из них.
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

// Сериализация envelope содержит все обязательные ключи.
func TestEnvelope_JSONKeys(t *testing.T) {
	raw, err := json.Marshal(validEnvelope(ContractApprovedAction))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"event_id", "occurred_at", "tenant_id", "correlation_id", "name", "schema_version"} {
		if _, ok := got[key]; !ok {
			t.Errorf("в JSON envelope отсутствует ключ %s", key)
		}
	}
}

func validApprovedAction() ApprovedAction {
	a := NewApprovedAction(validEnvelope(ContractApprovedAction))
	a.ApprovedActionID = "act-1"
	a.CampaignID = "camp-1"
	a.DraftID = "draft-1"
	a.DraftVersion = 3
	a.DecisionID = "dec-1"
	a.Channel = "email"
	a.RecipientRef = "recipient-ref-1"
	a.MaterialVersionRef = "mat-ref-1"
	a.DecidedAt = testInstant.Add(time.Minute)
	return a
}

// Round-trip approved-action: сериализация и обратная десериализация совпадают.
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
	if err := restored.Validate(); err != nil {
		t.Fatalf("восстановленный контракт должен быть валиден: %v", err)
	}
}

func TestApprovedAction_RequiredFields(t *testing.T) {
	if err := validApprovedAction().Validate(); err != nil {
		t.Fatalf("полный контракт должен быть валиден: %v", err)
	}
	if err := NewApprovedAction(validEnvelope(ContractApprovedAction)).Validate(); err == nil {
		t.Fatal("ожидалась ошибка об обязательных полях approved-action")
	}
}

// Валидатор сообщает все отсутствующие/некорректные поля сразу.
func TestApprovedAction_ValidateReportsAllMissingFields(t *testing.T) {
	err := NewApprovedAction(validEnvelope(ContractApprovedAction)).Validate()
	if err == nil {
		t.Fatal("ожидалась ошибка об обязательных полях approved-action")
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

// Канал проверяется доменным VO action-channel, а не произвольной строкой.
// Внутренняя обёртка ValidateApprovedAction — то место, где домен остаётся
// последней инстанцией о допустимости канала.
func TestApprovedAction_ValidatesChannelByDomainValueObject(t *testing.T) {
	unknown := validApprovedAction()
	unknown.Channel = "telegram"
	if err := ValidateApprovedAction(unknown); err == nil || !strings.Contains(err.Error(), "channel") {
		t.Fatalf("неизвестный домену канал должен отвергаться, got %v", err)
	}

	canonical := validApprovedAction()
	canonical.Channel = "  EMAIL "
	if err := ValidateApprovedAction(canonical); err != nil {
		t.Fatalf("канал домена с другим регистром и пробелами допустим: %v", err)
	}
}

// Ключ идемпотентности approved-action — (tenant_id, campaign_id, draft_id, draft_version).
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

// FromActionApproved строит кампанийно-уникальный идентификатор утверждённого
// действия: один получатель-черновик в разных кампаниях — разные действия, повтор
// одного действия — тот же идентификатор.
func TestFromActionApprovedUsesCampaignScopedIdentity(t *testing.T) {
	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	common := domain.ActionApproved{
		TenantID:     "tenant-1",
		DraftID:      "info@norbit.ru",
		DraftVersion: 1,
		DecisionID:   "dec-1",
		Channel:      channel,
		Recipient:    "info@norbit.ru",
		At:           testInstant,
	}
	first := common
	first.CampaignID = "camp-1"
	second := common
	second.CampaignID = "camp-2"

	a := FromActionApproved(first)
	b := FromActionApproved(second)
	if a.ApprovedActionID == b.ApprovedActionID {
		t.Fatalf("идентификаторы разных кампаний совпали: %q", a.ApprovedActionID)
	}
	if want := approvedaction.ApprovedActionIdentity("camp-1", "info@norbit.ru", 1); a.ApprovedActionID != want {
		t.Fatalf("идентификатор кампании 1: got %q, want %q", a.ApprovedActionID, want)
	}
	if repeat := FromActionApproved(first); repeat.ApprovedActionID != a.ApprovedActionID {
		t.Fatalf("повтор действия дал другой идентификатор: %q != %q", repeat.ApprovedActionID, a.ApprovedActionID)
	}
	if a.IdempotencyKey() == b.IdempotencyKey() {
		t.Fatal("действия разных кампаний не должны считаться повтором")
	}
}

func validCampaignCommand() CampaignCommand {
	c := NewCampaignCommand(validEnvelope(ContractCampaignCommand))
	c.CommandID = "cmd-1"
	c.CommandType = CommandTypeDraftAction
	c.CampaignID = "camp-1"
	c.DraftID = "draft-1"
	c.ExpectedDraftVersion = 2
	c.AudienceVersionRef = "aud-snap-1"
	c.ContentVersionRef = "content-v-7"
	c.ActorRef = "operator-ref-1"
	return c
}

// Round-trip campaign-command: сериализация и обратная десериализация совпадают.
func TestCampaignCommand_RoundTrip(t *testing.T) {
	orig := validCampaignCommand()
	if err := orig.Validate(); err != nil {
		t.Fatalf("исходный контракт должен быть валиден: %v", err)
	}
	raw, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var restored CampaignCommand
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, restored) {
		t.Fatalf("round-trip не совпал:\norig: %+v\n got: %+v", orig, restored)
	}
	if err := restored.Validate(); err != nil {
		t.Fatalf("восстановленный контракт должен быть валиден: %v", err)
	}
}

func TestCampaignCommand_RequiredFields(t *testing.T) {
	if err := validCampaignCommand().Validate(); err != nil {
		t.Fatalf("полный контракт должен быть валиден: %v", err)
	}

	// draft-action создаёт черновик: draft_id нужен, версия ещё не существует.
	missingDraft := validCampaignCommand()
	missingDraft.DraftID = ""
	if err := missingDraft.Validate(); err == nil || !strings.Contains(err.Error(), "draft_id") {
		t.Fatalf("draft-action без draft_id должен отвергаться, got %v", err)
	}

	creation := validCampaignCommand()
	creation.ExpectedDraftVersion = 0
	if err := creation.Validate(); err != nil {
		t.Fatalf("draft-action — создание черновика, expected_draft_version не требуется: %v", err)
	}

	// Команды над существующим черновиком требуют его точной версии.
	for _, commandType := range []string{
		CommandTypeApproveDraft, CommandTypeRejectDraft,
		CommandTypeReviseDraft, CommandTypeExecuteDraft,
	} {
		t.Run(commandType, func(t *testing.T) {
			complete := validCampaignCommand()
			complete.CommandType = commandType
			if err := complete.Validate(); err != nil {
				t.Fatalf("%s с версией должен быть валиден: %v", commandType, err)
			}

			noVersion := validCampaignCommand()
			noVersion.CommandType = commandType
			noVersion.ExpectedDraftVersion = 0
			if err := noVersion.Validate(); err == nil || !strings.Contains(err.Error(), "expected_draft_version") {
				t.Fatalf("%s без expected_draft_version должен отвергаться, got %v", commandType, err)
			}

			noDraft := validCampaignCommand()
			noDraft.CommandType = commandType
			noDraft.DraftID = ""
			if err := noDraft.Validate(); err == nil || !strings.Contains(err.Error(), "draft_id") {
				t.Fatalf("%s без draft_id должен отвергаться, got %v", commandType, err)
			}
		})
	}

	unknownType := validCampaignCommand()
	unknownType.CommandType = "nope"
	if err := unknownType.Validate(); err == nil || !strings.Contains(err.Error(), "command_type") {
		t.Fatalf("неизвестный command_type должен отвергаться, got %v", err)
	}

	// create-campaign не требует draft_id и версии черновика.
	create := NewCampaignCommand(validEnvelope(ContractCampaignCommand))
	create.CommandID = "cmd-2"
	create.CommandType = CommandTypeCreateCampaign
	create.CampaignID = "camp-1"
	create.ActorRef = "operator-ref-1"
	if err := create.Validate(); err != nil {
		t.Fatalf("create-campaign должен быть валиден без черновика: %v", err)
	}
}

// Валидатор команды собирает все отсутствующие поля, а не первое.
func TestCampaignCommand_ValidateReportsAllMissingFields(t *testing.T) {
	command := NewCampaignCommand(validEnvelope(ContractCampaignCommand))
	command.CommandType = CommandTypeApproveDraft
	err := command.Validate()
	if err == nil {
		t.Fatal("ожидалась ошибка об обязательных полях campaign-command")
	}
	for _, field := range []string{"command_id", "campaign_id", "actor_ref", "draft_id", "expected_draft_version"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
	}
}

// Ключ идемпотентности campaign-command — (tenant_id, command_id).
func TestCampaignCommand_IdempotencyKey(t *testing.T) {
	c := validCampaignCommand()
	if got, want := c.IdempotencyKey(), "tenant-1|cmd-1"; got != want {
		t.Fatalf("ключ идемпотентности: got %q, want %q", got, want)
	}
	d := c
	d.Envelope.EventID = "evt-2"
	if d.IdempotencyKey() != c.IdempotencyKey() {
		t.Fatal("повтор команды с тем же command_id должен давать тот же ключ")
	}
	d.CommandID = "cmd-2"
	if d.IdempotencyKey() == c.IdempotencyKey() {
		t.Fatal("другая команда должна иметь другой ключ")
	}
}

// Разделитель не допускает коллизий между разными парами компонентов.
func TestIdempotencyKeysDoNotCollideOnSeparator(t *testing.T) {
	cmdA := validCampaignCommand()
	cmdA.TenantID = "tenant|1"
	cmdA.CommandID = "2"
	cmdB := validCampaignCommand()
	cmdB.TenantID = "tenant"
	cmdB.CommandID = "1|2"
	if cmdA.IdempotencyKey() == cmdB.IdempotencyKey() {
		t.Fatalf("ключи команд совпали: %q", cmdA.IdempotencyKey())
	}

	actA := validApprovedAction()
	actA.TenantID = "tenant|1"
	actA.DraftID = "2"
	actB := validApprovedAction()
	actB.TenantID = "tenant"
	actB.DraftID = "1|2"
	if actA.IdempotencyKey() == actB.IdempotencyKey() {
		t.Fatalf("ключи действий совпали: %q", actA.IdempotencyKey())
	}
}
