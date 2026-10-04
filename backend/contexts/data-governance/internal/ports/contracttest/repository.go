// Package contracttest содержит общий contract suite портов хранения контекста
// data-governance. Suite запускается против любой реализации через фабрику и
// потому не зависит от адаптера: in-memory и PostgreSQL проверяются одним и тем
// же набором проверок.
//
// Пакет предназначен для использования из тестов адаптеров и импортирует только
// сам ports и его domain.
package contracttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// ProcessingBasisRepositoryFactory создаёт репозиторий для одного прогона suite.
//
// Фабрика обязана вернуть репозиторий с пустым состоянием. Для общей
// PostgreSQL-базы она же отвечает за изоляцию прогонов: suite вызывает её перед
// каждым подтестом и ничего не знает о хранилище.
type ProcessingBasisRepositoryFactory func(t *testing.T) ports.ProcessingBasisRepository

// baseTime — детерминированная точка отсчёта, чтобы проверки не зависели от
// системных часов и совпадали для всех реализаций.
var baseTime = time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)

// RunProcessingBasisRepositoryContract проверяет, что реализация
// ProcessingBasisRepository соблюдает контракт порта: снимок агрегата вместе с
// записями стоп-листа переживает запись и загрузку, оптимистичная блокировка
// отвергает устаревшее изменение, отсутствие даёт ports.ErrBasisNotFound, а
// выборки воспроизводимы.
func RunProcessingBasisRepositoryContract(t *testing.T, newRepository ProcessingBasisRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("снимок переживает запись и загрузку", func(t *testing.T) {
		repository := newRepository(t)
		basis := mustBasis(t, "basis-1", "tenant-1")
		withSuppressions(t, basis)
		if err := repository.Save(ctx, basis, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, basis.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		assertBasis(t, loaded, basis)
	})

	t.Run("изменение с верной версией сохраняется", func(t *testing.T) {
		repository := newRepository(t)
		basis := mustBasis(t, "basis-1", "tenant-1")
		if err := repository.Save(ctx, basis, 0); err != nil {
			t.Fatalf("Save создания: %v", err)
		}
		expected := basis.Version()
		if _, err := basis.SuppressContact(domain.SuppressEntryParams{
			ID: "entry-1", Contact: "contact-1", Reason: "opt-out",
			Actor: "actor-1", At: at(1), EventID: "evt-sup-1",
		}); err != nil {
			t.Fatalf("внесение в стоп-лист: %v", err)
		}
		if err := repository.Save(ctx, basis, expected); err != nil {
			t.Fatalf("Save изменения: %v", err)
		}
		loaded, err := repository.FindByID(ctx, basis.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		assertBasis(t, loaded, basis)
	})

	t.Run("устаревшее изменение отвергается и не меняет состояние", func(t *testing.T) {
		repository := newRepository(t)
		basis := mustBasis(t, "basis-1", "tenant-1")
		if err := repository.Save(ctx, basis, 0); err != nil {
			t.Fatalf("Save создания: %v", err)
		}
		storedVersion := basis.Version()
		if _, err := basis.SuppressContact(domain.SuppressEntryParams{
			ID: "entry-1", Contact: "contact-1", Reason: "opt-out",
			Actor: "actor-1", At: at(1), EventID: "evt-sup-1",
		}); err != nil {
			t.Fatalf("внесение в стоп-лист: %v", err)
		}
		if err := repository.Save(ctx, basis, 0); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("устаревшее изменение: ошибка = %v, ожидалась ErrVersionConflict", err)
		}

		loaded, err := repository.FindByID(ctx, basis.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if loaded.Version() != storedVersion || len(loaded.Suppressions()) != 0 {
			t.Fatalf("состояние изменилось: версия %d (ожидалась %d), записей %d (ожидалось 0)",
				loaded.Version(), storedVersion, len(loaded.Suppressions()))
		}
	})

	t.Run("повторное создание отвергается", func(t *testing.T) {
		repository := newRepository(t)
		basis := mustBasis(t, "basis-1", "tenant-1")
		if err := repository.Save(ctx, basis, 0); err != nil {
			t.Fatalf("Save: %v", err)
		}
		if err := repository.Save(ctx, basis, 0); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("повторное создание: ошибка = %v, ожидалась ErrVersionConflict", err)
		}
	})

	t.Run("отсутствующее основание", func(t *testing.T) {
		repository := newRepository(t)
		loaded, err := repository.FindByID(ctx, "missing")
		if err == nil {
			t.Fatal("FindByID отсутствующего основания вернул nil-ошибку")
		}
		if !errors.Is(err, ports.ErrBasisNotFound) {
			t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, ErrBasisNotFound)", err)
		}
		if loaded != nil {
			t.Fatalf("для отсутствующего основания ожидался nil, получено %v", loaded)
		}
	})

	t.Run("основания тенанта упорядочены по идентификатору", func(t *testing.T) {
		repository := newRepository(t)
		for _, id := range []domain.BasisID{"basis-b", "basis-a", "basis-c"} {
			if err := repository.Save(ctx, mustBasis(t, id, "tenant-1"), 0); err != nil {
				t.Fatalf("Save %s: %v", id, err)
			}
		}
		if err := repository.Save(ctx, mustBasis(t, "basis-d", "tenant-2"), 0); err != nil {
			t.Fatalf("Save basis-d: %v", err)
		}

		found, err := repository.FindByTenant(ctx, "tenant-1")
		if err != nil {
			t.Fatalf("FindByTenant: %v", err)
		}
		if len(found) != 3 {
			t.Fatalf("FindByTenant: получено %d основания, ожидалось 3", len(found))
		}
		for i, want := range []domain.BasisID{"basis-a", "basis-b", "basis-c"} {
			if found[i].ID() != want {
				t.Fatalf("FindByTenant[%d]: получено %q, ожидалось %q", i, found[i].ID(), want)
			}
		}

		other, err := repository.FindByTenant(ctx, "tenant-2")
		if err != nil {
			t.Fatalf("FindByTenant чужого тенанта: %v", err)
		}
		if len(other) != 1 || other[0].ID() != "basis-d" {
			t.Fatalf("FindByTenant тенанта-2 = %+v", other)
		}

		empty, err := repository.FindByTenant(ctx, "tenant-3")
		if err != nil {
			t.Fatalf("FindByTenant пустого тенанта: %v", err)
		}
		if len(empty) != 0 {
			t.Fatalf("FindByTenant пустого тенанта = %+v", empty)
		}
	})

	t.Run("истёкшие действующие основания", func(t *testing.T) {
		repository := newRepository(t)
		expired := mustBasisWithWindow(t, "basis-expired", "tenant-1", baseTime, baseTime.Add(24*time.Hour))
		if err := repository.Save(ctx, expired, 0); err != nil {
			t.Fatalf("Save истёкшего: %v", err)
		}

		// Отозванное основание с истёкшим окном не выбирается: истечение касается
		// только действующих (инвариант статуса).
		revoked := mustBasisWithWindow(t, "basis-revoked", "tenant-1", baseTime, baseTime.Add(24*time.Hour))
		if _, err := revoked.Revoke(domain.RevokeParams{
			Actor: "actor-1", Reason: "отзыв", At: at(30), EventID: "evt-revoke",
		}); err != nil {
			t.Fatalf("отзыв: %v", err)
		}
		if err := repository.Save(ctx, revoked, 0); err != nil {
			t.Fatalf("Save отозванного: %v", err)
		}

		active := mustBasisWithWindow(t, "basis-active", "tenant-1", baseTime, baseTime.Add(10*24*time.Hour))
		if err := repository.Save(ctx, active, 0); err != nil {
			t.Fatalf("Save действующего: %v", err)
		}

		got, err := repository.ListExpired(ctx, baseTime.Add(48*time.Hour))
		if err != nil {
			t.Fatalf("ListExpired: %v", err)
		}
		if len(got) != 1 || got[0].ID() != "basis-expired" {
			t.Fatalf("ListExpired = %+v, ожидалось одно basis-expired", got)
		}
	})
}

// mustBasis собирает действующее основание обработки с детерминированными полями
// и окном действия в одни сутки от baseTime либо завершает тест.
func mustBasis(t *testing.T, id domain.BasisID, tenant domain.TenantID) *domain.ProcessingBasis {
	t.Helper()
	return mustBasisWithWindow(t, id, tenant, baseTime, baseTime.AddDate(0, 0, 1))
}

// mustBasisWithWindow собирает действующее основание с заданным окном действия
// либо завершает тест.
func mustBasisWithWindow(t *testing.T, id domain.BasisID, tenant domain.TenantID, from, until time.Time) *domain.ProcessingBasis {
	t.Helper()
	purpose, err := domain.NewProcessingPurpose("outreach", domain.CategoryGreen)
	if err != nil {
		t.Fatalf("цель обработки: %v", err)
	}
	retention, err := domain.NewRetentionPeriod(90)
	if err != nil {
		t.Fatalf("срок хранения: %v", err)
	}
	basis, err := domain.NewProcessingBasis(domain.NewBasisParams{
		ID:         id,
		TenantID:   tenant,
		Purpose:    purpose,
		Retention:  retention,
		ValidFrom:  from,
		ValidUntil: until,
		Actor:      "actor-1",
		At:         baseTime,
		EventID:    domain.EventID("evt-create-" + id.String()),
	})
	if err != nil {
		t.Fatalf("создание основания %s: %v", id, err)
	}
	return basis
}

// withSuppressions вносит две записи стоп-листа и снимает вторую: снимок несёт
// и активную запись, и историю снятой, поэтому round-trip проверяется на полном
// состоянии сущности.
func withSuppressions(t *testing.T, basis *domain.ProcessingBasis) {
	t.Helper()
	if _, err := basis.SuppressContact(domain.SuppressEntryParams{
		ID: "entry-1", Contact: "contact-1", Reason: "opt-out",
		Actor: "actor-1", At: at(1), EventID: "evt-sup-1",
	}); err != nil {
		t.Fatalf("внесение записи entry-1: %v", err)
	}
	if _, err := basis.SuppressContact(domain.SuppressEntryParams{
		ID: "entry-2", Contact: "contact-2", Reason: "complaint",
		Actor: "actor-1", At: at(2), EventID: "evt-sup-2",
	}); err != nil {
		t.Fatalf("внесение записи entry-2: %v", err)
	}
	if _, err := basis.LiftSuppression(domain.LiftParams{
		EntryID: "entry-2", Actor: "actor-1", Reason: "ошибочное внесение",
		At: at(3), EventID: "evt-lift-2",
	}); err != nil {
		t.Fatalf("снятие записи entry-2: %v", err)
	}
}

// at возвращает момент времени, сдвинутый на minutes минут от baseTime.
func at(minutes int) time.Time {
	return baseTime.Add(time.Duration(minutes) * time.Minute)
}

// assertBasis сверяет загруженное основание с ожидаемым по наблюдаемому через
// публичный API состоянию. Буфер неподтверждённых событий не сравнивается: он не
// является частью сохраняемого состояния агрегата.
func assertBasis(t *testing.T, got, want *domain.ProcessingBasis) {
	t.Helper()
	if got.ID() != want.ID() {
		t.Fatalf("ID: получено %q, ожидалось %q", got.ID(), want.ID())
	}
	if got.TenantID() != want.TenantID() {
		t.Fatalf("TenantID: получено %q, ожидалось %q", got.TenantID(), want.TenantID())
	}
	if got.Purpose().Name() != want.Purpose().Name() || got.Purpose().Category() != want.Purpose().Category() {
		t.Fatalf("Purpose: получено (%q, %q), ожидалось (%q, %q)",
			got.Purpose().Name(), got.Purpose().Category(), want.Purpose().Name(), want.Purpose().Category())
	}
	if got.Retention().Days() != want.Retention().Days() {
		t.Fatalf("Retention: получено %d, ожидалось %d", got.Retention().Days(), want.Retention().Days())
	}
	if !got.ValidFrom().Equal(want.ValidFrom()) {
		t.Fatalf("ValidFrom: получено %s, ожидалось %s", got.ValidFrom(), want.ValidFrom())
	}
	if !got.ValidUntil().Equal(want.ValidUntil()) {
		t.Fatalf("ValidUntil: получено %s, ожидалось %s", got.ValidUntil(), want.ValidUntil())
	}
	if got.Status() != want.Status() {
		t.Fatalf("Status: получено %q, ожидалось %q", got.Status(), want.Status())
	}
	if got.Version() != want.Version() {
		t.Fatalf("Version: получено %d, ожидалось %d", got.Version(), want.Version())
	}
	assertEntries(t, got.Suppressions(), want.Suppressions())
}

// assertEntries сверяет записи стоп-листа, включая порядок и историю снятия.
func assertEntries(t *testing.T, got, want []domain.SuppressionEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("число записей стоп-листа: получено %d, ожидалось %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID() != want[i].ID() {
			t.Fatalf("Entries[%d].ID: получено %q, ожидалось %q", i, got[i].ID(), want[i].ID())
		}
		if got[i].Contact() != want[i].Contact() {
			t.Fatalf("Entries[%d].Contact: получено %q, ожидалось %q", i, got[i].Contact(), want[i].Contact())
		}
		if got[i].Reason() != want[i].Reason() {
			t.Fatalf("Entries[%d].Reason: получено %q, ожидалось %q", i, got[i].Reason(), want[i].Reason())
		}
		if got[i].IsActive() != want[i].IsActive() {
			t.Fatalf("Entries[%d].IsActive: получено %v, ожидалось %v", i, got[i].IsActive(), want[i].IsActive())
		}
		if !got[i].CreatedAt().Equal(want[i].CreatedAt()) {
			t.Fatalf("Entries[%d].CreatedAt: получено %s, ожидалось %s", i, got[i].CreatedAt(), want[i].CreatedAt())
		}
		if !got[i].RevokedAt().Equal(want[i].RevokedAt()) {
			t.Fatalf("Entries[%d].RevokedAt: получено %s, ожидалось %s", i, got[i].RevokedAt(), want[i].RevokedAt())
		}
	}
}
