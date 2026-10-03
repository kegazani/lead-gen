package publishing_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	platformadapter "lead-gen/backend/contexts/publishing/internal/adapters/platform"
)

// publishCommandBody — тело команды publication-command по HTTP.
func publishCommandBody() []byte {
	body := map[string]any{
		"tenant_id":    "tenant-1",
		"actor":        "operator-1",
		"digest_id":    "digest-1",
		"community_id": "community-1",
		"period":       "2026-W10",
		"targets": []map[string]any{
			{
				"publication_id":  "pub-li",
				"platform":        "linkedin",
				"account":         "platform-account",
				"idempotency_key": "key-li",
				"title":           "Пост",
				"body":            "Текст поста",
			},
		},
	}
	raw, _ := json.Marshal(body)
	return raw
}

func postJSON(t *testing.T, client *http.Client, url string, payload []byte) *http.Response {
	t.Helper()
	resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func TestHTTPFlow(t *testing.T) {
	slice := newSlice(t, platformadapter.NewDeterministic())
	server := httptest.NewServer(slice.Server)
	defer server.Close()
	client := server.Client()

	// Healthz.
	health, err := client.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	if health.StatusCode != http.StatusOK {
		t.Fatalf("healthz статус = %d", health.StatusCode)
	}
	health.Body.Close()

	// Команда публикации.
	created := postJSON(t, client, server.URL+"/publications", publishCommandBody())
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("создание: статус = %d", created.StatusCode)
	}
	created.Body.Close()

	// Публикация без утверждения — отказ домена (400).
	notApproved := postJSON(t, client, server.URL+"/publications/pub-li/publication", []byte(`{"tenant_id":"tenant-1","actor":"operator-2"}`))
	if notApproved.StatusCode != http.StatusBadRequest {
		t.Fatalf("без утверждения: статус = %d, ожидался 400", notApproved.StatusCode)
	}
	notApproved.Body.Close()

	// Утверждение.
	approved := postJSON(t, client, server.URL+"/publications/pub-li/approval", []byte(`{"tenant_id":"tenant-1","actor":"operator-1"}`))
	if approved.StatusCode != http.StatusOK {
		t.Fatalf("утверждение: статус = %d", approved.StatusCode)
	}
	approved.Body.Close()

	// Исполнение публикации.
	published := postJSON(t, client, server.URL+"/publications/pub-li/publication", []byte(`{"tenant_id":"tenant-1","actor":"operator-2"}`))
	if published.StatusCode != http.StatusOK {
		t.Fatalf("публикация: статус = %d", published.StatusCode)
	}
	var payload struct {
		Status  string `json:"status"`
		Address string `json:"address"`
	}
	if err := json.NewDecoder(published.Body).Decode(&payload); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	published.Body.Close()
	if payload.Status != "published" || payload.Address == "" {
		t.Fatalf("ответ публикации неверен: %+v", payload)
	}

	// Чтение.
	got, err := client.Get(server.URL + "/publications/pub-li?tenant_id=tenant-1")
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if got.StatusCode != http.StatusOK {
		t.Fatalf("чтение: статус = %d", got.StatusCode)
	}
	got.Body.Close()
}

func TestHTTPRejectsUnknownFields(t *testing.T) {
	slice := newSlice(t, platformadapter.NewDeterministic())
	server := httptest.NewServer(slice.Server)
	defer server.Close()

	resp := postJSON(t, server.Client(), server.URL+"/publications", []byte(`{"tenant_id":"t","unknown":1}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("статус = %d, ожидался 400", resp.StatusCode)
	}
}

// TestRootRespondsWithDescriptor проверяет, что корень сервиса отвечает
// дескриптором, а не 404: HTML-экрана у API нет.
func TestRootRespondsWithDescriptor(t *testing.T) {
	slice := newSlice(t, platformadapter.NewDeterministic())
	server := httptest.NewServer(slice.Server)
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, ожидался 200", resp.StatusCode)
	}
	body := map[string]string{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("разбор тела: %v", err)
	}
	if body["service"] != "publishing" {
		t.Fatalf("service = %q, ожидался publishing", body["service"])
	}
}
