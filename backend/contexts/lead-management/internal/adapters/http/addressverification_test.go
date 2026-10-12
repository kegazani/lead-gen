package httpapi_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	httpapi "lead-gen/backend/contexts/lead-management/internal/adapters/http"
	"lead-gen/contracts/lead-management/addressverification"
	"lead-gen/contracts/lead-management/addressverification/httpport"
)

// newAddressVerificationClient собирает сервер роли api с портом верификации и
// сетевой клиент контракта address-verification к нему (round-trip).
func newAddressVerificationClient(t *testing.T, port addressverification.Port) *httpport.Client {
	t.Helper()
	server := httpapi.NewServer(httpapi.Dependencies{AddressVerification: port})
	ts := httptest.NewServer(server)
	t.Cleanup(ts.Close)
	client, err := httpport.New(ts.URL, httpport.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	return client
}

// TestAddressVerificationHTTPRoundTrip проверяет сквозной путь контракта: сетевой
// клиент → POST /address-verification → порт владельца → снимок, читаемый клиентом.
func TestAddressVerificationHTTPRoundTrip(t *testing.T) {
	var got addressverification.Query
	port := addressverification.InProc(addressverification.InProcHandler{
		VerificationFunc: func(_ context.Context, q addressverification.Query) (addressverification.Snapshot, error) {
			got = q
			return addressverification.Snapshot{
				RecipientRef: q.RecipientRef,
				Status:       addressverification.StatusDeliverable,
				Verified:     true,
				Reason:       "принят",
			}, nil
		},
	})
	client := newAddressVerificationClient(t, port)

	snapshot, err := client.Verification(context.Background(), addressverification.Query{
		TenantID:     "tenant-1",
		RecipientRef: "contact-7",
	})
	if err != nil {
		t.Fatalf("Verification: %v", err)
	}
	if got.TenantID != "tenant-1" || got.RecipientRef != "contact-7" {
		t.Fatalf("порт получил запрос %+v", got)
	}
	if snapshot.Status != addressverification.StatusDeliverable || !snapshot.Verified {
		t.Fatalf("снимок = %+v", snapshot)
	}
}

// TestAddressVerificationHTTPRejects проверяет перевод доменного отказа владельца в
// 400 и ErrRejected на стороне клиента: вердикт не выдумывается.
func TestAddressVerificationHTTPRejects(t *testing.T) {
	port := addressverification.InProc(addressverification.InProcHandler{
		VerificationFunc: func(context.Context, addressverification.Query) (addressverification.Snapshot, error) {
			return addressverification.Snapshot{}, addressverification.ErrRejected
		},
	})
	client := newAddressVerificationClient(t, port)

	_, err := client.Verification(context.Background(), addressverification.Query{
		TenantID: "tenant-1", RecipientRef: "missing",
	})
	if !errors.Is(err, addressverification.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestAddressVerificationHTTPUnavailable проверяет, что недоступность владельца
// честно отвечает недоступностью, а не выдуманным вердиктом.
func TestAddressVerificationHTTPUnavailable(t *testing.T) {
	port := addressverification.InProc(addressverification.InProcHandler{
		VerificationFunc: func(context.Context, addressverification.Query) (addressverification.Snapshot, error) {
			return addressverification.Snapshot{}, addressverification.ErrUnavailable
		},
	})
	client := newAddressVerificationClient(t, port)

	_, err := client.Verification(context.Background(), addressverification.Query{
		TenantID: "tenant-1", RecipientRef: "contact-7",
	})
	if !errors.Is(err, addressverification.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestAddressVerificationHTTPNotMounted проверяет, что незамонтированный контракт
// честно отвечает недоступностью, а не выдуманным вердиктом.
func TestAddressVerificationHTTPNotMounted(t *testing.T) {
	client := newAddressVerificationClient(t, nil)

	_, err := client.Verification(context.Background(), addressverification.Query{
		TenantID: "tenant-1", RecipientRef: "contact-7",
	})
	if !errors.Is(err, addressverification.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
