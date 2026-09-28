package contracttest

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// LeadActivityStoreFactory создаёт хранилище истории переходов для одного
// прогона suite. Как и прочие фабрики, обязана вернуть пустое состояние.
type LeadActivityStoreFactory func(t *testing.T) ports.LeadActivityStore

// RunLeadActivityStoreContract проверяет, что реализация LeadActivityStore
// фиксирует переход с временем и автором и возвращает историю по лиду.
func RunLeadActivityStoreContract(t *testing.T, newStore LeadActivityStoreFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("фиксация и чтение истории", func(t *testing.T) {
		store := newStore(t)
		lead := domain.CompanyID("lead.example.com")
		other := domain.CompanyID("other.example.com")

		if err := store.RecordStageChange(ctx, lead, domain.StageEmailSent, "оператор", at(1)); err != nil {
			t.Fatalf("RecordStageChange 1: %v", err)
		}
		if err := store.RecordStageChange(ctx, lead, domain.StageWarming, "оператор", at(2)); err != nil {
			t.Fatalf("RecordStageChange 2: %v", err)
		}
		if err := store.RecordStageChange(ctx, other, domain.StageCold, "оператор", at(3)); err != nil {
			t.Fatalf("RecordStageChange other: %v", err)
		}

		list, err := store.ListByLead(ctx, lead)
		if err != nil {
			t.Fatalf("ListByLead: %v", err)
		}
		if len(list) != 2 {
			t.Fatalf("ListByLead вернул %d записей, ожидалось 2", len(list))
		}
		if list[0].Stage != domain.StageEmailSent || list[0].Author != "оператор" {
			t.Fatalf("запись[0] = %+v", list[0])
		}
		if list[1].Stage != domain.StageWarming {
			t.Fatalf("запись[1] = %+v", list[1])
		}
	})

	t.Run("пустая история", func(t *testing.T) {
		store := newStore(t)
		list, err := store.ListByLead(ctx, domain.CompanyID("missing.example.com"))
		if err != nil {
			t.Fatalf("ListByLead: %v", err)
		}
		if len(list) != 0 {
			t.Fatalf("ожидалась пустая история, получено %d записей", len(list))
		}
	})
}
