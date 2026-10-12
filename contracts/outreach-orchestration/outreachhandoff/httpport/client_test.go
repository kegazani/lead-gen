package httpport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
)

func command() outreachhandoff.Command {
	return outreachhandoff.Command{
		RequestID: "req-1",
		TenantID:  "tenant-1",
		Actor:     "operator-1",
		Items: []outreachhandoff.Item{
			{LeadID: "example.com", RecipientRef: "info@example.com", MaterialRef: "asset@v1"},
		},
	}
}

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

func TestSubmitSuccess(t *testing.T) {
	var gotPath, gotKey string
	var gotBody map[string]any
	client := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("Idempotency-Key")
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		_, _ = io.WriteString(w, `{"campaign_id":"camp-req-1","accepted":1,"skipped":0,"repeated":false}`)
	}))

	result, err := client.Submit(context.Background(), command())
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if gotPath != "/outreach-handoff" {
		t.Fatalf("путь = %q", gotPath)
	}
	if gotKey != "req-1" {
		t.Fatalf("Idempotency-Key = %q", gotKey)
	}
	if gotBody["request_id"] != "req-1" {
		t.Fatalf("тело = %v", gotBody)
	}
	if result.CampaignID != "camp-req-1" || result.Accepted != 1 {
		t.Fatalf("результат неполон: %+v", result)
	}
}

func TestSubmitRejected(t *testing.T) {
	client := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"нет состава"}`)
	}))
	_, err := client.Submit(context.Background(), command())
	if !errors.Is(err, outreachhandoff.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

func TestSubmitUnavailable(t *testing.T) {
	client := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":"недоступно"}`)
	}))
	_, err := client.Submit(context.Background(), command())
	if !errors.Is(err, outreachhandoff.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

func TestCancelRequestUsesOwnerFenceEndpoint(t *testing.T) {
	var gotPath, gotKey string
	var gotBody cancelRequest
	client := newClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("Idempotency-Key")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode cancellation: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	input := outreachhandoff.RequestCancellation{TenantID: "tenant-1", RequestID: "req-1"}
	if err := client.CancelRequest(context.Background(), input); err != nil {
		t.Fatalf("CancelRequest: %v", err)
	}
	if gotPath != "/outreach-handoff/cancellation" || gotKey != "tenant-1/req-1" {
		t.Fatalf("path/key = %q/%q", gotPath, gotKey)
	}
	if gotBody.TenantID != input.TenantID || gotBody.RequestID != input.RequestID {
		t.Fatalf("body = %+v", gotBody)
	}
}
