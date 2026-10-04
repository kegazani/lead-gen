package contracttest

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// ContactRepositoryFactory создаёт репозиторий для одного прогона suite. Как и
// LeadProfileRepositoryFactory, фабрика обязана вернуть пустое состояние и сама
// отвечает за изоляцию прогонов.
type ContactRepositoryFactory func(t *testing.T) ports.ContactRepository

// RunContactRepositoryContract проверяет, что реализация ContactRepository
// соблюдает контракт порта: сохраняет и загружает контакт и возвращает по
// ListByCompany только контакты данной компании.
func RunContactRepositoryContract(t *testing.T, newRepository ContactRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("сохранение и загрузка", func(t *testing.T) {
		repository := newRepository(t)
		contact := mustContact(t, "contact-roundtrip", domain.CompanyID("roundtrip.example.com"))
		if err := repository.Save(ctx, contact); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, contact.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		assertContact(t, loaded, contact)
	})

	t.Run("отсутствующий контакт даёт ErrContactNotFound", func(t *testing.T) {
		repository := newRepository(t)
		if _, err := repository.FindByID(ctx, domain.ContactID("missing-contact")); !errors.Is(err, ports.ErrContactNotFound) {
			t.Fatalf("FindByID отсутствующего контакта: ошибка = %v, ожидалась ErrContactNotFound", err)
		}
	})

	t.Run("ListByCompany возвращает только контакты компании", func(t *testing.T) {
		repository := newRepository(t)
		acme := domain.CompanyID("acme.example.com")
		other := domain.CompanyID("other.example.com")

		first := mustContact(t, "contact-acme-1", acme)
		second := mustContact(t, "contact-acme-2", acme)
		foreign := mustContact(t, "contact-other-1", other)

		for _, contact := range []*domain.Contact{first, second, foreign} {
			if err := repository.Save(ctx, contact); err != nil {
				t.Fatalf("Save %s: %v", contact.ID(), err)
			}
		}

		list, err := repository.ListByCompany(ctx, acme)
		if err != nil {
			t.Fatalf("ListByCompany: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("ListByCompany вернул %d контактов, ожидалось 2", len(list))
		}
		byID := map[domain.ContactID]*domain.Contact{}
		for _, contact := range list {
			if contact.CompanyID() != acme {
				t.Fatalf("контакт %s принадлежит %q, ожидалась %q", contact.ID(), contact.CompanyID(), acme)
			}
			byID[contact.ID()] = contact
		}
		assertContact(t, byID[first.ID()], first)
		assertContact(t, byID[second.ID()], second)
	})

	t.Run("ListByCompanies возвращает только контакты набора компаний", func(t *testing.T) {
		repository := newRepository(t)
		acme := domain.CompanyID("acme.example.com")
		other := domain.CompanyID("other.example.com")
		missing := domain.CompanyID("missing.example.com")

		first := mustContact(t, "contact-acme-1", acme)
		second := mustContact(t, "contact-acme-2", acme)
		foreign := mustContact(t, "contact-other-1", other)

		for _, contact := range []*domain.Contact{first, second, foreign} {
			if err := repository.Save(ctx, contact); err != nil {
				t.Fatalf("Save %s: %v", contact.ID(), err)
			}
		}

		list, err := repository.ListByCompanies(ctx, []domain.CompanyID{acme, other})
		if err != nil {
			t.Fatalf("ListByCompanies: %v", err)
		}
		if len(list) != 3 {
			t.Fatalf("ListByCompanies вернул %d контактов, ожидалось 3", len(list))
		}
		byID := map[domain.ContactID]*domain.Contact{}
		for _, contact := range list {
			if contact.CompanyID() == missing {
				t.Fatalf("ListByCompanies вернул контакт компании вне набора: %s", contact.ID())
			}
			byID[contact.ID()] = contact
		}
		assertContact(t, byID[first.ID()], first)
		assertContact(t, byID[second.ID()], second)
		assertContact(t, byID[foreign.ID()], foreign)
	})

	t.Run("ListByCompanies по компании без контактов", func(t *testing.T) {
		repository := newRepository(t)
		acme := domain.CompanyID("acme.example.com")
		maybe := domain.CompanyID("maybe.example.com")

		if err := repository.Save(ctx, mustContact(t, "contact-acme-1", acme)); err != nil {
			t.Fatalf("Save: %v", err)
		}

		list, err := repository.ListByCompanies(ctx, []domain.CompanyID{maybe})
		if err != nil {
			t.Fatalf("ListByCompanies: %v", err)
		}
		if len(list) != 0 {
			t.Fatalf("у компании без контактов ожидался пустой список, получено %d", len(list))
		}
	})

	t.Run("ListByCompanies на пустом наборе", func(t *testing.T) {
		repository := newRepository(t)
		if err := repository.Save(ctx, mustContact(t, "contact-acme-1", domain.CompanyID("acme.example.com"))); err != nil {
			t.Fatalf("Save: %v", err)
		}

		for _, set := range [][]domain.CompanyID{nil, {}} {
			list, err := repository.ListByCompanies(ctx, set)
			if err != nil {
				t.Fatalf("ListByCompanies: %v", err)
			}
			if len(list) != 0 {
				t.Fatalf("на пустом наборе ожидался пустой список, получено %d", len(list))
			}
		}
	})
}
