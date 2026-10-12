package requestintent_test

import (
	"encoding/json"
	"strings"
	"testing"

	"lead-gen/contracts/request-fulfillment/requestintent"
)

func TestEditorialIntentJSONRoundTripContainsOnlySenderAllowlist(t *testing.T) {
	intent := requestintent.SubmitIntent{
		ExecutionPurpose: "editorial",
		SenderCompany: &requestintent.SenderCompanySnapshot{
			CompanyName: "ООО Ромашка", Website: "https://example.test", Description: "CRM",
			Services: []requestintent.SenderServiceSnapshot{{Name: "Автоматизация", Short: "Подбор", Full: "Внедрение CRM"}},
		},
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{`"execution_purpose":"editorial"`, `"sender_company"`, `"company_name":"ООО Ромашка"`, `"website":"https://example.test"`, `"description":"CRM"`, `"services"`, `"short":"Подбор"`, `"full":"Внедрение CRM"`} {
		if !strings.Contains(text, want) {
			t.Errorf("JSON %s не содержит %s", text, want)
		}
	}
	for _, forbidden := range []string{"password", "secret", "inn", "email", "phone", "contact"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("JSON содержит запрещённое поле %q: %s", forbidden, text)
		}
	}
	var decoded requestintent.SubmitIntent
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ExecutionPurpose != intent.ExecutionPurpose || decoded.SenderCompany == nil || decoded.SenderCompany.Services[0] != intent.SenderCompany.Services[0] {
		t.Fatalf("round-trip = %+v", decoded)
	}
}

func TestLegacyIntentWithoutPurposeOrSenderRemainsEmpty(t *testing.T) {
	var intent requestintent.SubmitIntent
	if err := json.Unmarshal([]byte(`{"request_id":"r","content":"offer"}`), &intent); err != nil {
		t.Fatal(err)
	}
	if intent.ExecutionPurpose != "" || intent.SenderCompany != nil {
		t.Fatalf("legacy intent = %+v", intent)
	}
}
