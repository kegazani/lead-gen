package contracttest

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// LeadProfileRepositoryFactory создаёт репозиторий для одного прогона suite.
//
// Фабрика обязана вернуть репозиторий с пустым состоянием. Для общей
// PostgreSQL-базы она же отвечает за изоляцию прогонов (очистку): suite вызывает
// фабрику перед каждым подтестом и ничего не знает о хранилище.
type LeadProfileRepositoryFactory func(t *testing.T) ports.LeadProfileRepository

// RunLeadProfileRepositoryContract проверяет, что реализация
// LeadProfileRepository соблюдает контракт порта: сохраняет и загружает агрегат,
// различает отсутствие карточки, скрывает невидимые в витрине и удаляет.
func RunLeadProfileRepositoryContract(t *testing.T, newRepository LeadProfileRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("сохранение и загрузка", func(t *testing.T) {
		repository := newRepository(t)
		company := mustCompany(t, "roundtrip.example.com")
		if err := repository.Save(ctx, company); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, company.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		assertCompany(t, loaded, company)
	})

	t.Run("отсутствие карточки", func(t *testing.T) {
		repository := newRepository(t)
		loaded, err := repository.FindByID(ctx, domain.CompanyID("missing.example.com"))
		if err == nil {
			t.Fatal("FindByID отсутствующей карточки вернул nil-ошибку")
		}
		if !errors.Is(err, ports.ErrProfileNotFound) {
			t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, ErrProfileNotFound)", err)
		}
		if loaded != nil {
			t.Fatalf("для отсутствующей карточки ожидался nil, получено %v", loaded)
		}
	})

	t.Run("ListVisible исключает скрытые карточки", func(t *testing.T) {
		repository := newRepository(t)
		visible := mustCompany(t, "visible.example.com")
		hidden := mustCompany(t, "hidden.example.com")
		hidden.Hide(at(1))
		if err := repository.Save(ctx, visible); err != nil {
			t.Fatalf("Save видимой: %v", err)
		}
		if err := repository.Save(ctx, hidden); err != nil {
			t.Fatalf("Save скрытой: %v", err)
		}

		list, err := repository.ListVisible(ctx, 100)
		if err != nil {
			t.Fatalf("ListVisible: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("ListVisible вернул %d карточек, ожидалась 1", len(list))
		}
		assertCompany(t, list[0], visible)
	})

	t.Run("удаление", func(t *testing.T) {
		repository := newRepository(t)
		company := mustCompany(t, "delete.example.com")
		if err := repository.Save(ctx, company); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if err := repository.Delete(ctx, company.ID()); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := repository.FindByID(ctx, company.ID()); !errors.Is(err, ports.ErrProfileNotFound) {
			t.Fatalf("после Delete ошибка %v не удовлетворяет errors.Is(err, ErrProfileNotFound)", err)
		}
	})

	t.Run("ListSnoozed возвращает отложенных лидов", func(t *testing.T) {
		repository := newRepository(t)
		snoozed := mustCompany(t, "snoozed.example.com")
		snoozed.Snooze(at(2), at(1))
		plain := mustCompany(t, "plain.example.com")
		if err := repository.Save(ctx, snoozed); err != nil {
			t.Fatalf("Save snoozed: %v", err)
		}
		if err := repository.Save(ctx, plain); err != nil {
			t.Fatalf("Save plain: %v", err)
		}

		list, err := repository.ListSnoozed(ctx)
		if err != nil {
			t.Fatalf("ListSnoozed: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("ListSnoozed вернул %d карточек, ожидалась 1", len(list))
		}
		assertCompany(t, list[0], snoozed)
	})
}
