package contracts

import (
	"fmt"

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
