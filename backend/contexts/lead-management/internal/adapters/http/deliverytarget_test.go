package httpapi_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	httpapi "lead-gen/backend/contexts/lead-management/internal/adapters/http"
	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/contracts/lead-management/deliverytarget"
	"lead-gen/contracts/lead-management/deliverytarget/httpport"
)

// seedDeliveryTargetContact кладёт контакт только с email-адресом: резолвер
// получателя берёт адрес из атрибута email, остальные атрибуты не нужны.
func seedDeliveryTargetContact(t *testing.T, repository *memory.ContactRepository, id, address string) {
	t.Helper()
	provenance := domain.AttributeProvenance{
		SourceURL:   "https://lead.example/",
		ExtractedAt: testTime,
		Method:      domain.MethodRegex,
	}
	contact, err := domain.CreateContact(domain.CreateContactParams{
		ID:        id,
		CompanyID: domain.CompanyID("lead.example"),
		Email:     &domain.ContactAttribute{Value: address, Provenance: provenance},
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("создание контакта: %v", err)
	}
	if err := repository.Save(context.Background(), contact); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// newDeliveryTargetClient собирает сервер роли api с резолвером получателя и
// сетевой клиент контракта delivery-target к нему (round-trip).
func newDeliveryTargetClient(t *testing.T, useCase *app.ResolveDeliveryTarget) *httpport.Client {
	t.Helper()
	server := httpapi.NewServer(httpapi.Dependencies{ResolveDeliveryTarget: useCase})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	client, err := httpport.New(ts.URL, httpport.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	return client
}

// TestDeliveryTargetHTTPRoundTrip проверяет сквозной путь контракта: сетевой
// клиент → POST /delivery-target → сценарий → адрес, читаемый клиентом.
func TestDeliveryTargetHTTPRoundTrip(t *testing.T) {
	contacts := memory.NewContactRepository()
	seedDeliveryTargetContact(t, contacts, "sales@lead.example", "sales@lead.example")

	useCase := &app.ResolveDeliveryTarget{Contacts: contacts, TenantID: "tenant-1"}
	client := newDeliveryTargetClient(t, useCase)

	recipient, err := client.ResolveRecipient(context.Background(), "tenant-1", "sales@lead.example")
	if err != nil {
		t.Fatalf("ResolveRecipient: %v", err)
	}
	if recipient.Address != "sales@lead.example" {
		t.Fatalf("Address = %q, ожидался sales@lead.example", recipient.Address)
	}
}

// TestDeliveryTargetHTTPRejectsUnknownRef проверяет перевод доменного отказа в 400
// и ErrRejected на стороне клиента: неизвестная ссылка не разрешается и адрес не
// выдумывается.
func TestDeliveryTargetHTTPRejectsUnknownRef(t *testing.T) {
	contacts := memory.NewContactRepository()
	seedDeliveryTargetContact(t, contacts, "sales@lead.example", "sales@lead.example")

	useCase := &app.ResolveDeliveryTarget{Contacts: contacts, TenantID: "tenant-1"}
	client := newDeliveryTargetClient(t, useCase)

	if _, err := client.ResolveRecipient(context.Background(), "tenant-1", "missing@lead.example"); !errors.Is(err, deliverytarget.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestDeliveryTargetHTTPUnavailable проверяет, что незамонтированный контракт
// честно отвечает недоступностью, а не выдуманным адресом.
func TestDeliveryTargetHTTPUnavailable(t *testing.T) {
	client := newDeliveryTargetClient(t, nil)

	if _, err := client.ResolveRecipient(context.Background(), "tenant-1", "sales@lead.example"); !errors.Is(err, deliverytarget.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
