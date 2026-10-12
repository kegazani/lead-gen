package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/analytics/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/ports/contracttest"
	"lead-gen/backend/platform/migrate"
)

// PostgreSQL-тесты адаптера запускаются против реальной базы: contract suite
// проверяет поведение хранилища, а не мока. Строка подключения берётся из
// переменной окружения LEADGEN_POSTGRES_TEST_DSN; если она не задана, тесты
// пропускаются, поэтому `make check` не требует базы.
//
// Примеры запуска: при ручном запуске задайте LEADGEN_POSTGRES_TEST_DSN на свою
// базу; цель `make test-postgres` поднимает одноразовую базу сама.
const dsnEnv = "LEADGEN_POSTGRES_TEST_DSN"

// migrationsDir — каталог SQL-миграций сервиса относительно каталога теста
// (internal/adapters/persistence/postgres).
var migrationsDir = filepath.Join("..", "..", "..", "..", "migrations")

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

// TestProjectionStoreContract прогоняет общий contract suite порта
// ProjectionStore против PostgreSQL-реализации. Тот же suite проходит и против
// in-memory-реализации.
func TestProjectionStoreContract(t *testing.T) {
	contracttest.RunProjectionStoreContract(t, func(t *testing.T) ports.ProjectionStore {
		t.Helper()
		return newStore(t)
	})
}

// newStore возвращает хранилище с пустым состоянием: suite вызывает фабрику перед
// каждым подтестом и ожидает изоляцию прогонов.
func newStore(t *testing.T) ports.ProjectionStore {
	t.Helper()
	pool := testPool(t)
	if _, err := pool.Exec(context.Background(), truncateTable); err != nil {
		t.Fatalf("очистка проекции: %v", err)
	}
	return postgres.NewProjectionStore(pool)
}

// TestInsightStoreContract прогоняет общий contract suite порта InsightStore
// против PostgreSQL-реализации.
func TestInsightStoreContract(t *testing.T) {
	contracttest.RunInsightStoreContract(t, func(t *testing.T) ports.InsightStore {
		t.Helper()
		return newInsightStore(t)
	})
}

// newInsightStore возвращает хранилище инсайтов с пустым состоянием.
func newInsightStore(t *testing.T) ports.InsightStore {
	t.Helper()
	pool := testPool(t)
	if _, err := pool.Exec(context.Background(), truncateInsightTables); err != nil {
		t.Fatalf("очистка инсайтов: %v", err)
	}
	return postgres.NewInsightStore(pool)
}

// testPool подключается к базе из окружения и применяет миграции один раз за
// прогон. Без заданной строки подключения тесты пропускаются.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skipf("%s не задан: PostgreSQL-тесты адаптера пропущены", dsnEnv)
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

// TRUNCATE очищает проекцию, сохраняя схему и таблицу.
const truncateTable = `TRUNCATE "analytics".company_projection`

// TRUNCATE очищает таблицы инсайтов, сохраняя схему и таблицы.
const truncateInsightTables = `TRUNCATE "analytics".quality_insight_snapshot, "analytics".quality_insight_application`
