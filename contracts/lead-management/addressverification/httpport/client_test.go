package httpport_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/lead-management/addressverification"
	"lead-gen/contracts/lead-management/addressverification/httpport"
)

// newClient собирает адаптер на тестовом сервере.
func newClient(t *testing.T, server *httptest.Server) *httpport.Client {
	t.Helper()
	client, err := httpport.New(server.URL, httpport.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

// TestVerificationSuccess проверяет успешный запрос: метод, путь, тело запроса и
// перевод ответа владельца в Snapshot.
func TestVerificationSuccess(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"recipient_ref":"contact-7","status":"deliverable","verified":true,"reason":"принят","checked_at":"2026-10-06T12:00:00Z"}`)
	}))
	defer server.Close()

	snapshot, err := newClient(t, server).Verification(context.Background(), addressverification.Query{
		TenantID: "tenant-1", RecipientRef: "contact-7",
	})
	if err != nil {
		t.Fatalf("Verification: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/address-verification" {
		t.Fatalf("запрос %s %s", gotMethod, gotPath)
	}
	if gotBody["tenant_id"] != "tenant-1" || gotBody["recipient_ref"] != "contact-7" {
		t.Fatalf("тело запроса = %+v", gotBody)
	}
	if snapshot.Status != addressverification.StatusDeliverable || !snapshot.Verified {
		t.Fatalf("снимок = %+v", snapshot)
	}
	if snapshot.RecipientRef != "contact-7" || snapshot.Reason != "принят" {
		t.Fatalf("снимок = %+v", snapshot)
	}
	if snapshot.CheckedAt.IsZero() {
		t.Fatal("CheckedAt не разобран")
	}
}

// TestErrorMapping проверяет перевод HTTP-кодов владельца в исходы контракта:
// 400 и 404 — доменный отказ, прочее — недоступность.
func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"400 отклонение", http.StatusBadRequest, addressverification.ErrRejected},
		{"404 неизвестная ссылка", http.StatusNotFound, addressverification.ErrRejected},
		{"409 конфликт", http.StatusConflict, addressverification.ErrUnavailable},
		{"500 сбой", http.StatusInternalServerError, addressverification.ErrUnavailable},
		{"503 недоступно", http.StatusServiceUnavailable, addressverification.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"error":"отказ владельца"}`, tc.status)
			}))
			defer server.Close()

			_, err := newClient(t, server).Verification(context.Background(), addressverification.Query{
				TenantID: "tenant-1", RecipientRef: "contact-7",
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Verification(%d) ошибка = %v, ожидалась %v", tc.status, err, tc.want)
			}
		})
	}
}

// TestUnknownStatusUnavailable проверяет, что успешный ответ без распознанного
// состояния верификации считается недоступностью: вердикт не выдумывается.
func TestUnknownStatusUnavailable(t *testing.T) {
	for _, body := range []string{
		`{"recipient_ref":"contact-7"}`,
		`{"recipient_ref":"contact-7","status":"made-up","verified":true}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}))

		_, err := newClient(t, server).Verification(context.Background(), addressverification.Query{
			TenantID: "tenant-1", RecipientRef: "contact-7",
		})
		if !errors.Is(err, addressverification.ErrUnavailable) {
			t.Fatalf("тело %s: ошибка = %v, ожидалась ErrUnavailable", body, err)
		}
		server.Close()
	}
}

// TestUnverifiedStatusIsVerdict проверяет, что распознанный статус с вердиктом
// «не подтверждён» доходит как снимок, а не как ошибка: потребитель сам выводит
// признак риска.
func TestUnverifiedStatusIsVerdict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"risky","verified":false,"reason":"catch-all"}`)
	}))
	defer server.Close()

	snapshot, err := newClient(t, server).Verification(context.Background(), addressverification.Query{
		TenantID: "tenant-1", RecipientRef: "contact-7",
	})
	if err != nil {
		t.Fatalf("Verification: %v", err)
	}
	if snapshot.Verified || snapshot.Status != addressverification.StatusRisky {
		t.Fatalf("снимок = %+v", snapshot)
	}
	if snapshot.RecipientRef != "contact-7" {
		t.Fatalf("ссылка не подставлена из запроса: %+v", snapshot)
	}
}

// TestMalformedSuccessBodyUnavailable проверяет, что неразборчивый успешный ответ
// считается сбоем транспорта, а не выдуманным вердиктом.
func TestMalformedSuccessBodyUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{не json`)
	}))
	defer server.Close()

	_, err := newClient(t, server).Verification(context.Background(), addressverification.Query{
		TenantID: "tenant-1", RecipientRef: "contact-7",
	})
	if !errors.Is(err, addressverification.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestNetworkFailureUnavailable проверяет, что недоступный сервис даёт
// ErrUnavailable, а не доменный исход.
func TestNetworkFailureUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	client, err := httpport.New(url)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Verification(context.Background(), addressverification.Query{
		TenantID: "tenant-1", RecipientRef: "contact-7",
	})
	if !errors.Is(err, addressverification.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestNewRejectsInvalidBaseURL проверяет, что некорректный базовый адрес виден при
// сборке адаптера, а не при первом запросе.
func TestNewRejectsInvalidBaseURL(t *testing.T) {
	for _, base := range []string{"", "   ", "://", "lead-management:8080", "ftp://host"} {
		if _, err := httpport.New(base); err == nil {
			t.Errorf("New(%q) ошибки не вернул", base)
		}
	}
}
