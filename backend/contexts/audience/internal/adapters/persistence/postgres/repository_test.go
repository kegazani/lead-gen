package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/audience/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
	"lead-gen/backend/platform/migrate"
)

// PostgreSQL-тесты адаптеров запускаются против реальной базы: contract suite
// проверяет поведение хранилища, а не мока. Строка подключения берётся из
// переменной окружения LEADGEN_POSTGRES_TEST_DSN; если она не задана, тесты
// пропускаются, поэтому `make check` не требует базы.
//
// Примеры запуска: цель `make test-postgres` поднимает одноразовую базу сама;
// при ручном запуске задайте LEADGEN_POSTGRES_TEST_DSN на свою базу.
const dsnEnv = "LEADGEN_POSTGRES_TEST_DSN"

// migrationsDir — каталог SQL-миграций сервиса относительно каталога теста
// (internal/adapters/persistence/postgres). Зона данных у контекста одна, поэтому
// каталог плоский.
var migrationsDir = filepath.Join("..", "..", "..", "..", "migrations")

// epoch — детерминированное время теста.
var epoch = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

var (
	poolOnce sync.Once
	pool     *pgxpool.Pool
	poolErr  error
)

// TestMain освобождает пул соединений после прогона.
func TestMain(m *testing.M) {
	code := m.Run()
	if pool != nil {
		pool.Close()
	}
	os.Exit(code)
}

// TestSegmentRepositoryContract проверяет поведение ports.SegmentRepository:
// round-trip снимка, оптимистичную блокировку, отсутствие и упорядоченный список.
func TestSegmentRepositoryContract(t *testing.T) {
	t.Run("снимок сегмента переживает запись и чтение", func(t *testing.T) {
		repo := newSegmentRepository(t)
		segment := newSegment(t, "segment-1", "tenant-1")
		scoreLead(t, segment, "lead-1", 0.9)
		scoreLead(t, segment, "lead-2", 0.1) // ниже порога — вне состава

		if err := repo.Save(context.Background(), segment, 0); err != nil {
			t.Fatalf("сохранение сегмента: %v", err)
		}
		loaded, err := repo.FindByID(context.Background(), "segment-1")
		if err != nil {
			t.Fatalf("загрузка сегмента: %v", err)
		}
		if got, want := loaded.Snapshot(), segment.Snapshot(); !reflect.DeepEqual(got, want) {
			t.Fatalf("снимок после round-trip:\n got=%+v\nwant=%+v", got, want)
		}
	})

	t.Run("оптимистичная блокировка отвергает устаревшую версию", func(t *testing.T) {
		repo := newSegmentRepository(t)
		ctx := context.Background()
		segment := newSegment(t, "segment-1", "tenant-1")

		if err := repo.Save(ctx, segment, 0); err != nil {
			t.Fatalf("создание: %v", err)
		}
		// Повторная вставка занятого идентификатора — конфликт, а не перезапись.
		if err := repo.Save(ctx, segment, 0); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("повторная вставка: ошибка = %v, ожидался ErrVersionConflict", err)
		}

		scoreLead(t, segment, "lead-1", 0.9) // версия 1 → 2
		if err := repo.Save(ctx, segment, 0); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("устаревшее изменение: ошибка = %v, ожидался ErrVersionConflict", err)
		}
		if err := repo.Save(ctx, segment, 1); err != nil {
			t.Fatalf("изменение по актуальной версии: %v", err)
		}
		if err := repo.Save(ctx, segment, 1); !errors.Is(err, ports.ErrVersionConflict) {
			t.Fatalf("повтор с прежней версией: ошибка = %v, ожидался ErrVersionConflict", err)
		}
	})

	t.Run("отсутствующий сегмент даёт ErrSegmentNotFound", func(t *testing.T) {
		repo := newSegmentRepository(t)
		if _, err := repo.FindByID(context.Background(), "missing"); !errors.Is(err, ports.ErrSegmentNotFound) {
			t.Fatalf("ошибка = %v, ожидался ErrSegmentNotFound", err)
		}
	})

	t.Run("список тенанта и полный список упорядочены по идентификатору", func(t *testing.T) {
		repo := newSegmentRepository(t)
		ctx := context.Background()
		for _, s := range []struct{ id, tenant string }{
			{"segment-b", "tenant-1"},
			{"segment-a", "tenant-1"},
			{"segment-c", "tenant-2"},
		} {
			if err := repo.Save(ctx, newSegment(t, s.id, s.tenant), 0); err != nil {
				t.Fatalf("сохранение %s: %v", s.id, err)
			}
		}

		tenantSegments, err := repo.ListByTenant(ctx, "tenant-1")
		if err != nil {
			t.Fatalf("список тенанта: %v", err)
		}
		if got := segmentIDs(tenantSegments); !reflect.DeepEqual(got, []string{"segment-a", "segment-b"}) {
			t.Fatalf("сегменты тенанта = %v, ожидались [segment-a segment-b]", got)
		}

		all, err := repo.List(ctx)
		if err != nil {
			t.Fatalf("полный список: %v", err)
		}
		if got := segmentIDs(all); !reflect.DeepEqual(got, []string{"segment-a", "segment-b", "segment-c"}) {
			t.Fatalf("полный список = %v", got)
		}
	})
}

// TestAudienceSnapshotRepositoryContract проверяет поведение
// ports.AudienceSnapshotRepository: round-trip неизменяемого снимка, upsert по
// идентификатору, выборку по сегменту и отсутствие.
func TestAudienceSnapshotRepositoryContract(t *testing.T) {
	t.Run("снимок аудитории переживает запись и чтение", func(t *testing.T) {
		repo := newSnapshotRepository(t)
		ctx := context.Background()
		snapshot := newAudienceSnapshot(t, "segment-1", "tenant-1", "snapshot-1")

		if err := repo.Save(ctx, snapshot); err != nil {
			t.Fatalf("сохранение снимка: %v", err)
		}
		loaded, err := repo.FindByID(ctx, "snapshot-1")
		if err != nil {
			t.Fatalf("загрузка снимка: %v", err)
		}
		if got, want := loaded.State(), snapshot.State(); !reflect.DeepEqual(got, want) {
			t.Fatalf("состояние после round-trip:\n got=%+v\nwant=%+v", got, want)
		}
		if got, want := loaded.MemberIDs(), snapshot.MemberIDs(); !reflect.DeepEqual(got, want) {
			t.Fatalf("состав снимка = %v, ожидался %v", got, want)
		}
	})

	t.Run("upsert по идентификатору не создаёт второй снимок", func(t *testing.T) {
		repo := newSnapshotRepository(t)
		ctx := context.Background()
		snapshot := newAudienceSnapshot(t, "segment-1", "tenant-1", "snapshot-1")

		for i := 0; i < 2; i++ {
			if err := repo.Save(ctx, snapshot); err != nil {
				t.Fatalf("сохранение %d: %v", i, err)
			}
		}
		snapshots, err := repo.ListBySegment(ctx, "segment-1")
		if err != nil {
			t.Fatalf("список снимков сегмента: %v", err)
		}
		if len(snapshots) != 1 {
			t.Fatalf("снимков сегмента = %d, ожидался 1", len(snapshots))
		}
	})

	t.Run("отсутствующий снимок даёт ErrSnapshotNotFound", func(t *testing.T) {
		repo := newSnapshotRepository(t)
		if _, err := repo.FindByID(context.Background(), "missing"); !errors.Is(err, ports.ErrSnapshotNotFound) {
			t.Fatalf("ошибка = %v, ожидался ErrSnapshotNotFound", err)
		}
	})
}

// TestUnitOfWorkContract проверяет ports.UnitOfWork: запись агрегата и запись
// событий в outbox фиксируются одной транзакцией, ошибка use case откатывает обе.
func TestUnitOfWorkContract(t *testing.T) {
	t.Run("commit сохраняет агрегат и outbox атомарно", func(t *testing.T) {
		pool := testPool(t)
		truncateTables(t)
		ctx := context.Background()
		repository := postgres.NewSegmentRepository(pool)
		journal := postgres.NewJournal(pool)
		uow := postgres.NewUnitOfWork(pool)

		segment := newSegment(t, "segment-1", "tenant-1")
		scoreLead(t, segment, "lead-1", 0.9)
		events := segment.DomainEvents()
		if len(events) != 1 {
			t.Fatalf("событий сегмента = %d, ожидалось 1", len(events))
		}

		err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
			if err := tx.Segments().Save(ctx, segment, 0); err != nil {
				return err
			}
			return tx.Outbox().Append(ctx, events...)
		})
		if err != nil {
			t.Fatalf("транзакция: %v", err)
		}

		if _, err := repository.FindByID(ctx, "segment-1"); err != nil {
			t.Fatalf("сегмент не сохранён: %v", err)
		}
		pending, err := journal.Pending(ctx, 0)
		if err != nil {
			t.Fatalf("чтение outbox: %v", err)
		}
		if len(pending) != 1 {
			t.Fatalf("записей outbox = %d, ожидалась 1", len(pending))
		}
		if want := string(events[0].Header().EventID); pending[0].ID != want {
			t.Fatalf("ID записи outbox = %q, ожидался %q", pending[0].ID, want)
		}

		if err := journal.MarkPublished(ctx, pending[0].ID); err != nil {
			t.Fatalf("подтверждение записи: %v", err)
		}
		pending, err = journal.Pending(ctx, 0)
		if err != nil {
			t.Fatalf("повторное чтение outbox: %v", err)
		}
		if len(pending) != 0 {
			t.Fatalf("после подтверждения записей outbox = %d, ожидалось 0", len(pending))
		}
		if err := journal.MarkPublished(ctx, pendingUnusedID); err == nil {
			t.Fatal("подтверждение неизвестной записи должно быть ошибкой")
		}
	})

	t.Run("ошибка use case откатывает агрегат и outbox", func(t *testing.T) {
		pool := testPool(t)
		truncateTables(t)
		ctx := context.Background()
		repository := postgres.NewSegmentRepository(pool)
		journal := postgres.NewJournal(pool)
		uow := postgres.NewUnitOfWork(pool)

		segment := newSegment(t, "segment-1", "tenant-1")
		scoreLead(t, segment, "lead-1", 0.9)

		wantErr := errors.New("сбой сценария")
		err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
			if err := tx.Segments().Save(ctx, segment, 0); err != nil {
				return err
			}
			if err := tx.Outbox().Append(ctx, segment.DomainEvents()...); err != nil {
				return err
			}
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Fatalf("ошибка транзакции = %v, ожидалась %v", err, wantErr)
		}

		if _, err := repository.FindByID(ctx, "segment-1"); !errors.Is(err, ports.ErrSegmentNotFound) {
			t.Fatalf("после отката ошибка = %v, ожидался ErrSegmentNotFound", err)
		}
		pending, err := journal.Pending(ctx, 0)
		if err != nil {
			t.Fatalf("чтение outbox: %v", err)
		}
		if len(pending) != 0 {
			t.Fatalf("после отката записей outbox = %d, ожидалось 0", len(pending))
		}
	})
}

// pendingUnusedID — идентификатор, которого нет в outbox: подтверждение
// неизвестной записи обязано быть ошибкой.
const pendingUnusedID = "evt-unknown"

// newSegmentRepository возвращает репозиторий сегментов с пустым состоянием.
func newSegmentRepository(t *testing.T) *postgres.SegmentRepository {
	t.Helper()
	pool := testPool(t)
	truncateTables(t)
	return postgres.NewSegmentRepository(pool)
}

// newSnapshotRepository возвращает репозиторий снимков с пустым состоянием.
func newSnapshotRepository(t *testing.T) *postgres.SnapshotRepository {
	t.Helper()
	pool := testPool(t)
	truncateTables(t)
	return postgres.NewSnapshotRepository(pool)
}

// newSegment собирает пустой сегмент модели v1 с порогом 0.5 и обязательным
// сигналом fit.
func newSegment(t *testing.T, id, tenant string) *domain.Segment {
	t.Helper()
	minScore, err := domain.NewScoreValue(0.5)
	if err != nil {
		t.Fatalf("порог: %v", err)
	}
	criteria, err := domain.NewSegmentCriteria(minScore, []string{"fit"}, false)
	if err != nil {
		t.Fatalf("критерии: %v", err)
	}
	segment, err := domain.NewSegment(domain.NewSegmentParams{
		ID:           domain.SegmentID(id),
		TenantID:     domain.TenantID(tenant),
		Name:         "Тёплые лиды",
		ModelVersion: "v1",
		Criteria:     criteria,
	})
	if err != nil {
		t.Fatalf("сегмент: %v", err)
	}
	return segment
}

// scoreLead оценивает лид и пересчитывает его принадлежность сегменту.
func scoreLead(t *testing.T, segment *domain.Segment, lead string, strength float64) {
	t.Helper()
	signal, err := domain.NewSignal("fit", strength)
	if err != nil {
		t.Fatalf("сигнал: %v", err)
	}
	if _, err := segment.ScoreLead(
		domain.LeadProfile{
			LeadID:     domain.LeadID(lead),
			TenantID:   segment.TenantID(),
			Signals:    []domain.Signal{signal},
			ObservedAt: epoch,
		},
		domain.Provenance{EventID: domain.EventID("evt-" + lead), At: epoch, Actor: "actor-1"},
	); err != nil {
		t.Fatalf("скоринг лида %s: %v", lead, err)
	}
}

// newAudienceSnapshot фиксирует снимок сегмента с одним лидом состава.
func newAudienceSnapshot(t *testing.T, segmentID, tenant, snapshotID string) *domain.AudienceSnapshot {
	t.Helper()
	segment := newSegment(t, segmentID, tenant)
	scoreLead(t, segment, "lead-1", 0.9)
	snapshot, err := domain.FixAudienceSnapshot(segment, domain.NewAudienceSnapshotParams{
		ID:       domain.SnapshotID(snapshotID),
		TenantID: domain.TenantID(tenant),
		Provenance: domain.Provenance{
			EventID: domain.EventID("evt-" + snapshotID),
			At:      epoch,
			Actor:   "actor-1",
		},
	})
	if err != nil {
		t.Fatalf("фиксация снимка: %v", err)
	}
	return snapshot
}

// segmentIDs возвращает идентификаторы сегментов в порядке среза.
func segmentIDs(segments []*domain.Segment) []string {
	ids := make([]string, 0, len(segments))
	for _, segment := range segments {
		ids = append(ids, segment.ID().String())
	}
	return ids
}

// truncateTables очищает таблицы сервиса, сохраняя схему. Ссылочных связей между
// таблицами нет, поэтому обычного TRUNCATE достаточно.
func truncateTables(t *testing.T) {
	t.Helper()
	if _, err := testPool(t).Exec(context.Background(), truncateTablesSQL); err != nil {
		t.Fatalf("очистка таблиц: %v", err)
	}
}

// testPool подключается к базе из окружения и применяет миграции один раз за
// прогон. Без заданной строки подключения тесты пропускаются.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skipf("%s не задан: PostgreSQL-тесты адаптеров пропущены", dsnEnv)
	}

	poolOnce.Do(func() {
		ctx := context.Background()
		pool, poolErr = pgxpool.New(ctx, dsn)
		if poolErr != nil {
			return
		}
		if err := pool.Ping(ctx); err != nil {
			poolErr = err
			return
		}
		poolErr = applyMigrations(ctx, pool)
	})
	if poolErr != nil {
		t.Fatalf("подключение к PostgreSQL: %v", poolErr)
	}
	return pool
}

// applyMigrations применяет миграции схемы сервиса. Файлы читаются загрузчиком
// backend/platform/migrate, который до выполнения проверяет принадлежность схеме
// сервиса, поэтому чужая миграция сюда попасть не может. Файл содержит несколько
// операторов, поэтому выполняется без аргументов (простой протокол).
func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	migrations, err := migrate.Load(migrationsDir, migrate.OwnSchema(postgres.Schema))
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			return err
		}
	}
	return nil
}

// truncateTablesSQL очищает таблицы сервиса.
const truncateTablesSQL = `TRUNCATE "audience".segment, "audience".audience_snapshot, "audience".outbox`
