package domain

import "time"

// AuditAction — значимое действие, фиксируемое в журнале аудита.
type AuditAction string

const (
	// ActionTenantCreated — создание тенанта.
	ActionTenantCreated AuditAction = "tenant-created"
	// ActionTenantDisabled — отключение тенанта.
	ActionTenantDisabled AuditAction = "tenant-disabled"
	// ActionRegistrationApproved — заявка на регистрацию одобрена.
	ActionRegistrationApproved AuditAction = "registration-approved"
	// ActionRegistrationRejected — заявка на регистрацию отклонена.
	ActionRegistrationRejected AuditAction = "registration-rejected"
	// ActionProfileApproved — профиль компании подтверждён.
	ActionProfileApproved AuditAction = "profile-approved"
	// ActionProfileRejected — профиль компании отклонён.
	ActionProfileRejected AuditAction = "profile-rejected"
	// ActionRoleAssigned — назначение роли пользователю.
	ActionRoleAssigned AuditAction = "role-assigned"
	// ActionRoleRevoked — отзыв роли у пользователя.
	ActionRoleRevoked AuditAction = "role-revoked"
	// ActionAuditCorrected — корректировка прежней записи аудита.
	ActionAuditCorrected AuditAction = "audit-corrected"
)

// auditActions — закрытый перечень значимых действий.
var auditActions = map[AuditAction]struct{}{
	ActionTenantCreated:        {},
	ActionTenantDisabled:       {},
	ActionRegistrationApproved: {},
	ActionRegistrationRejected: {},
	ActionProfileApproved:      {},
	ActionProfileRejected:      {},
	ActionRoleAssigned:         {},
	ActionRoleRevoked:          {},
	ActionAuditCorrected:       {},
}

// isValid сообщает, что действие объявлено доменом.
func (a AuditAction) isValid() bool {
	_, ok := auditActions[a]
	return ok
}

// AuditOutcome — результат значимого действия.
type AuditOutcome string

const (
	// OutcomeSucceeded — действие выполнено.
	OutcomeSucceeded AuditOutcome = "succeeded"
	// OutcomeRejected — действие отклонено.
	OutcomeRejected AuditOutcome = "rejected"
)

// isValid сообщает, что результат объявлен доменом.
func (o AuditOutcome) isValid() bool {
	return o == OutcomeSucceeded || o == OutcomeRejected
}

// AuditRecord — entity «запись аудита»: неизменяемый факт значимого действия
// (субъект, действие, объект, время, результат).
//
// Поля закрыты, сеттеров нет: изменить существующую запись невозможно.
// Корректировка выполняется только новой записью со ссылкой на исходную
// (CorrectionOf), а не изменением прежней.
type AuditRecord struct {
	id           AuditRecordID
	actor        ActorID
	action       AuditAction
	object       string
	at           time.Time
	outcome      AuditOutcome
	correctionOf AuditRecordID
}

// newAuditRecord собирает запись аудита, проверяя обязательные поля: субъект,
// известное действие, результат, время и идентификатор. Ссылка на исходную
// запись задаётся только для корректирующей записи.
func newAuditRecord(
	id AuditRecordID,
	actor ActorID,
	action AuditAction,
	object string,
	at time.Time,
	outcome AuditOutcome,
	correctionOf AuditRecordID,
) (AuditRecord, error) {
	if err := id.validate(); err != nil {
		return AuditRecord{}, err
	}
	if err := actor.validate(); err != nil {
		return AuditRecord{}, err
	}
	if !action.isValid() {
		return AuditRecord{}, ErrUnknownAuditAction
	}
	if !outcome.isValid() {
		return AuditRecord{}, ErrUnknownAuditOutcome
	}
	if at.IsZero() {
		return AuditRecord{}, ErrMissingTime
	}
	if correctionOf != "" {
		if err := correctionOf.validate(); err != nil {
			return AuditRecord{}, err
		}
	}
	return AuditRecord{
		id:           id,
		actor:        actor,
		action:       action,
		object:       object,
		at:           at,
		outcome:      outcome,
		correctionOf: correctionOf,
	}, nil
}

// ID возвращает идентификатор записи.
func (r AuditRecord) ID() AuditRecordID { return r.id }

// Actor возвращает субъект действия.
func (r AuditRecord) Actor() ActorID { return r.actor }

// Action возвращает действие.
func (r AuditRecord) Action() AuditAction { return r.action }

// Object возвращает объект действия.
func (r AuditRecord) Object() string { return r.object }

// At возвращает время действия.
func (r AuditRecord) At() time.Time { return r.at }

// Outcome возвращает результат действия.
func (r AuditRecord) Outcome() AuditOutcome { return r.outcome }

// CorrectionOf возвращает идентификатор исходной записи для корректирующей
// записи; пустое значение — запись исходная.
func (r AuditRecord) CorrectionOf() AuditRecordID { return r.correctionOf }

// IsCorrection сообщает, что запись корректирует прежнюю.
func (r AuditRecord) IsCorrection() bool { return r.correctionOf != "" }
