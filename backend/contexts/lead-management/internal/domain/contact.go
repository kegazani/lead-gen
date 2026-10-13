package domain

import (
	"fmt"
	"strings"
	"time"
)

// ContactID — идентификатор контакта. В срезе — стабильный ключ, производный от
// нормализованного адреса в пределах компании; вычисление — прикладной слой.
type ContactID string

// validate проверяет непустоту идентификатора.
func (id ContactID) validate() error {
	if strings.TrimSpace(string(id)) == "" {
		return ErrEmptyContactID
	}
	return nil
}

// String возвращает идентификатор строкой.
func (id ContactID) String() string { return string(id) }

// ContactAttribute — значение атрибута контакта (email/телефон/роль) с
// происхождением. Пустое значение означает «не найдено».
type ContactAttribute struct {
	Value      string
	Provenance AttributeProvenance
}

// validate проверяет согласованность значения и происхождения.
func (a ContactAttribute) validate() error {
	if strings.TrimSpace(a.Value) == "" {
		return nil
	}
	return a.Provenance.validate()
}

// Contact — aggregate root contact. Контакт с email/телефоном/ролью; каждый
// заполненный атрибут несёт происхождение (инвариант каталога «каждое внешнее
// значение имеет происхождение»).
type Contact struct {
	id        ContactID
	companyID CompanyID
	email     *ContactAttribute
	phone     *ContactAttribute
	role      *ContactAttribute
	createdAt time.Time
	updatedAt time.Time
}

// CreateContactParams — параметры создания контакта.
type CreateContactParams struct {
	ID        string
	CompanyID CompanyID
	Email     *ContactAttribute
	Phone     *ContactAttribute
	Role      *ContactAttribute
	CreatedAt time.Time
}

// CreateContact создаёт контакт. Идентификатор и компания обязательны; каждый
// заполненный атрибут обязан нести происхождение.
func CreateContact(params CreateContactParams) (*Contact, error) {
	id := ContactID(strings.TrimSpace(params.ID))
	if err := id.validate(); err != nil {
		return nil, err
	}
	if err := params.CompanyID.validate(); err != nil {
		return nil, err
	}
	if params.Email != nil {
		if err := params.Email.validate(); err != nil {
			return nil, err
		}
	}
	if params.Phone != nil {
		if err := params.Phone.validate(); err != nil {
			return nil, err
		}
	}
	if params.Role != nil {
		if err := params.Role.validate(); err != nil {
			return nil, err
		}
	}
	if !hasValue(params.Email) && !hasValue(params.Phone) && !hasValue(params.Role) {
		return nil, fmt.Errorf("%w: контакт %s", ErrEmptyContactValue, id)
	}
	now := params.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return &Contact{
		id:        id,
		companyID: params.CompanyID,
		email:     params.Email,
		phone:     params.Phone,
		role:      params.Role,
		createdAt: now,
		updatedAt: now,
	}, nil
}

func hasValue(a *ContactAttribute) bool {
	return a != nil && strings.TrimSpace(a.Value) != ""
}

// ID возвращает идентификатор контакта.
func (c *Contact) ID() ContactID { return c.id }

// CompanyID возвращает идентификатор компании-владельца.
func (c *Contact) CompanyID() CompanyID { return c.companyID }

// Email возвращает email контакта (nil, если не найден).
func (c *Contact) Email() *ContactAttribute { return c.email }

// Phone возвращает телефон контакта (nil, если не найден).
func (c *Contact) Phone() *ContactAttribute { return c.phone }

// Role возвращает роль контакта (nil, если не найдена).
func (c *Contact) Role() *ContactAttribute { return c.role }

// CreatedAt возвращает момент создания.
func (c *Contact) CreatedAt() time.Time { return c.createdAt }

// UpdatedAt возвращает момент последнего изменения.
func (c *Contact) UpdatedAt() time.Time { return c.updatedAt }

// Enrich подтверждает атрибуты того же контакта новым источником, сохраняя
// время создания и поля, которые новый обход не нашёл.
func (c *Contact) Enrich(from *Contact, at time.Time) error {
	if from == nil || c.id != from.id || c.companyID != from.companyID {
		return fmt.Errorf("domain: нельзя дополнить контакт данными другой записи")
	}
	if from.email != nil && from.email.Value != "" {
		c.email = from.email
	}
	if from.phone != nil && from.phone.Value != "" {
		c.phone = from.phone
	}
	if from.role != nil && from.role.Value != "" {
		c.role = from.role
	}
	c.updatedAt = at
	return nil
}
