package deliverycommand

import (
	"strings"
	"testing"
)

func validCommand() Command {
	return Command{
		CommandID:        "cmd-1",
		TenantID:         "tenant-1",
		ApprovedActionID: "act-1",
		RecipientRef:     "ref-1",
		DeliveryAddress:  "lead@example.com",
		SenderDomain:     "mail.example.com",
		Channel:          ChannelEmailName,
		CorrelationID:    "act-1",
	}
}

func TestCommand_Validate(t *testing.T) {
	if err := validCommand().Validate(); err != nil {
		t.Fatalf("корректная команда отвергнута: %v", err)
	}

	err := (Command{}).Validate()
	if err == nil {
		t.Fatal("ожидалась ошибка об обязательных полях")
	}
	for _, field := range []string{"command_id", "tenant_id", "approved_action_id", "recipient_ref", "delivery_address", "sender_domain", "channel", "correlation_id"} {
		if !strings.Contains(err.Error(), field) {
			t.Errorf("ошибка не упоминает %s: %v", field, err)
		}
	}
}

func TestCommand_ChannelClosed(t *testing.T) {
	unknown := validCommand()
	unknown.Channel = "sms"
	if err := unknown.Validate(); err == nil || !strings.Contains(err.Error(), "channel") {
		t.Fatalf("неизвестный канал должен отвергаться, got %v", err)
	}
	// Регистр и пробелы не значимы.
	canonical := validCommand()
	canonical.Channel = "  EMAIL "
	if err := canonical.Validate(); err != nil {
		t.Fatalf("канал email в другом регистре допустим: %v", err)
	}
}

func TestCommand_IdempotencyKey(t *testing.T) {
	command := validCommand()
	if got, want := command.IdempotencyKey(), "tenant-1|act-1"; got != want {
		t.Fatalf("ключ идемпотентности: got %q, want %q", got, want)
	}
	other := command
	other.CommandID = "cmd-2"
	if other.IdempotencyKey() != command.IdempotencyKey() {
		t.Fatal("повтор одного действия с другим command_id должен давать тот же ключ")
	}
	other.ApprovedActionID = "act-2"
	if other.IdempotencyKey() == command.IdempotencyKey() {
		t.Fatal("другое действие должно давать другой ключ")
	}
}

func TestCommand_ContentOptional(t *testing.T) {
	// Поля содержимого необязательны: команда без темы и тела остаётся валидной.
	if err := validCommand().Validate(); err != nil {
		t.Fatalf("команда без содержимого должна быть допустима: %v", err)
	}

	// Непустые тема и тело также допустимы и не ломают разбор.
	withContent := validCommand()
	withContent.Subject = "Тема письма"
	withContent.Body = "Тело письма"
	if err := withContent.Validate(); err != nil {
		t.Fatalf("команда с содержимым отвергнута: %v", err)
	}
	if withContent.Subject != "Тема письма" || withContent.Body != "Тело письма" {
		t.Fatalf("содержимое не сохранено: %+v", withContent)
	}
}

func TestSchemaVersion_MinorAddition(t *testing.T) {
	// Добавление темы/тела и ссылки на кампанию — MINOR: MAJOR остаётся 1.
	if SchemaVersion != "1.2.0" {
		t.Fatalf("ожидалась версия схемы 1.2.0, got %q", SchemaVersion)
	}
}

func TestCommand_CampaignOptional(t *testing.T) {
	// Ссылка на кампанию (версия 1.2.0) необязательна: команда без неё валидна.
	if err := validCommand().Validate(); err != nil {
		t.Fatalf("команда без кампании должна быть допустима: %v", err)
	}
	withCampaign := validCommand()
	withCampaign.CampaignID = "handoff-req-1"
	if err := withCampaign.Validate(); err != nil {
		t.Fatalf("команда со ссылкой на кампанию отвергнута: %v", err)
	}
	if withCampaign.CampaignID != "handoff-req-1" {
		t.Fatalf("ссылка на кампанию не сохранена: %+v", withCampaign)
	}
}

func TestInProcHandler_WithoutFuncIsUnavailable(t *testing.T) {
	port := InProc(InProcHandler{})
	if _, err := port.Deliver(nil, validCommand()); err != ErrUnavailable {
		t.Fatalf("ожидалась ErrUnavailable, got %v", err)
	}
}
