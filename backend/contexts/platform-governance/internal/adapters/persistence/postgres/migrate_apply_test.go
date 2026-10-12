package postgres_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/platform/migrate"
)

// TestApplyMigrationsIsIdempotent доказывает повторный `up`: применение миграций
// схемы сервиса на уже мигрированной базе завершается без ошибки (в том числе для
// миграции `0002_access_and_outbox.sql`, где перенос `role → roles` защищён
// проверкой наличия колонки).
func TestApplyMigrationsIsIdempotent(t *testing.T) {
	ctx := context.Background()
	// testPool применяет миграции один раз за прогон; повтор идёт через реальный
	// путь migrate.Apply.
	pool := testPool(t)
	if err := applyMigrations(ctx, pool); err != nil {
		t.Fatalf("повторное применение миграций: %v", err)
	}
	if err := applyMigrations(ctx, pool); err != nil {
		t.Fatalf("третье применение миграций: %v", err)
	}

	// Симуляция базы, мигрированной прежним кодом (без учёта версий): таблицы
	// версий нет, поэтому файлы выполняются заново. Миграция
	// 0002_access_and_outbox.sql должна быть идемпотентна — именно на ней падал
	// повторный старт живого прогона.
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS "platform-governance".schema_migrations`); err != nil {
		t.Fatalf("сброс таблицы версий: %v", err)
	}
	if err := applyMigrations(ctx, pool); err != nil {
		t.Fatalf("применение на базе без учёта версий: %v", err)
	}
}

// TestApplyConcurrentFirstStart доказывает безопасность конкурентного первого
// старта: несколько соединений одновременно применяют миграции чистой схемы,
// содержащей `CREATE SCHEMA IF NOT EXISTS` (та же гонка, что ломала общий контур
// consoleauth). Advisory-блокировка сериализует применение, поэтому ни один
// процесс не падает и схема создаётся один раз.
func TestApplyConcurrentFirstStart(t *testing.T) {
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skipf("%s не задан: PostgreSQL-тесты адаптера пропущены", dsnEnv)
	}

	ctx := context.Background()
	schema := migrate.Schema(fmt.Sprintf("migr-conc-%d", time.Now().UnixNano()))
	dir := t.TempDir()
	quoted := pgx.Identifier{schema.String()}.Sanitize()
	migration := fmt.Sprintf(
		"-- schema: %s\nCREATE SCHEMA IF NOT EXISTS %s;\nCREATE TABLE IF NOT EXISTS %s.t (id text PRIMARY KEY);\n",
		schema, quoted, quoted,
	)
	if err := os.WriteFile(filepath.Join(dir, "0001_init.sql"), []byte(migration), 0o600); err != nil {
		t.Fatalf("подготовка миграции: %v", err)
	}

	const processes = 6
	pools := make([]*pgxpool.Pool, 0, processes)
	for i := 0; i < processes; i++ {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatalf("пул %d: %v", i, err)
		}
		pools = append(pools, pool)
	}
	t.Cleanup(func() {
		for _, pool := range pools {
			pool.Close()
		}
		cleanup, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			return
		}
		defer cleanup.Close()
		_, _ = cleanup.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
	})

	start := make(chan struct{})
	errs := make(chan error, processes)
	var wg sync.WaitGroup
	for _, pool := range pools {
		wg.Add(1)
		go func(pool *pgxpool.Pool) {
			defer wg.Done()
			<-start
			errs <- migrate.Apply(ctx, pool, dir, schema)
		}(pool)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("конкурентное применение: %v", err)
		}
	}

	var tables, versions int
	if err := pools[0].QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = 't'`,
		schema.String()).Scan(&tables); err != nil {
		t.Fatalf("подсчёт таблиц: %v", err)
	}
	if err := pools[0].QueryRow(ctx, "SELECT count(*) FROM "+quoted+".schema_migrations").Scan(&versions); err != nil {
		t.Fatalf("подсчёт версий: %v", err)
	}
	if tables != 1 {
		t.Fatalf("таблиц схемы = %d, ожидалась 1", tables)
	}
	if versions != 1 {
		t.Fatalf("записей версий = %d, ожидалась 1", versions)
	}
}

// TestApplyFailedMigrationLeavesNoPartialEffect доказывает атомарность: файл,
// падающий после частичного изменения, откатывается целиком — ни объектов, ни
// записи версии не остаётся.
func TestApplyFailedMigrationLeavesNoPartialEffect(t *testing.T) {
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skipf("%s не задан: PostgreSQL-тесты адаптера пропущены", dsnEnv)
	}

	ctx := context.Background()
	schema := migrate.Schema(fmt.Sprintf("migr-fail-%d", time.Now().UnixNano()))
	dir := t.TempDir()
	quoted := pgx.Identifier{schema.String()}.Sanitize()
	migration := fmt.Sprintf(
		"-- schema: %s\nCREATE SCHEMA IF NOT EXISTS %s;\nCREATE TABLE IF NOT EXISTS %s.t (id text PRIMARY KEY);\nSELECT 1/0;\n",
		schema, quoted, quoted,
	)
	if err := os.WriteFile(filepath.Join(dir, "0001_bad.sql"), []byte(migration), 0o600); err != nil {
		t.Fatalf("подготовка миграции: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("пул: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quoted+" CASCADE")
		pool.Close()
	})

	if err := migrate.Apply(ctx, pool, dir, schema); err == nil {
		t.Fatal("падающая миграция должна вернуть ошибку")
	}

	var tables, versions int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = $1 AND table_name = 't'`,
		schema.String()).Scan(&tables); err != nil {
		t.Fatalf("подсчёт таблиц: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+quoted+".schema_migrations").Scan(&versions); err != nil {
		t.Fatalf("подсчёт версий: %v", err)
	}
	if tables != 0 {
		t.Fatalf("таблиц после отката = %d, ожидалось 0", tables)
	}
	if versions != 0 {
		t.Fatalf("записей версий после отката = %d, ожидалось 0", versions)
	}
}
