package httpport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/integration-platform/adapterstatus"
)

// TestStatusesReadsAdapterStatus проверяет успешное чтение состояния адаптеров
// через сетевой адаптер.
func TestStatusesReadsAdapterStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/adapter-status" {
			t.Errorf("неожидаемый запрос: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(adapterstatus.Response{
			Contract:      adapterstatus.ContractName,
			SchemaVersion: adapterstatus.SchemaVersion,
			Adapters: []adapterstatus.Status{
				{Adapter: "firecrawl", State: adapterstatus.StateUp},
			},
		})
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("сборка клиента: %v", err)
	}
	statuses, err := client.Statuses(context.Background())
	if err != nil {
		t.Fatalf("чтение состояния: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Adapter != "firecrawl" {
		t.Fatalf("неожидаемое состояние: %+v", statuses)
	}
}

// TestStatusesUnavailableOnErrorCode проверяет, что не-200 ответ владельца
// переводится в ErrUnavailable, а не в пустой список.
func TestStatusesUnavailableOnErrorCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "нет источника", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("сборка клиента: %v", err)
	}
	_, err = client.Statuses(context.Background())
	if !errors.Is(err, adapterstatus.ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
}

// TestNewRejectsInvalidBaseURL проверяет, что пустой и неполный базовый адрес
// отвергаются при сборке.
func TestNewRejectsInvalidBaseURL(t *testing.T) {
	for _, base := range []string{"", "not-a-url", "/relative"} {
		if _, err := New(base); err == nil {
			t.Fatalf("ожидалась ошибка для базового адреса %q", base)
		}
	}
}
