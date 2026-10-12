package app

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

func seedCompany(profiles *memProfiles, id, name string) *domain.Company {
	return seedCompanyInTenant(profiles, "", id, name)
}

// seedCompanyInTenant сохраняет карточку в заданном тенанте: сценарии, читающие
// пул по тенанту-владельцу, должны получить карточку в том же тенанте.
func seedCompanyInTenant(profiles *memProfiles, tenant, id, name string) *domain.Company {
	company, err := domain.CreateCompany(domain.CreateCompanyParams{
		Domain:      id,
		Name:        domain.AttributedValue{Value: name, Provenance: domain.Provenance{SourceURL: "https://" + id, ExtractedAt: testTime}},
		Description: domain.AttributedValue{Value: "desc", Provenance: domain.Provenance{SourceURL: "https://" + id, ExtractedAt: testTime}},
		CreatedAt:   testTime,
	})
	if err != nil {
		panic(err)
	}
	if err := profiles.Save(ports.WithTenant(context.Background(), tenant), company); err != nil {
		panic(err)
	}
	return company
}

func TestShowcaseCard(t *testing.T) {
	profiles := newMemProfiles()
	contacts := newMemContacts()
	seedCompany(profiles, "example.com", "Example Corp")

	email := &domain.ContactAttribute{
		Value:      "hello@example.com",
		Provenance: domain.AttributeProvenance{SourceURL: "https://example.com", ExtractedAt: testTime, Method: domain.MethodRegex},
	}
	contact, err := domain.CreateContact(domain.CreateContactParams{
		ID:        "hello@example.com",
		CompanyID: "example.com",
		Email:     email,
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}
	if err := contacts.Save(context.Background(), contact); err != nil {
		t.Fatalf("Save contact: %v", err)
	}

	s := &Showcase{Profiles: profiles, Contacts: contacts}
	card, err := s.Card(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("Card: %v", err)
	}
	if card.ID != "example.com" || card.Name != "Example Corp" || card.Description != "desc" {
		t.Fatalf("Card = %+v", card)
	}
	if len(card.Contacts) != 1 || card.Contacts[0].Email != "hello@example.com" {
		t.Fatalf("Contacts = %+v", card.Contacts)
	}
}

func TestShowcaseCardNotFound(t *testing.T) {
	s := &Showcase{Profiles: newMemProfiles(), Contacts: newMemContacts()}
	if _, err := s.Card(context.Background(), "missing.com"); !errors.Is(err, ErrCardNotFound) {
		t.Fatalf("ожидалась ErrCardNotFound, получено %v", err)
	}
}

// seedContactWithoutEmail сохраняет контакт компании без email, чтобы проверить
// признак «нет email».
func seedContactWithoutEmail(contacts *memContacts, companyID, id string) {
	prov := domain.AttributeProvenance{SourceURL: "https://" + companyID, ExtractedAt: testTime, Method: domain.MethodRegex}
	contact, err := domain.CreateContact(domain.CreateContactParams{
		ID:        id,
		CompanyID: domain.CompanyID(companyID),
		Phone:     &domain.ContactAttribute{Value: "+7 000 000-00-00", Provenance: prov},
		CreatedAt: testTime,
	})
	if err != nil {
		panic(err)
	}
	if err := contacts.Save(context.Background(), contact); err != nil {
		panic(err)
	}
}

// TestShowcaseListCardsEmail проверяет, что список берёт email первого контакта
// с непустым адресом и не помечает компанию как «без email».
func TestShowcaseListCardsEmail(t *testing.T) {
	profiles := newMemProfiles()
	contacts := newMemContacts()
	seedCompany(profiles, "with.example.com", "С Email")
	// Контакт без email идёт первым по идентификатору и не должен перекрыть email.
	seedContactWithoutEmail(contacts, "with.example.com", "a-phone")
	seedContact(contacts, "with.example.com", "anna@with.example.com", "CEO")

	s := &Showcase{Profiles: profiles, Contacts: contacts}
	cards, err := s.ListCards(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListCards: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("ListCards = %+v", cards)
	}
	if cards[0].Email != "anna@with.example.com" || cards[0].NoEmail {
		t.Fatalf("у компании с email ожидался адрес без пометки «нет email»: %+v", cards[0])
	}
}

// TestShowcaseListCardsWithoutEmail проверяет, что компания с контактом без email
// помечается признаком «нет email».
func TestShowcaseListCardsWithoutEmail(t *testing.T) {
	profiles := newMemProfiles()
	contacts := newMemContacts()
	seedCompany(profiles, "phone.example.com", "Только Телефон")
	seedContactWithoutEmail(contacts, "phone.example.com", "a-phone")

	s := &Showcase{Profiles: profiles, Contacts: contacts}
	cards, err := s.ListCards(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListCards: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("ListCards = %+v", cards)
	}
	if cards[0].Email != "" || !cards[0].NoEmail {
		t.Fatalf("у компании без email ожидалась пометка «нет email»: %+v", cards[0])
	}
}

// TestShowcaseListCardsWithoutContacts проверяет, что компания без контактов
// показывается как карточка без email и не роняет загрузку списка.
func TestShowcaseListCardsWithoutContacts(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "Example Corp")

	s := &Showcase{Profiles: profiles, Contacts: newMemContacts()}
	cards, err := s.ListCards(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListCards: %v", err)
	}
	if len(cards) != 1 || cards[0].Name != "Example Corp" {
		t.Fatalf("ListCards = %+v", cards)
	}
	if !cards[0].NoEmail || cards[0].Email != "" {
		t.Fatalf("компания без контактов должна быть без email: %+v", cards[0])
	}
}

func TestShowcaseHideCard(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "Example Corp")

	s := &Showcase{Profiles: profiles, Contacts: newMemContacts()}
	if err := s.HideCard(context.Background(), "example.com"); err != nil {
		t.Fatalf("HideCard: %v", err)
	}
	company := profiles.byID["example.com"]
	if !company.Hidden() {
		t.Fatal("карточка должна быть скрыта")
	}

	cards, err := s.ListCards(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListCards: %v", err)
	}
	if len(cards) != 0 {
		t.Fatalf("скрытая карточка видна в витрине: %+v", cards)
	}
}

func TestShowcaseHideCardNotFound(t *testing.T) {
	s := &Showcase{Profiles: newMemProfiles()}
	if err := s.HideCard(context.Background(), "missing.com"); !errors.Is(err, ErrCardNotFound) {
		t.Fatalf("ожидалась ErrCardNotFound, получено %v", err)
	}
}

func TestShowcaseDeleteCard(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "Example Corp")

	s := &Showcase{Profiles: profiles, Contacts: newMemContacts()}
	if err := s.DeleteCard(context.Background(), "example.com"); err != nil {
		t.Fatalf("DeleteCard: %v", err)
	}
	if _, err := s.Card(context.Background(), "example.com"); !errors.Is(err, ErrCardNotFound) {
		t.Fatalf("после удаления ожидалась ErrCardNotFound, получено %v", err)
	}
}

func TestShowcaseListJobs(t *testing.T) {
	store := newMemJobs()
	if _, err := store.CreateSearchJob(context.Background(), "q1", 0); err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	if _, err := store.CreateSearchJob(context.Background(), "q2", 0); err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}

	s := &Showcase{Jobs: store}
	list, err := s.ListJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("заданий %d, ожидалось 2", len(list))
	}
	if list[0].Query != "q2" {
		t.Fatalf("задания должны идти новые первыми, первый %q", list[0].Query)
	}
}

func TestShowcaseJob(t *testing.T) {
	store := newMemJobs()
	job, err := store.CreateSearchJob(context.Background(), "пример запроса", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	if err := store.RecordResult(context.Background(), job.ID, []string{"a.example", "b.example"}); err != nil {
		t.Fatalf("RecordResult: %v", err)
	}
	if err := store.RecordEvent(context.Background(), ports.SearchJobEvent{
		JobID: job.ID, Type: ports.EventEnqueued, CreatedAt: testTime,
	}); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	s := &Showcase{Jobs: store}
	detail, err := s.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Job: %v", err)
	}
	if detail.Job.Query != "пример запроса" || len(detail.Job.ResultRef) != 2 {
		t.Fatalf("Job = %+v", detail.Job)
	}
	if len(detail.Events) != 1 || detail.Events[0].Type != ports.EventEnqueued {
		t.Fatalf("Events = %+v", detail.Events)
	}
}

func TestShowcaseJobWithoutEvents(t *testing.T) {
	store := newMemJobs()
	job, err := store.CreateSearchJob(context.Background(), "q1", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}

	s := &Showcase{Jobs: store}
	detail, err := s.Job(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("Job: %v", err)
	}
	if len(detail.Events) != 0 {
		t.Fatalf("у задания без событий ожидался пустой журнал: %+v", detail.Events)
	}
}

func TestShowcaseJobNotFound(t *testing.T) {
	s := &Showcase{Jobs: newMemJobs()}
	if _, err := s.Job(context.Background(), "missing"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("ожидалась ErrJobNotFound, получено %v", err)
	}
}

func TestShowcaseDashboard(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "Example Corp")
	jobs := newMemJobs()
	if _, err := jobs.CreateSearchJob(context.Background(), "q1", 0); err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}

	s := &Showcase{Jobs: jobs, Profiles: profiles, Contacts: newMemContacts()}
	dashboard, err := s.Dashboard(context.Background(), 10, 10)
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if len(dashboard.Jobs) != 1 || len(dashboard.Cards) != 1 {
		t.Fatalf("Dashboard = %+v", dashboard)
	}
}

func TestShowcaseHistory(t *testing.T) {
	messages := newMemMessages()
	message, err := domain.CreateOutboundMessage(domain.CreateOutboundMessageParams{
		ID:        "m1",
		LeadID:    "example.com",
		Recipient: "anna@example.com",
		Template:  "intro",
		Body:      "тело",
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	message.MarkSent(testTime)
	if err := messages.Save(context.Background(), message); err != nil {
		t.Fatalf("Save: %v", err)
	}

	s := &Showcase{Messages: messages}
	history, err := s.History(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) != 1 || history[0].Status != "sent" || history[0].Recipient != "anna@example.com" {
		t.Fatalf("History = %+v", history)
	}
}
