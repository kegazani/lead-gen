package httpport_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/platform-governance/accessdecision"
	"lead-gen/contracts/platform-governance/accessdecision/httpport"
)

// TestCanActRoundTrip проверяет сквозной путь: клиент → POST {PathCanAct} → ответ
// владельца, читаемый клиентом; положительное и отрицательное решения.
func TestCanActRoundTrip(t *testing.T) {
	var seen []accessdecision.CanActRequest
	allowed := map[string]bool{"user-1": true, "stranger": false}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != accessdecision.PathCanAct {
			t.Errorf("запрос = %s %s, ожидался POST %s", r.Method, r.URL.Path, accessdecision.PathCanAct)
		}
		var req accessdecision.CanActRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("разбор запроса: %v", err)
		}
		seen = append(seen, req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(accessdecision.CanActResponse{Allowed: allowed[req.Actor]})
	}))
	defer server.Close()

	client, err := httpport.New(server.URL, httpport.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}

	ok, err := client.CanAct(context.Background(), "user-1", "tenant-1")
	if err != nil || !ok {
		t.Fatalf("CanAct члена тенанта = %v, %v", ok, err)
	}
	deny, err := client.CanAct(context.Background(), "stranger", "tenant-1")
	if err != nil || deny {
		t.Fatalf("CanAct постороннего = %v, %v", deny, err)
	}
	if len(seen) != 2 || seen[0].Tenant != "tenant-1" {
		t.Fatalf("запросы владельцу = %+v", seen)
	}
}

// TestCanActUnavailableOnServerError проверяет, что не-2xx код — недоступность, а
// не положительное решение.
func TestCanActUnavailableOnServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"нет решения"}`, http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := httpport.New(server.URL, httpport.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	ok, err := client.CanAct(context.Background(), "user-1", "tenant-1")
	if ok {
		t.Fatal("недоступность владельца не должна давать разрешение")
	}
	if !errors.Is(err, accessdecision.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestCanActUnavailableOnNetworkFailure проверяет сбой сети: удалённый адрес даёт
// честный отказ.
func TestCanActUnavailableOnNetworkFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	client, err := httpport.New(url)
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	if _, err := client.CanAct(context.Background(), "user-1", "tenant-1"); !errors.Is(err, accessdecision.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestNewRejectsInvalidBaseURL проверяет, что адрес владельца обязан быть
// абсолютным http(s)-URL: ошибка видна при сборке, а не при первом решении.
func TestNewRejectsInvalidBaseURL(t *testing.T) {
	for _, base := range []string{"", "   ", "://bad", "/relative", "ftp://host"} {
		if _, err := httpport.New(base); err == nil {
			t.Fatalf("адрес %q должен быть отвергнут", base)
		}
	}
}
