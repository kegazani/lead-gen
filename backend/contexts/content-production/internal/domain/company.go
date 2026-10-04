package domain

import (
	"fmt"
	"strings"
)

// Данные о компании клиента без персональных данных (capability
// landing-page-generator, требования «Данные о компании без персональных данных»
// и «Извлечение содержания с сайта клиента»).
//
// Наполнение несёт реквизиты и общие контакты компании. Личные контакты и имена
// сотрудников в наполнение не переносятся: домен отделяет их от общих и сообщает
// об исключении, а не хранит.

// CompanyContactKind — вид контакта компании.
type CompanyContactKind string

const (
	// CompanyContactPhone — телефон.
	CompanyContactPhone CompanyContactKind = "phone"

	// CompanyContactEmail — адрес электронной почты.
	CompanyContactEmail CompanyContactKind = "email"
)

// CompanyContact — контакт компании с признаком персональности. Признак ставит
// источник (разбор сайта или материалов): личный номер или адрес сотрудника
// помечается как персональный и в наполнение не попадает.
type CompanyContact struct {
	kind     CompanyContactKind
	value    string
	personal bool
}

// NewCompanyContact собирает контакт. Пустое значение и неизвестный вид
// отвергаются.
func NewCompanyContact(kind CompanyContactKind, value string, personal bool) (CompanyContact, error) {
	switch kind {
	case CompanyContactPhone, CompanyContactEmail:
	default:
		return CompanyContact{}, fmt.Errorf("%w: %q", ErrInvalidContactKind, kind)
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return CompanyContact{}, fmt.Errorf("%w: пустое значение контакта %q", ErrInvalidContactValue, kind)
	}
	return CompanyContact{kind: kind, value: trimmed, personal: personal}, nil
}

// Kind возвращает вид контакта.
func (c CompanyContact) Kind() CompanyContactKind { return c.kind }

// Value возвращает значение контакта.
func (c CompanyContact) Value() string { return c.value }

// Personal сообщает, что контакт персональный и в наполнение не переносится.
func (c CompanyContact) Personal() bool { return c.personal }

// CompanyProfile — реквизиты и контакты компании клиента. Неизменяем.
type CompanyProfile struct {
	name     string
	address  string
	contacts []CompanyContact
}

// NewCompanyProfile собирает профиль компании. Наименование обязательно:
// наполнение без него не идентифицирует компанию.
func NewCompanyProfile(name, address string, contacts []CompanyContact) (CompanyProfile, error) {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return CompanyProfile{}, ErrCompanyNameRequired
	}
	return CompanyProfile{
		name:     trimmedName,
		address:  strings.TrimSpace(address),
		contacts: append([]CompanyContact(nil), contacts...),
	}, nil
}

// Name возвращает наименование компании.
func (p CompanyProfile) Name() string { return p.name }

// Address возвращает адрес компании (может быть пустым).
func (p CompanyProfile) Address() string { return p.address }

// Contacts возвращает все контакты в порядке добавления.
func (p CompanyProfile) Contacts() []CompanyContact {
	return append([]CompanyContact(nil), p.contacts...)
}

// GeneralContacts возвращает общие контакты компании, переносимые в наполнение.
func (p CompanyProfile) GeneralContacts() []CompanyContact {
	out := make([]CompanyContact, 0, len(p.contacts))
	for _, contact := range p.contacts {
		if !contact.personal {
			out = append(out, contact)
		}
	}
	return out
}

// ExcludedContacts возвращает персональные контакты, исключённые из наполнения, —
// факт исключения сообщается оператору (требование 5.2).
func (p CompanyProfile) ExcludedContacts() []CompanyContact {
	out := make([]CompanyContact, 0, len(p.contacts))
	for _, contact := range p.contacts {
		if contact.personal {
			out = append(out, contact)
		}
	}
	return out
}

// IsZero сообщает, что профиль компании не предоставлен.
func (p CompanyProfile) IsZero() bool { return p.name == "" }
