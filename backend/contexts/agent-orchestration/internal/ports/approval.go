package ports

import (
	"context"
	"errors"

	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// Подача черновика в очередь утверждения владельца (design.md, Реш. 6: «подача
// черновика в утверждение через существующий вход черновиков / campaign-command»).
// Контракт campaign-command владельца outreach-orchestration на момент реализации
// не опубликован, поэтому порт описывает минимальную форму, которой достаточно
// блоку await-approval; адаптер поверх опубликованного контракта заменит
// инъектируемую реализацию без изменения блока.
//
// Порт несёт только непрозрачные ссылки: персональные данные и содержимое письма
// через него не проходят.
var (
	// ErrApprovalSubmitRejected — черновик отклонён владельцем по доменной причине
	// (пустая или непригодная форма). Отличается от сбоя транспорта.
	ErrApprovalSubmitRejected = errors.New("approval: подача на утверждение отклонена владельцем")
	// ErrApprovalSubmitUnavailable — владелец утверждения недоступен: контракт не
	// подключён или сбой транспорта. Потребитель не выдаёт это за приём.
	ErrApprovalSubmitUnavailable = errors.New("approval: владелец утверждения недоступен")
)

// ApprovalSubmission — черновик, подаваемый в очередь утверждения оператором.
type ApprovalSubmission struct {
	TenantID     string
	RunID        string
	MissionID    string
	CompanyID    string
	DraftRef     string
	DraftVersion int
	// MaterialRef — непрозрачная ссылка на материал черновика (asset@version),
	// из которого владелец разрешает тему и тело письма.
	MaterialRef string
}

// ApprovalSubmitter — подача черновика в очередь утверждения. Отсутствие
// реализации (nil) означает честную недоступность блока await-approval.
type ApprovalSubmitter interface {
	SubmitForApproval(ctx context.Context, in ApprovalSubmission) error
}

// Источник утверждённого действия для шага отправки: по непрозрачной ссылке
// черновика возвращает утверждённое оператором действие (владелец —
// outreach-orchestration; решение публикуется в message-delivery контрактом
// approved-action). Отдельного read-контракта решения пока нет, поэтому порт —
// точка подключения блока send; реализация честна в различении «решение не
// принято», «отказ» и «источник недоступен».
var (
	// ErrApprovedActionPending — решение по черновику ещё не принято.
	ErrApprovedActionPending = errors.New("approved-action: решение по черновику ещё не принято")
	// ErrApprovedActionRejected — оператор отклонил черновик.
	ErrApprovedActionRejected = errors.New("approved-action: оператор отклонил черновик")
	// ErrApprovedActionUnavailable — источник решения недоступен.
	ErrApprovedActionUnavailable = errors.New("approved-action: источник решения недоступен")
)

// ApprovedActionLookup — вход запроса утверждённого действия по черновику.
type ApprovedActionLookup struct {
	TenantID     string
	DraftRef     string
	DraftVersion int
}

// ApprovedActionSource — источник утверждённого действия. Отсутствие реализации
// (nil) означает честную недоступность блока отправки.
type ApprovedActionSource interface {
	ApprovedAction(ctx context.Context, in ApprovedActionLookup) (approvedaction.ApprovedAction, error)
}
