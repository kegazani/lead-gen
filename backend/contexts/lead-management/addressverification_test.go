package leadmanagement

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/contracts/lead-management/addressverification"
)

// stubDNS — детерминированный DNS-резолвер теста: заданный набор MX.
type stubDNS struct{ mx []string }

func (s stubDNS) LookupMX(context.Context, string) ([]string, error)   { return s.mx, nil }
func (s stubDNS) LookupHost(context.Context, string) ([]string, error) { return nil, nil }

// stubVerifier — детерминированный сервис верификации теста.
type stubVerifier struct {
	verdict ports.MailboxVerdict
	err     error
}

func (s stubVerifier) VerifyMailbox(context.Context, string) (ports.MailboxVerdict, error) {
	return s.verdict, s.err
}

// TestNewAddressVerificationReturnsOwnerVerdict проверяет перевод доменного
// статуса верификации в форму контракта: подтверждённый адрес даёт Verified.
func TestNewAddressVerificationReturnsOwnerVerdict(t *testing.T) {
	contacts := memory.NewContactRepository()
	seedDeliveryContact(t, contacts, "ivan@example.com", "ivan@example.com")

	port := NewAddressVerification(AddressVerificationDeps{
		Repositories: Repositories{Contacts: contacts},
		TenantID:     "tenant-1",
		DNS:          stubDNS{mx: []string{"mx.example.com"}},
		Verifier:     stubVerifier{verdict: ports.MailboxVerdict{Outcome: ports.MailboxAccepted, Reason: "принят", Source: "stub"}},
	})
	snapshot, err := port.Verification(context.Background(), addressverification.Query{
		TenantID: "tenant-1", RecipientRef: "ivan@example.com",
	})
	if err != nil {
		t.Fatalf("Verification: %v", err)
	}
	if snapshot.Status != addressverification.StatusDeliverable {
		t.Fatalf("статус = %q, ожидался deliverable", snapshot.Status)
	}
	if !snapshot.Verified {
		t.Fatal("подтверждённый адрес должен иметь Verified=true")
	}
}

// TestNewAddressVerificationUnavailableVerifierIsError проверяет, что
// недоступность внешней проверки — ошибка контракта, а не «подтверждён».
func TestNewAddressVerificationUnavailableVerifierIsError(t *testing.T) {
	contacts := memory.NewContactRepository()
	seedDeliveryContact(t, contacts, "ivan@example.com", "ivan@example.com")

	port := NewAddressVerification(AddressVerificationDeps{
		Repositories: Repositories{Contacts: contacts},
		TenantID:     "tenant-1",
		DNS:          stubDNS{mx: []string{"mx.example.com"}},
		Verifier:     stubVerifier{err: ports.ErrVerifierUnavailable},
	})
	_, err := port.Verification(context.Background(), addressverification.Query{
		TenantID: "tenant-1", RecipientRef: "ivan@example.com",
	})
	if !errors.Is(err, addressverification.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestNewAddressVerificationRejectsForeignTenant проверяет изоляцию тенанта:
// чужая ссылка — доменный отказ, а не статус.
func TestNewAddressVerificationRejectsForeignTenant(t *testing.T) {
	contacts := memory.NewContactRepository()
	seedDeliveryContact(t, contacts, "ivan@example.com", "ivan@example.com")

	port := NewAddressVerification(AddressVerificationDeps{
		Repositories: Repositories{Contacts: contacts},
		TenantID:     "tenant-1",
		DNS:          stubDNS{mx: []string{"mx.example.com"}},
		Verifier:     stubVerifier{verdict: ports.MailboxVerdict{Outcome: ports.MailboxAccepted}},
	})
	_, err := port.Verification(context.Background(), addressverification.Query{
		TenantID: "tenant-2", RecipientRef: "ivan@example.com",
	})
	if !errors.Is(err, addressverification.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestNewAddressVerificationWithoutRepositoryIsUnavailable проверяет честную
// недоступность, когда владелец не собран: статус не выдумывается.
func TestNewAddressVerificationWithoutRepositoryIsUnavailable(t *testing.T) {
	port := NewAddressVerification(AddressVerificationDeps{TenantID: "tenant-1"})
	_, err := port.Verification(context.Background(), addressverification.Query{
		TenantID: "tenant-1", RecipientRef: "ivan@example.com",
	})
	if !errors.Is(err, addressverification.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
