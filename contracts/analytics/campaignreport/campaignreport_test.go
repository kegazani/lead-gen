package campaignreport

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRequestValidate(t *testing.T) {
	if err := (Request{Window: time.Hour, Actor: "admin"}).Validate(); err != nil {
		t.Fatalf("корректный запрос отвергнут: %v", err)
	}
	if err := (Request{}).Validate(); err != nil {
		t.Fatalf("нулевое окно допустимо (по умолчанию владельца): %v", err)
	}
	if err := (Request{Window: -time.Second}).Validate(); err == nil {
		t.Fatal("отрицательное окно должно быть отвергнуто")
	}
}

func TestInProcUnavailable(t *testing.T) {
	port := InProc(InProcHandler{})
	if _, err := port.Numbers(context.Background(), Request{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Numbers без функции = %v, ожидалась ErrUnavailable", err)
	}
	if _, err := port.Clients(context.Background(), Request{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Clients без функции = %v, ожидалась ErrUnavailable", err)
	}
}

func TestInProcRoundTrip(t *testing.T) {
	port := InProc(InProcHandler{
		NumbersFunc: func(_ context.Context, request Request) (NumbersResponse, error) {
			if request.Window != time.Hour {
				t.Fatalf("окно запроса = %v", request.Window)
			}
			return NumbersResponse{
				Contract:      ContractName,
				SchemaVersion: SchemaVersion,
				Clients:       []ClientNumbers{{ClientID: "tenant-1", Found: 3, Processed: 2, Rejected: 1, InProgress: 1, Result: 1}},
				Periods:       []PeriodEntry{{Date: "2026-10-06", Found: 3, Processed: 2, Rejected: 1}},
				Count:         1,
			}, nil
		},
		ClientsFunc: func(context.Context, Request) (ClientsResponse, error) {
			return ClientsResponse{
				Contract:      ContractName,
				SchemaVersion: SchemaVersion,
				Clients:       []ClientAggregate{{ClientID: "tenant-1", Found: 3, Anomalies: []string{AnomalyStalled}}},
				Count:         1,
			}, nil
		},
	})

	numbers, err := port.Numbers(context.Background(), Request{Window: time.Hour})
	if err != nil {
		t.Fatalf("Numbers: %v", err)
	}
	if numbers.Contract != ContractName || numbers.SchemaVersion != SchemaVersion {
		t.Fatalf("метаданные ответа = %+v", numbers)
	}
	if len(numbers.Clients) != 1 || numbers.Clients[0].ClientID != "tenant-1" || numbers.Clients[0].Rejected != 1 {
		t.Fatalf("цифры = %+v", numbers.Clients)
	}
	if len(numbers.Periods) != 1 || numbers.Periods[0].Date != "2026-10-06" {
		t.Fatalf("разбивка по времени = %+v", numbers.Periods)
	}

	clients, err := port.Clients(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Clients: %v", err)
	}
	if len(clients.Clients) != 1 || clients.Clients[0].Anomalies[0] != AnomalyStalled {
		t.Fatalf("агрегаты = %+v", clients.Clients)
	}
}
