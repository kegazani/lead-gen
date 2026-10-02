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
	ID    UserID
	Login string
	Role  Role
}

// TenantSnapshot — полное сохранённое состояние aggregate root tenant.
type TenantSnapshot struct {
	ID          TenantID
	CompanyName string
	Description string
	Users       []TenantUserSnapshot
	CreatedAt   time.Time
}

// Snapshot возвращает состояние агрегата для сохранения.
func (t *Tenant) Snapshot() TenantSnapshot {
	users := make([]TenantUserSnapshot, 0, len(t.users))
	for _, user := range t.users {
		users = append(users, TenantUserSnapshot{ID: user.id, Login: user.login, Role: user.role})
	}
	return TenantSnapshot{
		ID:          t.id,
		CompanyName: t.companyName,
		Description: t.description,
		Users:       users,
		CreatedAt:   t.createdAt,
	}
}

// RehydrateTenant восстанавливает aggregate root из сохранённого состояния,
// проверяя инварианты: непустые идентификатор, название и описание компании,
// время регистрации, известность ролей, уникальность пользователей и непустой
// состав. События не эмитятся.
func RehydrateTenant(s TenantSnapshot) (*Tenant, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	companyName := strings.TrimSpace(s.CompanyName)
	if companyName == "" {
		return nil, fmt.Errorf("%w: тенант %s", ErrCompanyNameRequired, s.ID)
	}
	description := strings.TrimSpace(s.Description)
	if description == "" {
		return nil, fmt.Errorf("%w: тенант %s", ErrDescriptionRequired, s.ID)
	}
	if s.CreatedAt.IsZero() {
		return nil, fmt.Errorf("%w: тенант %s", ErrInvalidSnapshot, s.ID)
	}

	tenant := &Tenant{
		id:          s.ID,
		companyName: companyName,
		description: description,
		createdAt:   s.CreatedAt,
	}
	seen := make(map[UserID]bool, len(s.Users))
	for _, user := range s.Users {
		if err := user.ID.validate(); err != nil {
			return nil, fmt.Errorf("%w: тенант %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
		if seen[user.ID] {
			return nil, fmt.Errorf("%w: тенант %s: %v: %s", ErrInvalidSnapshot, s.ID, ErrDuplicateUser, user.ID)
		}
		seen[user.ID] = true
		login := strings.TrimSpace(user.Login)
		if login == "" {
			return nil, fmt.Errorf("%w: тенант %s: %v", ErrInvalidSnapshot, s.ID, ErrUserLoginRequired)
		}
		if !user.Role.isValid() {
			return nil, fmt.Errorf("%w: тенант %s: %v: %q", ErrInvalidSnapshot, s.ID, ErrUnknownRole, user.Role)
		}
		tenant.users = append(tenant.users, TenantUser{id: user.ID, login: login, role: user.Role})
	}
	if len(tenant.users) == 0 {
		return nil, fmt.Errorf("%w: тенант %s без пользователей", ErrInvalidSnapshot, s.ID)
	}
	return tenant, nil
}
