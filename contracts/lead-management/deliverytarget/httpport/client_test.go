package httpport_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/lead-management/deliverytarget"
	"lead-gen/contracts/lead-management/deliverytarget/httpport"
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

// TestResolveSuccess проверяет успешное разрешение: метод, путь, тело запроса и
// перевод ответа владельца в Recipient.
func TestResolveSuccess(t *testing.T) {
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
		_, _ = io.WriteString(w, `{"address":"sales@lead.example"}`)
	}))
	defer server.Close()

	recipient, err := newClient(t, server).ResolveRecipient(context.Background(), "tenant-1", "contact-7")
	if err != nil {
		t.Fatalf("ResolveRecipient: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/delivery-target" {
		t.Fatalf("запрос %s %s", gotMethod, gotPath)
	}
	if gotBody["tenant_id"] != "tenant-1" || gotBody["recipient_ref"] != "contact-7" {
		t.Fatalf("тело команды = %+v", gotBody)
	}
	if recipient.Address != "sales@lead.example" {
		t.Fatalf("Address = %q", recipient.Address)
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
		{"400 отклонение", http.StatusBadRequest, deliverytarget.ErrRejected},
		{"404 неизвестная ссылка", http.StatusNotFound, deliverytarget.ErrRejected},
		{"409 конфликт", http.StatusConflict, deliverytarget.ErrUnavailable},
		{"500 сбой", http.StatusInternalServerError, deliverytarget.ErrUnavailable},
		{"503 недоступно", http.StatusServiceUnavailable, deliverytarget.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"error":"отказ владельца"}`, tc.status)
			}))
			defer server.Close()

			if _, err := newClient(t, server).ResolveRecipient(context.Background(), "tenant-1", "contact-7"); !errors.Is(err, tc.want) {
				t.Fatalf("ResolveRecipient(%d) ошибка = %v, ожидалась %v", tc.status, err, tc.want)
			}
		})
	}
}

// TestEmptyAddressUnavailable проверяет, что успешный ответ без адреса считается
// недоступностью: пустой адрес не подставляется вызывающему.
func TestEmptyAddressUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"address":"   "}`)
	}))
	defer server.Close()

	if _, err := newClient(t, server).ResolveRecipient(context.Background(), "tenant-1", "contact-7"); !errors.Is(err, deliverytarget.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestMalformedSuccessBodyUnavailable проверяет, что неразборчивый успешный ответ
// считается сбоем транспорта, а не выдуманным адресом.
func TestMalformedSuccessBodyUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{не json`)
	}))
	defer server.Close()

	if _, err := newClient(t, server).ResolveRecipient(context.Background(), "tenant-1", "contact-7"); !errors.Is(err, deliverytarget.ErrUnavailable) {
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
	if _, err := client.ResolveRecipient(context.Background(), "tenant-1", "contact-7"); !errors.Is(err, deliverytarget.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestNewRejectsInvalidBaseURL проверяет, что некорректный базовый адрес виден при
// сборке адаптера, а не при первом разрешении.
func TestNewRejectsInvalidBaseURL(t *testing.T) {
	for _, base := range []string{"", "   ", "://", "lead-management:8080", "ftp://host"} {
		if _, err := httpport.New(base); err == nil {
			t.Errorf("New(%q) ошибки не вернул", base)
		}
	}
}
