package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/data-governance/internal/domain"
)

// ErrAccessUnavailable — источник решения о доступе не подключён: проверить
// право невозможно. Это отказ инфраструктуры, а не отрицательное решение.
var ErrAccessUnavailable = errors.New("порты: источник решения о доступе недоступен")

// AccessDecision — потребляемое решение о доступе актора к тенанту.
//
// Полная семантика RBAC принадлежит контексту platform-governance и приходит
// контрактом access-decision; здесь объявляется только порт, чтобы сценарии роли
// api не воспроизводили проверку прав локально (docs/domain-model.md).
type AccessDecision interface {
	// CanAct сообщает, вправе ли актор действовать в тенанте. Ошибка означает
	// отказ инфраструктуры проверить право, а не отрицательное решение.
	CanAct(ctx context.Context, actor domain.ActorID, tenant domain.TenantID) (bool, error)
}
