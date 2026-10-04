package domain

import (
	"fmt"
	"strings"
	"time"
)

// TenantUser — пользователь тенанта (entity user-role).
//
// Сущность внутри агрегата tenant: идентификатор уникален в пределах тенанта,
// логин и набор ролей обязательны. Инвариант «роль принадлежит ровно одному
// тенанту» означает, что пользователь не переносится между тенантами — попытка
// добавить того же пользователя в другой агрегат невозможна, потому что
// репозиторий адресует агрегат по тенанту.
type TenantUser struct {
	id    UserID
	login string
	roles RoleSet
}

// ID возвращает идентификатор пользователя.
func (u TenantUser) ID() UserID { return u.id }

// Login возвращает логин пользователя.
func (u TenantUser) Login() string { return u.login }

// Roles возвращает набор ролей пользователя.
func (u TenantUser) Roles() RoleSet { return u.roles }

// NewUser — данные нового пользователя тенанта.
type NewUser struct {
	// ID — идентификатор пользователя.
	ID UserID
	// Login — логин пользователя.
	Login string
	// Roles — начальный набор ролей (непустой).
	Roles RoleSet
}

// RegisterTenantParams — данные создания тенанта вместе с необязательным первым
// пользователем.
type RegisterTenantParams struct {
	// ID — идентификатор создаваемого тенанта (обязателен).
	ID TenantID
	// CompanyName — название компании клиента (обязательно).
	CompanyName string
	// Description — описание компании клиента (обязательно).
	Description string
	// At — время создания (UTC).
	At time.Time
	// Actor — субъект создания: уполномоченный актор либо сам клиент при
	// самообслуживании.
	Actor ActorID
	// EventID — стабильный идентификатор события tenant-access-changed.
	EventID EventID
	// AuditID — идентификатор записи аудита о создании.
	AuditID AuditRecordID
	// User — необязательный первый пользователь тенанта.
	User *NewUser
	// CorrelationID, CausationID — необязательные поля сквозного сценария.
	CorrelationID string
	CausationID   string
}

// ChangeContext — параметры значимого изменения доступа: субъект, время и
// идентификаторы события и записи аудита.
type ChangeContext struct {
	At            time.Time
	Actor         ActorID
	EventID       EventID
	AuditID       AuditRecordID
	CorrelationID string
	CausationID   string
}

// header собирает конверт события изменения доступа.
func (c ChangeContext) header(tenant TenantID) EventHeader {
	return EventHeader{
		EventID:       c.EventID,
		OccurredAt:    c.At,
		TenantID:      tenant,
		CorrelationID: c.CorrelationID,
		CausationID:   c.CausationID,
	}
}

// validate проверяет обязательные поля контекста изменения.
func (c ChangeContext) validate() error {
	if err := c.Actor.validate(); err != nil {
		return err
	}
	if err := c.EventID.validate(); err != nil {
		return err
	}
	if err := c.AuditID.validate(); err != nil {
		return err
	}
	if c.At.IsZero() {
		return ErrMissingTime
	}
	return nil
}

// Tenant — aggregate root контекста platform-governance.
//
// Агрегат владеет сведениями о компании, составом её пользователей, журналом
// аудита и обеспечивает инварианты:
//
//	I1 — тенант без названия и описания компании не создаётся;
//	I2 — роль принадлежит ровно одному тенанту: пользователь уникален в его
//	     составе и не переносится между тенантами;
//	I3 — роль пользователя объявлена доменом; набор ролей неизменяем;
//	I4 — каждое изменение доступа фиксируется событием tenant-access-changed,
//	     каждое значимое действие — записью аудита;
//	I5 — отключённый тенант не принимает новых назначений ролей и не выдаёт
//	     положительных решений доступа.
//
// Поля закрыты: состояние меняется только методами агрегата.
type Tenant struct {
	id          TenantID
	companyName string
	description string
	state       TenantState
	version     uint64
	users       []TenantUser
	audit       []AuditRecord
	createdAt   time.Time
	events      []DomainEvent
}

// RegisterTenant создаёт активный тенант, фиксируя создание одной записью
// аудита и одним событием tenant-access-changed. Необязательный первый
// пользователь входит в состав и отражается в том же событии создания.
// Неполная форма (пустое название или описание) не создаёт тенант.
func RegisterTenant(params RegisterTenantParams) (*Tenant, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	companyName := strings.TrimSpace(params.CompanyName)
	if companyName == "" {
		return nil, ErrCompanyNameRequired
	}
	description := strings.TrimSpace(params.Description)
	if description == "" {
		return nil, ErrDescriptionRequired
	}
	if params.At.IsZero() {
		return nil, ErrMissingTime
	}
	if err := params.Actor.validate(); err != nil {
		return nil, err
	}

	tenant := &Tenant{
		id:          params.ID,
		companyName: companyName,
		description: description,
		state:       TenantActive,
		createdAt:   params.At,
	}

	header := EventHeader{
		EventID:       params.EventID,
		OccurredAt:    params.At,
		TenantID:      params.ID,
		CorrelationID: params.CorrelationID,
		CausationID:   params.CausationID,
	}
	if err := header.validate(); err != nil {
		return nil, err
	}

	event := TenantAccessChanged{EventHeader: header, Change: AccessTenantCreated}
	if params.User != nil {
		user, err := tenant.prepareUser(*params.User)
		if err != nil {
			return nil, err
		}
		tenant.users = append(tenant.users, user)
		event.UserID = user.id
		event.Login = user.login
		event.Roles = user.roles.Roles()
	}
	tenant.version = 1
	tenant.events = append(tenant.events, event)
	if err := tenant.appendAudit(header, newAuditRecordOrPanic(params.AuditID, params.Actor,
		ActionTenantCreated, params.ID.String(), params.At, OutcomeSucceeded, "")); err != nil {
		return nil, err
	}
	return tenant, nil
}

// prepareUser проверяет данные пользователя и собирает сущность.
func (t *Tenant) prepareUser(user NewUser) (TenantUser, error) {
	if err := user.ID.validate(); err != nil {
		return TenantUser{}, err
	}
	login := strings.TrimSpace(user.Login)
	if login == "" {
		return TenantUser{}, ErrUserLoginRequired
	}
	if user.Roles.Empty() {
		return TenantUser{}, ErrRolesRequired
	}
	for _, existing := range t.users {
		if existing.id == user.ID {
			return TenantUser{}, fmt.Errorf("%w: %s", ErrDuplicateUser, user.ID)
		}
	}
	roles, err := NewRoleSet(user.Roles.Roles()...)
	if err != nil {
		return TenantUser{}, err
	}
	return TenantUser{id: user.ID, login: login, roles: roles}, nil
}

// AddUser добавляет пользователя в состав тенанта, фиксируя изменение доступа
// событием и записью аудита. Отключённый тенант пользователей не принимает.
func (t *Tenant) AddUser(change ChangeContext, user NewUser) error {
	if err := change.validate(); err != nil {
		return err
	}
	if t.state != TenantActive {
		return ErrTenantDisabled
	}
	prepared, err := t.prepareUser(user)
	if err != nil {
		return err
	}
	t.users = append(t.users, prepared)
	t.version++
	t.events = append(t.events, TenantAccessChanged{
		EventHeader: change.header(t.id),
		UserID:      prepared.id,
		Login:       prepared.login,
		Roles:       prepared.roles.Roles(),
		Change:      AccessUserAdded,
	})
	return t.appendAudit(change.header(t.id), newAuditRecordOrPanic(change.AuditID, change.Actor,
		ActionRoleAssigned, prepared.id.String(), change.At, OutcomeSucceeded, ""))
}

// ID возвращает идентификатор тенанта.
func (t *Tenant) ID() TenantID { return t.id }

// CompanyName возвращает название компании клиента.
func (t *Tenant) CompanyName() string { return t.companyName }

// Description возвращает описание компании клиента.
func (t *Tenant) Description() string { return t.description }

// State возвращает состояние тенанта.
func (t *Tenant) State() TenantState { return t.state }

// Version возвращает монотонную версию правил доступа (снимок access-decision).
func (t *Tenant) Version() uint64 { return t.version }

// Users возвращает копию состава пользователей в порядке добавления.
func (t *Tenant) Users() []TenantUser {
	return append([]TenantUser(nil), t.users...)
}

// AuditJournal возвращает копию журнала аудита в порядке добавления. Журнал
// неизменяем: элементы — значения с закрытыми полями.
func (t *Tenant) AuditJournal() []AuditRecord {
	return append([]AuditRecord(nil), t.audit...)
}

// CreatedAt возвращает время регистрации тенанта.
func (t *Tenant) CreatedAt() time.Time { return t.createdAt }

// DomainEvents возвращает копию неподтверждённых событий агрегата.
func (t *Tenant) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), t.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (t *Tenant) PullDomainEvents() []DomainEvent {
	if len(t.events) == 0 {
		return nil
	}
	out := t.events
	t.events = nil
	return out
}

// AssignRole назначает роль пользователю, создавая новый неизменяемый набор
// ролей. Инварианты: отключённый тенант назначений не принимает (I5); роль
// принадлежит ровно одному тенанту, поэтому пользователь обязан быть в составе
// этого тенанта (I2). Повторное назначение уже имеющейся роли — идемпотентный
// no-op без события.
func (t *Tenant) AssignRole(change ChangeContext, userID UserID, role Role) error {
	if err := change.validate(); err != nil {
		return err
	}
	if t.state != TenantActive {
		return ErrTenantDisabled
	}
	if !role.isValid() {
		return fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	index, err := t.userIndex(userID)
	if err != nil {
		return err
	}
	before := t.users[index].roles
	updated, err := before.With(role)
	if err != nil {
		return err
	}
	if updated.Equal(before) {
		return nil
	}
	t.users[index].roles = updated
	return t.recordAccessChange(change, t.users[index], AccessRoleAssigned, ActionRoleAssigned)
}

// RevokeRole отзывает роль у пользователя, создавая новый неизменяемый набор
// ролей. Отзыв отсутствующей роли отвергается.
func (t *Tenant) RevokeRole(change ChangeContext, userID UserID, role Role) error {
	if err := change.validate(); err != nil {
		return err
	}
	if t.state != TenantActive {
		return ErrTenantDisabled
	}
	if !role.isValid() {
		return fmt.Errorf("%w: %q", ErrUnknownRole, role)
	}
	index, err := t.userIndex(userID)
	if err != nil {
		return err
	}
	if !t.users[index].roles.Has(role) {
		return fmt.Errorf("%w: %s: %s", ErrRoleNotAssigned, userID, role)
	}
	updated, err := t.users[index].roles.Without(role)
	if err != nil {
		return err
	}
	t.users[index].roles = updated
	return t.recordAccessChange(change, t.users[index], AccessRoleRevoked, ActionRoleRevoked)
}

// Disable переводит тенант в состояние «отключён», фиксируя это событием и
// записью аудита. Повторное отключение идемпотентно и не создаёт новых записей.
func (t *Tenant) Disable(change ChangeContext) error {
	if err := change.validate(); err != nil {
		return err
	}
	if t.state == TenantDisabled {
		return nil
	}
	t.state = TenantDisabled
	t.version++
	t.events = append(t.events, TenantAccessChanged{
		EventHeader: change.header(t.id),
		Change:      AccessTenantDisabled,
	})
	return t.appendAudit(change.header(t.id), newAuditRecordOrPanic(change.AuditID, change.Actor,
		ActionTenantDisabled, t.id.String(), change.At, OutcomeSucceeded, ""))
}

// DecideAccess выдаёт решение доступа для актора строго по назначенным ролям.
//
// Пустой актор — ошибка, а не отказ: отсутствие субъекта нельзя трактовать как
// отрицательное решение. Отключённый тенант, как и отсутствие ролей, даёт
// отрицательное решение (false, nil) — новых положительных решений отключённый
// тенант не выдаёт.
func (t *Tenant) DecideAccess(actor ActorID) (bool, error) {
	if err := actor.validate(); err != nil {
		return false, err
	}
	if t.state != TenantActive {
		return false, nil
	}
	for _, user := range t.users {
		if string(user.id) != string(actor) {
			continue
		}
		if !user.roles.Empty() {
			return true, nil
		}
	}
	return false, nil
}

// CorrectAudit добавляет корректирующую запись со ссылкой на исходную.
// Существующая запись не меняется: корректировка выражается новой записью.
func (t *Tenant) CorrectAudit(change ChangeContext, original AuditRecordID, object string, outcome AuditOutcome) error {
	if err := change.validate(); err != nil {
		return err
	}
	if _, err := t.auditByID(original); err != nil {
		return err
	}
	t.version++
	record := newAuditRecordOrPanic(change.AuditID, change.Actor, ActionAuditCorrected, object, change.At, outcome, original)
	return t.appendAudit(change.header(t.id), record)
}

// AccessDecisionSnapshot формирует снимок правил тенанта для контракта
// access-decision: состояние, монотонную версию и список «актор → роли».
func (t *Tenant) AccessDecisionSnapshot(at time.Time) AccessDecisionSnapshot {
	assignments := make([]AccessAssignment, 0, len(t.users))
	for _, user := range t.users {
		assignments = append(assignments, AccessAssignment{
			Actor: ActorID(user.id),
			Roles: user.roles.Roles(),
		})
	}
	return AccessDecisionSnapshot{
		TenantID:    t.id,
		State:       t.state,
		Version:     t.version,
		Assignments: assignments,
		OccurredAt:  at,
	}
}

// recordAccessChange фиксирует изменение ролей пользователя событием и записью
// аудита, поднимая версию правил доступа.
func (t *Tenant) recordAccessChange(change ChangeContext, user TenantUser, kind AccessChangeKind, action AuditAction) error {
	t.version++
	t.events = append(t.events, TenantAccessChanged{
		EventHeader: change.header(t.id),
		UserID:      user.id,
		Login:       user.login,
		Roles:       user.roles.Roles(),
		Change:      kind,
	})
	return t.appendAudit(change.header(t.id), newAuditRecordOrPanic(change.AuditID, change.Actor,
		action, user.id.String(), change.At, OutcomeSucceeded, ""))
}

// appendAudit добавляет запись аудита и парное событие audit-recorded. Идентификатор
// события выводится из идентификатора записи, поэтому повтор записи невозможен.
func (t *Tenant) appendAudit(header EventHeader, record AuditRecord) error {
	if record.id == "" {
		return ErrAuditRecordInvalid
	}
	t.audit = append(t.audit, record)
	t.events = append(t.events, AuditRecorded{
		EventHeader: EventHeader{
			EventID:       EventID("audit:" + record.id.String()),
			OccurredAt:    record.at,
			TenantID:      t.id,
			CorrelationID: header.CorrelationID,
			CausationID:   header.CausationID,
		},
		AuditRecordID: record.id,
		Actor:         record.actor,
		Action:        record.action,
		Object:        record.object,
		Outcome:       record.outcome,
		CorrectionOf:  record.correctionOf,
	})
	return nil
}

// userIndex возвращает индекс пользователя в составе или ErrUserNotFound.
func (t *Tenant) userIndex(id UserID) (int, error) {
	if err := id.validate(); err != nil {
		return 0, err
	}
	for i := range t.users {
		if t.users[i].id == id {
			return i, nil
		}
	}
	return 0, fmt.Errorf("%w: %s", ErrUserNotFound, id)
}

// auditByID возвращает запись аудита по идентификатору.
func (t *Tenant) auditByID(id AuditRecordID) (AuditRecord, error) {
	for _, record := range t.audit {
		if record.id == id {
			return record, nil
		}
	}
	return AuditRecord{}, fmt.Errorf("%w: %s", ErrAuditRecordNotFound, id)
}

// newAuditRecordOrPanic собирает запись аудита из значений, уже проверенных
// вызывающим доменом (идентификатор, субъект, действие, время). Нарушение здесь
// невозможно без ошибки в коде домена, поэтому паника — сигнал о дефекте, а не
// путь обработки пользовательской ошибки.
func newAuditRecordOrPanic(
	id AuditRecordID,
	actor ActorID,
	action AuditAction,
	object string,
	at time.Time,
	outcome AuditOutcome,
	correctionOf AuditRecordID,
) AuditRecord {
	record, err := newAuditRecord(id, actor, action, object, at, outcome, correctionOf)
	if err != nil {
		panic(fmt.Sprintf("домен: некорректная запись аудита: %v", err))
	}
	return record
}
