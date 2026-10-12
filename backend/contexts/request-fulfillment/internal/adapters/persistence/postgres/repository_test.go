package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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
const allowSchemaResetEnv = "LEADGEN_POSTGRES_TEST_ALLOW_REQUEST_SCHEMA_RESET"

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
	var lockConn *pgx.Conn
	if os.Getenv(dsnEnv) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var err error
		lockConn, err = pgx.Connect(ctx, os.Getenv(dsnEnv))
		if err != nil {
			panic("подключение для блокировки request PostgreSQL suite: " + err.Error())
		}
		if _, err := lockConn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended('leadgen-request-fulfillment-postgres-suite', 0))`); err != nil {
			panic("блокировка request PostgreSQL suite: " + err.Error())
		}
	}
	code := m.Run()
	if pool != nil {
		pool.Close()
	}
	if lockConn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, _ = lockConn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended('leadgen-request-fulfillment-postgres-suite', 0))`)
		cancel()
		lockConn.Close(context.Background())
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

// TestExecutionAttemptContract прогоняет ту же suite, что и memory, на реальном
// PostgreSQL из LEADGEN_POSTGRES_TEST_DSN. Без DSN тест пропускается.
func TestExecutionAttemptContract(t *testing.T) {
	contracttest.RunExecutionAttemptContract(t, func(t *testing.T) contracttest.ExecutionAttemptHarness {
		t.Helper()
		p := testPool(t)
		truncateTables(t)
		return contracttest.ExecutionAttemptHarness{UnitOfWork: postgres.NewUnitOfWork(p), Repository: postgres.NewRepository(p), Attempts: postgres.NewExecutionAttemptRepository(p), Journal: postgres.NewJournal(p), ExpireLease: func() { time.Sleep(40 * time.Millisecond) }, AdvanceClock: func(time.Duration) { time.Sleep(5 * time.Millisecond) }}
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

// truncateTables пересоздаёт изолированную схему тестов целиком. Append-only
// audit triggers и FK не обходятся очисткой строк: после удаления схемы миграции
// применяются заново, как при создании новой тестовой базы.
func truncateTables(t *testing.T) {
	t.Helper()
	p := testPool(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `DROP SCHEMA IF EXISTS "request-fulfillment" CASCADE`); err != nil {
		t.Fatalf("удалить изолированную тестовую схему: %v", err)
	}
	if err := applyMigrations(ctx, p); err != nil {
		t.Fatalf("пересоздать тестовую схему миграциями: %v", err)
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
	if os.Getenv(allowSchemaResetEnv) != "1" {
		t.Fatalf("%s=1 обязателен: suite удаляет и пересоздаёт только принадлежащую request-fulfillment схему в disposable базе", allowSchemaResetEnv)
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
