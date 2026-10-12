package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	analytics "lead-gen/backend/contexts/analytics"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
	"lead-gen/contracts/analytics/campaignreport"
)

// operationsNow — фиксированный момент расчёта агрегатов процесса.
var operationsNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// newOperationsServer собирает сервер analytics поверх заполненных проекций
// владельца: цифры процесса отдаются по контракту campaign-report.
func newOperationsServer(t *testing.T) *httptest.Server {
	t.Helper()
	companies := readmodel.New()
	for _, event := range []readmodel.Event{
		{TenantID: "tenant-1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c1", Status: "not warmed", OccurredAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c2", Status: "warming", OccurredAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c3", Status: "cold", OccurredAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", Kind: readmodel.KindMessageSent, CompanyID: "c1", OccurredAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", Kind: readmodel.KindMessageSent, CompanyID: "c2", OccurredAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", Kind: readmodel.KindMessageSent, CompanyID: "c3", OccurredAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", Kind: readmodel.KindEngagementObserved, CompanyID: "c1", Outcome: "replied", OccurredAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)},
	} {
		companies.Apply(event)
	}
	insights := readmodel.NewInsightModel()
	for _, outcome := range []readmodel.InsightOutcome{
		{TenantID: "tenant-1", JobID: "job-a", CompanyID: "o1", Verdict: readmodel.VerdictFits, OccurredAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", JobID: "job-a", CompanyID: "o2", Verdict: readmodel.VerdictUnsuitable, Reason: readmodel.ReasonRefuted, OccurredAt: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", JobID: "job-b", CompanyID: "o3", Verdict: readmodel.VerdictFits, OccurredAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", JobID: "job-b", CompanyID: "o4", Verdict: readmodel.VerdictHypothesis, Reason: readmodel.ReasonNoSignal, OccurredAt: time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC)},
		{TenantID: "tenant-1", JobID: "job-b", CompanyID: "o5", Verdict: readmodel.VerdictUnsuitable, Reason: readmodel.ReasonRefuted, OccurredAt: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", JobID: "job-b", CompanyID: "o6", Verdict: readmodel.VerdictUnsuitable, Reason: readmodel.ReasonRefuted, OccurredAt: time.Date(2026, 10, 2, 11, 0, 0, 0, time.UTC)},
	} {
		insights.Apply(outcome)
	}
	slice, err := analytics.Build(analytics.Config{Clock: fakeClock{now: operationsNow}, ReadModel: companies, InsightModel: insights})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// TestOperationsNumbersEndpoint проверяет HTTP-поверхность контракта
// campaign-report: цифры процесса и разбивка по времени отдаются в форме
// контракта.
func TestOperationsNumbersEndpoint(t *testing.T) {
	server := newOperationsServer(t)
	resp, err := http.Get(server.URL + "/operations/numbers?window_seconds=2592000&actor=admin")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("код = %d, ожидался 200", resp.StatusCode)
	}
	var response campaignreport.NumbersResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if response.Contract != campaignreport.ContractName || response.SchemaVersion != campaignreport.SchemaVersion {
		t.Fatalf("метаданные = %+v", response)
	}
	if len(response.Clients) != 1 || response.Clients[0].ClientID != "tenant-1" || response.Clients[0].Found != 3 {
		t.Fatalf("цифры = %+v", response.Clients)
	}
	if len(response.Periods) != 2 || response.Periods[0].Date != "2026-10-01" {
		t.Fatalf("разбивка = %+v", response.Periods)
	}
}

// TestOperationsClientsEndpoint проверяет агрегаты дашборда в форме контракта.
func TestOperationsClientsEndpoint(t *testing.T) {
	server := newOperationsServer(t)
	resp, err := http.Get(server.URL + "/operations/clients?window_seconds=2592000")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("код = %d, ожидался 200", resp.StatusCode)
	}
	var response campaignreport.ClientsResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if len(response.Clients) != 1 || response.Clients[0].ClientID != "tenant-1" {
		t.Fatalf("агрегаты = %+v", response.Clients)
	}
	if len(response.Clients[0].Anomalies) != 1 || response.Clients[0].Anomalies[0] != campaignreport.AnomalyRejectionSpike {
		t.Fatalf("аномалии = %+v", response.Clients[0].Anomalies)
	}
}

// TestOperationsEndpointRejectsBadWindow проверяет, что некорректное окно
// отвергается кодом 400, а не подменяется ответом.
func TestOperationsEndpointRejectsBadWindow(t *testing.T) {
	server := newOperationsServer(t)
	resp, err := http.Get(server.URL + "/operations/numbers?window_seconds=-1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("код = %d, ожидался 400", resp.StatusCode)
	}
}
