package app

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// TestResolveDeliveryTargetResolvesAddress проверяет успешное разрешение ссылки в
// адрес по контакту своего тенанта.
func TestResolveDeliveryTargetResolvesAddress(t *testing.T) {
	contacts := newMemContacts()
	seedContact(contacts, "lead.example", "sales@lead.example", "Коммерческий директор")

	resolver := &ResolveDeliveryTarget{Contacts: contacts, TenantID: "tenant-1"}
	recipient, err := resolver.Run(context.Background(), ResolveRecipientInput{
		TenantID: "tenant-1", RecipientRef: "sales@lead.example",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if recipient.Address != "sales@lead.example" {
		t.Fatalf("Address = %q, ожидался sales@lead.example", recipient.Address)
	}
}

// TestResolveDeliveryTargetRejectsUnknownRef проверяет, что неизвестная ссылка —
// доменный отказ, а не выдуманный адрес.
func TestResolveDeliveryTargetRejectsUnknownRef(t *testing.T) {
	resolver := &ResolveDeliveryTarget{Contacts: newMemContacts(), TenantID: "tenant-1"}
	_, err := resolver.Run(context.Background(), ResolveRecipientInput{
		TenantID: "tenant-1", RecipientRef: "missing@lead.example",
	})
	if !errors.Is(err, ErrInvalidDeliveryTarget) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidDeliveryTarget", err)
	}
}

// TestResolveDeliveryTargetResolvesCompanyRef проверяет, что ссылка-компания
// (RecipientRef без контакта с таким id) разрешается в адрес контакта компании —
// контракт campaign-command использует CompanyID как минимального получателя.
func TestResolveDeliveryTargetResolvesCompanyRef(t *testing.T) {
	contacts := newMemContacts()
	seedContact(contacts, "norbit.ru", "info@norbit.ru", "Общий")

	resolver := &ResolveDeliveryTarget{Contacts: contacts, TenantID: "tenant-1"}
	recipient, err := resolver.Run(context.Background(), ResolveRecipientInput{
		TenantID: "tenant-1", RecipientRef: "norbit.ru",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if recipient.Address != "info@norbit.ru" {
		t.Fatalf("Address = %q, ожидался info@norbit.ru", recipient.Address)
	}
}

// TestResolveDeliveryTargetRejectsCompanyWithoutAddress проверяет, что компания
// без контакта с адресом — доменный отказ, а не выдуманный адрес.
func TestResolveDeliveryTargetRejectsCompanyWithoutAddress(t *testing.T) {
	contacts := newMemContacts()
	seedContactWithoutEmail(contacts, "norbit.ru", "phone-only")

	resolver := &ResolveDeliveryTarget{Contacts: contacts, TenantID: "tenant-1"}
	_, err := resolver.Run(context.Background(), ResolveRecipientInput{
		TenantID: "tenant-1", RecipientRef: "norbit.ru",
	})
	if !errors.Is(err, ErrInvalidDeliveryTarget) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidDeliveryTarget", err)
	}
}

// TestResolveDeliveryTargetRejectsForeignTenant проверяет изоляцию тенанта: ссылка
// через резолвер чужого тенанта не разрешается.
func TestResolveDeliveryTargetRejectsForeignTenant(t *testing.T) {
	contacts := newMemContacts()
	seedContact(contacts, "lead.example", "sales@lead.example", "Коммерческий директор")

	resolver := &ResolveDeliveryTarget{Contacts: contacts, TenantID: "tenant-1"}
	_, err := resolver.Run(context.Background(), ResolveRecipientInput{
		TenantID: "tenant-2", RecipientRef: "sales@lead.example",
	})
	if !errors.Is(err, ErrInvalidDeliveryTarget) {
		t.Fatalf("чужая ссылка: ошибка = %v, ожидалась ErrInvalidDeliveryTarget", err)
	}
}

// TestResolveDeliveryTargetRejectsEmptyInput проверяет, что пустые входы — отказ.
func TestResolveDeliveryTargetRejectsEmptyInput(t *testing.T) {
	resolver := &ResolveDeliveryTarget{Contacts: newMemContacts(), TenantID: "tenant-1"}
	for _, in := range []ResolveRecipientInput{
		{RecipientRef: "sales@lead.example"},
		{TenantID: "tenant-1"},
		{TenantID: "  ", RecipientRef: "  "},
	} {
		if _, err := resolver.Run(context.Background(), in); !errors.Is(err, ErrInvalidDeliveryTarget) {
			t.Fatalf("вход %+v: ошибка = %v, ожидалась ErrInvalidDeliveryTarget", in, err)
		}
	}
}

// TestResolveDeliveryTargetUnboundTenant проверяет честную недоступность, когда
// резолвер не привязан к тенанту: изоляция не гарантируется.
func TestResolveDeliveryTargetUnboundTenant(t *testing.T) {
	contacts := newMemContacts()
	seedContact(contacts, "lead.example", "sales@lead.example", "Коммерческий директор")

	resolver := &ResolveDeliveryTarget{Contacts: contacts}
	_, err := resolver.Run(context.Background(), ResolveRecipientInput{
		TenantID: "tenant-1", RecipientRef: "sales@lead.example",
	})
	if !errors.Is(err, ErrDeliveryTargetUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrDeliveryTargetUnavailable", err)
	}
}

// TestResolveDeliveryTargetRejectsContactWithoutAddress проверяет, что контакт без
// адреса не даёт выдуманного адреса.
func TestResolveDeliveryTargetRejectsContactWithoutAddress(t *testing.T) {
	contacts := newMemContacts()
	seedContactWithoutEmail(contacts, "lead.example", "contact-no-email")

	resolver := &ResolveDeliveryTarget{Contacts: contacts, TenantID: "tenant-1"}
	_, err := resolver.Run(context.Background(), ResolveRecipientInput{
		TenantID: "tenant-1", RecipientRef: "contact-no-email",
	})
	if !errors.Is(err, ErrInvalidDeliveryTarget) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidDeliveryTarget", err)
	}
}

// TestResolveDeliveryTargetWithoutRepository проверяет недоступность без
// репозитория контактов.
func TestResolveDeliveryTargetWithoutRepository(t *testing.T) {
	resolver := &ResolveDeliveryTarget{TenantID: "tenant-1"}
	_, err := resolver.Run(context.Background(), ResolveRecipientInput{
		TenantID: "tenant-1", RecipientRef: "sales@lead.example",
	})
	if !errors.Is(err, ErrDeliveryTargetUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrDeliveryTargetUnavailable", err)
	}
}

// failingContacts — репозиторий контактов, у которого чтение падает: сбой
// хранилища должен различаться с неизвестной ссылкой.
type failingContacts struct{ err error }

func (f *failingContacts) Save(context.Context, *domain.Contact) error { return f.err }
func (f *failingContacts) FindByID(context.Context, domain.ContactID) (*domain.Contact, error) {
	return nil, f.err
}
func (f *failingContacts) ListByCompany(context.Context, domain.CompanyID) ([]*domain.Contact, error) {
	return nil, f.err
}
func (f *failingContacts) ListByCompanies(context.Context, []domain.CompanyID) ([]*domain.Contact, error) {
	return nil, f.err
}

var _ ports.ContactRepository = (*failingContacts)(nil)

// TestResolveDeliveryTargetRepositoryFailure проверяет, что сбой чтения даёт
// недоступность, а не доменный отказ.
func TestResolveDeliveryTargetRepositoryFailure(t *testing.T) {
	resolver := &ResolveDeliveryTarget{
		Contacts: &failingContacts{err: errors.New("сбой хранилища")},
		TenantID: "tenant-1",
	}
	_, err := resolver.Run(context.Background(), ResolveRecipientInput{
		TenantID: "tenant-1", RecipientRef: "sales@lead.example",
	})
	if !errors.Is(err, ErrDeliveryTargetUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrDeliveryTargetUnavailable", err)
	}
	if errors.Is(err, ErrInvalidDeliveryTarget) {
		t.Fatalf("сбой хранилища не должен выдаваться за доменный отказ: %v", err)
	}
}
