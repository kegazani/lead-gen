package platformgovernance

import (
	"context"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/contracts/platform-governance/accessdecision"
)

// Привязка опубликованного контракта access-decision к решению владельца.
//
// Корневой пакет контекста — composition root: он связывает потребительский
// интерфейс контракта (accessdecision.Decider) с use case решения доступа
// (Slice.DecideAccess). Никаких бизнес-правил здесь нет: решение выдаёт домен
// агрегата tenant через use case владельца, а этот файл лишь переводит форму
// контракта (строковые actor/tenant) в доменные типы.

// NewAccessDecision связывает контракт access-decision с решением владельца.
//
// Если срез не собран, возвращается честный недоступный решатель: потребитель
// получит ошибку и ответит отказом, а не получит выдуманное разрешение.
func NewAccessDecision(slice *Slice) accessdecision.Decider {
	if slice == nil || slice.DecideAccess == nil {
		return accessdecision.Unavailable{}
	}
	return accessDecision{handler: slice.DecideAccess}
}

// accessDecision реализует accessdecision.Decider поверх use case владельца.
type accessDecision struct {
	handler interface {
		CanAct(ctx context.Context, actor domain.ActorID, tenant domain.TenantID) (bool, error)
	}
}

// CanAct реализует accessdecision.Decider. Решение выдаёт владелец: RBAC
// потребителем не воспроизводится.
func (d accessDecision) CanAct(ctx context.Context, actor, tenant string) (bool, error) {
	return d.handler.CanAct(ctx, domain.ActorID(actor), domain.TenantID(tenant))
}

// Проверка соответствия контракту на этапе компиляции.
var _ accessdecision.Decider = accessDecision{}
