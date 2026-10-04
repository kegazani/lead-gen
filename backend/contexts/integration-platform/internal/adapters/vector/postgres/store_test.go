package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	vectoradapter "lead-gen/backend/contexts/integration-platform/internal/adapters/vector"
	vectorpostgres "lead-gen/backend/contexts/integration-platform/internal/adapters/vector/postgres"
	"lead-gen/backend/platform/migrate"
	"lead-gen/contracts/integration-platform/contracttest"
	contract "lead-gen/contracts/integration-platform/vector"
)

// PostgreSQL-тесты векторного индекса запускаются против реальной базы: contract
// suite проверяет поведение хранилища, а не мока. Строка подключения берётся из
// переменной окружения LEADGEN_POSTGRES_TEST_DSN; если она не задана, тесты
// пропускаются, поэтому `make check` не требует базы.
const dsnEnv = "LEADGEN_POSTGRES_TEST_DSN"

// migrationsDir — каталог SQL-миграций сервиса относительно каталога теста
// (internal/adapters/vector/postgres).
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

// TestVectorPortContract прогоняет общий contract suite порта vector против
// PostgreSQL-реализации. Тот же suite проходит и против in-memory-реализации
// (internal/adapters/vector).
func TestVectorPortContract(t *testing.T) {
	contracttest.RunVectorPortContract(t, func(t *testing.T) contract.Port {
		t.Helper()
		clean(t)
		return vectorpostgres.NewStore(testPool(t), vectoradapter.HashEmbedder{})
	})
}

// TestStateSurvivesRestart проверяет персистентность индекса: документ,
// записанный одним экземпляром хранилища, читается другим поверх того же пула —
// состояние живёт в PostgreSQL, а не в памяти процесса.
func TestStateSurvivesRestart(t *testing.T) {
	clean(t)
	ctx := context.Background()

	first := vectorpostgres.NewStore(testPool(t), vectoradapter.HashEmbedder{})
	if _, err := first.Upsert(ctx, contract.Document{ID: "company-1", Text: "разработка программного обеспечения"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	second := vectorpostgres.NewStore(testPool(t), vectoradapter.HashEmbedder{})
	matches, err := second.Search(ctx, "разработка программного обеспечения", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 1 || matches[0].ID != "company-1" {
		t.Fatalf("после рестарта получено %+v, ожидался company-1", matches)
	}
}

// clean очищает таблицу векторного индекса, чтобы каждый прогон suite начинался
// с пустого состояния.
func clean(t *testing.T) {
	t.Helper()
	if _, err := testPool(t).Exec(context.Background(), truncateDocuments); err != nil {
		t.Fatalf("очистка таблицы векторов: %v", err)
	}
}

// testPool подключается к базе из окружения и применяет миграции один раз за
// прогон. Без заданной строки подключения тесты пропускаются.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skipf("%s не задан: PostgreSQL-тесты векторного индекса пропущены", dsnEnv)
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
// сервиса, поэтому чужая миграция сюда попасть не может.
func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	migrations, err := migrate.Load(migrationsDir, migrate.OwnSchema(vectorpostgres.Schema))
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

// TRUNCATE очищает индекс, сохраняя схему и таблицу.
const truncateDocuments = `TRUNCATE "integration-platform".vector_document`
