package platformgovernance

import (
	"lead-gen/backend/contexts/platform-governance/internal/adapters/grants"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// GrantsFromStrings собирает порт платформенных прав из пары «актор → роли» для
// точки входа. Неизвестная роль — ошибка сборки сервиса.
func GrantsFromStrings(actor string, roles []string) (ports.Grants, error) {
	return grants.FromStrings(actor, roles)
}

// OperatorGrants собирает платформенные права консольного оператора: актор
// получает роль администратора платформы. Публичная точка для точки входа, чтобы
// значение доменной роли не дублировалось в конфигурации процесса.
func OperatorGrants(operator string) (ports.Grants, error) {
	return grants.FromStrings(operator, []string{string(domain.RoleAdmin)})
}
