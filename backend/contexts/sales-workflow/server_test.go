package salesworkflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	crmunavailable "lead-gen/backend/contexts/sales-workflow/internal/adapters/crm/unavailable"
	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/contracts/sales-workflow/crmevent"
	"lead-gen/contracts/sales-workflow/dealstate"
	"lead-gen/contracts/sales-workflow/envelope"
)

func doJSON(t *testing.T, handler http.Handler, method, target string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(payload)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var decoded map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("разбор ответа %q: %v", rec.Body.String(), err)
		}
	}
	return rec, decoded
}

func TestAPIServerLifecycle(t *testing.T) {
	e := newEnvironment(t, nil)

	rec, _ := doJSON(t, e.slice.APIServer, http.MethodPost, "/deals", map[string]any{
		"deal_id":      "deal-1",
		"tenant_id":    "tenant-1",
		"company_ref":  "company-1",
		"external_ref": "crm-deal-1",
		"actor":        "operator-1",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /deals = %d, тело %s", rec.Code, rec.Body.String())
	}

	rec, _ = doJSON(t, e.slice.APIServer, http.MethodPost, "/deals/deal-1/stage", map[string]any{
		"tenant_id": "tenant-1",
		"actor":     "operator-1",
		"to":        "qualified",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST stage = %d, тело %s", rec.Code, rec.Body.String())
	}
	rec, _ = doJSON(t, e.slice.APIServer, http.MethodPost, "/deals/deal-1/stage", map[string]any{
		"tenant_id": "tenant-1",
		"actor":     "operator-1",
		"to":        "proposal",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST stage = %d, тело %s", rec.Code, rec.Body.String())
	}

	rec, decoded := doJSON(t, e.slice.APIServer, http.MethodGet, "/deals/deal-1?tenant_id=tenant-1", nil)
	if rec.Code != http.StatusOK || decoded["stage"] != string(domain.StageProposal) {
		t.Fatalf("GET /deals = %d, тело %s", rec.Code, rec.Body.String())
	}

	rec, _ = doJSON(t, e.slice.APIServer, http.MethodPost, "/deals/deal-1/attribution", map[string]any{
		"tenant_id":    "tenant-1",
		"actor":        "operator-1",
		"action_ref":   "action-1",
		"evidence_ref": "evidence-1",
		"external_ref": "crm-deal-1",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("POST attribution = %d, тело %s", rec.Code, rec.Body.String())
	}

	rec, decoded = doJSON(t, e.slice.APIServer, http.MethodGet, "/deals/deal-1/crm-sync?tenant_id=tenant-1", nil)
	if rec.Code != http.StatusOK || decoded["status"] != string(domain.SyncSynced) {
		t.Fatalf("GET crm-sync = %d, тело %s", rec.Code, rec.Body.String())
	}

	rec, _ = doJSON(t, e.slice.APIServer, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d", rec.Code)
	}
}

func TestAPIServerErrorMapping(t *testing.T) {
	e := newEnvironment(t, nil)
	e.createDeal(t, "deal-1", 1)

	// Чужой тенант не раскрывает существование сделки.
	rec, _ := doJSON(t, e.slice.APIServer, http.MethodGet, "/deals/deal-1?tenant_id=tenant-other", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("чужой тенант = %d, ожидался 404", rec.Code)
	}

	// Недопустимый переход — 409.
	rec, _ = doJSON(t, e.slice.APIServer, http.MethodPost, "/deals/deal-1/stage", map[string]any{
		"tenant_id": "tenant-1",
		"actor":     "operator-1",
		"to":        "won",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("недопустимый переход = %d, ожидался 409", rec.Code)
	}

	// Битый JSON — 400.
	req := httptest.NewRequest(http.MethodPost, "/deals", bytes.NewReader([]byte("{не json")))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	e.slice.APIServer.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("битый JSON = %d, ожидался 400", rec.Code)
	}
}

func TestAPIServerCRMUnavailable(t *testing.T) {
	e := newEnvironment(t, func(cfg *Config) { cfg.CRM = crmunavailable.New() })
	rec, _ := doJSON(t, e.slice.APIServer, http.MethodPost, "/deals", map[string]any{
		"deal_id":      "deal-1",
		"tenant_id":    "tenant-1",
		"company_ref":  "company-1",
		"external_ref": "crm-deal-1",
		"actor":        "operator-1",
	})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("недоступная CRM = %d, ожидался 503", rec.Code)
	}
}

func TestIngestServerAppliesAndConflicts(t *testing.T) {
	e := newEnvironment(t, nil)
	e.createDeal(t, "deal-1", 1)
	e.changeStage(t, "deal-1", domain.StageQualified)
	e.changeStage(t, "deal-1", domain.StageProposal)

	apply := crmevent.NewCRMEvent(envelope.Envelope{
		EventID:       "evt-crm-1",
		OccurredAt:    testNow,
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
		Name:          crmevent.ContractName,
		SchemaVersion: crmevent.SchemaVersion,
	})
	apply.DealID = "deal-1"
	apply.CompanyRef = "company-1"
	apply.ExternalRef = "crm-deal-1"
	apply.ExternalRevision = 2
	apply.Stage = crmevent.StageNegotiation

	rec, decoded := doJSON(t, e.slice.IngestServer, http.MethodPost, "/crm/events", apply)
	if rec.Code != http.StatusOK || decoded["applied"] != true {
		t.Fatalf("ingest apply = %d, тело %s", rec.Code, rec.Body.String())
	}

	conflict := apply
	conflict.EventID = "evt-crm-2"
	conflict.ExternalRevision = 5
	conflict.Stage = crmevent.StageLost
	rec, decoded = doJSON(t, e.slice.IngestServer, http.MethodPost, "/crm/events", conflict)
	if rec.Code != http.StatusOK || decoded["applied"] != false || decoded["sync_status"] != string(domain.SyncConflict) {
		t.Fatalf("ingest conflict = %d, тело %s", rec.Code, rec.Body.String())
	}

	// Неизвестный этап в контракте — 400.
	bad := apply
	bad.Stage = "nonsense"
	rec, _ = doJSON(t, e.slice.IngestServer, http.MethodPost, "/crm/events", bad)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("неизвестный этап = %d, ожидался 400", rec.Code)
	}

	rec, _ = doJSON(t, e.slice.IngestServer, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ingest /healthz = %d", rec.Code)
	}
}

func TestDealStatePortMapsNotFound(t *testing.T) {
	e := newEnvironment(t, nil)
	e.createDeal(t, "deal-1", 1)

	state, err := e.slice.DealStatePort.Get(context.Background(),
		dealstate.Query{DealID: "deal-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("DealStatePort.Get: %v", err)
	}
	if state.Stage != string(domain.StageLead) {
		t.Fatalf("состояние сделки: %+v", state)
	}

	if _, err := e.slice.DealStatePort.Get(context.Background(),
		dealstate.Query{DealID: "deal-missing", TenantID: "tenant-1"}); !errors.Is(err, dealstate.ErrNotFound) {
		t.Fatalf("отсутствующая сделка: %v", err)
	}
}
