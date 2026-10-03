package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports/contracttest"
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

// TestRequestRepositoryContract прогоняет общий contract suite порта
// RequestRepository против PostgreSQL-реализации. Тот же suite проходит и против
// in-memory-реализации.
func TestRequestRepositoryContract(t *testing.T) {
	contracttest.RunRequestRepositoryContract(t, func(t *testing.T) ports.RequestRepository {
		t.Helper()
		return newRepository(t)
	})
}

// TestUnitOfWorkContract прогоняет общий contract suite порта UnitOfWork против
// PostgreSQL-реализации: запись агрегата и запись событий в outbox фиксируются
// одной транзакцией, ошибка use case откатывает обе.
func TestUnitOfWorkContract(t *testing.T) {
	contracttest.RunUnitOfWorkContract(t, func(t *testing.T) contracttest.UnitOfWorkHarness {
		t.Helper()
		pool := testPool(t)
		truncateTables(t)
		return contracttest.UnitOfWorkHarness{
			UnitOfWork: postgres.NewUnitOfWork(pool),
			Repository: postgres.NewRepository(pool),
			Journal:    postgres.NewJournal(pool),
		}
	})
}

// TestEmailDraftStoreContract прогоняет общий contract suite порта EmailDraftStore
// против PostgreSQL-реализации.
func TestEmailDraftStoreContract(t *testing.T) {
	contracttest.RunEmailDraftStoreContract(t, func(t *testing.T) ports.EmailDraftStore {
		t.Helper()
		return newDraftStore(t)
	})
}

// newRepository возвращает репозиторий заявок с пустым состоянием: suite вызывает
// фабрику перед каждым подтестом и ожидает изоляцию прогонов.
func newRepository(t *testing.T) ports.RequestRepository {
	t.Helper()
	pool := testPool(t)
	truncateTables(t)
	return postgres.NewRepository(pool)
}

// newDraftStore возвращает хранилище черновиков с пустым состоянием.
func newDraftStore(t *testing.T) ports.EmailDraftStore {
	t.Helper()
	pool := testPool(t)
	truncateTables(t)
	return postgres.NewDraftStore(pool)
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
const truncateTablesSQL = `TRUNCATE "request-fulfillment".request, "request-fulfillment".email_draft, "request-fulfillment".outbox`
