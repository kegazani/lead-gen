package domain

import (
	"fmt"
	"strings"
	"time"
)

// Сохранённое состояние aggregate root tenant.
//
// Адаптер репозитория не имеет доступа к закрытым полям агрегата, поэтому он
// читает и пишет TenantSnapshot с экспортированными полями. Доменный объект из
// снимка собирается только через RehydrateTenant, который проверяет инварианты и
// не эмитит события: восстановление не является новым фактом домена.

// TenantUserSnapshot — сохранённое состояние пользователя тенанта.
type TenantUserSnapshot struct {
	ID           UserID
	Login        string
	PasswordHash string
	Roles        []Role
}

// AuditRecordSnapshot — сохранённое состояние записи аудита.
type AuditRecordSnapshot struct {
	ID           AuditRecordID
	Actor        ActorID
	Action       AuditAction
	Object       string
	At           time.Time
	Outcome      AuditOutcome
	CorrectionOf AuditRecordID
}

// TenantSnapshot — полное сохранённое состояние aggregate root tenant.
type TenantSnapshot struct {
	ID                 TenantID
	CompanyName        string
	Description        string
	INN                string
	CompanyEmail       string
	Website            string
	LegalData          string
	State              TenantState
	RegistrationReason string
	Profile            Profile
	ProfileStatus      ProfileStatus
	Version            uint64
	CreatedAt          time.Time
	Users              []TenantUserSnapshot
	Audit              []AuditRecordSnapshot
}

// Snapshot возвращает состояние агрегата для сохранения.
func (t *Tenant) Snapshot() TenantSnapshot {
	users := make([]TenantUserSnapshot, 0, len(t.users))
	for _, user := range t.users {
		users = append(users, TenantUserSnapshot{ID: user.id, Login: user.login, PasswordHash: user.passwordHash, Roles: user.roles.Roles()})
	}
	audit := make([]AuditRecordSnapshot, 0, len(t.audit))
	for _, record := range t.audit {
		audit = append(audit, AuditRecordSnapshot{
			ID:           record.id,
			Actor:        record.actor,
			Action:       record.action,
			Object:       record.object,
			At:           record.at,
			Outcome:      record.outcome,
			CorrectionOf: record.correctionOf,
		})
	}
	return TenantSnapshot{
		ID:                 t.id,
		CompanyName:        t.companyName,
		Description:        t.description,
		INN:                t.inn,
		CompanyEmail:       t.companyEmail,
		Website:            t.website,
		LegalData:          t.legalData,
		State:              t.state,
		RegistrationReason: t.registrationReason,
		Profile:            t.profile,
		ProfileStatus:      t.profileStatus,
		Version:            t.version,
		CreatedAt:          t.createdAt,
		Users:              users,
		Audit:              audit,
	}
}

// RehydrateTenant восстанавливает aggregate root из сохранённого состояния,
// проверяя инварианты: непустые идентификатор, название и описание компании,
// время и состояние тенанта, известность ролей, уникальность пользователей,
// целостность журнала аудита (уникальные идентификаторы, ссылки корректировок на
// существующие записи). События не эмитятся.
//
// Роль, объявленная вне домена (в том числе роль чужого тенанта), делает снимок
// противоречивым: восстановление завершается ErrInvalidSnapshot.
func RehydrateTenant(s TenantSnapshot) (*Tenant, error) {
	if err := s.ID.validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSnapshot, err)
	}
	companyName := strings.TrimSpace(s.CompanyName)
	if companyName == "" {
		return nil, fmt.Errorf("%w: тенант %s: %v", ErrInvalidSnapshot, s.ID, ErrCompanyNameRequired)
	}
	description := strings.TrimSpace(s.Description)
	if description == "" {
		return nil, fmt.Errorf("%w: тенант %s: %v", ErrInvalidSnapshot, s.ID, ErrDescriptionRequired)
	}
	inn := strings.TrimSpace(s.INN)
	companyEmail := strings.TrimSpace(s.CompanyEmail)
	website := strings.TrimSpace(s.Website)
	legalData := strings.TrimSpace(s.LegalData)
	if s.CreatedAt.IsZero() {
		return nil, fmt.Errorf("%w: тенант %s", ErrInvalidSnapshot, s.ID)
	}
	state := s.State
	if state == "" {
		state = TenantActive
	}
	if !state.isValid() {
		return nil, fmt.Errorf("%w: тенант %s: неизвестное состояние %q", ErrInvalidSnapshot, s.ID, s.State)
	}
	profileStatus := s.ProfileStatus
	if profileStatus == "" {
		profileStatus = ProfileEmpty
	}
	if !profileStatus.isValid() {
		return nil, fmt.Errorf("%w: тенант %s: неизвестное состояние профиля %q", ErrInvalidSnapshot, s.ID, s.ProfileStatus)
	}

	tenant := &Tenant{
		id:                 s.ID,
		companyName:        companyName,
		description:        description,
		inn:                inn,
		companyEmail:       companyEmail,
		website:            website,
		legalData:          legalData,
		state:              state,
		registrationReason: s.RegistrationReason,
		profile:            s.Profile,
		profileStatus:      profileStatus,
		version:            s.Version,
		createdAt:          s.CreatedAt,
	}

	seenUsers := make(map[UserID]bool, len(s.Users))
	for _, user := range s.Users {
		if err := user.ID.validate(); err != nil {
			return nil, fmt.Errorf("%w: тенант %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
		if seenUsers[user.ID] {
			return nil, fmt.Errorf("%w: тенант %s: %v: %s", ErrInvalidSnapshot, s.ID, ErrDuplicateUser, user.ID)
		}
		seenUsers[user.ID] = true
		login := strings.TrimSpace(user.Login)
		if login == "" {
			return nil, fmt.Errorf("%w: тенант %s: %v", ErrInvalidSnapshot, s.ID, ErrUserLoginRequired)
		}
		roles, err := NewRoleSet(user.Roles...)
		if err != nil {
			return nil, fmt.Errorf("%w: тенант %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
		if roles.Empty() {
			return nil, fmt.Errorf("%w: тенант %s: %v", ErrInvalidSnapshot, s.ID, ErrRolesRequired)
		}
		tenant.users = append(tenant.users, TenantUser{id: user.ID, login: login, passwordHash: user.PasswordHash, roles: roles})
	}

	// Первый проход: проверка записей и сбор идентификаторов. Второй проход:
	// ссылки корректировок обязаны указывать на существующую запись независимо
	// от её позиции в снимке.
	records := make([]AuditRecord, 0, len(s.Audit))
	seenAudit := make(map[AuditRecordID]bool, len(s.Audit))
	for _, saved := range s.Audit {
		if seenAudit[saved.ID] {
			return nil, fmt.Errorf("%w: тенант %s: дубликат записи аудита %s", ErrInvalidSnapshot, s.ID, saved.ID)
		}
		record, err := newAuditRecord(saved.ID, saved.Actor, saved.Action, saved.Object, saved.At, saved.Outcome, saved.CorrectionOf)
		if err != nil {
			return nil, fmt.Errorf("%w: тенант %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
		seenAudit[saved.ID] = true
		records = append(records, record)
	}
	for _, record := range records {
		if record.IsCorrection() && !seenAudit[record.correctionOf] {
			return nil, fmt.Errorf("%w: тенант %s: корректировка %s ссылается на неизвестную запись %s",
				ErrInvalidSnapshot, s.ID, record.id, record.correctionOf)
		}
	}
	tenant.audit = records

	return tenant, nil
}
