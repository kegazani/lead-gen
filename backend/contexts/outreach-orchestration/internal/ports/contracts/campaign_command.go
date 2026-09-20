package contracts

import (
	"fmt"
	"strings"
)

// ContractCampaignCommand — имя потребляемого контракта: команды кампании,
// обрабатываемые политикой operator-approval-required.
const ContractCampaignCommand = "campaign-command"

// CampaignCommandSchemaVersion — текущая версия схемы. Изменять по правилам
// semver из envelope.go.
const CampaignCommandSchemaVersion = "1.0.0"

// Типы команд контракта campaign-command. Значения совпадают с
// domain.CampaignCommandKind, чтобы политика домена обрабатывала любую команду
// контракта, а контракт не отвергал вид, который домен умеет обрабатывать.
const (
	// CommandTypeCreateCampaign — создание кампании.
	CommandTypeCreateCampaign = "create-campaign"
	// CommandTypeDraftAction — постановка нового черновика действия.
	CommandTypeDraftAction = "draft-action"
	// CommandTypeApproveDraft — утверждение существующего черновика.
	CommandTypeApproveDraft = "approve-draft"
	// CommandTypeRejectDraft — отклонение существующего черновика.
	CommandTypeRejectDraft = "reject-draft"
	// CommandTypeReviseDraft — доработка существующего черновика.
	CommandTypeReviseDraft = "revise-draft"
	// CommandTypeExecuteDraft — исполнение утверждённого черновика.
	CommandTypeExecuteDraft = "execute-draft"
)

// CampaignCommand — потребляемый контракт команды. Обрабатывается идемпотентно
// на границе контекста.
//
// Ключ идемпотентности — (TenantID, CommandID): повтор команды с тем же ключом
// не создаёт вторую кампанию или черновик.
//
// Контракт передаёт только идентификаторы и ссылки на версии входов
// (audience-snapshot, content-version). Содержимое сегмента и материала, а
// равно персональные данные, через границу не проходят.
type CampaignCommand struct {
	Envelope

	// CommandID — идентификатор команды; часть ключа идемпотентности.
	CommandID string `json:"command_id"`
	// CommandType — тип команды (см. CommandType*).
	CommandType string `json:"command_type"`
	// CampaignID — ссылка на кампанию.
	CampaignID string `json:"campaign_id"`
	// DraftID — ссылка на черновик; обязательна для draft-action и для команд
	// над существующим черновиком.
	DraftID string `json:"draft_id,omitempty"`
	// ExpectedDraftVersion — ожидаемая версия черновика; защита от решения по
	// устаревшей версии и гонок. Обязательна для команд над существующим
	// черновиком (approve/reject/revise/execute) и не требуется при создании
	// кампании или нового черновика, где версия ещё не существует.
	ExpectedDraftVersion int64 `json:"expected_draft_version,omitempty"`
	// AudienceVersionRef — ссылка на версию audience-snapshot.
	AudienceVersionRef string `json:"audience_version_ref,omitempty"`
	// ContentVersionRef — ссылка на версию content-version.
	ContentVersionRef string `json:"content_version_ref,omitempty"`
	// ActorRef — ссылка на инициатора команды (не персональные данные).
	ActorRef string `json:"actor_ref"`
}

// NewCampaignCommand возвращает контракт с проставленными именем и версией
// схемы. Остальные поля заполняет вызывающий.
func NewCampaignCommand(env Envelope) CampaignCommand {
	env.Name = ContractCampaignCommand
	env.SchemaVersion = CampaignCommandSchemaVersion
	return CampaignCommand{Envelope: env}
}

// Validate проверяет envelope и обязательные поля контракта. Ошибка называет
// все отсутствующие или некорректные поля, а не первое из них.
func (c CampaignCommand) Validate() error {
	if err := c.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if c.Name != ContractCampaignCommand {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(c.CommandID) == "" {
		invalid = append(invalid, "command_id")
	}
	knownType := isKnownCommandType(c.CommandType)
	if !knownType {
		invalid = append(invalid, "command_type")
	}
	if strings.TrimSpace(c.CampaignID) == "" {
		invalid = append(invalid, "campaign_id")
	}
	if strings.TrimSpace(c.ActorRef) == "" {
		invalid = append(invalid, "actor_ref")
	}
	if knownType && (c.CommandType == CommandTypeDraftAction || requiresExistingDraft(c.CommandType)) {
		if strings.TrimSpace(c.DraftID) == "" {
			invalid = append(invalid, "draft_id")
		}
	}
	if knownType && requiresExistingDraft(c.CommandType) && c.ExpectedDraftVersion <= 0 {
		invalid = append(invalid, "expected_draft_version")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("contracts: campaign-command: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// IdempotencyKey возвращает ключ идемпотентности контракта. Ключ внутренний и
// не является частью wire-формата; компоненты кодируются, чтобы разделитель
// не допускал коллизий.
func (c CampaignCommand) IdempotencyKey() string {
	return fmt.Sprintf("%s|%s", encodeKeyPart(c.TenantID), encodeKeyPart(c.CommandID))
}

// requiresExistingDraft сообщает, что команда адресована уже существующему
// черновику и поэтому требует его точной версии.
func requiresExistingDraft(commandType string) bool {
	switch commandType {
	case CommandTypeApproveDraft, CommandTypeRejectDraft, CommandTypeReviseDraft, CommandTypeExecuteDraft:
		return true
	default:
		return false
	}
}

func isKnownCommandType(t string) bool {
	switch t {
	case CommandTypeCreateCampaign, CommandTypeDraftAction,
		CommandTypeApproveDraft, CommandTypeRejectDraft,
		CommandTypeReviseDraft, CommandTypeExecuteDraft:
		return true
	default:
		return false
	}
}
