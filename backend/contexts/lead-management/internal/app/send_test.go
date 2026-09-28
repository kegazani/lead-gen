package app

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

// seedContact сохраняет контакт компании с email и ролью.
func seedContact(contacts *memContacts, companyID, email, role string) {
	prov := domain.AttributeProvenance{SourceURL: "https://" + companyID, ExtractedAt: testTime, Method: domain.MethodRegex}
	contact, err := domain.CreateContact(domain.CreateContactParams{
		ID:        email,
		CompanyID: domain.CompanyID(companyID),
		Email:     &domain.ContactAttribute{Value: email, Provenance: prov},
		Role:      &domain.ContactAttribute{Value: role, Provenance: prov},
		CreatedAt: testTime,
	})
	if err != nil {
		panic(err)
	}
	if err := contacts.Save(context.Background(), contact); err != nil {
		panic(err)
	}
}

// seedTemplate сохраняет шаблон письма с объявленными переменными.
func seedTemplate(templates *memTemplates, id, subject, body string, variables ...domain.MergeVariable) {
	template, err := domain.CreateTemplate(domain.CreateTemplateParams{
		ID:        id,
		Name:      id,
		Subject:   subject,
		Body:      body,
		Variables: variables,
		CreatedAt: testTime,
	})
	if err != nil {
		panic(err)
	}
	if err := templates.Save(context.Background(), template); err != nil {
		panic(err)
	}
}

// newSendToLead собирает use case отправки письма лиду.
func newSendToLead(profiles *memProfiles, contacts *memContacts, messages *memMessages, templates *memTemplates, mailPort *fakeMail) *SendToLead {
	return &SendToLead{
		Profiles:  profiles,
		Contacts:  contacts,
		Messages:  messages,
		Templates: templates,
		Mail:      mailPort,
		From:      "sender@example.com",
		Clock:     fakeClock{t: testTime},
	}
}

func TestSendToLeadSendsAndRecordsMessage(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "ООО Ромашка")
	contacts := newMemContacts()
	seedContact(contacts, "example.com", "anna@example.com", "CEO")
	messages := newMemMessages()
	templates := newMemTemplates()
	seedTemplate(templates, "intro", "Тема для {{company}}", "Здравствуйте, {{company}}!",
		domain.MergeVariable{Key: domain.MergeVariableCompany, Fallback: "ваша компания"},
	)
	mailPort := &fakeMail{}

	u := newSendToLead(profiles, contacts, messages, templates, mailPort)
	result, err := u.Send(context.Background(), "example.com", "intro", nil, "key-1")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Status != domain.MessageStatusSent {
		t.Fatalf("Status %q, ожидался %q", result.Status, domain.MessageStatusSent)
	}

	if len(mailPort.sent) != 1 {
		t.Fatalf("отправлено %d писем, ожидалось 1", len(mailPort.sent))
	}
	if mailPort.sent[0].To != "anna@example.com" {
		t.Fatalf("получатель %q", mailPort.sent[0].To)
	}
	if mailPort.sent[0].Body != "Здравствуйте, ООО Ромашка!" {
		t.Fatalf("тело %q", mailPort.sent[0].Body)
	}
	if mailPort.sent[0].Subject != "Тема для ООО Ромашка" {
		t.Fatalf("тема %q", mailPort.sent[0].Subject)
	}

	history, err := messages.ListByLead(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("ListByLead: %v", err)
	}
	if len(history) != 1 || history[0].ID() != "key-1" {
		t.Fatalf("история = %+v", history)
	}
	if history[0].Template() != "intro@1" {
		t.Fatalf("ссылка на шаблон = %q, ожидалась intro@1", history[0].Template())
	}
}

func TestSendToLeadUnknownTemplate(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "ООО Ромашка")
	contacts := newMemContacts()
	seedContact(contacts, "example.com", "anna@example.com", "CEO")

	u := newSendToLead(profiles, contacts, newMemMessages(), newMemTemplates(), &fakeMail{})
	if _, err := u.Send(context.Background(), "example.com", "missing", nil, "key-3"); !errors.Is(err, ErrTemplateNotFound) {
		t.Fatalf("ожидалась ErrTemplateNotFound, получено %v", err)
	}
}

func TestSendToLeadRefusesSuppressedLead(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "ООО Ромашка")
	contacts := newMemContacts()
	seedContact(contacts, "example.com", "anna@example.com", "CEO")
	messages := newMemMessages()
	templates := newMemTemplates()
	seedTemplate(templates, "intro", "Тема", "Тело")

	// Подавляющий исход прошлого сообщения.
	bounced, err := domain.CreateOutboundMessage(domain.CreateOutboundMessageParams{
		ID:        "old-1",
		LeadID:    "example.com",
		Recipient: "anna@example.com",
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	bounced.MarkBounced("mailbox full", testTime)
	if err := messages.Save(context.Background(), bounced); err != nil {
		t.Fatalf("Save: %v", err)
	}

	u := newSendToLead(profiles, contacts, messages, templates, &fakeMail{})
	if _, err := u.Send(context.Background(), "example.com", "intro", nil, "key-2"); !errors.Is(err, ErrLeadSuppressed) {
		t.Fatalf("ожидалась ErrLeadSuppressed, получено %v", err)
	}
}

func TestSendToLeadKeepsHistoryImmutableAfterTemplateEdit(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "example.com", "ООО Ромашка")
	contacts := newMemContacts()
	seedContact(contacts, "example.com", "anna@example.com", "CEO")
	messages := newMemMessages()
	templates := newMemTemplates()
	seedTemplate(templates, "intro", "Тема", "Здравствуйте, {{company}}!",
		domain.MergeVariable{Key: domain.MergeVariableCompany, Fallback: "ваша компания"},
	)

	u := newSendToLead(profiles, contacts, messages, templates, &fakeMail{})
	if _, err := u.Send(context.Background(), "example.com", "intro", nil, "key-1"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Правка шаблона после отправки не переписывает уже зафиксированное сообщение.
	template, err := templates.FindByID(context.Background(), "intro")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if err := template.EditTemplate(domain.EditTemplateParams{
		Name:      "intro",
		Subject:   "Новая тема",
		Body:      "Совсем другое тело",
		UpdatedAt: testTime,
	}); err != nil {
		t.Fatalf("EditTemplate: %v", err)
	}
	if err := templates.Save(context.Background(), template); err != nil {
		t.Fatalf("Save: %v", err)
	}

	history, err := messages.ListByLead(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("ListByLead: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("история = %d сообщений, ожидалось 1", len(history))
	}
	if history[0].Template() != "intro@1" || history[0].Body() != "Здравствуйте, ООО Ромашка!" {
		t.Fatalf("сохранённое сообщение изменилось: ссылка %q, тело %q", history[0].Template(), history[0].Body())
	}
}

func TestSendToSegmentExcludesSuppressed(t *testing.T) {
	profiles := newMemProfiles()
	seedCompany(profiles, "clean.example.com", "Clean Corp")
	seedCompany(profiles, "suppressed.example.com", "Suppressed Corp")
	contacts := newMemContacts()
	seedContact(contacts, "clean.example.com", "clean@example.com", "CEO")
	seedContact(contacts, "suppressed.example.com", "sup@example.com", "CEO")
	messages := newMemMessages()
	templates := newMemTemplates()
	seedTemplate(templates, "intro", "Тема для {{company}}", "Привет, {{company}}!",
		domain.MergeVariable{Key: domain.MergeVariableCompany, Fallback: "компания"},
	)

	suppressed, err := domain.CreateOutboundMessage(domain.CreateOutboundMessageParams{
		ID:        "old-1",
		LeadID:    "suppressed.example.com",
		Recipient: "sup@example.com",
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateOutboundMessage: %v", err)
	}
	suppressed.MarkRejected("spam", testTime)
	if err := messages.Save(context.Background(), suppressed); err != nil {
		t.Fatalf("Save: %v", err)
	}

	mailPort := &fakeMail{}
	u := &SendToSegment{
		Profiles:  profiles,
		Contacts:  contacts,
		Messages:  messages,
		Templates: templates,
		Mail:      mailPort,
		From:      "sender@example.com",
		Clock:     fakeClock{t: testTime},
	}

	result, err := u.Send(context.Background(), []string{"clean.example.com", "suppressed.example.com"}, "intro", "batch-1")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Sent != 1 || result.Skipped != 1 {
		t.Fatalf("результат = %+v, ожидалось sent=1 skipped=1", result)
	}
	if len(mailPort.sent) != 1 || mailPort.sent[0].To != "clean@example.com" {
		t.Fatalf("отправленные = %+v", mailPort.sent)
	}
}
