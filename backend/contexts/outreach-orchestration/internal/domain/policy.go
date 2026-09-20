package domain

import "fmt"

// CampaignCommandKind — вид команды контракта campaign-command.
type CampaignCommandKind string

const (
	// CampaignCommandCreateCampaign — создание кампании.
	CampaignCommandCreateCampaign CampaignCommandKind = "create-campaign"

	// CampaignCommandDraftAction — постановка черновика действия.
	CampaignCommandDraftAction CampaignCommandKind = "draft-action"

	// CampaignCommandApproveDraft — утверждение черновика оператором.
	CampaignCommandApproveDraft CampaignCommandKind = "approve-draft"

	// CampaignCommandRejectDraft — отклонение черновика оператором.
	CampaignCommandRejectDraft CampaignCommandKind = "reject-draft"

	// CampaignCommandReviseDraft — доработка черновика.
	CampaignCommandReviseDraft CampaignCommandKind = "revise-draft"

	// CampaignCommandExecuteDraft — попытка исполнения черновика.
	CampaignCommandExecuteDraft CampaignCommandKind = "execute-draft"
)

// CampaignCommand — доменное представление входящей команды кампании.
type CampaignCommand struct {
	CommandID  CommandID
	Kind       CampaignCommandKind
	TenantID   TenantID
	CampaignID CampaignID
	DraftID    DraftID
}

// ApprovalRequirement — результат политики operator-approval-required.
type ApprovalRequirement struct {
	// Kind — вид команды, к которой относится требование.
	Kind CampaignCommandKind

	// RequiresApproval — обязано ли действие быть подкреплено положительным
	// решением до исполнения.
	RequiresApproval bool

	// GateEvent — событие-шлюз, которым решение выражается. Политика
	// контекста связывает любую команду кампании с action-approved.
	GateEvent EventName

	// Reason — основание требования в терминах домена.
	Reason string
}

// OperatorApprovalRequired — политика operator-approval-required
// (campaign-command → action-approved).
//
// Чистая функция: не имеет побочных эффектов, не читает время и не обращается
// к хранилищу. По виду команды она сообщает, требуется ли до исполнения
// исходящего действия событие action-approved, и называет это событие.
// Команды решения (утверждение, отклонение) сами порождают action-approved и
// поэтому дополнительного шлюза не требуют; команды, ведущие к исходящему
// действию, требуют.
func OperatorApprovalRequired(cmd CampaignCommand) (ApprovalRequirement, error) {
	req := ApprovalRequirement{Kind: cmd.Kind, GateEvent: EventActionApproved}

	switch cmd.Kind {
	case CampaignCommandCreateCampaign:
		if err := cmd.TenantID.validate(); err != nil {
			return ApprovalRequirement{}, err
		}
		req.RequiresApproval = false
		req.Reason = "создание кампании исходящего действия не выполняет"
		return req, nil

	case CampaignCommandDraftAction:
		if err := validateCommandScope(cmd); err != nil {
			return ApprovalRequirement{}, err
		}
		req.RequiresApproval = true
		req.Reason = "черновик становится исходящим действием только после решения оператора"
		return req, nil

	case CampaignCommandReviseDraft:
		if err := validateCommandScope(cmd); err != nil {
			return ApprovalRequirement{}, err
		}
		req.RequiresApproval = true
		req.Reason = "изменённый черновик требует нового решения по новой версии"
		return req, nil

	case CampaignCommandExecuteDraft:
		if err := validateCommandScope(cmd); err != nil {
			return ApprovalRequirement{}, err
		}
		req.RequiresApproval = true
		req.Reason = "исполнение допустимо только при положительном решении по точной версии черновика"
		return req, nil

	case CampaignCommandApproveDraft:
		if err := validateCommandScope(cmd); err != nil {
			return ApprovalRequirement{}, err
		}
		req.RequiresApproval = false
		req.Reason = "команда сама является решением и порождает action-approved"
		return req, nil

	case CampaignCommandRejectDraft:
		if err := validateCommandScope(cmd); err != nil {
			return ApprovalRequirement{}, err
		}
		req.RequiresApproval = false
		req.Reason = "отклонение исходящего действия не порождает"
		return req, nil

	default:
		return ApprovalRequirement{}, fmt.Errorf("%w: %q", ErrUnknownCampaignCommand, cmd.Kind)
	}
}

// validateCommandScope проверяет ссылки команды, адресованной конкретному
// черновику внутри кампании.
func validateCommandScope(cmd CampaignCommand) error {
	if err := cmd.TenantID.validate(); err != nil {
		return err
	}
	if err := cmd.CampaignID.validate(); err != nil {
		return err
	}
	return cmd.DraftID.validate()
}
