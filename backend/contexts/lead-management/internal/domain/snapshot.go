package domain

import (
	"fmt"
	"strings"
	"time"
)

// Сохранённое состояние агрегатов. Адаптер репозитория не имеет доступа к
// закрытым полям, поэтому читает и пишет снимки с экспортированными полями.
// Доменный объект из снимка собирается только через Rehydrate*, который
// проверяет инварианты и не эмитит события: восстановление не является новым
// фактом домена.

// CompanySnapshot — сохранённое состояние aggregate root company.
type CompanySnapshot struct {
	ID          CompanyID
	Name        AttributedValue
	Description AttributedValue
	Socials     []AttributedValue
	Hidden      bool
	Status      LeadStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Snapshot возвращает состояние агрегата для сохранения.
func (c *Company) Snapshot() CompanySnapshot {
	return CompanySnapshot{
		ID:          c.id,
		Name:        c.name,
		Description: c.description,
		Socials:     append([]AttributedValue(nil), c.socials...),
		Hidden:      c.hidden,
		Status:      c.status.Snapshot(),
		CreatedAt:   c.createdAt,
		UpdatedAt:   c.updatedAt,
	}
}

// RehydrateCompany восстанавливает компанию из сохранённого состояния. Проверяет
// идентификатор, обязательное название и происхождение каждого заполненного
// поля. События не эмитятся.
func RehydrateCompany(s CompanySnapshot) (*Company, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(s.Name.Value) == "" {
		return nil, fmt.Errorf("%w: компания %s", ErrEmptyCompanyName, s.ID)
	}
	if err := s.Name.validate(); err != nil {
		return nil, err
	}
	if err := s.Description.validate(); err != nil {
		return nil, fmt.Errorf("%w: описание компании %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	for _, social := range s.Socials {
		if err := social.validate(); err != nil {
			return nil, fmt.Errorf("%w: соцсеть компании %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
	}
	status, err := RehydrateLeadStatus(s.Status)
	if err != nil {
		return nil, fmt.Errorf("%w: компания %s: %v", ErrInvalidSnapshot, s.ID, err)
	}
	return &Company{
		id:          s.ID,
		name:        s.Name,
		description: s.Description,
		socials:     append([]AttributedValue(nil), s.Socials...),
		hidden:      s.Hidden,
		status:      *status,
		createdAt:   s.CreatedAt,
		updatedAt:   s.UpdatedAt,
	}, nil
}

// ContactSnapshot — сохранённое состояние aggregate root contact.
type ContactSnapshot struct {
	ID        ContactID
	CompanyID CompanyID
	Email     *ContactAttribute
	Phone     *ContactAttribute
	Role      *ContactAttribute
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Snapshot возвращает состояние контакта для сохранения.
func (c *Contact) Snapshot() ContactSnapshot {
	return ContactSnapshot{
		ID:        c.id,
		CompanyID: c.companyID,
		Email:     c.email,
		Phone:     c.phone,
		Role:      c.role,
		CreatedAt: c.createdAt,
		UpdatedAt: c.updatedAt,
	}
}

// RehydrateContact восстанавливает контакт из сохранённого состояния, проверяя
// идентификатор, компанию и происхождение каждого заполненного атрибута.
func RehydrateContact(s ContactSnapshot) (*Contact, error) {
	if err := s.ID.validate(); err != nil {
		return nil, err
	}
	if err := s.CompanyID.validate(); err != nil {
		return nil, err
	}
	for _, attr := range []*ContactAttribute{s.Email, s.Phone, s.Role} {
		if attr == nil {
			continue
		}
		if err := attr.validate(); err != nil {
			return nil, fmt.Errorf("%w: контакт %s: %v", ErrInvalidSnapshot, s.ID, err)
		}
	}
	if !hasValue(s.Email) && !hasValue(s.Phone) && !hasValue(s.Role) {
		return nil, fmt.Errorf("%w: контакт %s без значений", ErrEmptyContactValue, s.ID)
	}
	return &Contact{
		id:        s.ID,
		companyID: s.CompanyID,
		email:     s.Email,
		phone:     s.Phone,
		role:      s.Role,
		createdAt: s.CreatedAt,
		updatedAt: s.UpdatedAt,
	}, nil
}
