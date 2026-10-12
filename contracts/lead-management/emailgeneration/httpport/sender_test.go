package httpport_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lead-gen/contracts/lead-management/emailgeneration"
	"lead-gen/contracts/lead-management/emailgeneration/httpport"
)

func TestGenerateTransportsSenderSnapshotAndLegacyPayloadRemainsOptional(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"subject":"Тема","body":"Тело"}`)
	}))
	defer server.Close()
	client, err := httpport.New(server.URL, httpport.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Generate(context.Background(), emailgeneration.Request{
		LeadID: "lead.example", Criteria: "Предложение",
		SenderCompany: &emailgeneration.SenderCompany{
			CompanyName: "Отправитель", Website: "https://sender.example", Description: "Описание",
			Services: []emailgeneration.SenderServiceSnapshot{{Name: "Услуга", Short: "Кратко", Full: "Подробно"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	sender, ok := payload["sender_company"].(map[string]any)
	if !ok || sender["company_name"] != "Отправитель" || sender["website"] != "https://sender.example" || sender["description"] != "Описание" {
		t.Fatalf("wire sender = %#v", payload["sender_company"])
	}
	services, ok := sender["services"].([]any)
	if !ok || len(services) != 1 || services[0].(map[string]any)["full"] != "Подробно" {
		t.Fatalf("wire services = %#v", sender["services"])
	}
	encoded, _ := json.Marshal(payload)
	for _, forbidden := range []string{"email", "password", "secret", "phone", "contact"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Errorf("payload содержит поле %q: %s", forbidden, encoded)
		}
	}

	payload = nil
	_, err = client.Generate(context.Background(), emailgeneration.Request{LeadID: "legacy", Criteria: "legacy offer"})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := payload["sender_company"]; exists {
		t.Fatalf("legacy payload добавил пустой snapshot: %#v", payload)
	}
}
