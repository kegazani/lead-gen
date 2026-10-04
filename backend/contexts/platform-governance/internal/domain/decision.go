package domain

import "time"

// TenantState — состояние тенанта.
type TenantState string

const (
	// TenantActive — тенант активен и выдаёт решения доступа.
	TenantActive TenantState = "active"
	// TenantDisabled — тенант отключён и новых решений доступа не выдаёт.
	TenantDisabled TenantState = "disabled"
)

// isValid сообщает, что состояние объявлено доменом.
func (s TenantState) isValid() bool {
	return s == TenantActive || s == TenantDisabled
}

// AccessAssignment — правило снимка правил: набор ролей актора в тенанте.
type AccessAssignment struct {
	// Actor — пользователь тенанта.
	Actor ActorID
	// Roles — роли, назначенные актору.
	Roles []Role
}

// AccessDecisionSnapshot — снимок правил тенанта, публикуемый контрактом
// access-decision (policy).
//
// Потребитель применяет снимок локально и механически сверяет с ним запрос, не
// воспроизводя RBAC: состояние тенанта, монотонная версия снимка и список
// «актор → роли». Поля экспортированы: снимок — опубликованный язык владельца, а
// не внутреннее состояние агрегата.
type AccessDecisionSnapshot struct {
	// TenantID — тенант, к которому относятся правила.
	TenantID TenantID
	// State — состояние тенанта: отключённый тенант новых решений не выдаёт.
	State TenantState
	// Version — монотонная версия снимка. Ключ идемпотентности потребителя —
	// (TenantID, Version).
	Version uint64
	// Assignments — правила «актор → набор ролей».
	Assignments []AccessAssignment
	// OccurredAt — время формирования снимка.
	OccurredAt time.Time
}
