package ports

import (
	"context"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// Grants — платформенные права актора (гранты). Актор, управляющий платформой
// (создание и отключение тенантов, назначение ролей), получает их вне тенантов:
// это начальное состояние, которое задаёт вызывающий при сборке сервиса
// (design.md, Решение 8). Порт не знает о токенах, сессиях и claims —
// аутентификация вне среза.
type Grants interface {
	// Roles возвращает набор платформенных ролей актора. Неизвестный актор
	// получает пустой набор, а не ошибку: право решает политика least-privilege.
	Roles(ctx context.Context, actor domain.ActorID) (domain.RoleSet, error)
}
