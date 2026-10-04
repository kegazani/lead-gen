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
	// TenantID — идентификатор aggregate root tenant. Стабильная граница
	// изоляции данных и доступа (VO tenant-id каталога).
	TenantID string

	// UserID — идентификатор пользователя тенанта (entity user-role).
	UserID string

	// EventID — стабильный идентификатор события домена. Служит ключом
	// дедупликации у потребителя.
	EventID string

	// ActorID — идентификатор субъекта, инициировавшего действие. Используется
	// в решениях доступа и записях аудита.
	ActorID string

	// CommandID — клиентский идентификатор изменяющей команды. Вместе с
	// TenantID составляет ключ идемпотентности (docs/architecture.md §6.1).
	CommandID string

	// AuditRecordID — идентификатор записи аудита. Стабилен: на него ссылается
	// корректирующая запись.
	AuditRecordID string

	// Role — роль пользователя внутри тенанта. Роль принадлежит ровно одному
	// тенанту: пользователь не переносится между тенантами.
	Role string
)

// Роли, объявленные доменом. Набор ролей закрыт: расширение — правка домена с
// тестами (design.md, Решение 1). Права ролей задаёт политика least-privilege
// (policy.go); сущности «роль» в каталоге нет, поэтому реестр — знание домена.
const (
	// RoleAdmin — роль управления платформой: создание и отключение тенантов,
	// назначение и отзыв ролей.
	RoleAdmin Role = "platform-admin"

	// RoleClient — роль клиента: пользователь самообслуживания портала.
	RoleClient Role = "client"
)

func (id TenantID) String() string      { return string(id) }
func (id UserID) String() string        { return string(id) }
func (id EventID) String() string       { return string(id) }
func (id ActorID) String() string       { return string(id) }
func (id CommandID) String() string     { return string(id) }
func (id AuditRecordID) String() string { return string(id) }
func (r Role) String() string           { return string(r) }

// isValid сообщает, что роль объявлена доменом.
func (r Role) isValid() bool {
	_, ok := roleRights[r]
	return ok
}

// Validate проверяет, что идентификатор тенанта заполнен.
func (id TenantID) Validate() error { return id.validate() }

// Validate проверяет, что идентификатор актора заполнен.
func (id ActorID) Validate() error { return id.validate() }

// Validate проверяет, что идентификатор пользователя заполнен.
func (id UserID) Validate() error { return id.validate() }

func (id TenantID) validate() error      { return validateID("tenant_id", string(id)) }
func (id UserID) validate() error        { return validateID("user_id", string(id)) }
func (id EventID) validate() error       { return validateID("event_id", string(id)) }
func (id ActorID) validate() error       { return validateID("actor_id", string(id)) }
func (id CommandID) validate() error     { return validateID("command_id", string(id)) }
func (id AuditRecordID) validate() error { return validateID("audit_record_id", string(id)) }

// validateID отвергает пустой или состоящий из пробелов идентификатор.
func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
