// Package grants реализует порт платформенных прав актора поверх оперативной
// памяти.
//
// Начальное состояние (первый актор с ролью управления платформой) задаёт
// вызывающий при сборке сервиса — конфигурацией процесса, а не выдуманной
// политикой composition (design.md, Решение 8). Пакет не знает о токенах,
// сессиях и claims: аутентификация вне среза, права задаются явно.
package grants

import (
	"context"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// Grants — таблица «актор → набор ролей» в памяти. Безопасна для конкурентного
// чтения: после сборки не меняется.
type Grants struct {
	actors map[domain.ActorID]domain.RoleSet
}

// Grants реализует порт платформенных прав.
var _ ports.Grants = (*Grants)(nil)

// New собирает таблицу прав из отображения «актор → роли». Неизвестная роль —
// ошибка сборки: расхождение конфигурации и домена должно быть видно на старте,
// а не превращаться в тихий отказ.
func New(actors map[string][]string) (*Grants, error) {
	table := make(map[domain.ActorID]domain.RoleSet, len(actors))
	for actor, roles := range actors {
		trimmed := domain.ActorID(strings.TrimSpace(actor))
		if trimmed == "" {
			continue
		}
		set, err := domain.NewRoleSet(toRoles(roles)...)
		if err != nil {
			return nil, err
		}
		table[trimmed] = set
	}
	return &Grants{actors: table}, nil
}

// FromStrings собирает таблицу прав из одной пары «актор → роли». Удобно для
// конфигурации процесса с единственным администратором.
func FromStrings(actor string, roles []string) (*Grants, error) {
	return New(map[string][]string{actor: roles})
}

// Roles возвращает набор платформенных ролей актора. Неизвестный актор получает
// пустой набор, а не ошибку: право решает политика least-privilege.
func (g *Grants) Roles(_ context.Context, actor domain.ActorID) (domain.RoleSet, error) {
	if g == nil {
		return domain.RoleSet{}, nil
	}
	return g.actors[actor], nil
}

// toRoles переводит строки конфигурации в доменные роли.
func toRoles(roles []string) []domain.Role {
	parsed := make([]domain.Role, 0, len(roles))
	for _, role := range roles {
		trimmed := strings.TrimSpace(role)
		if trimmed == "" {
			continue
		}
		parsed = append(parsed, domain.Role(trimmed))
	}
	return parsed
}
