package contracts

import (
	"fmt"
	"strings"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// Контракт approved-action не объявляется здесь заново: единственный источник
// истины — опубликованный модуль
// lead-gen/contracts/outreach-orchestration/approvedaction. Ниже — реэкспорт его
// типов и констант, поэтому существующие места вызова (contracts.ApprovedAction,
// contracts.NewApprovedAction, contracts.Envelope{...}) продолжают компилироваться,
// а wire-форма определена ровно один раз.

// ContractApprovedAction — имя публикуемого контракта. Реэкспорт
// approvedaction.ContractName.
const ContractApprovedAction = approvedaction.ContractName

// ApprovedActionSchemaVersion — текущая версия схемы. Реэкспорт
// approvedaction.SchemaVersion; изменять по правилам semver из envelope.go.
const ApprovedActionSchemaVersion = approvedaction.SchemaVersion

// ApprovedAction — публикуемый контракт утверждённого человеком-оператором
// действия. Реэкспорт approvedaction.ApprovedAction: определение структуры, её
// валидация формы и ключ идемпотентности принадлежат опубликованному модулю.
type ApprovedAction = approvedaction.ApprovedAction

// NewApprovedAction возвращает контракт с проставленными именем и версией схемы.
// Реэкспорт approvedaction.NewApprovedAction.
func NewApprovedAction(env Envelope) ApprovedAction {
	return approvedaction.NewApprovedAction(env)
}

// ValidateApprovedAction проверяет контракт approved-action на внутренней границе
// контекста. Сначала применяется формальная валидация опубликованного контракта
// (approvedaction.ApprovedAction.Validate), затем канал дополнительно сверяется с
// доменным VO action-channel.
//
// Почему обёртка, а не голый публичный Validate. Публичный Validate проверяет
// канал по закрытому набору строк, объявленному в самом контракте. Внутренний
// путь публикации (relay→outbox, transport) исторически принимал контракт только
// тогда, когда канал допускает ДОМЕН владельца (domain.ParseActionChannel), а не
// совпадающая копия набора строк. После перехода на единственное определение
// wire-формы эту доменную проверку больше негде выразить методом (метод алиаса
// принадлежит публичному модулю, который не вправе импортировать домен), поэтому
// она сохранена здесь явно — так проверка не ослаблена и домен остаётся
// последней инстанцией о допустимости канала.
func ValidateApprovedAction(action ApprovedAction) error {
	if err := action.Validate(); err != nil {
		return err
	}
	if _, err := domain.ParseActionChannel(action.Channel); err != nil {
		return fmt.Errorf("contracts: approved-action: некорректный channel: %w", err)
	}
	return nil
}

// FromActionApproved отображает доменное событие action-approved в контракт
// approved-action. Это единственное определение отображения: его используют и
// relay (публикация утверждённого действия), и чтение решения по черновику
// (draftdecision), поэтому поля и ключ идемпотентности совпадают.
//
// EventID намеренно не заполняется: при публикации его проставляет relay
// идентификатором записи outbox, а при чтении решения вызывающий задаёт свой
// стабильный идентификатор (само событие снимается из агрегата, а не из outbox).
// CorrelationID — кампания как сквозной бизнес-процесс, CausationID — решение
// оператора.
//
// ApprovedActionID строится кампанийно-уникальным
// (approvedaction.ApprovedActionIdentity): черновик адресует получателя и
// повторяется между кампаниями к одному получателю, поэтому без кампании
// идентификаторы разных действий столкнулись бы и получатель счёл бы второе
// действие повтором первого. Повтор одного действия даёт тот же идентификатор.
//
// RecipientRef заполняется непрозрачной ссылкой на получателя черновика из
// события. Устаревшие события без получателя сохраняют прежнее поведение: ссылка
// на черновик становится ссылкой на получателя (совместимость со старым outbox).
// Получатель разрешает её на своей стороне. MaterialVersionRef заполняется ссылкой
// на материал утверждённого черновика: без неё материал письма не разрешается и
// письмо уходит пустым.
func FromActionApproved(approved domain.ActionApproved) ApprovedAction {
	action := NewApprovedAction(Envelope{
		OccurredAt:    approved.At,
		TenantID:      approved.TenantID.String(),
		CorrelationID: approved.CampaignID.String(),
		CausationID:   approved.DecisionID.String(),
	})
	action.ApprovedActionID = approvedaction.ApprovedActionIdentity(approved.CampaignID.String(), approved.DraftID.String(), int64(approved.DraftVersion))
	action.CampaignID = approved.CampaignID.String()
	action.DraftID = approved.DraftID.String()
	action.DraftVersion = int64(approved.DraftVersion)
	action.DecisionID = approved.DecisionID.String()
	action.Channel = approved.Channel.String()
	action.RecipientRef = string(approved.Recipient)
	if strings.TrimSpace(action.RecipientRef) == "" {
		action.RecipientRef = approved.DraftID.String()
	}
	action.MaterialVersionRef = approved.Material.String()
	action.DecidedAt = approved.At
	return action
}
