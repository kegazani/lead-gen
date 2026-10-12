package inmemory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports/contracttest"
)

var ctx = context.Background()

// TestRequestRepositoryContract прогоняет общий contract suite порта
// RequestRepository против in-memory-реализации. Тот же suite проходит и против
// PostgreSQL-реализации.
func TestRequestRepositoryContract(t *testing.T) {
	contracttest.RunRequestRepositoryContract(t, func(t *testing.T) ports.RequestRepository {
		t.Helper()
		return inmemory.New()
	})
}

func newRequest(t *testing.T) *domain.Request {
	t.Helper()
	request, err := domain.NewRequest(domain.NewRequestParams{
		ID:       "req-1",
		OfferID:  "offer-1",
		TenantID: "tenant-1",
		Title:    "Заявка автоматизации найма",
		Content:  "Предлагаем автоматизацию найма",
		Actor:    "actor-1",
		At:       time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		EventID:  "evt-1",
	})
	if err != nil {
		t.Fatalf("создание заявки: %v", err)
	}
	return request
}

// TestSaveAndLoad — интеграционная проверка записи и чтения заявки: сохранённый
// агрегат восстанавливается из снимка с теми же статусом, оффером и журналом.
func TestSaveAndLoad(t *testing.T) {
	repo := inmemory.New()
	request := newRequest(t)
	if err := repo.Save(ctx, request, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := repo.FindByID(ctx, request.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if loaded.Status() != domain.StatusSubmitted {
		t.Fatalf("статус = %q", loaded.Status())
	}
	if loaded.Offer().Content().Text() != "Предлагаем автоматизацию найма" {
		t.Fatalf("оффер = %q", loaded.Offer().Content().Text())
	}
	if loaded.Version() != 1 {
		t.Fatalf("версия = %d, ожидалась 1", loaded.Version())
	}
}

// TestNotFound — отсутствующая заявка даёт ErrRequestNotFound.
func TestNotFound(t *testing.T) {
	repo := inmemory.New()
	if _, err := repo.FindByID(ctx, "unknown"); !errors.Is(err, ports.ErrRequestNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrRequestNotFound", err)
	}
}

// TestOptimisticLocking — сценарий конкурентного изменения: сохранение по
// устаревшей версии отвергается конфликтом.
func TestOptimisticLocking(t *testing.T) {
	repo := inmemory.New()
	request := newRequest(t)
	if err := repo.Save(ctx, request, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}

	stale, err := repo.FindByID(ctx, request.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	fresh, err := repo.FindByID(ctx, request.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}

	expected := fresh.Version()
	if _, err := fresh.StartValidation(domain.TransitionInput{
		Actor: "actor-1", At: time.Now(), EventID: "evt-2",
	}); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if err := repo.Save(ctx, fresh, expected); err != nil {
		t.Fatalf("Save свежей версии: %v", err)
	}

	if err := repo.Save(ctx, stale, expected); !errors.Is(err, ports.ErrVersionConflict) {
		t.Fatalf("устаревшее сохранение: ошибка = %v, ожидалась ErrVersionConflict", err)
	}

	reloaded, err := repo.FindByID(ctx, request.ID())
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if reloaded.Status() != domain.StatusValidating {
		t.Fatalf("конфликтное сохранение изменило состояние: %q", reloaded.Status())
	}
}

// TestSaveNewWithNonZeroExpectedVersion — сохранение новой заявки с ненулевой
// ожидаемой версией отвергается: версия не совпала с «отсутствием».
func TestSaveNewWithNonZeroExpectedVersion(t *testing.T) {
	repo := inmemory.New()
	request := newRequest(t)
	if err := repo.Save(ctx, request, 5); !errors.Is(err, ports.ErrVersionConflict) {
		t.Fatalf("ошибка = %v, ожидалась ErrVersionConflict", err)
	}
}
