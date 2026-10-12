package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports/contracttest"
	"lead-gen/backend/platform/migrate"
)

// PostgreSQL-тесты адаптеров запускаются против реальной базы: contract suite
// проверяет поведение хранилища, а не мока. Строка подключения берётся из
// переменной окружения LEADGEN_POSTGRES_TEST_DSN; если она не задана, тесты
// пропускаются, поэтому `make check` не требует базы. Цель `make test-postgres`
// поднимает одноразовую базу сама.
const dsnEnv = "LEADGEN_POSTGRES_TEST_DSN"

// migrationsDir — каталог SQL-миграций сервиса относительно каталога теста
// (internal/adapters/persistence/postgres). Зона данных у контекста одна — green.
var migrationsDir = filepath.Join("..", "..", "..", "..", "migrations", "green")

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

// TestRunRepositoryContract прогоняет общий contract suite репозитория запусков
// против PostgreSQL-реализации.
func TestRunRepositoryContract(t *testing.T) {
	contracttest.RunRunRepositoryContract(t, func(t *testing.T) ports.RunRepository {
		t.Helper()
		truncateTables(t)
		return postgres.NewRunRepository(testPool(t))
	})
}

// TestPipelineRepositoryContract прогоняет общий contract suite репозитория
// пайплайнов против PostgreSQL-реализации.
func TestPipelineRepositoryContract(t *testing.T) {
	contracttest.RunPipelineRepositoryContract(t, func(t *testing.T) ports.PipelineRepository {
		t.Helper()
		truncateTables(t)
		return postgres.NewPipelineRepository(testPool(t))
	})
}

// TestUnitOfWorkContract прогоняет общий contract suite транзакции против
// PostgreSQL-реализации: запись агрегата и запись событий в outbox фиксируются одной
// транзакцией, ошибка use case откатывает обе.
func TestUnitOfWorkContract(t *testing.T) {
	contracttest.RunUnitOfWorkContract(t, func(t *testing.T) contracttest.UnitOfWorkHarness {
		t.Helper()
		truncateTables(t)
		pool := testPool(t)
		return contracttest.UnitOfWorkHarness{
			UnitOfWork: postgres.NewUnitOfWork(pool),
			Runs:       postgres.NewRunRepository(pool),
			Journal:    postgres.NewJournal(pool),
		}
	})
}

// truncateTables очищает таблицы сервиса, сохраняя схему.
func truncateTables(t *testing.T) {
	t.Helper()
	if _, err := testPool(t).Exec(context.Background(), truncateTablesSQL); err != nil {
		t.Fatalf("очистка таблиц: %v", err)
	}
}

// testPool подключается к базе из окружения и применяет миграции один раз за прогон.
// Без заданной строки подключения тесты пропускаются.
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
// сервиса.
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
const truncateTablesSQL = `TRUNCATE "agent-orchestration".agent_pipeline, "agent-orchestration".agent_run, "agent-orchestration".outbox`
