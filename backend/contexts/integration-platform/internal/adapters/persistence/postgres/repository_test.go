package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/integration-platform/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
	"lead-gen/backend/contexts/integration-platform/internal/ports/contracttest"
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

// TestAdapterRegistrationRepositoryContract прогоняет общий contract suite порта
// AdapterRegistrationRepository против PostgreSQL-реализации. Тот же suite
// проходит и против in-memory-реализации.
func TestAdapterRegistrationRepositoryContract(t *testing.T) {
	contracttest.RunAdapterRegistrationRepositoryContract(t, func(t *testing.T) ports.AdapterRegistrationRepository {
		t.Helper()
		clean(t)
		return postgres.NewAdapterRegistrationRepository(testPool(t))
	})
}

// TestIntegrationInvocationRepositoryContract прогоняет общий contract suite
// порта IntegrationInvocationRepository против PostgreSQL-реализации.
func TestIntegrationInvocationRepositoryContract(t *testing.T) {
	contracttest.RunIntegrationInvocationRepositoryContract(t, func(t *testing.T) ports.IntegrationInvocationRepository {
		t.Helper()
		clean(t)
		return postgres.NewIntegrationInvocationRepository(testPool(t))
	})
}

// clean очищает таблицы схемы сервиса, чтобы каждый прогон suite начинался с
// пустого состояния.
func clean(t *testing.T) {
	t.Helper()
	if _, err := testPool(t).Exec(context.Background(), truncateTables); err != nil {
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

// TRUNCATE ... CASCADE очищает регистрации адаптеров вместе с возможностями и
// вызовы интеграций, сохраняя схему и таблицы.
const truncateTables = `
TRUNCATE "integration-platform".adapter_registration, "integration-platform".integration_invocation CASCADE`
