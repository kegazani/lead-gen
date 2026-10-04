package remote

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/ports"
)

func TestVerifyMailbox_WithoutEndpointIsUnavailable(t *testing.T) {
	var adapter *Verifier = New(Config{})
	if _, err := adapter.VerifyMailbox(context.Background(), "sales@example.com"); !errors.Is(err, ports.ErrVerifierUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrVerifierUnavailable", err)
	}
}

func TestVerifyMailbox_MapsAcceptedWithCatchAll(t *testing.T) {
	var gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"outcome":"accepted","catch_all":true,"reason":"принят","source":"provider-x"}`))
	}))
	defer server.Close()

	adapter := New(Config{Endpoint: server.URL, APIKey: "secret"})
	verdict, err := adapter.VerifyMailbox(context.Background(), "sales@example.com")
	if err != nil {
		t.Fatalf("VerifyMailbox: %v", err)
	}
	if verdict.Outcome != ports.MailboxAccepted || !verdict.CatchAll {
		t.Fatalf("verdict = %+v", verdict)
	}
	if verdict.Reason != "принят" || verdict.Source != "provider-x" {
		t.Fatalf("основания = %+v", verdict)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotBody != `{"address":"sales@example.com"}` {
		t.Fatalf("тело запроса = %q", gotBody)
	}
}

func TestVerifyMailbox_ServerErrorIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	adapter := New(Config{Endpoint: server.URL})
	if _, err := adapter.VerifyMailbox(context.Background(), "sales@example.com"); !errors.Is(err, ports.ErrVerifierUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrVerifierUnavailable", err)
	}
}

func TestVerifyMailbox_MalformedJSONIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer server.Close()

	adapter := New(Config{Endpoint: server.URL})
	if _, err := adapter.VerifyMailbox(context.Background(), "sales@example.com"); !errors.Is(err, ports.ErrVerifierUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrVerifierUnavailable", err)
	}
}

func TestVerifyMailbox_UnknownOutcomeIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"outcome":"maybe"}`))
	}))
	defer server.Close()

	adapter := New(Config{Endpoint: server.URL})
	if _, err := adapter.VerifyMailbox(context.Background(), "sales@example.com"); !errors.Is(err, ports.ErrVerifierUnavailable) {
		t.Fatalf("нераспознанный исход: ошибка = %v, ожидалась ErrVerifierUnavailable", err)
	}
}
