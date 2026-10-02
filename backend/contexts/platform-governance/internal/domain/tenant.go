package domain

import (
	"fmt"
	"strings"
	"time"
)

// TenantUser — пользователь тенанта.
//
// Сущность внутри агрегата tenant: идентификатор уникален в пределах тенанта,
// логин и роль обязательны. Инвариант «роль принадлежит ровно одному тенанту»
// означает, что пользователь не переносится между тенантами — попытка добавить
// того же пользователя в другой агрегат невозможна, потому что репозиторий
// адресует агрегат по тенанту.
type TenantUser struct {
	id    UserID
	login string
	role  Role
}

// ID возвращает идентификатор пользователя.
func (u TenantUser) ID() UserID { return u.id }

// Login возвращает логин пользователя.
func (u TenantUser) Login() string { return u.login }

// Role возвращает роль пользователя.
func (u TenantUser) Role() Role { return u.role }

// RegisterTenantParams — данные регистрации тенанта вместе с первым
// пользователем роли клиента.
type RegisterTenantParams struct {
	// ID — идентификатор создаваемого тенанта.
	ID TenantID
	// CompanyName — название компании клиента (обязательно).
	CompanyName string
	// Description — описание компании клиента (обязательно).
	Description string
	// UserID — идентификатор регистрируемого пользователя.
	UserID UserID
	// Login — логин пользователя.
	Login string
	// Role — роль пользователя (для самообслуживания — RoleClient).
	Role Role
	// At — время регистрации (UTC).
	At time.Time
	// EventID — стабильный идентификатор порождаемого события.
	EventID EventID
}

// Tenant — aggregate root контекста platform-governance.
//
// Агрегат владеет сведениями о компании клиента и составом её пользователей и
// обеспечивает инварианты:
//
//	I1 — тенант без названия и описания компании не создаётся;
//	I2 — роль принадлежит ровно одному тенанту: пользователь уникален в его
//	     составе и не переносится между тенантами;
//	I3 — роль пользователя объявлена доменом;
//	I4 — каждое изменение доступа фиксируется событием tenant-access-changed.
//
// Поля закрыты: состояние меняется только методами агрегата.
type Tenant struct {
	id          TenantID
	companyName string
	description string
	users       []TenantUser
	createdAt   time.Time
	events      []DomainEvent
}

// RegisterTenant создаёт тенант с первым пользователем заданной роли.
// Регистрация фиксируется событием tenant-access-changed. Неполная форма
// (пустое название или описание) не создаёт тенант.
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

	tenant := &Tenant{
		id:          params.ID,
		companyName: companyName,
		description: description,
		createdAt:   params.At,
	}
	if err := tenant.addUser(TenantUser{id: params.UserID, login: params.Login, role: params.Role}, params.At, params.EventID); err != nil {
		return nil, err
	}
	return tenant, nil
}

// addUser добавляет пользователя в состав тенанта, проверяя инварианты I2 и I3,
// и фиксирует изменение доступа событием tenant-access-changed.
func (t *Tenant) addUser(user TenantUser, at time.Time, eventID EventID) error {
	if err := user.id.validate(); err != nil {
		return err
	}
	login := strings.TrimSpace(user.login)
	if login == "" {
		return ErrUserLoginRequired
	}
	if !user.role.isValid() {
		return fmt.Errorf("%w: %q", ErrUnknownRole, user.role)
	}
	for _, existing := range t.users {
		if existing.id == user.id {
			return fmt.Errorf("%w: %s", ErrDuplicateUser, user.id)
		}
	}
	header := EventHeader{EventID: eventID, OccurredAt: at, TenantID: t.id}
	if err := header.validate(); err != nil {
		return err
	}
	t.users = append(t.users, TenantUser{id: user.id, login: login, role: user.role})
	t.events = append(t.events, TenantAccessChanged{
		EventHeader: header,
		UserID:      user.id,
		Login:       login,
		Role:        user.role,
	})
	return nil
}

// ID возвращает идентификатор тенанта.
func (t *Tenant) ID() TenantID { return t.id }

// CompanyName возвращает название компании клиента.
func (t *Tenant) CompanyName() string { return t.companyName }

// Description возвращает описание компании клиента.
func (t *Tenant) Description() string { return t.description }

// Users возвращает копию состава пользователей в порядке добавления.
func (t *Tenant) Users() []TenantUser {
	return append([]TenantUser(nil), t.users...)
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
