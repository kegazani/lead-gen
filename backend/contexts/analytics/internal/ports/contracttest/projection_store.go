// Package contracttest содержит общий contract suite портов контекста analytics.
// Suite запускается против любой реализации через фабрику и потому не зависит от
// адаптера: in-memory и PostgreSQL проверяются одним и тем же набором проверок.
//
// Пакет предназначен для использования из тестов адаптеров и импортирует только
// ports и readmodel.
package contracttest

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// ProjectionStoreFactory создаёт хранилище для одного прогона suite.
//
// Фабрика обязана вернуть хранилище с пустым состоянием. Для общей
// PostgreSQL-базы она же отвечает за изоляцию прогонов (очистку): suite вызывает
// фабрику перед каждым подтестом и ничего не знает о носителе.
type ProjectionStoreFactory func(t *testing.T) ports.ProjectionStore

// baseTime — детерминированная точка отсчёта, чтобы проверки не зависели от
// системных часов и совпадали для всех реализаций.
var baseTime = time.Date(2026, time.January, 2, 9, 0, 0, 0, time.UTC)

// RunProjectionStoreContract проверяет, что реализация ProjectionStore соблюдает
// контракт порта: сохраняет и читает снимок проекции, заменяет состояние по ключу
// и различает компании по (тенант, компания).
func RunProjectionStoreContract(t *testing.T, newStore ProjectionStoreFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("пустое хранилище", func(t *testing.T) {
		store := newStore(t)
		records, err := store.LoadCompanies(ctx)
		if err != nil {
			t.Fatalf("LoadCompanies пустого хранилища: %v", err)
		}
		if len(records) != 0 {
			t.Fatalf("пустое хранилище вернуло %d записей, ожидалось 0", len(records))
		}
	})

	t.Run("сохранение и загрузка", func(t *testing.T) {
		store := newStore(t)
		record := readmodel.CompanyRecord{
			TenantID:  "t1",
			CompanyID: "c1",
			Stage:     readmodel.StageWarming,
			FoundAt:   baseTime,
			SentAt:    baseTime.Add(time.Hour),
			RepliedAt: baseTime.Add(2 * time.Hour),
			HasSent:   true,
			HasReply:  true,
		}
		if err := store.SaveCompany(ctx, record); err != nil {
			t.Fatalf("SaveCompany: %v", err)
		}

		got, ok := findRecord(t, ctx, store, "t1", "c1")
		if !ok {
			t.Fatal("сохранённый снимок компании не найден")
		}
		assertRecord(t, got, record)
	})

	t.Run("нулевые отметки времени", func(t *testing.T) {
		store := newStore(t)
		record := readmodel.CompanyRecord{
			TenantID:  "t1",
			CompanyID: "c9",
			Stage:     readmodel.StageNotWarmed,
			FoundAt:   baseTime,
		}
		if err := store.SaveCompany(ctx, record); err != nil {
			t.Fatalf("SaveCompany: %v", err)
		}

		got, ok := findRecord(t, ctx, store, "t1", "c9")
		if !ok {
			t.Fatal("сохранённый снимок компании не найден")
		}
		if !got.SentAt.IsZero() || !got.RepliedAt.IsZero() {
			t.Fatalf("незаполненные отметки должны остаться нулевыми: %+v", got)
		}
		if got.HasSent || got.HasReply {
			t.Fatalf("незаполненные признаки должны остаться false: %+v", got)
		}
	})

	t.Run("замена по ключу", func(t *testing.T) {
		store := newStore(t)
		first := readmodel.CompanyRecord{
			TenantID:  "t1",
			CompanyID: "c1",
			Stage:     readmodel.StageNotWarmed,
			FoundAt:   baseTime,
		}
		second := readmodel.CompanyRecord{
			TenantID:  "t1",
			CompanyID: "c1",
			Stage:     readmodel.StageCold,
			FoundAt:   baseTime,
			SentAt:    baseTime.Add(time.Hour),
		}
		if err := store.SaveCompany(ctx, first); err != nil {
			t.Fatalf("первый SaveCompany: %v", err)
		}
		if err := store.SaveCompany(ctx, second); err != nil {
			t.Fatalf("второй SaveCompany: %v", err)
		}

		records, err := store.LoadCompanies(ctx)
		if err != nil {
			t.Fatalf("LoadCompanies: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("повторная запись создала %d строк, ожидалась одна", len(records))
		}
		assertRecord(t, records[0], second)
	})

	t.Run("различение компаний и тенантов", func(t *testing.T) {
		store := newStore(t)
		save(t, ctx, store, readmodel.CompanyRecord{TenantID: "t1", CompanyID: "c1", Stage: readmodel.StageWarming, FoundAt: baseTime})
		save(t, ctx, store, readmodel.CompanyRecord{TenantID: "t1", CompanyID: "c2", Stage: readmodel.StageCold, FoundAt: baseTime})
		save(t, ctx, store, readmodel.CompanyRecord{TenantID: "t2", CompanyID: "c1", Stage: readmodel.StageEmailSent, FoundAt: baseTime})

		records, err := store.LoadCompanies(ctx)
		if err != nil {
			t.Fatalf("LoadCompanies: %v", err)
		}
		if len(records) != 3 {
			t.Fatalf("загружено %d записей, ожидалось 3", len(records))
		}
		if got, ok := findRecord(t, ctx, store, "t2", "c1"); !ok {
			t.Fatal("компания t2/c1 не найдена")
		} else if got.Stage != readmodel.StageEmailSent {
			t.Fatalf("стадия t2/c1 = %q, ожидалась %q", got.Stage, readmodel.StageEmailSent)
		}
		if got, ok := findRecord(t, ctx, store, "t1", "c1"); !ok {
			t.Fatal("компания t1/c1 не найдена")
		} else if got.Stage != readmodel.StageWarming {
			t.Fatalf("стадия t1/c1 = %q, ожидалась %q", got.Stage, readmodel.StageWarming)
		}
	})
}

// findRecord находит снимок компании в загруженном списке.
func findRecord(t *testing.T, ctx context.Context, store ports.ProjectionStore, tenant, company string) (readmodel.CompanyRecord, bool) {
	t.Helper()
	records, err := store.LoadCompanies(ctx)
	if err != nil {
		t.Fatalf("LoadCompanies: %v", err)
	}
	for _, record := range records {
		if record.TenantID == tenant && record.CompanyID == company {
			return record, true
		}
	}
	return readmodel.CompanyRecord{}, false
}

// save сохраняет снимок или завершает тест.
func save(t *testing.T, ctx context.Context, store ports.ProjectionStore, record readmodel.CompanyRecord) {
	t.Helper()
	if err := store.SaveCompany(ctx, record); err != nil {
		t.Fatalf("SaveCompany %s/%s: %v", record.TenantID, record.CompanyID, err)
	}
}

// assertRecord сравнивает загруженный снимок с ожидаемым по всем полям.
func assertRecord(t *testing.T, got, want readmodel.CompanyRecord) {
	t.Helper()
	if got.TenantID != want.TenantID || got.CompanyID != want.CompanyID {
		t.Fatalf("ключ: получено %s/%s, ожидалось %s/%s", got.TenantID, got.CompanyID, want.TenantID, want.CompanyID)
	}
	if got.Stage != want.Stage {
		t.Fatalf("стадия: получено %q, ожидалось %q", got.Stage, want.Stage)
	}
	if !got.FoundAt.Equal(want.FoundAt) {
		t.Fatalf("FoundAt: получено %s, ожидалось %s", got.FoundAt, want.FoundAt)
	}
	if !got.SentAt.Equal(want.SentAt) {
		t.Fatalf("SentAt: получено %s, ожидалось %s", got.SentAt, want.SentAt)
	}
	if !got.RepliedAt.Equal(want.RepliedAt) {
		t.Fatalf("RepliedAt: получено %s, ожидалось %s", got.RepliedAt, want.RepliedAt)
	}
	if got.HasSent != want.HasSent || got.HasReply != want.HasReply {
		t.Fatalf("признаки: получено has_sent=%v has_reply=%v, ожидалось %v/%v",
			got.HasSent, got.HasReply, want.HasSent, want.HasReply)
	}
}
