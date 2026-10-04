package ports

import (
	"context"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// AccessDecision — порт проверки права актора действовать в тенанте. Форма
// совпадает с портом потребителя (outreach-orchestration): владелец реализует
// этот интерфейс, потребитель не воспроизводит RBAC локально.
//
// При co-location потребитель связывается с владельцем in-process адаптером,
// который применяет решение владельца напрямую; сетевой адаптер (подписка на
// снимки access-decision) добавляется при разделении сервисов и форму порта не
// меняет.
type AccessDecision interface {
	// CanAct сообщает, вправе ли актор действовать в тенанте. Ошибка означает
	// отказ инфраструктуры проверить право (в том числе пустой субъект), а не
	// отрицательное решение.
	CanAct(ctx context.Context, actor domain.ActorID, tenant domain.TenantID) (bool, error)
}
