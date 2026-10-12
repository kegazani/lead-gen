package platformgovernance_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/contracts/analytics/campaignreport"
	campaignreporthttp "lead-gen/contracts/analytics/campaignreport/httpport"
)

// TestNewOperationsAggregatesNilIsUnavailable проверяет, что неподключённый
// владелец не создаёт агрегатор: раздел цифр честно недоступен, а не пуст.
func TestNewOperationsAggregatesNilIsUnavailable(t *testing.T) {
	if aggregates := platformgovernance.NewOperationsAggregates(nil); aggregates != nil {
		t.Fatalf("nil-порт дал непустой агрегатор: %#v", aggregates)
	}
}

// TestNewOperationsAggregatesMapsContract проверяет перевод формы контракта в
// форму экрана при co-location (прямой вызов владельца).
func TestNewOperationsAggregatesMapsContract(t *testing.T) {
	port := campaignreport.InProc(campaignreport.InProcHandler{
		NumbersFunc: func(_ context.Context, request campaignreport.Request) (campaignreport.NumbersResponse, error) {
			if request.Window != time.Hour {
				t.Fatalf("окно запроса = %v", request.Window)
			}
			return campaignreport.NumbersResponse{
				Contract:      campaignreport.ContractName,
				SchemaVersion: campaignreport.SchemaVersion,
				Clients:       []campaignreport.ClientNumbers{{ClientID: "tenant-1", Found: 7, Processed: 4, Rejected: 2, InProgress: 1, Result: 1}},
				Periods:       []campaignreport.PeriodEntry{{Date: "2026-10-06", Found: 7, Processed: 4, Rejected: 2}},
				Count:         1,
			}, nil
		},
		ClientsFunc: func(context.Context, campaignreport.Request) (campaignreport.ClientsResponse, error) {
			return campaignreport.ClientsResponse{
				Clients: []campaignreport.ClientAggregate{{ClientID: "tenant-1", Found: 7, Processed: 4, Anomalies: []string{campaignreport.AnomalyStalled}}},
				Count:   1,
			}, nil
		},
	})
	aggregates := platformgovernance.NewOperationsAggregates(port)

	numbers, err := aggregates.Numbers(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Numbers: %v", err)
	}
	if len(numbers.Clients) != 1 || numbers.Clients[0].ClientID != "tenant-1" || numbers.Clients[0].Found != 7 {
		t.Fatalf("цифры = %+v", numbers.Clients)
	}
	if len(numbers.Periods) != 1 || numbers.Periods[0].Date != "2026-10-06" || numbers.Periods[0].Processed != 4 {
		t.Fatalf("разбивка = %+v", numbers.Periods)
	}

	clients, err := aggregates.Clients(context.Background(), time.Hour)
	if err != nil {
		t.Fatalf("Clients: %v", err)
	}
	if len(clients) != 1 || clients[0].ClientID != "tenant-1" || len(clients[0].Anomalies) != 1 {
		t.Fatalf("агрегаты = %+v", clients)
	}
}

// TestNewOperationsAggregatesNetworkRoundTrip проверяет сетевой путь: адаптер
// поверх клиента контракта читает форму wire владельца и переводит её в форму
// экрана.
func TestNewOperationsAggregatesNetworkRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/operations/numbers":
			_, _ = w.Write([]byte(`{"contract":"campaign-report","schema_version":"1.0.0",` +
				`"clients":[{"client_id":"tenant-1","found":9,"processed":5,"rejected":3,"in_progress":2,"result":1}],` +
				`"periods":[{"date":"2026-10-06","found":4,"processed":5,"rejected":3}],"count":1}`))
		case "/operations/clients":
			_, _ = w.Write([]byte(`{"contract":"campaign-report","schema_version":"1.0.0",` +
				`"clients":[{"client_id":"tenant-1","found":9,"processed":5,"anomalies":["rejection_spike"]}],"count":1}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	client, err := campaignreporthttp.New(server.URL)
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	aggregates := platformgovernance.NewOperationsAggregates(client)

	numbers, err := aggregates.Numbers(context.Background(), 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Numbers: %v", err)
	}
	if len(numbers.Clients) != 1 || numbers.Clients[0].Processed != 5 || numbers.Clients[0].InProgress != 2 {
		t.Fatalf("цифры по сети = %+v", numbers.Clients)
	}
	if len(numbers.Periods) != 1 || numbers.Periods[0].Rejected != 3 {
		t.Fatalf("разбивка по сети = %+v", numbers.Periods)
	}

	clients, err := aggregates.Clients(context.Background(), 30*24*time.Hour)
	if err != nil {
		t.Fatalf("Clients: %v", err)
	}
	if len(clients) != 1 || len(clients[0].Anomalies) != 1 || clients[0].Anomalies[0] != campaignreport.AnomalyRejectionSpike {
		t.Fatalf("агрегаты по сети = %+v", clients)
	}
}

// TestNewOperationsAggregatesUnavailable проверяет, что недоступность владельца
// (несобранный in-proc контракт и сбой сети) возвращается ошибкой, а не пустым
// успехом.
func TestNewOperationsAggregatesUnavailable(t *testing.T) {
	empty := platformgovernance.NewOperationsAggregates(campaignreport.InProc(campaignreport.InProcHandler{}))
	if _, err := empty.Numbers(context.Background(), time.Hour); !errors.Is(err, campaignreport.ErrUnavailable) {
		t.Fatalf("Numbers: err = %v, ожидалась недоступность", err)
	}
	if _, err := empty.Clients(context.Background(), time.Hour); !errors.Is(err, campaignreport.ErrUnavailable) {
		t.Fatalf("Clients: err = %v, ожидалась недоступность", err)
	}

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(broken.Close)
	client, err := campaignreporthttp.New(broken.URL)
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	aggregates := platformgovernance.NewOperationsAggregates(client)
	if _, err := aggregates.Numbers(context.Background(), time.Hour); !errors.Is(err, campaignreport.ErrUnavailable) {
		t.Fatalf("Numbers недоступного владельца: err = %v, ожидалась недоступность", err)
	}
}
