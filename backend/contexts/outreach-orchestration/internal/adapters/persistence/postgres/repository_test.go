package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracttest"
	"lead-gen/backend/platform/migrate"
)

// PostgreSQL-тесты адаптера запускаются против реальной базы: contract suite
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

// TestCampaignRepositoryContract прогоняет общий contract suite порта
// CampaignRepository против PostgreSQL-реализации (задача 5.2). Тот же suite
// проходит и против in-memory-реализации.
func TestCampaignRepositoryContract(t *testing.T) {
	contracttest.RunCampaignRepositoryContract(t, func(t *testing.T) ports.CampaignRepository {
		t.Helper()
		return newRepository(t)
	})
}

// TestDraftWindowContract прогоняет общий contract suite загрузки окна
// черновиков против PostgreSQL-реализации (задача 5.2).
func TestDraftWindowContract(t *testing.T) {
	contracttest.RunDraftWindowContract(t, func(t *testing.T) ports.DraftWindowRepository {
		t.Helper()
		return newRepository(t)
	})
}

// newRepository возвращает репозиторий с пустым состоянием: suite вызывает
// фабрику перед каждым подтестом и ожидает изоляцию прогонов.
func newRepository(t *testing.T) ports.DraftWindowRepository {
	t.Helper()
	pool := testPool(t)
	if _, err := pool.Exec(context.Background(), truncateTables); err != nil {
		t.Fatalf("очистка таблиц: %v", err)
	}
	return postgres.New(pool)
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

// TRUNCATE ... CASCADE очищает кампании и подчинённые строки черновиков и
// решений вместе с журналом outbox, сохраняя схему и таблицы.
const truncateTables = `TRUNCATE "outreach-orchestration".campaign, "outreach-orchestration".outbox CASCADE`

// TestListCampaignIDs проверяет порт CampaignLister против PostgreSQL. Тест
// пропускается без заданной строки подключения, как и остальные PG-suite.
func TestListCampaignIDs(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if _, err := pool.Exec(ctx, truncateTables); err != nil {
		t.Fatalf("очистка таблиц: %v", err)
	}
	repo := postgres.New(pool)

	ids, err := repo.ListCampaignIDs(ctx)
	if err != nil {
		t.Fatalf("ListCampaignIDs пустого репозитория: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("пустой репозиторий вернул %d кампаний", len(ids))
	}

	window, err := domain.NewCampaignWindow(time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("NewCampaignWindow: %v", err)
	}
	campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       "campaign-list",
		TenantID: "tenant-1",
		Name:     "Кампания перечисления",
		Window:   window,
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	if err := repo.Save(ctx, campaign); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ids, err = repo.ListCampaignIDs(ctx)
	if err != nil {
		t.Fatalf("ListCampaignIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "campaign-list" {
		t.Fatalf("ожидалась одна кампания campaign-list, получено %v", ids)
	}
}
