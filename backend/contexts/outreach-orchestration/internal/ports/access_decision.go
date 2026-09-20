package ports

import (
	"context"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// AccessDecision — порт проверки права актора действовать в тенанте.
//
// В срезе подключается минимальная реализация, проверяющая право оператора в
// пределах тенанта; полная семантика RBAC остаётся за контекстом
// platform-governance. Порт не знает о ролях, claims и токенах: наружу
// отдаётся только решение о доступе.
type AccessDecision interface {
	// CanAct сообщает, вправе ли актор действовать в тенанте. Ошибка означает
	// отказ инфраструктуры проверить право, а не отрицательное решение.
	CanAct(ctx context.Context, actor domain.ActorID, tenant domain.TenantID) (bool, error)
}
