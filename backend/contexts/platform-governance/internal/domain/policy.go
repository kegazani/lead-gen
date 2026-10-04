package domain

import "time"

// Права доступа платформы. Право — не сущность и не данные: это знание домена,
// задаваемое политикой least-privilege (role → права). Сущности «право» в
// каталоге нет, поэтому расширение перечня — правка домена с тестами.
type Right string

const (
	// RightManageTenants — создание и отключение тенантов.
	RightManageTenants Right = "manage-tenants"

	// RightManageRoles — назначение и отзыв ролей пользователям тенанта.
	RightManageRoles Right = "manage-roles"

	// RightUsePortal — самообслуживание клиента: подача заявки и просмотр её
	// состояния.
	RightUsePortal Right = "use-portal"
)

// roleRights — политика least-privilege: какие права даёт каждая роль. Роль без
// записи не существует (см. Role.isValid).
var roleRights = map[Role][]Right{
	RoleAdmin:  {RightManageTenants, RightManageRoles, RightUsePortal},
	RoleClient: {RightUsePortal},
}

// RightsOf возвращает копию прав роли по политике least-privilege. Неизвестная
// роль прав не даёт.
func RightsOf(role Role) []Right {
	return append([]Right(nil), roleRights[role]...)
}

// RoleSetHasRight сообщает, даёт ли набор ролей право. Права, не следующие из
// назначенных ролей, не предоставляются: проверка идёт только по составу набора.
//
// Функция чистая: она не читает хранилище и не имеет побочных эффектов, поэтому
// политику least-privilege можно проверить unit-тестом без адаптеров.
func RoleSetHasRight(roles RoleSet, right Right) bool {
	for _, role := range roles.Roles() {
		for _, granted := range roleRights[role] {
			if granted == right {
				return true
			}
		}
	}
	return false
}

// TenantIsolationParams — вход политики tenant-isolation: действие в контексте
// тенанта (каталог: вход — контракт tenant-context, действие — audit-recorded).
type TenantIsolationParams struct {
	// TenantID — контекст тенанта. Пустой идентификатор политикой отвергается:
	// действие вне тенанта невозможно.
	TenantID TenantID
	// Actor — субъект действия.
	Actor ActorID
	// Action — значимое действие.
	Action AuditAction
	// Object — объект действия (например, пользователь).
	Object string
	// Outcome — результат действия.
	Outcome AuditOutcome
	// At — время действия.
	At time.Time
	// AuditRecordID — идентификатор записи аудита.
	AuditRecordID AuditRecordID
}

// TenantIsolation выполняет политику tenant-isolation: проверяет контекст
// тенанта и превращает действие в неизменяемую запись аудита. Функция чистая —
// она не читает хранилище и не имеет побочных эффектов (задача 1.9).
//
// Действие без идентификатора тенанта политикой отвергается: запись аудита вне
// границы тенанта не создаётся.
func TenantIsolation(params TenantIsolationParams) (AuditRecord, error) {
	if err := params.TenantID.validate(); err != nil {
		return AuditRecord{}, err
	}
	return newAuditRecord(params.AuditRecordID, params.Actor, params.Action, params.Object, params.At, params.Outcome, "")
}
