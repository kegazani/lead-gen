package researchmemory

import (
	"errors"
	contract "lead-gen/contracts/lead-management/researchmemory"
	"strings"
	"testing"
	"time"
)

func testObservation() contract.SaveObservationRequest {
	body := "Компания Альфа\nИНН 7707083893\nEmail Info@example.org\n"
	now := time.Now().UTC()
	fresh := now.Add(time.Hour)
	locator := func(s string) contract.Locator {
		n := strings.Index(body, s)
		return contract.Locator{Start: n, End: n + len(s), Quote: s}
	}
	return contract.SaveObservationRequest{Scope: contract.Scope{TenantID: "tenant", Purpose: "lead-research"}, ObservationID: "obs", MissionID: "mission", ActionID: "action", QueryAttemptID: "attempt", Source: contract.Source{URL: "https://example.org/company", Revision: "v1", Representation: "markdown", Content: body, ObservedAt: now, Kind: "page"}, Subject: contract.Subject{Key: "alpha", Name: "Компания Альфа", Kind: "company", Locator: locator(body)}, Identifiers: []contract.Identifier{{Kind: "inn", Jurisdiction: "RU", Authority: "fns", Original: "7707083893", Method: "source-block", MethodVersion: "1", Locator: locator("7707083893")}}, Claims: []contract.Claim{{Predicate: "email", Value: "Info@example.org", SubjectKey: "alpha", Locator: locator("Info@example.org"), Knowledge: "confirmed", Method: "subject-block", MethodVersion: "1", FreshUntil: &fresh}}}
}
func TestObservationRequiresSubjectLocalEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*contract.SaveObservationRequest)
	}{
		{"footer", func(r *contract.SaveObservationRequest) {
			r.Subject.Locator.End = len("Компания Альфа")
			r.Subject.Locator.Quote = "Компания Альфа"
		}},
		{"invented quote", func(r *contract.SaveObservationRequest) { r.Claims[0].Locator.Quote = "nobody@example.org" }},
		{"llm confirmation", func(r *contract.SaveObservationRequest) { r.Claims[0].Method = "llm" }},
		{"snippet confirmation", func(r *contract.SaveObservationRequest) { r.Source.Kind = "search-snippet" }},
		{"other subject", func(r *contract.SaveObservationRequest) { r.Claims[0].SubjectKey = "publisher" }},
		{"missing purpose", func(r *contract.SaveObservationRequest) { r.Scope.Purpose = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testObservation()
			tc.change(&r)
			if _, e := NormalizeObservation(r); !errors.Is(e, contract.ErrInvalid) {
				t.Fatalf("ожидался отказ, получено %v", e)
			}
		})
	}
}
func TestIdentifierFormatDoesNotConfirmExistence(t *testing.T) {
	r := testObservation()
	n, e := NormalizeObservation(r)
	if e != nil {
		t.Fatal(e)
	}
	if n.Identifiers[0].Format != "valid" || n.Identifiers[0].Existence != "unknown" || StrongIdentifier(n.Identifiers[0]) {
		t.Fatalf("формат выдан за существование: %+v", n.Identifiers[0])
	}
	r.Source.Kind = "registry"
	r.Source.Authority = "fns"
	r.Identifiers[0].Method = "registry-record"
	n, e = NormalizeObservation(r)
	if e != nil || !StrongIdentifier(n.Identifiers[0]) {
		t.Fatalf("реестровое основание не распознано: %v %+v", e, n.Identifiers)
	}
}
func TestIdentifierNormalizationPreservesJurisdictionAndLeadingZero(t *testing.T) {
	tests := []struct{ jurisdiction, kind, value, want string }{
		{"RU", "inn", "7707083893", "valid"}, {"RU", "inn", "7707083894", "invalid"}, {"RU", "kpp", "770701001", "unknown"},
		{"GB", "company-number", "SC012345", "valid"}, {"GB", "company-number", "00000001", "valid"}, {"GB", "company-number", "00000000", "invalid"}, {"GB", "company-number", "ZZ012345", "unknown"}, {"RU", "inn", "0000000000", "invalid"}, {"DE", "register-number", "001234", "unknown"},
		{"FR", "siren", "732829320", "valid"},
	}
	for _, tt := range tests {
		n, state := NormalizeIdentifier(contract.Identifier{Jurisdiction: tt.jurisdiction, Kind: tt.kind, Original: tt.value})
		if n != tt.value || state != tt.want {
			t.Fatalf("%+v -> %s/%s", tt, n, state)
		}
	}
}
func TestContactNormalizationDoesNotInventPhoneCountry(t *testing.T) {
	for _, v := range []string{"173525620", "249375288", "249376288", "9032007", "2026-10-12", "example.org/id-173525", "+7 abc 1234567890"} {
		if _, ok := NormalizePhone(v); ok {
			t.Fatalf("принят не контакт: %s", v)
		}
	}
	if p, ok := NormalizePhone("+44 (20) 7946-0958"); !ok || p != "+442079460958" {
		t.Fatalf("телефон %s %v", p, ok)
	}
	if e, ok := NormalizeEmail("Sales@EXAMPLE.ORG"); !ok || e != "Sales@example.org" {
		t.Fatalf("утрачен local-part: %s", e)
	}
}
func TestCompanyWithoutContactOrDomainIsValid(t *testing.T) {
	r := testObservation()
	r.Claims = nil
	r.Identifiers = nil
	if _, e := NormalizeObservation(r); e != nil {
		t.Fatal(e)
	}
}
