package leadmanagement

import (
	"context"
	"net/http/httptest"
	"testing"

	httpapi "lead-gen/backend/contexts/lead-management/internal/adapters/http"
	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/contracts/lead-management/addressverification"
	"lead-gen/contracts/lead-management/addressverification/httpport"
)

// TestAddressVerificationNetworkMatchesInProc проверяет, что вердикт сетевой
// поверхности совпадает с in-proc адаптером контракта: смена транспорта не меняет
// наблюдаемый вердикт владельца (design.md, Решение 1).
func TestAddressVerificationNetworkMatchesInProc(t *testing.T) {
	contacts := memory.NewContactRepository()
	seedDeliveryContact(t, contacts, "ivan@example.com", "ivan@example.com")

	port := NewAddressVerification(AddressVerificationDeps{
		Repositories: Repositories{Contacts: contacts},
		TenantID:     "tenant-1",
		DNS:          stubDNS{mx: []string{"mx.example.com"}},
		Verifier:     stubVerifier{verdict: ports.MailboxVerdict{Outcome: ports.MailboxAccepted, Reason: "принят", Source: "stub"}},
	})
	query := addressverification.Query{TenantID: "tenant-1", RecipientRef: "ivan@example.com"}

	inProc, err := port.Verification(context.Background(), query)
	if err != nil {
		t.Fatalf("in-proc Verification: %v", err)
	}

	server := httpapi.NewServer(httpapi.Dependencies{AddressVerification: port})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	client, err := httpport.New(ts.URL, httpport.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	overNetwork, err := client.Verification(context.Background(), query)
	if err != nil {
		t.Fatalf("сетевое Verification: %v", err)
	}

	if inProc.Status != overNetwork.Status || inProc.Verified != overNetwork.Verified {
		t.Fatalf("вердикт расходится: in-proc %+v, сеть %+v", inProc, overNetwork)
	}
	if !overNetwork.Verified || overNetwork.Status != addressverification.StatusDeliverable {
		t.Fatalf("сетевой снимок = %+v", overNetwork)
	}
}
