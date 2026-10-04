package leadmanagement

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/mail"
	"lead-gen/contracts/lead-management/deliverytarget"
)

// seedDeliveryContact кладёт в репозиторий контакт с email-адресом.
func seedDeliveryContact(t *testing.T, repository *memory.ContactRepository, id, address string) {
	t.Helper()
	provenance := domain.AttributeProvenance{
		SourceURL:   "https://lead.example/",
		ExtractedAt: time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC),
		Method:      domain.MethodRegex,
	}
	contact, err := domain.CreateContact(domain.CreateContactParams{
		ID:        id,
		CompanyID: domain.CompanyID("lead.example"),
		Email:     &domain.ContactAttribute{Value: address, Provenance: provenance},
	})
	if err != nil {
		t.Fatalf("создание контакта: %v", err)
	}
	if err := repository.Save(context.Background(), contact); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// TestNewDeliveryTargetResolves проверяет, что публичная фабрика разрешает ссылку
// в адрес по контакту своего тенанта.
func TestNewDeliveryTargetResolves(t *testing.T) {
	contacts := memory.NewContactRepository()
	seedDeliveryContact(t, contacts, "sales@lead.example", "sales@lead.example")

	resolver := NewDeliveryTarget(DeliveryTargetDeps{
		Repositories: Repositories{Contacts: contacts},
		TenantID:     "tenant-1",
	})
	recipient, err := resolver.ResolveRecipient(context.Background(), "tenant-1", "sales@lead.example")
	if err != nil {
		t.Fatalf("ResolveRecipient: %v", err)
	}
	if recipient.Address != "sales@lead.example" {
		t.Fatalf("Address = %q", recipient.Address)
	}
}

// TestNewDeliveryTargetRejectsUnknownRef проверяет перевод доменного отказа в
// ErrRejected опубликованного контракта.
func TestNewDeliveryTargetRejectsUnknownRef(t *testing.T) {
	resolver := NewDeliveryTarget(DeliveryTargetDeps{
		Repositories: Repositories{Contacts: memory.NewContactRepository()},
		TenantID:     "tenant-1",
	})
	_, err := resolver.ResolveRecipient(context.Background(), "tenant-1", "missing@lead.example")
	if !errors.Is(err, deliverytarget.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestNewDeliveryTargetRejectsForeignTenant проверяет изоляцию тенанта через
// контракт.
func TestNewDeliveryTargetRejectsForeignTenant(t *testing.T) {
	contacts := memory.NewContactRepository()
	seedDeliveryContact(t, contacts, "sales@lead.example", "sales@lead.example")

	resolver := NewDeliveryTarget(DeliveryTargetDeps{
		Repositories: Repositories{Contacts: contacts},
		TenantID:     "tenant-1",
	})
	_, err := resolver.ResolveRecipient(context.Background(), "tenant-2", "sales@lead.example")
	if !errors.Is(err, deliverytarget.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestNewDeliveryTargetWithoutRepository проверяет честную недоступность, когда
// владелец не собран: адрес не выдумывается.
func TestNewDeliveryTargetWithoutRepository(t *testing.T) {
	resolver := NewDeliveryTarget(DeliveryTargetDeps{TenantID: "tenant-1"})
	_, err := resolver.ResolveRecipient(context.Background(), "tenant-1", "sales@lead.example")
	if !errors.Is(err, deliverytarget.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// noopMailPort — порт отправки, не задействованный в сценарии резолвера.
type noopMailPort struct{}

// Send реализует mail.Port.
func (noopMailPort) Send(context.Context, mail.Message) error { return nil }

var _ mail.Port = noopMailPort{}

// TestServiceDeliveryTargetResolver проверяет мост «approved-action →
// delivery-command»: аксессор собранного сервиса отдаёт резолвер, разрешающий
// ссылку получателя в адрес по контакту того же хранилища, что использует сервис.
func TestServiceDeliveryTargetResolver(t *testing.T) {
	contacts := memory.NewContactRepository()
	seedDeliveryContact(t, contacts, "sales@lead.example", "sales@lead.example")

	service, err := New(Config{
		Profiles:  memory.NewLeadProfileRepository(),
		Contacts:  contacts,
		Jobs:      memory.NewSearchJobStore(),
		Messages:  memory.NewOutboundMessageRepository(),
		Templates: memory.NewEmailTemplateRepository(),
		Activity:  memory.NewLeadActivityStore(),
		Search:    &fakeSearch{},
		Crawl:     &fakeCrawl{},
		Mail:      noopMailPort{},
		Logger:    observ.NewLogger(),
		Metrics:   observ.NewMetrics(),
		Tracer:    observ.NewTracer(ServiceID),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resolver := service.DeliveryTargetResolver("tenant-1")
	recipient, err := resolver.ResolveRecipient(context.Background(), "tenant-1", "sales@lead.example")
	if err != nil {
		t.Fatalf("ResolveRecipient: %v", err)
	}
	if recipient.Address != "sales@lead.example" {
		t.Fatalf("Address = %q", recipient.Address)
	}
}
