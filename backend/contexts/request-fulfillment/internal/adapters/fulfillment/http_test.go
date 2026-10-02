package fulfillment_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/reliability/resilience"
	transport "lead-gen/backend/platform/transport/http"
)

// retryOnce — политика без повторов: тест проверяет один вызов.
func retryOnce() resilience.RetryPolicy {
	return resilience.RetryPolicy{MaxAttempts: 1}
}

// TestHTTPQueryReadsStatus — обращение к синхронному query-контракту владельца
// артефакта: статус читается по идентификатору через sync/http-json.
func TestHTTPQueryReadsStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("метод = %s, ожидался GET", r.Method)
		}
		if r.URL.Path != "/audience-snapshot/snap-1" {
			t.Errorf("путь = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"fixed","found":true}`))
	}))
	defer server.Close()

	client := transport.NewClient(server.Client().Transport, retryOnce(), nil)
	query := fulfillment.NewHTTPQuery(client, map[ports.ArtifactKind]string{
		ports.ArtifactAudienceSnapshot: server.URL,
	})

	status, err := query.Status(context.Background(), ports.ArtifactRef{
		Kind: ports.ArtifactAudienceSnapshot, ID: "snap-1",
	})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Status != "fixed" || !status.Found {
		t.Fatalf("статус = %+v", status)
	}
}

// TestHTTPQueryNotFound — отсутствие артефакта приходит как found=false, а не как
// ошибка чтения.
func TestHTTPQueryNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "нет такого артефакта", http.StatusNotFound)
	}))
	defer server.Close()

	client := transport.NewClient(server.Client().Transport, retryOnce(), nil)
	query := fulfillment.NewHTTPQuery(client, map[ports.ArtifactKind]string{
		ports.ArtifactCampaign: server.URL,
	})

	status, err := query.Status(context.Background(), ports.ArtifactRef{Kind: ports.ArtifactCampaign, ID: "camp-1"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Found {
		t.Fatalf("артефакт считается найденным: %+v", status)
	}
}

// TestHTTPQueryWithoutAddress — вид артефакта без адреса владельца даёт отказ
// чтения, а не выдуманный статус.
func TestHTTPQueryWithoutAddress(t *testing.T) {
	query := fulfillment.NewHTTPQuery(nil, nil)
	if _, err := query.Status(context.Background(), ports.ArtifactRef{Kind: ports.ArtifactDeal, ID: "deal-1"}); err == nil {
		t.Fatal("ожидался отказ чтения")
	}
}
