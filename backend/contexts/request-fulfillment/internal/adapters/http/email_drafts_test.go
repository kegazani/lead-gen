package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// newDraftServer собирает сервер среза с заявкой и, если заданы, черновиками писем.
// Черновики кладутся в хранилище среза напрямую: шаг их генерации проверяется
// отдельно, здесь — только чтение.
func newDraftServer(t *testing.T, drafts []ports.EmailDraft) *httptest.Server {
	t.Helper()
	ctx := context.Background()
	slice, err := requestfulfillment.Build(requestfulfillment.Config{})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	if _, err := slice.SubmitRequest.Handle(ctx, command.SubmitRequest{
		RequestID: requestID, OfferID: "offer-1", TenantID: tenantID,
		Content: "Предлагаем автоматизацию найма", Actor: "client",
	}); err != nil {
		t.Fatalf("подача заявки: %v", err)
	}
	if len(drafts) > 0 {
		if err := slice.DraftStore.SaveDrafts(ctx, drafts); err != nil {
			t.Fatalf("сохранение черновиков: %v", err)
		}
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// TestHTTPEmailDrafts проверяет, что оператор видит письма, сгенерированные под
// найденных лидов, через эндпоинт чтения черновиков.
func TestHTTPEmailDrafts(t *testing.T) {
	server := newDraftServer(t, []ports.EmailDraft{
		{RequestID: requestID, TenantID: tenantID, LeadID: "lead-a", Subject: "Тема A", Body: "Тело A", Language: "ru"},
		{RequestID: requestID, TenantID: tenantID, LeadID: "lead-b", Subject: "Тема B", Body: "Тело B"},
	})

	view := decodeView(t, do(t, server, http.MethodGet, "/requests/"+requestID+"/email-drafts?tenant_id="+tenantID, nil))
	drafts, ok := view["drafts"].([]any)
	if !ok || len(drafts) != 2 {
		t.Fatalf("черновики = %v, ожидалось 2", view["drafts"])
	}
	first, _ := drafts[0].(map[string]any)
	if first["lead_id"] != "lead-a" || first["subject"] != "Тема A" || first["body"] != "Тело A" || first["language"] != "ru" {
		t.Fatalf("черновик = %v", first)
	}
}

// TestHTTPEmailDraftsEmpty проверяет, что заявка без черновиков отдаёт пустой
// список, а не ошибку.
func TestHTTPEmailDraftsEmpty(t *testing.T) {
	server := newDraftServer(t, nil)

	view := decodeView(t, do(t, server, http.MethodGet, "/requests/"+requestID+"/email-drafts?tenant_id="+tenantID, nil))
	drafts, ok := view["drafts"].([]any)
	if !ok || len(drafts) != 0 {
		t.Fatalf("черновики = %v, ожидался пустой список", view["drafts"])
	}
}

// TestHTTPEmailDraftsUnknownRequest проверяет, что черновики неизвестной заявки
// отдаются как 404: состояние чужого тенанта не выдумывается.
func TestHTTPEmailDraftsUnknownRequest(t *testing.T) {
	server := newDraftServer(t, nil)

	resp := do(t, server, http.MethodGet, "/requests/unknown/email-drafts?tenant_id="+tenantID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET email-drafts неизвестной заявки = %d, ожидался 404", resp.StatusCode)
	}
	resp.Body.Close()

	resp = do(t, server, http.MethodGet, "/requests/"+requestID+"/email-drafts?tenant_id=tenant-other", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET email-drafts чужого тенанта = %d, ожидался 404", resp.StatusCode)
	}
	resp.Body.Close()
}
