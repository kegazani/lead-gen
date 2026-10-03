package contracttest

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// EmailDraftStoreFactory создаёт хранилище черновиков для одного прогона suite.
// Фабрика обязана вернуть пустое состояние и отвечает за изоляцию прогонов.
type EmailDraftStoreFactory func(t *testing.T) ports.EmailDraftStore

// RunEmailDraftStoreContract проверяет, что реализация EmailDraftStore соблюдает
// контракт порта: хранит черновики по заявке в порядке найденных лидов,
// идемпотентно заменяет повторный черновик по тому же лиду и не раскрывает
// черновики чужого тенанта.
func RunEmailDraftStoreContract(t *testing.T, newStore EmailDraftStoreFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("порядок найденных лидов", func(t *testing.T) {
		store := newStore(t)
		if err := store.SaveDrafts(ctx, []ports.EmailDraft{
			{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a", Subject: "a", Body: "a-body"},
			{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-b", Subject: "b", Body: "b-body"},
		}); err != nil {
			t.Fatalf("SaveDrafts: %v", err)
		}

		got, err := store.ListByRequest(ctx, "req-1", "tenant-1")
		if err != nil {
			t.Fatalf("ListByRequest: %v", err)
		}
		if len(got) != 2 || got[0].LeadID != "lead-a" || got[1].LeadID != "lead-b" {
			t.Fatalf("черновики = %+v, ожидались lead-a, lead-b по порядку", got)
		}
	})

	t.Run("повторный черновик заменяет прежний", func(t *testing.T) {
		store := newStore(t)
		if err := store.SaveDrafts(ctx, []ports.EmailDraft{
			{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a", Subject: "старая", Body: "старое"},
			{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-b", Subject: "b", Body: "b-body"},
		}); err != nil {
			t.Fatalf("SaveDrafts: %v", err)
		}
		if err := store.SaveDrafts(ctx, []ports.EmailDraft{
			{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a", Subject: "новая", Body: "новое"},
		}); err != nil {
			t.Fatalf("повторный SaveDrafts: %v", err)
		}

		got, err := store.ListByRequest(ctx, "req-1", "tenant-1")
		if err != nil {
			t.Fatalf("ListByRequest: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("черновиков = %d, ожидалось 2 без дублей", len(got))
		}
		if got[0].LeadID != "lead-a" || got[0].Subject != "новая" {
			t.Fatalf("черновик = %+v, ожидалась замена на месте", got[0])
		}
	})

	t.Run("изоляция по тенанту", func(t *testing.T) {
		store := newStore(t)
		if err := store.SaveDrafts(ctx, []ports.EmailDraft{
			{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a"},
			{RequestID: "req-1", TenantID: "tenant-2", LeadID: "lead-secret"},
		}); err != nil {
			t.Fatalf("SaveDrafts: %v", err)
		}

		got, err := store.ListByRequest(ctx, "req-1", "tenant-1")
		if err != nil {
			t.Fatalf("ListByRequest: %v", err)
		}
		if len(got) != 1 || got[0].LeadID != "lead-a" {
			t.Fatalf("черновики = %+v, ожидался только лид своего тенанта", got)
		}
	})
}
