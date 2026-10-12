package httpport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/content-production/contentrequest"
)

func newClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}

func TestRequestContentSuccess(t *testing.T) {
	var gotPath, gotKey string
	var gotBody map[string]any
	client := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("Idempotency-Key")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"asset_id":"asset-1","version_id":"v1","render_job_id":"job-1","state":"requested"}`)
	}))

	result, err := client.RequestContent(context.Background(), contentrequest.Intent{
		RequestID:  "req-1",
		TenantID:   "tenant-1",
		AssetID:    "asset-1",
		Format:     "document",
		Language:   "ru",
		TemplateID: "doc",
		Body:       "Тема\n\nТело",
		Actor:      "actor-1",
	})
	if err != nil {
		t.Fatalf("RequestContent: %v", err)
	}
	if gotPath != "/content/produce" {
		t.Fatalf("путь = %q", gotPath)
	}
	if gotKey != "req-1" {
		t.Fatalf("Idempotency-Key = %q", gotKey)
	}
	if gotBody["format"] != "document" {
		t.Fatalf("тело = %v", gotBody)
	}
	if result.AssetID != "asset-1" || result.VersionID != "v1" {
		t.Fatalf("результат неполон: %+v", result)
	}
}

func TestRequestContentRejected(t *testing.T) {
	client := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"пустой текст"}`)
	}))
	_, err := client.RequestContent(context.Background(), contentrequest.Intent{RequestID: "r"})
	if !errors.Is(err, contentrequest.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

func TestRequestContentUnavailable(t *testing.T) {
	client := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":"недоступно"}`)
	}))
	_, err := client.RequestContent(context.Background(), contentrequest.Intent{RequestID: "r"})
	if !errors.Is(err, contentrequest.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
