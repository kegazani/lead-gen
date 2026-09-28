package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/contexts/lead-management/internal/ports/contracttest"
	"lead-gen/backend/platform/migrate"
)

// PostgreSQL-тесты адаптеров запускаются против реальной базы: contract suite
// проверяет поведение хранилища, а не мока. Строка подключения берётся из
// переменной окружения LEADGEN_POSTGRES_TEST_DSN; если она не задана, тесты
// пропускаются, поэтому обычная сборка не требует базы.
//
// Пример ручного запуска: задайте LEADGEN_POSTGRES_TEST_DSN на свою базу.
const dsnEnv = "LEADGEN_POSTGRES_TEST_DSN"

// migrationsDir — каталог SQL-миграций сервиса относительно каталога теста
// (internal/adapters/persistence/postgres). Миграции разложены по зонам green и
// red, поэтому загрузчик обходит оба подкаталога.
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

// TestLeadProfileRepositoryContract прогоняет общий contract suite порта
// LeadProfileRepository против PostgreSQL-реализации. Тот же suite проходит и
// против in-memory-реализации.
func TestLeadProfileRepositoryContract(t *testing.T) {
	contracttest.RunLeadProfileRepositoryContract(t, func(t *testing.T) ports.LeadProfileRepository {
		t.Helper()
		truncateTables(t)
		return postgres.NewLeadProfileRepository(testPool(t))
	})
}

// TestContactRepositoryContract прогоняет общий contract suite порта
// ContactRepository против PostgreSQL-реализации.
func TestContactRepositoryContract(t *testing.T) {
	contracttest.RunContactRepositoryContract(t, func(t *testing.T) ports.ContactRepository {
		t.Helper()
		truncateTables(t)
		return postgres.NewContactRepository(testPool(t))
	})
}

// TestSearchJobStoreContract прогоняет общий contract suite порта SearchJobStore
// против PostgreSQL-реализации.
func TestSearchJobStoreContract(t *testing.T) {
	contracttest.RunSearchJobStoreContract(t, func(t *testing.T) ports.SearchJobStore {
		t.Helper()
		truncateTables(t)
		return postgres.NewSearchJobStore(testPool(t))
	})
}

// TestOutboundMessageRepositoryContract прогоняет общий contract suite порта
// OutboundMessageRepository против PostgreSQL-реализации.
func TestOutboundMessageRepositoryContract(t *testing.T) {
	contracttest.RunOutboundMessageRepositoryContract(t, func(t *testing.T) ports.OutboundMessageRepository {
		t.Helper()
		truncateTables(t)
		return postgres.NewOutboundMessageRepository(testPool(t))
	})
}

// TestLeadActivityStoreContract прогоняет общий contract suite порта
// LeadActivityStore против PostgreSQL-реализации.
func TestLeadActivityStoreContract(t *testing.T) {
	contracttest.RunLeadActivityStoreContract(t, func(t *testing.T) ports.LeadActivityStore {
		t.Helper()
		truncateTables(t)
		return postgres.NewLeadActivityStore(testPool(t))
	})
}

// TestLeadAssessmentStoreContract прогоняет общий contract suite порта
// LeadAssessmentStore против PostgreSQL-реализации.
func TestLeadAssessmentStoreContract(t *testing.T) {
	contracttest.RunAssessmentStoreContract(t, func(t *testing.T) ports.LeadAssessmentStore {
		t.Helper()
		truncateTables(t)
		return postgres.NewAssessmentStore(testPool(t))
	})
}

// TestEmailTemplateRepositoryContract прогоняет общий contract suite порта
// EmailTemplateRepository против PostgreSQL-реализации.
func TestEmailTemplateRepositoryContract(t *testing.T) {
	contracttest.RunEmailTemplateRepositoryContract(t, func(t *testing.T) ports.EmailTemplateRepository {
		t.Helper()
		truncateTables(t)
		return postgres.NewEmailTemplateRepository(testPool(t))
	})
}

// truncateTables очищает все таблицы сервиса, сохраняя схему. Фабрика вызывает её
// перед каждым подтестом и ожидает изоляцию прогонов.
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

// applyMigrations применяет миграции схемы сервиса из обеих зон данных. Файлы
// читаются загрузчиком backend/platform/migrate, который до выполнения проверяет
// принадлежность схеме сервиса, поэтому чужая миграция сюда попасть не может.
// Файл содержит несколько операторов, поэтому выполняется без аргументов
// (простой протокол).
func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	for _, zone := range []string{"green", "red"} {
		migrations, err := migrate.Load(filepath.Join(migrationsDir, zone), migrate.OwnSchema(postgres.Schema))
		if err != nil {
			return err
		}
		for _, migration := range migrations {
			if _, err := pool.Exec(ctx, migration.SQL); err != nil {
				return err
			}
		}
	}
	return nil
}

// truncateTablesSQL очищает таблицы сервиса. Ссылочных связей между ними нет,
// поэтому обычного TRUNCATE достаточно.
const truncateTablesSQL = `TRUNCATE "lead-management".lead_profile, "lead-management".contact, "lead-management".search_job, "lead-management".search_job_event, "lead-management".outbound_message, "lead-management".lead_activity, "lead-management".email_template, "lead-management".lead_assessment`
