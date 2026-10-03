package inmemory

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports/contracttest"
)

// TestDraftStoreContract прогоняет общий contract suite порта EmailDraftStore
// против in-memory-реализации. Тот же suite проходит и против PostgreSQL.
func TestDraftStoreContract(t *testing.T) {
	contracttest.RunEmailDraftStoreContract(t, func(t *testing.T) ports.EmailDraftStore {
		t.Helper()
		return NewDraftStore()
	})
}

// TestDraftStoreSavesAndListsInOrder проверяет, что черновики хранятся по заявке и
// возвращаются в порядке найденных лидов.
func TestDraftStoreSavesAndListsInOrder(t *testing.T) {
	store := NewDraftStore()
	ctx := context.Background()
	if err := store.SaveDrafts(ctx, []ports.EmailDraft{
		{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a", Subject: "a", Body: "a-body"},
		{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-b", Subject: "b", Body: "b-body"},
	}); err != nil {
		t.Fatalf("сохранение черновиков: %v", err)
	}

	got, err := store.ListByRequest(ctx, "req-1", "tenant-1")
	if err != nil {
		t.Fatalf("чтение черновиков: %v", err)
	}
	if len(got) != 2 || got[0].LeadID != "lead-a" || got[1].LeadID != "lead-b" {
		t.Fatalf("черновики = %+v, ожидались lead-a, lead-b по порядку", got)
	}
}

// TestDraftStoreReplacesSameLead проверяет идемпотентность: повторный черновик по
// тому же лиду заменяет прежний, сохраняя место в порядке.
func TestDraftStoreReplacesSameLead(t *testing.T) {
	store := NewDraftStore()
	ctx := context.Background()
	_ = store.SaveDrafts(ctx, []ports.EmailDraft{
		{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a", Subject: "старая", Body: "старое"},
		{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-b", Subject: "b", Body: "b-body"},
	})
	_ = store.SaveDrafts(ctx, []ports.EmailDraft{
		{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a", Subject: "новая", Body: "новое"},
	})

	got, err := store.ListByRequest(ctx, "req-1", "tenant-1")
	if err != nil {
		t.Fatalf("чтение черновиков: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("черновиков = %d, ожидалось 2 без дублей", len(got))
	}
	if got[0].LeadID != "lead-a" || got[0].Subject != "новая" {
		t.Fatalf("черновик = %+v, ожидалась замена на месте", got[0])
	}
}

// TestDraftStoreIsolatesTenants проверяет, что чтение не раскрывает черновики
// чужого тенанта.
func TestDraftStoreIsolatesTenants(t *testing.T) {
	store := NewDraftStore()
	ctx := context.Background()
	_ = store.SaveDrafts(ctx, []ports.EmailDraft{
		{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a"},
		{RequestID: "req-1", TenantID: "tenant-2", LeadID: "lead-secret"},
	})

	got, err := store.ListByRequest(ctx, "req-1", "tenant-1")
	if err != nil {
		t.Fatalf("чтение черновиков: %v", err)
	}
	if len(got) != 1 || got[0].LeadID != "lead-a" {
		t.Fatalf("черновики = %+v, ожидался только лид своего тенанта", got)
	}
}
