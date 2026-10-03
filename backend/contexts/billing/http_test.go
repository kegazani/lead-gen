package billing_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// doJSON отправляет JSON-запрос и возвращает ответ и разобранное тело.
func doJSON(t *testing.T, client *http.Client, method, url string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer response.Body.Close()
	decoded := map[string]any{}
	raw, _ := io.ReadAll(response.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return response, decoded
}

// TestHTTPActivateAndUsage проверяет сквозной HTTP-сценарий роли api.
func TestHTTPActivateAndUsage(t *testing.T) {
	slice, _ := newSlice(t, &fixedClock{now: testNow})
	server := httptest.NewServer(slice.Server)
	defer server.Close()

	activate := map[string]any{
		"subscription_id": "sub-1",
		"tenant_id":       "tenant-1",
		"tariff_id":       "tariff-pro",
		"price_minor":     500000,
		"currency":        "rub",
		"period_start":    "2026-01-01T00:00:00Z",
		"period_end":      "2026-01-31T00:00:00Z",
		"limits": []map[string]any{
			{"feature": "outreach-channel-email", "limit": 10},
		},
	}
	response, _ := doJSON(t, server.Client(), http.MethodPost, server.URL+"/subscriptions", activate)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("активация: статус %d", response.StatusCode)
	}

	response, usage := doJSON(t, server.Client(), http.MethodPost, server.URL+"/subscriptions/sub-1/usage", map[string]any{
		"tenant_id": "tenant-1",
		"usage_id":  "usage-1",
		"feature":   "outreach-channel-email",
		"quantity":  3,
		"usage_key": "k1",
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("фиксация потребления: статус %d", response.StatusCode)
	}
	if remaining, _ := usage["remaining"].(float64); remaining != 7 {
		t.Fatalf("остаток лимита = %v", usage["remaining"])
	}

	response, view := doJSON(t, server.Client(), http.MethodGet, server.URL+"/subscriptions/sub-1?tenant_id=tenant-1", nil)
	if response.StatusCode != http.StatusOK || view["status"] != "active" {
		t.Fatalf("чтение подписки: статус %d, тело %+v", response.StatusCode, view)
	}

	response, _ = doJSON(t, server.Client(), http.MethodGet, server.URL+"/subscriptions/ghost?tenant_id=tenant-1", nil)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("неизвестная подписка: статус %d", response.StatusCode)
	}
}

// TestHTTPHealthz проверяет эндпойнт здоровья роли api.
func TestHTTPHealthz(t *testing.T) {
	slice, _ := newSlice(t, &fixedClock{now: testNow})
	server := httptest.NewServer(slice.Server)
	defer server.Close()

	response, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/healthz", nil)
	if response.StatusCode != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("healthz: статус %d, тело %+v", response.StatusCode, body)
	}
}

// TestHTTPRootDescriptor проверяет, что корень сервиса отвечает дескриптором, а
// не 404: HTML-экрана у API нет.
func TestHTTPRootDescriptor(t *testing.T) {
	slice, _ := newSlice(t, &fixedClock{now: testNow})
	server := httptest.NewServer(slice.Server)
	defer server.Close()

	response, body := doJSON(t, server.Client(), http.MethodGet, server.URL+"/", nil)
	if response.StatusCode != http.StatusOK || body["service"] != "billing" {
		t.Fatalf("корень: статус %d, тело %+v", response.StatusCode, body)
	}
}
