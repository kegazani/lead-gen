package httpport_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/request-fulfillment/requestintent"
	"lead-gen/contracts/request-fulfillment/requestintent/httpport"
)

func TestSubmitTransportsEditorialPurposeAndSenderAllowlist(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &payload)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"request_id":"req-1","status":"submitted"}`)
	}))
	defer server.Close()
	client, err := httpport.New(server.URL, httpport.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Submit(context.Background(), requestintent.SubmitIntent{
		RequestID: "req-1", TenantID: "tenant-1", Title: "Editorial offer", Content: "Offer", Target: "B2B",
		ExecutionPurpose: "editorial",
		SenderCompany: &requestintent.SenderCompanySnapshot{
			CompanyName: "Отправитель", Website: "https://sender.example", Description: "CRM",
			Services: []requestintent.SenderServiceSnapshot{{Name: "Услуга", Short: "Кратко", Full: "Подробно"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if payload["execution_purpose"] != "editorial" || payload["content"] != "Offer" || payload["target"] != "B2B" {
		t.Fatalf("intent = %+v", payload)
	}
	sender, ok := payload["sender_company"].(map[string]any)
	if !ok || sender["company_name"] != "Отправитель" || sender["website"] != "https://sender.example" || sender["description"] != "CRM" {
		t.Fatalf("sender = %#v", payload["sender_company"])
	}
	services := sender["services"].([]any)
	if len(services) != 1 || services[0].(map[string]any)["short"] != "Кратко" {
		t.Fatalf("services = %#v", sender["services"])
	}
}
