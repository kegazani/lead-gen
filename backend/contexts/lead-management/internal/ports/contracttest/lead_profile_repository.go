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

// Тенанты suite: основная область хранения и второй тенант для проверки изоляции.
const (
	tenantA = "tenant-a"
	tenantB = "tenant-b"
)

// RunLeadProfileRepositoryContract проверяет, что реализация
// LeadProfileRepository соблюдает контракт порта: сохраняет и загружает агрегат,
// различает отсутствие карточки, скрывает невидимые в витрине, удаляет и
// изолирует данные по тенанту.
func RunLeadProfileRepositoryContract(t *testing.T, newRepository LeadProfileRepositoryFactory) {
	t.Helper()
	ctx := ports.WithTenant(context.Background(), tenantA)

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

	t.Run("выборка двух тенантов не пересекается", func(t *testing.T) {
		repository := newRepository(t)
		a := mustCompany(t, "a.example.com")
		b := mustCompany(t, "b.example.com")
		if err := repository.Save(ctx, a); err != nil {
			t.Fatalf("Save A: %v", err)
		}
		if err := repository.Save(ports.WithTenant(context.Background(), tenantB), b); err != nil {
			t.Fatalf("Save B: %v", err)
		}

		listA, err := repository.ListVisible(ctx, 100)
		if err != nil {
			t.Fatalf("ListVisible A: %v", err)
		}
		if len(listA) != 1 || listA[0].ID() != a.ID() {
			t.Fatalf("витрина тенанта A: %+v, ожидалась только карточка A", cardIDs(listA))
		}

		listB, err := repository.ListVisible(ports.WithTenant(context.Background(), tenantB), 100)
		if err != nil {
			t.Fatalf("ListVisible B: %v", err)
		}
		if len(listB) != 1 || listB[0].ID() != b.ID() {
			t.Fatalf("витрина тенанта B: %+v, ожидалась только карточка B", cardIDs(listB))
		}
	})

	t.Run("чтение чужого тенанта не раскрывает данные", func(t *testing.T) {
		repository := newRepository(t)
		company := mustCompany(t, "secret.example.com")
		if err := repository.Save(ctx, company); err != nil {
			t.Fatalf("Save A: %v", err)
		}

		foreign := ports.WithTenant(context.Background(), tenantB)
		loaded, err := repository.FindByID(foreign, company.ID())
		if !errors.Is(err, ports.ErrProfileNotFound) {
			t.Fatalf("чтение чужого тенанта: ошибка %v, ожидалось ErrProfileNotFound", err)
		}
		if loaded != nil {
			t.Fatalf("чтение чужого тенанта вернуло данные %v", loaded)
		}
		if _, err := repository.ListVisible(foreign, 100); err != nil {
			t.Fatalf("ListVisible чужого тенанта: %v", err)
		}
		// Удаление из чужого тенанта не затрагивает данные владельца.
		if err := repository.Delete(foreign, company.ID()); err != nil {
			t.Fatalf("Delete чужого тенанта: %v", err)
		}
		if _, err := repository.FindByID(ctx, company.ID()); err != nil {
			t.Fatalf("карточка владельца исчезла после удаления из чужого тенанта: %v", err)
		}
	})

	t.Run("отложенные лиды скоупятся тенантом", func(t *testing.T) {
		repository := newRepository(t)
		a := mustCompany(t, "a-snoozed.example.com")
		a.Snooze(at(2), at(1))
		b := mustCompany(t, "b-snoozed.example.com")
		b.Snooze(at(2), at(1))
		if err := repository.Save(ctx, a); err != nil {
			t.Fatalf("Save A: %v", err)
		}
		if err := repository.Save(ports.WithTenant(context.Background(), tenantB), b); err != nil {
			t.Fatalf("Save B: %v", err)
		}

		list, err := repository.ListSnoozed(ctx)
		if err != nil {
			t.Fatalf("ListSnoozed: %v", err)
		}
		if len(list) != 1 || list[0].ID() != a.ID() {
			t.Fatalf("отложенные тенанта A: %+v, ожидалась только карточка A", cardIDs(list))
		}
	})

	t.Run("пустой тенант сохраняет прежнее поведение", func(t *testing.T) {
		repository := newRepository(t)
		empty := mustCompany(t, "empty.example.com")
		// Контекст без области хранения: тенант по умолчанию ('').
		if err := repository.Save(context.Background(), empty); err != nil {
			t.Fatalf("Save: %v", err)
		}
		list, err := repository.ListVisible(context.Background(), 100)
		if err != nil {
			t.Fatalf("ListVisible: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("пустой тенант вернул %d карточек, ожидалась 1", len(list))
		}
		// Карточка пустого тенанта недостижима в именованном тенанте.
		if _, err := repository.FindByID(ctx, empty.ID()); !errors.Is(err, ports.ErrProfileNotFound) {
			t.Fatalf("карточка пустого тенанта видна в тенанте A: %v", err)
		}
	})
}

// cardIDs возвращает идентификаторы карточек для диагностики.
func cardIDs(companies []*domain.Company) []string {
	ids := make([]string, 0, len(companies))
	for _, company := range companies {
		ids = append(ids, company.ID().String())
	}
	return ids
}
