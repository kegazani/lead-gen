package access

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
	"lead-gen/contracts/platform-governance/accessdecision"
)

// Contract — тонкий перевод типов к опубликованному контракту access-decision
// владельца platform-governance.
//
// Потребитель держит форму контракта (accessdecision.Decider) и не импортирует
// внутренние пакеты владельца; решение о доступе выдаёт владелец. Адаптер лишь
// переводит доменные типы потребителя в форму контракта и сохраняет прежнюю
// семантику пустого субъекта: пустые актор или тенант — отказ инфраструктуры, а
// не положительное решение.
type Contract struct {
	decider accessdecision.Decider
}

// NewContract связывает контракт access-decision с портом потребителя. nil-
// реализация отвергается ошибкой сборки: молчаливое «разрешено» недопустимо.
func NewContract(decider accessdecision.Decider) (*Contract, error) {
	if decider == nil {
		return nil, errors.New("access: не задана реализация контракта access-decision")
	}
	return &Contract{decider: decider}, nil
}

// CanAct реализует ports.AccessDecision. Ошибка контракта (в том числе
// недоступность владельца) пробрасывается вызывающему и не превращается в
// положительное решение.
func (c *Contract) CanAct(ctx context.Context, actor domain.ActorID, tenant domain.TenantID) (bool, error) {
	if err := subjectError(actor, tenant); err != nil {
		return false, err
	}
	return c.decider.CanAct(ctx, string(actor), string(tenant))
}

// Проверка соответствия порту потребителя на этапе компиляции.
var _ ports.AccessDecision = (*Contract)(nil)
