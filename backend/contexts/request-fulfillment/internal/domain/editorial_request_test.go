package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

func TestRequestSnapshotPersistsEditorialDataAndDeepCopiesServices(t *testing.T) {
	sender := &domain.SenderCompanySnapshot{CompanyName: "ООО Ромашка", Website: "https://example.test", Description: "CRM", Services: []domain.SenderServiceSnapshot{{Name: "Автоматизация", Short: "Подбор", Full: "Внедрение"}}}
	request, err := domain.NewRequest(domain.NewRequestParams{ID: "req-editorial", OfferID: "offer-editorial", TenantID: "tenant-1", Content: "Offer", Target: "B2B", ExecutionPurpose: domain.ExecutionPurposeEditorial, SenderCompany: sender, Actor: "client", At: time.Now().UTC(), EventID: "evt-editorial"})
	if err != nil {
		t.Fatal(err)
	}
	sender.Services[0].Name = "mutated input"
	snapshot := request.Snapshot()
	if snapshot.ExecutionPurpose != domain.ExecutionPurposeEditorial || snapshot.SenderCompany.Services[0].Name != "Автоматизация" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	request.SenderCompanySnapshot().Services[0].Name = "mutated read"
	rehydrated, err := domain.RehydrateRequest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if rehydrated.ExecutionPurpose() != domain.ExecutionPurposeEditorial || rehydrated.SenderCompanySnapshot().Services[0].Name != "Автоматизация" {
		t.Fatal("snapshot не пережил rehydrate/deep copy")
	}
}

func TestLegacySnapshotHasNoSyntheticDeliveryFields(t *testing.T) {
	var snapshot domain.RequestSnapshot
	if err := json.Unmarshal([]byte(`{"ID":"req-old","TenantID":"tenant-1","Status":"submitted","Version":1,"Offer":{"ID":"offer-old","Content":"Offer","Target":"","Revision":1},"Transitions":[{"To":"submitted","At":"2026-01-01T00:00:00Z"}]}`), &snapshot); err != nil {
		t.Fatal(err)
	}
	request, err := domain.RehydrateRequest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if request.ExecutionPurpose() != domain.ExecutionPurposeDelivery || request.SenderCompanySnapshot() != nil {
		t.Fatalf("legacy defaults = %q / %+v", request.ExecutionPurpose(), request.SenderCompanySnapshot())
	}
	raw, err := json.Marshal(request.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"ExecutionPurpose", "SenderCompany"} {
		if strings.Contains(string(raw), field) {
			t.Errorf("legacy snapshot acquired synthetic %s: %s", field, raw)
		}
	}
}
