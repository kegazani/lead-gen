package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	datagovernance "lead-gen/backend/contexts/data-governance"
)

const (
	basisID  = "basis-1"
	tenantID = "tenant-1"
	actorID  = "actor-1"
	contact  = "a@example.com"
)

// newTestServer собирает HTTP-сервер среза поверх памяти с одним выданным правом.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	slice, err := datagovernance.Build(datagovernance.Config{
		Grants: []datagovernance.Grant{{Actor: actorID, Tenant: tenantID}},
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// do выполняет запрос с заданным телом и возвращает ответ.
func do(t *testing.T, server *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("сборка тела: %v", err)
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, server.URL+path, payload)
	if err != nil {
		t.Fatalf("сборка запроса: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("запрос %s %s: %v", method, path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// decode разбирает тело ответа в карту.
func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	return body
}

// TestHTTPLifecycle проверяет основные обработчики среза: создание основания,
// стоп-лист, проверку подавления и политики зон, отзыв.
func TestHTTPLifecycle(t *testing.T) {
	server := newTestServer(t)

	resp := do(t, server, http.MethodGet, "/healthz", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz = %d", resp.StatusCode)
	}

	resp = do(t, server, http.MethodPost, "/bases", map[string]any{
		"basis_id": basisID, "tenant_id": tenantID, "purpose": "outreach", "category": "red",
		"retention_days": 90, "actor": actorID,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /bases = %d: %s", resp.StatusCode, body(t, resp))
	}
	if got := decode(t, resp)["status"]; got != "active" {
		t.Fatalf("статус = %v", got)
	}

	resp = do(t, server, http.MethodPost, "/bases/"+basisID+"/suppressions", map[string]any{
		"tenant_id": tenantID, "suppression_id": "sup-1", "contact": contact,
		"reason": "отзыв согласия", "actor": actorID,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST suppressions = %d: %s", resp.StatusCode, body(t, resp))
	}

	resp = do(t, server, http.MethodGet, "/suppressions/check?tenant_id="+tenantID+"&contact="+contact, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET suppressions/check = %d", resp.StatusCode)
	}
	if got := decode(t, resp)["Suppressed"]; got != true {
		t.Fatalf("Suppressed = %v", got)
	}

	resp = do(t, server, http.MethodGet, "/zone-policy?tenant_id="+tenantID+"&purpose=outreach&category=red", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET zone-policy = %d", resp.StatusCode)
	}
	zone := decode(t, resp)
	if zone["Authorized"] != true || zone["SharedStorageAllowed"] != false {
		t.Fatalf("zone-policy = %+v", zone)
	}

	resp = do(t, server, http.MethodGet, "/bases/"+basisID+"?tenant_id="+tenantID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bases/{id} = %d", resp.StatusCode)
	}
	if got := decode(t, resp)["BasisID"]; got != basisID {
		t.Fatalf("BasisID = %v", got)
	}

	resp = do(t, server, http.MethodPost, "/bases/"+basisID+"/revocation", map[string]any{
		"tenant_id": tenantID, "actor": actorID, "reason": "отзыв клиента",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST revocation = %d: %s", resp.StatusCode, body(t, resp))
	}
	if got := decode(t, resp)["status"]; got != "revoked" {
		t.Fatalf("статус после отзыва = %v", got)
	}
}

// TestHTTPAccessDenied проверяет, что изменяющая операция без выданного права
// отвергается.
func TestHTTPAccessDenied(t *testing.T) {
	server := newTestServer(t)
	resp := do(t, server, http.MethodPost, "/bases", map[string]any{
		"basis_id": basisID, "tenant_id": "tenant-2", "purpose": "outreach", "category": "green",
		"retention_days": 90, "actor": actorID,
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /bases чужого тенанта = %d, ожидалось 403", resp.StatusCode)
	}
}

// TestHTTPUnknownBasisNotFound проверяет, что чтение отсутствующего основания даёт
// 404.
func TestHTTPUnknownBasisNotFound(t *testing.T) {
	server := newTestServer(t)
	resp := do(t, server, http.MethodGet, "/bases/ghost?tenant_id="+tenantID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET отсутствующего основания = %d, ожидалось 404", resp.StatusCode)
	}
}

// body читает тело ответа как строку (для диагностики).
func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(data)
}
