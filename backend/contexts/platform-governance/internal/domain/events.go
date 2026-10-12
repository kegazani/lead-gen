package domain

import "time"

// EventName — имя domain event. Имена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемый контракт оборачивает событие в
// конверт на границе адаптера.
type EventName string

const (
	// EventTenantAccessChanged — доступ тенанта изменён: тенант создан или
	// отключён, роли назначены или отозваны.
	EventTenantAccessChanged EventName = "tenant-access-changed"

	// EventAuditRecorded — значимое действие зафиксировано в журнале аудита.
	EventAuditRecorded EventName = "audit-recorded"
)

// EventHeader — обязательные поля события тенант-доступа. Каждое доменное
// событие несёт их: event_id (ключ дедупликации), occurred_at и tenant_id.
// correlation_id и causation_id необязательны и заполняются, если действие
// инициировано в рамках сквозного сценария.
type EventHeader struct {
	EventID       EventID
	OccurredAt    time.Time
	TenantID      TenantID
	CorrelationID string
	CausationID   string
}

// validate проверяет обязательные поля конверта события.
func (h EventHeader) validate() error {
	if err := h.EventID.validate(); err != nil {
		return err
	}
	if err := h.TenantID.validate(); err != nil {
		return err
	}
	if h.OccurredAt.IsZero() {
		return ErrMissingTime
	}
	return nil
}

// DomainEvent — факт домена, произведённый агрегатом.
type DomainEvent interface {
	// Name возвращает имя события.
	Name() EventName

	// OccurredAt возвращает время события.
	OccurredAt() time.Time

	// Header возвращает обязательные поля конверта события.
	Header() EventHeader
}

// AccessChangeKind — характер изменения доступа в событии
// tenant-access-changed.
type AccessChangeKind string

const (
	// AccessTenantCreated — тенант создан.
	AccessTenantCreated AccessChangeKind = "tenant-created"
	// AccessUserAdded — пользователь добавлен в тенант.
	AccessUserAdded AccessChangeKind = "user-added"
	// AccessRoleAssigned — роль назначена.
	AccessRoleAssigned AccessChangeKind = "role-assigned"
	// AccessRoleRevoked — роль отозвана.
	AccessRoleRevoked AccessChangeKind = "role-revoked"
	// AccessTenantDisabled — тенант отключён.
	AccessTenantDisabled AccessChangeKind = "tenant-disabled"
	// AccessTenantRegistered — заявка на регистрацию одобрена, тенант активен.
	AccessTenantRegistered AccessChangeKind = "tenant-registered"
	// AccessTenantRegistrationRejected — заявка на регистрацию отклонена.
	AccessTenantRegistrationRejected AccessChangeKind = "tenant-registration-rejected"
)

// TenantAccessChanged — доступ тенанта изменён. Поля экспортированы: событие —
// источник снимка правил для контракта access-decision.
type TenantAccessChanged struct {
	EventHeader
	// UserID — пользователь, чьи роли изменились; пусто для изменения
	// состояния тенанта.
	UserID UserID
	// Roles — роли пользователя после изменения.
	Roles []Role
	// Change — характер изменения.
	Change AccessChangeKind
}

// Name реализует DomainEvent.
func (e TenantAccessChanged) Name() EventName { return EventTenantAccessChanged }

// OccurredAt реализует DomainEvent.
func (e TenantAccessChanged) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e TenantAccessChanged) Header() EventHeader { return e.EventHeader }

// AuditRecorded — значимое действие зафиксировано в журнале аудита. Поля
// экспортированы: событие — источник контракта audit-recorded.
type AuditRecorded struct {
	EventHeader
	// AuditRecordID — идентификатор записи аудита; ключ идемпотентности
	// потребителя — (TenantID, AuditRecordID).
	AuditRecordID AuditRecordID
	// Actor — субъект действия.
	Actor ActorID
	// Action — действие.
	Action AuditAction
	// Object — объект действия.
	Object string
	// Outcome — результат действия.
	Outcome AuditOutcome
	// CorrectionOf — исходная запись для корректирующей записи.
	CorrectionOf AuditRecordID
}

// Name реализует DomainEvent.
func (e AuditRecorded) Name() EventName { return EventAuditRecorded }

// OccurredAt реализует DomainEvent.
func (e AuditRecorded) OccurredAt() time.Time { return e.EventHeader.OccurredAt }

// Header реализует DomainEvent.
func (e AuditRecorded) Header() EventHeader { return e.EventHeader }
