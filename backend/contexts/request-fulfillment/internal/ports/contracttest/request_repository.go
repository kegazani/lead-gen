package contracttest

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// RequestRepositoryFactory создаёт репозиторий для одного прогона suite.
//
// Фабрика обязана вернуть репозиторий с пустым состоянием. Для общей
// PostgreSQL-базы она же отвечает за изоляцию прогонов (очистку или
// собственную транзакцию): suite вызывает фабрику перед каждым подтестом и
// ничего не знает о хранилище.
type RequestRepositoryFactory func(t *testing.T) ports.RequestRepository

// RunRequestRepositoryContract проверяет, что реализация RequestRepository
// соблюдает контракт порта: сохраняет и загружает агрегат, различает отсутствие
// заявки, отвергает устаревшее сохранение конфликтом версий и возвращает заявки
// статуса в воспроизводимом порядке.
func RunRequestRepositoryContract(t *testing.T, newRepository RequestRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("сохранение и загрузка", func(t *testing.T) {
		repository := newRepository(t)
		request := mustRequest(t, "req-roundtrip")
		if err := repository.Save(ctx, request, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, request.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		assertRequest(t, loaded, request)
	})

	t.Run("отсутствие заявки", func(t *testing.T) {
		repository := newRepository(t)
		loaded, err := repository.FindByID(ctx, "req-missing")
		if err == nil {
			t.Fatal("FindByID отсутствующей заявки вернул nil-ошибку")
		}
		if !errors.Is(err, ports.ErrRequestNotFound) {
			t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, ErrRequestNotFound)", err)
		}
		if loaded != nil {
			t.Fatalf("для отсутствующей заявки ожидался nil, получено %v", loaded)
		}
	})

	t.Run("оптимистичная блокировка", func(t *testing.T) {
		repository := newRepository(t)
		request := mustRequest(t, "req-lock")
		if err := repository.Save(ctx, request, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}

		stale, err := repository.FindByID(ctx, request.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		fresh, err := repository.FindByID(ctx, request.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}

		expected := fresh.Version()
		if _, err := fresh.StartValidation(domain.TransitionInput{
			Actor: "actor-1", At: at(1), EventID: "evt-lock-1",
		}); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		if err := repository.Save(ctx, fresh, expected); err != nil {
			t.Fatalf("Save свежей версии: %v", err)
		}

		if err := repository.Save(ctx, stale, expected); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("устаревшее сохранение: ошибка = %v, ожидалась ErrVersionConflict", err)
		}

		reloaded, err := repository.FindByID(ctx, request.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if reloaded.Status() != domain.StatusValidating {
			t.Fatalf("конфликтное сохранение изменило состояние: %q", reloaded.Status())
		}
		if reloaded.Version() != expected+1 {
			t.Fatalf("версия после изменения = %d, ожидалась %d", reloaded.Version(), expected+1)
		}
	})

	t.Run("создание с ненулевой ожидаемой версией", func(t *testing.T) {
		repository := newRepository(t)
		request := mustRequest(t, "req-new-version")
		if err := repository.Save(ctx, request, 5); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("ошибка = %v, ожидалась ErrVersionConflict", err)
		}
		if _, err := repository.FindByID(ctx, request.ID()); !errors.Is(err, ports.ErrRequestNotFound) {
			t.Fatalf("отвергнутое создание оставило заявку: ошибка = %v", err)
		}
	})

	t.Run("выборка по статусу упорядочена", func(t *testing.T) {
		repository := newRepository(t)
		for _, id := range []domain.RequestID{"req-b", "req-a"} {
			if err := repository.Save(ctx, mustRequest(t, id), 0); err != nil {
				t.Fatalf("Save %s: %v", id, err)
			}
		}
		moved := mustRequest(t, "req-c")
		if err := repository.Save(ctx, moved, 0); err != nil {
			t.Fatalf("Save req-c: %v", err)
		}
		if _, err := moved.StartValidation(domain.TransitionInput{
			Actor: "actor-1", At: at(1), EventID: "evt-req-c",
		}); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		if err := repository.Save(ctx, moved, 1); err != nil {
			t.Fatalf("Save req-c после перехода: %v", err)
		}

		submitted, err := repository.ListByStatus(ctx, domain.StatusSubmitted)
		if err != nil {
			t.Fatalf("ListByStatus submitted: %v", err)
		}
		if len(submitted) != 2 || submitted[0].ID() != "req-a" || submitted[1].ID() != "req-b" {
			t.Fatalf("submitted = %v, ожидались req-a, req-b по порядку", requestIDs(submitted))
		}

		validating, err := repository.ListByStatus(ctx, domain.StatusValidating)
		if err != nil {
			t.Fatalf("ListByStatus validating: %v", err)
		}
		if len(validating) != 1 || validating[0].ID() != "req-c" {
			t.Fatalf("validating = %v, ожидалась req-c", requestIDs(validating))
		}
	})

	t.Run("выборка по кампании", func(t *testing.T) {
		repository := newRepository(t)
		request := mustRequest(t, "req-camp")
		if err := request.RecordCampaign("handoff-req-camp"); err != nil {
			t.Fatalf("RecordCampaign: %v", err)
		}
		if err := repository.Save(ctx, request, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
		found, err := repository.FindByCampaign(ctx, request.TenantID(), "handoff-req-camp")
		if err != nil {
			t.Fatalf("FindByCampaign: %v", err)
		}
		if found.ID() != "req-camp" {
			t.Fatalf("FindByCampaign вернул %q, ожидалась req-camp", found.ID())
		}
		if _, err := repository.FindByCampaign(ctx, request.TenantID(), "handoff-missing"); !errors.Is(err, ports.ErrRequestNotFound) {
			t.Fatalf("отсутствие кампании: ошибка = %v, ожидалась ErrRequestNotFound", err)
		}
		if _, err := repository.FindByCampaign(ctx, "tenant-other", "handoff-req-camp"); !errors.Is(err, ports.ErrRequestNotFound) {
			t.Fatalf("чужая тенантом кампания: ошибка = %v, ожидалась ErrRequestNotFound", err)
		}
	})

	t.Run("подсчёт и список по тенанту", func(t *testing.T) {
		repository := newRepository(t)
		submitted := func(id domain.RequestID) *domain.Request { return mustRequest(t, id) }
		if err := repository.Save(ctx, submitted("req-b"), 0); err != nil {
			t.Fatalf("Save req-b: %v", err)
		}
		if err := repository.Save(ctx, submitted("req-a"), 0); err != nil {
			t.Fatalf("Save req-a: %v", err)
		}

		processing := mustRequest(t, "req-c")
		if _, err := processing.StartValidation(domain.TransitionInput{Actor: "actor-1", At: at(1), EventID: "evt-req-c-1"}); err != nil {
			t.Fatalf("StartValidation req-c: %v", err)
		}
		if _, err := processing.Validate(domain.TransitionInput{Actor: "actor-1", At: at(2), EventID: "evt-req-c-2"}); err != nil {
			t.Fatalf("Validate req-c: %v", err)
		}
		if _, err := processing.StartProcessing(domain.TransitionInput{Actor: "actor-1", At: at(3), EventID: "evt-req-c-3"}); err != nil {
			t.Fatalf("StartProcessing req-c: %v", err)
		}
		if err := repository.Save(ctx, processing, 0); err != nil {
			t.Fatalf("Save req-c: %v", err)
		}

		cancelled := mustRequest(t, "req-d")
		if _, err := cancelled.Cancel(domain.TransitionInput{Actor: "actor-1", At: at(1), EventID: "evt-req-d-1", Reason: "передумал"}); err != nil {
			t.Fatalf("Cancel req-d: %v", err)
		}
		if err := repository.Save(ctx, cancelled, 0); err != nil {
			t.Fatalf("Save req-d: %v", err)
		}

		// Упавшая заявка: терминальна и не должна считаться активной (в работе),
		// иначе блокирует запуск новой заявки тенанта.
		failed := mustRequest(t, "req-f")
		if _, err := failed.StartValidation(domain.TransitionInput{Actor: "actor-1", At: at(1), EventID: "evt-req-f-1"}); err != nil {
			t.Fatalf("StartValidation req-f: %v", err)
		}
		if _, err := failed.Validate(domain.TransitionInput{Actor: "actor-1", At: at(2), EventID: "evt-req-f-2"}); err != nil {
			t.Fatalf("Validate req-f: %v", err)
		}
		if _, err := failed.StartProcessing(domain.TransitionInput{Actor: "actor-1", At: at(3), EventID: "evt-req-f-3"}); err != nil {
			t.Fatalf("StartProcessing req-f: %v", err)
		}
		if _, err := failed.Fail(domain.TransitionInput{Actor: "actor-1", At: at(4), EventID: "evt-req-f-4", ReasonCode: "search_failed", Reason: "поиск упал"}); err != nil {
			t.Fatalf("Fail req-f: %v", err)
		}
		if err := repository.Save(ctx, failed, 0); err != nil {
			t.Fatalf("Save req-f: %v", err)
		}

		// Чужой тенант: заявка не должна попадать в подсчёт и список tenant-1.
		other, err := domain.NewRequest(domain.NewRequestParams{
			ID: "req-e", OfferID: "offer-req-e", TenantID: "tenant-2",
			Content: "Оффер чужого тенанта", Target: "IT", Actor: "actor-1", At: baseTime, EventID: "evt-req-e",
		})
		if err != nil {
			t.Fatalf("создание req-e: %v", err)
		}
		if err := repository.Save(ctx, other, 0); err != nil {
			t.Fatalf("Save req-e: %v", err)
		}

		count, err := repository.CountByTenant(ctx, "tenant-1")
		if err != nil {
			t.Fatalf("CountByTenant: %v", err)
		}
		if count != 3 {
			t.Fatalf("CountByTenant(tenant-1) = %d, ожидалось 3 (submitted+submitted+processing; failed не активна)", count)
		}

		inWork, err := repository.CountInWork(ctx, "tenant-1")
		if err != nil {
			t.Fatalf("CountInWork: %v", err)
		}
		if inWork != 1 {
			t.Fatalf("CountInWork(tenant-1) = %d, ожидалось 1", inWork)
		}

		listed, err := repository.ListByTenant(ctx, "tenant-1")
		if err != nil {
			t.Fatalf("ListByTenant: %v", err)
		}
		if len(listed) != 5 {
			t.Fatalf("ListByTenant(tenant-1) = %d заявок, ожидалось 5", len(listed))
		}
		for i, want := range []domain.RequestID{"req-a", "req-b", "req-c", "req-d", "req-f"} {
			if listed[i].ID() != want {
				t.Fatalf("ListByTenant[%d] = %q, ожидалась %q", i, listed[i].ID(), want)
			}
		}
	})
}

// requestIDs возвращает идентификаторы заявок для диагностики.
func requestIDs(requests []*domain.Request) []domain.RequestID {
	ids := make([]domain.RequestID, 0, len(requests))
	for _, request := range requests {
		ids = append(ids, request.ID())
	}
	return ids
}
