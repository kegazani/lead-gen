package riskfacts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/contracts/lead-management/addressverification/httpport"
)

// newNetworkVerification собирает сетевой клиент контракта address-verification к
// тестовому серверу, отдающему тело body.
func newNetworkVerification(t *testing.T, body string) *httpport.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client, err := httpport.New(server.URL, httpport.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	return client
}

// TestRiskReasonsOverNetworkVerificationTransport фиксирует, что сборщик фактов
// работает с сетевым транспортом контракта address-verification без изменения
// формы: сетевой клиент — тот же addressverification.Port. Это сценарий раздельной
// топологии (`core` без co-located lead-management): реальные риск-факты достижимы
// по сети.
func TestRiskReasonsOverNetworkVerificationTransport(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []domain.RiskReason
	}{
		{
			name: "подтверждённый адрес — чисто",
			body: `{"status":"deliverable","verified":true}`,
			want: nil,
		},
		{
			name: "неподтверждённый адрес — признак риска",
			body: `{"status":"risky","verified":false}`,
			want: []domain.RiskReason{domain.RiskAddressUnverified},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			collector := New(resolverStub{address: "lead@example.com"}, suppressionStub{}, newNetworkVerification(t, tc.body))
			reasons, err := collector.RiskReasons(context.Background(), request())
			if err != nil {
				t.Fatalf("RiskReasons: %v", err)
			}
			if len(reasons) != len(tc.want) {
				t.Fatalf("признаки = %v, ожидались %v", reasons, tc.want)
			}
			for i := range tc.want {
				if reasons[i] != tc.want[i] {
					t.Fatalf("признаки = %v, ожидались %v", reasons, tc.want)
				}
			}
		})
	}
}

// TestNetworkVerificationOwnerDownIsUnavailable проверяет, что недоступный по сети
// владелец верификации не превращается в «рисков нет»: сборщик честно отказывает.
func TestNetworkVerificationOwnerDownIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()
	client, err := httpport.New(url)
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}

	collector := New(resolverStub{address: "lead@example.com"}, suppressionStub{}, client)
	reasons, err := collector.RiskReasons(context.Background(), request())
	if !errors.Is(err, domain.ErrRiskFactsUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrRiskFactsUnavailable", err)
	}
	if reasons != nil {
		t.Fatalf("признаки = %v, недоступность не должна давать пустой список как «рисков нет»", reasons)
	}
}
