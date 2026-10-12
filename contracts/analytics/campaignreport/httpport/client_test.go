package httpport_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lead-gen/contracts/analytics/campaignreport"
	"lead-gen/contracts/analytics/campaignreport/httpport"
)

// ownerStub — двойник владельца: отдаёт фиксированные ответы и запоминает запросы,
// чтобы проверить перевод транспорта в исходы контракта.
func ownerStub(t *testing.T, handler http.HandlerFunc) (*httpport.Client, *[]*http.Request) {
	t.Helper()
	requests := make([]*http.Request, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Clone(context.Background()))
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := httpport.New(server.URL)
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	return client, &requests
}

// TestClientNumbersRoundTrip проверяет полный оборот формы контракта: владелец
// отдаёт NumbersResponse, клиент разбирает его без потерь.
func TestClientNumbersRoundTrip(t *testing.T) {
	client, requests := ownerStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"contract":"campaign-report","schema_version":"1.0.0",` +
			`"clients":[{"client_id":"tenant-1","found":7,"processed":4,"rejected":2,"in_progress":1,"result":1}],` +
			`"periods":[{"date":"2026-10-06","found":3,"processed":2,"rejected":1}],"count":1}`))
	})
	response, err := client.Numbers(context.Background(), campaignreport.Request{Window: 24 * time.Hour, Actor: "operator"})
	if err != nil {
		t.Fatalf("Numbers: %v", err)
	}
	if response.Contract != campaignreport.ContractName || response.SchemaVersion != campaignreport.SchemaVersion {
		t.Fatalf("метаданные = %+v", response)
	}
	if len(response.Clients) != 1 || response.Clients[0].Found != 7 || response.Clients[0].Result != 1 {
		t.Fatalf("цифры = %+v", response.Clients)
	}
	if len(response.Periods) != 1 || response.Periods[0].Date != "2026-10-06" || response.Periods[0].Processed != 2 {
		t.Fatalf("разбивка = %+v", response.Periods)
	}
	got := (*requests)[0]
	if got.URL.Path != "/operations/numbers" || got.URL.Query().Get("window_seconds") != "86400" || got.URL.Query().Get("actor") != "operator" {
		t.Fatalf("запрос = %s %s", got.URL.Path, got.URL.RawQuery)
	}
}

func TestClientClientsRoundTrip(t *testing.T) {
	client, requests := ownerStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"contract":"campaign-report","schema_version":"1.0.0",` +
			`"clients":[{"client_id":"tenant-1","found":10,"processed":5,"anomalies":["stalled"]}],"count":1}`))
	})
	response, err := client.Clients(context.Background(), campaignreport.Request{})
	if err != nil {
		t.Fatalf("Clients: %v", err)
	}
	if len(response.Clients) != 1 || response.Clients[0].ClientID != "tenant-1" {
		t.Fatalf("агрегаты = %+v", response.Clients)
	}
	if len(response.Clients[0].Anomalies) != 1 || response.Clients[0].Anomalies[0] != campaignreport.AnomalyStalled {
		t.Fatalf("аномалии = %+v", response.Clients[0].Anomalies)
	}
	got := (*requests)[0]
	if got.URL.Path != "/operations/clients" || got.URL.RawQuery != "" {
		t.Fatalf("запрос = %s %s", got.URL.Path, got.URL.RawQuery)
	}
}

func TestClientEmptyIsNotUnavailable(t *testing.T) {
	client, _ := ownerStub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"contract":"campaign-report","schema_version":"1.0.0","count":0}`))
	})
	response, err := client.Numbers(context.Background(), campaignreport.Request{})
	if err != nil {
		t.Fatalf("Numbers: %v", err)
	}
	if response.Clients == nil || len(response.Clients) != 0 {
		t.Fatalf("пустой список = %+v (nil означает подмену недоступности)", response.Clients)
	}
	if response.Periods == nil {
		t.Fatalf("пустая разбивка = nil (nil означает подмену недоступности)")
	}
}

func TestClientErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"отклонено", http.StatusBadRequest, campaignreport.ErrRejected},
		{"недоступно", http.StatusServiceUnavailable, campaignreport.ErrUnavailable},
		{"внутренняя ошибка", http.StatusInternalServerError, campaignreport.ErrUnavailable},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client, _ := ownerStub(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(`{"error":"причина"}`))
			})
			_, err := client.Numbers(context.Background(), campaignreport.Request{})
			if !errors.Is(err, testCase.want) {
				t.Fatalf("ошибка = %v, ожидалась %v", err, testCase.want)
			}
		})
	}
}

func TestClientRejectsInvalidRequestLocally(t *testing.T) {
	client, _ := ownerStub(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("владелец не должен вызываться при некорректном запросе")
	})
	if _, err := client.Numbers(context.Background(), campaignreport.Request{Window: -1}); !errors.Is(err, campaignreport.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

func TestNewRejectsBadBaseURL(t *testing.T) {
	if _, err := httpport.New(""); err == nil {
		t.Fatal("пустой адрес должен быть отвергнут")
	}
	if _, err := httpport.New("://bad"); err == nil {
		t.Fatal("некорректный адрес должен быть отвергнут")
	}
	if _, err := httpport.New("ftp://host"); err == nil {
		t.Fatal("не-http адрес должен быть отвергнут")
	}
}
