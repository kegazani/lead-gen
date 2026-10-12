package httpapi_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

type unavailableDraftStore struct{}

func (unavailableDraftStore) SaveDrafts(context.Context, []ports.EmailDraft) error {
	return ports.ErrDraftStoreUnavailable
}
func (unavailableDraftStore) ListByRequest(context.Context, string, string) ([]ports.EmailDraft, error) {
	return nil, ports.ErrDraftStoreUnavailable
}

func prepareApprovalScreenWithDraftStore(t *testing.T, store ports.EmailDraftStore) *consoleClient {
	t.Helper()
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		DraftStore: store, ConsoleUser: consoleUser, ConsolePassword: consolePassword,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	client := prepareApprovalClient(t, server)
	return client
}

func TestApprovalScreenShowsReadOnlyDraftsBoundToLead(t *testing.T) {
	store := persistence.NewDraftStore()
	client := prepareApprovalScreenWithDraftStore(t, store)
	if err := store.SaveDrafts(context.Background(), []ports.EmailDraft{{
		RequestID: requestID, TenantID: tenantID, LeadID: "lead.example",
		Subject: "Тема для лида", Body: "Здравствуйте!\nПредлагаем решение.", Language: "ru",
	}}); err != nil {
		t.Fatal(err)
	}

	body := readBody(t, client.get(t, approvalScreenPath()))
	for _, expected := range []string{"Сформированные письма", "lead.example", "Тема для лида", "Здравствуйте!", "Предлагаем решение."} {
		if !strings.Contains(body, expected) {
			t.Errorf("approval screen не содержит %q: %s", expected, body)
		}
	}
	if strings.Contains(body, "email-draft-approve") || strings.Contains(body, "email-draft-send") {
		t.Fatalf("экран добавил действие по письму: %s", body)
	}
}

func TestApprovalScreenDistinguishesEmptyDrafts(t *testing.T) {
	client := prepareApprovalScreenWithDraftStore(t, persistence.NewDraftStore())
	body := readBody(t, client.get(t, approvalScreenPath()))
	if !strings.Contains(body, "Для этой заявки писем пока нет.") {
		t.Fatalf("пустой список не показан отдельно: %s", body)
	}
	if strings.Contains(body, "Черновики писем сейчас недоступны.") {
		t.Fatalf("пустой список выдан за недоступность: %s", body)
	}
}

func TestApprovalScreenShowsDraftsUnavailableInsteadOfEmpty(t *testing.T) {
	client := prepareApprovalScreenWithDraftStore(t, unavailableDraftStore{})
	body := readBody(t, client.get(t, approvalScreenPath()))
	if !strings.Contains(body, "Черновики писем сейчас недоступны.") {
		t.Fatalf("недоступность хранилища скрыта: %s", body)
	}
	if strings.Contains(body, "Для этой заявки писем пока нет.") {
		t.Fatalf("недоступное хранилище показано как пустое: %s", body)
	}
}

func TestApprovalScreenDraftReadErrorIsNotAnEmptySuccess(t *testing.T) {
	store := errorDraftStore{err: errors.New("read failed")}
	client := prepareApprovalScreenWithDraftStore(t, store)
	body := readBody(t, client.get(t, approvalScreenPath()))
	if !strings.Contains(body, "Черновики писем сейчас недоступны.") || strings.Contains(body, "Для этой заявки писем пока нет.") {
		t.Fatalf("ошибка чтения замаскирована под пустой список: %s", body)
	}
}

type errorDraftStore struct{ err error }

func (s errorDraftStore) SaveDrafts(context.Context, []ports.EmailDraft) error { return s.err }
func (s errorDraftStore) ListByRequest(context.Context, string, string) ([]ports.EmailDraft, error) {
	return nil, s.err
}
