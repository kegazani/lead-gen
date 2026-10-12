package query

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/analytics/internal/readmodel"
	"lead-gen/contracts/analytics/campaignreport"
)

// operationsNow — фиксированный момент расчёта агрегатов процесса.
var operationsNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// operationsFixture — проекции владельца, заполненные детерминированным
// сценарием: одна компания ответила, одна прогрета, одна холодна; исходы
// классификации дают все три различаемые категории.
func operationsFixture(t *testing.T) (*readmodel.ReadModel, *readmodel.InsightModel) {
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
		// Тенант ниже порога минимальной группы: одна компания и один исход.
		{TenantID: "tenant-small", Kind: readmodel.KindLeadWorkflow, CompanyID: "s1", Status: "warming", OccurredAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)},
	} {
		companies.Apply(event)
	}

	insights := readmodel.NewInsightModel()
	outcomes := []readmodel.InsightOutcome{
		{TenantID: "tenant-1", JobID: "job-a", CompanyID: "o1", Verdict: readmodel.VerdictFits, OccurredAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", JobID: "job-a", CompanyID: "o2", Verdict: readmodel.VerdictUnsuitable, Reason: readmodel.ReasonRefuted, OccurredAt: time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", JobID: "job-b", CompanyID: "o3", Verdict: readmodel.VerdictFits, OccurredAt: time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", JobID: "job-b", CompanyID: "o4", Verdict: readmodel.VerdictHypothesis, Reason: readmodel.ReasonNoSignal, OccurredAt: time.Date(2026, 10, 2, 9, 30, 0, 0, time.UTC)},
		{TenantID: "tenant-1", JobID: "job-b", CompanyID: "o5", Verdict: readmodel.VerdictUnsuitable, Reason: readmodel.ReasonRefuted, OccurredAt: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-1", JobID: "job-b", CompanyID: "o6", Verdict: readmodel.VerdictUnsuitable, Reason: readmodel.ReasonRefuted, OccurredAt: time.Date(2026, 10, 2, 11, 0, 0, 0, time.UTC)},
		{TenantID: "tenant-small", JobID: "job-a", CompanyID: "s1", Verdict: readmodel.VerdictUnsuitable, Reason: readmodel.ReasonRefuted, OccurredAt: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)},
	}
	for _, outcome := range outcomes {
		insights.Apply(outcome)
	}
	return companies, insights
}

// TestOperationsNumbers проверяет цифры процесса по клиентам и разбивку по
// времени из read-моделей владельца.
func TestOperationsNumbers(t *testing.T) {
	companies, insights := operationsFixture(t)
	handler := NewOperationsHandler(companies, insights, testClock{at: operationsNow}, readmodel.MinGroup)

	response, err := handler.Numbers(context.Background(), campaignreport.Request{Window: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("Numbers: %v", err)
	}
	if response.Contract != campaignreport.ContractName || response.SchemaVersion != campaignreport.SchemaVersion {
		t.Fatalf("метаданные = %+v", response)
	}
	if response.Count != 1 || len(response.Clients) != 1 {
		t.Fatalf("клиенты = %+v (тенант ниже порога должен быть скрыт)", response.Clients)
	}
	client := response.Clients[0]
	if client.ClientID != "tenant-1" || client.Found != 3 || client.Processed != 6 || client.Rejected != 3 || client.InProgress != 2 || client.Result != 1 {
		t.Fatalf("цифры клиента = %+v", client)
	}

	wantPeriods := []campaignreport.PeriodEntry{
		{Date: "2026-10-01", Found: 2, Processed: 2, Rejected: 1},
		{Date: "2026-10-02", Found: 1, Processed: 4, Rejected: 2},
	}
	if len(response.Periods) != len(wantPeriods) {
		t.Fatalf("разбивка = %+v", response.Periods)
	}
	for i, want := range wantPeriods {
		if response.Periods[i] != want {
			t.Errorf("период %d = %+v, ожидался %+v", i, response.Periods[i], want)
		}
	}
}

// TestOperationsNumbersWindow проверяет, что окно запроса ограничивает разбивку по
// времени, но не текущие счётчики.
func TestOperationsNumbersWindow(t *testing.T) {
	companies, insights := operationsFixture(t)
	handler := NewOperationsHandler(companies, insights, testClock{at: operationsNow}, readmodel.MinGroup)

	response, err := handler.Numbers(context.Background(), campaignreport.Request{Window: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Numbers: %v", err)
	}
	if len(response.Periods) != 0 {
		t.Fatalf("за окном должны быть только свежие дни, получено %+v", response.Periods)
	}
	if len(response.Clients) != 1 || response.Clients[0].Found != 3 {
		t.Fatalf("окно не должно менять текущие счётчики: %+v", response.Clients)
	}
}

// TestOperationsClients проверяет агрегаты дашборда и вывод аномалии по
// наблюдаемому основанию.
func TestOperationsClients(t *testing.T) {
	companies, insights := operationsFixture(t)
	handler := NewOperationsHandler(companies, insights, testClock{at: operationsNow}, readmodel.MinGroup)

	response, err := handler.Clients(context.Background(), campaignreport.Request{Window: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("Clients: %v", err)
	}
	if len(response.Clients) != 1 {
		t.Fatalf("агрегаты = %+v", response.Clients)
	}
	client := response.Clients[0]
	if client.ClientID != "tenant-1" || client.Processed != 6 || client.Rejected != 3 {
		t.Fatalf("агрегат клиента = %+v", client)
	}
	if len(client.Anomalies) != 1 || client.Anomalies[0] != campaignreport.AnomalyRejectionSpike {
		t.Fatalf("аномалии = %+v, ожидался всплеск отклонений", client.Anomalies)
	}
}

// TestOperationsUnavailable проверяет честную недоступность: несобранный контур
// даёт ошибку, а не пустой успех.
func TestOperationsUnavailable(t *testing.T) {
	handler := NewOperationsHandler(nil, nil, testClock{at: operationsNow}, readmodel.MinGroup)
	if _, err := handler.Numbers(context.Background(), campaignreport.Request{}); !errors.Is(err, campaignreport.ErrUnavailable) {
		t.Fatalf("Numbers: err = %v, ожидалась недоступность", err)
	}
	if _, err := handler.Clients(context.Background(), campaignreport.Request{}); !errors.Is(err, campaignreport.ErrUnavailable) {
		t.Fatalf("Clients: err = %v, ожидалась недоступность", err)
	}
}

// TestOperationsRejectsInvalidRequest проверяет, что некорректный запрос
// отвергается владельцем, а не подменяется пустым ответом.
func TestOperationsRejectsInvalidRequest(t *testing.T) {
	companies, insights := operationsFixture(t)
	handler := NewOperationsHandler(companies, insights, testClock{at: operationsNow}, readmodel.MinGroup)
	if _, err := handler.Numbers(context.Background(), campaignreport.Request{Window: -time.Second}); err == nil {
		t.Fatal("отрицательное окно не отвергнуто")
	}
}
