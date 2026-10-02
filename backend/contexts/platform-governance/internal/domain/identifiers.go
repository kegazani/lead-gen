package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы домена platform-governance.
//
// Каждый идентификатор — отдельный именованный тип, чтобы тенант нельзя было
// передать вместо пользователя, а роль — вместо события. Значения неизменяемы.
type (
	// TenantID — идентификатор aggregate root tenant.
	TenantID string

	// UserID — идентификатор пользователя тенанта.
	UserID string

	// EventID — стабильный идентификатор события домена. Служит ключом
	// дедупликации у потребителя.
	EventID string

	// Role — роль пользователя внутри тенанта. Роль принадлежит ровно одному
	// тенанту: пользователь не переносится между тенантами.
	Role string
)

// RoleClient — роль клиента: пользователь самообслуживания портала.
const RoleClient Role = "client"

func (id TenantID) String() string { return string(id) }
func (id UserID) String() string   { return string(id) }
func (id EventID) String() string  { return string(id) }
func (r Role) String() string      { return string(r) }

// isValid сообщает, что роль объявлена доменом.
func (r Role) isValid() bool { return r == RoleClient }

func (id TenantID) validate() error { return validateID("tenant_id", string(id)) }
func (id UserID) validate() error   { return validateID("user_id", string(id)) }
func (id EventID) validate() error  { return validateID("event_id", string(id)) }

// validateID отвергает пустой или состоящий из пробелов идентификатор.
func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
