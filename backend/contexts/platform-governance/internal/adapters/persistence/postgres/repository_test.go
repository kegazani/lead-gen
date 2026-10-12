package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/platform-governance/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
	"lead-gen/backend/platform/migrate"
)

// PostgreSQL-тесты адаптера запускаются против реальной базы: contract suite
// проверяет поведение хранилища (round-trip снимка, уникальность, отсутствие), а
// не мок. Строка подключения берётся из переменной окружения
// LEADGEN_POSTGRES_TEST_DSN; если она не задана, тесты пропускаются, поэтому
// `make check` не требует базы.
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

// TestRepositorySaveAndFind проверяет round-trip снимка агрегата: сохранённый
// тенант загружается тем же составом пользователей в том же порядке.
func TestRepositorySaveAndFind(t *testing.T) {
	repository := newRepository(t)
	tenant := newTenant(t)
	if err := repository.Save(context.Background(), tenant); err != nil {
		t.Fatalf("Save: %v", err)
	}

	found, err := repository.FindByID(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if found.CompanyName() != "ООО Ромашка" {
		t.Fatalf("название компании = %q", found.CompanyName())
	}
	if found.Description() != "Поставщик промышленного оборудования" {
		t.Fatalf("описание компании = %q", found.Description())
	}
	users := found.Users()
	if len(users) != 1 {
		t.Fatalf("число пользователей = %d", len(users))
	}
	if users[0].ID() != "user-1" || users[0].Login() != "client-1" || !users[0].Roles().Has(domain.RoleClient) {
		t.Fatalf("пользователь = %+v", users[0])
	}
}

// TestRepositoryNotFound проверяет, что отсутствие тенанта — это ErrTenantNotFound.
func TestRepositoryNotFound(t *testing.T) {
	repository := newRepository(t)
	if _, err := repository.FindByID(context.Background(), "ghost"); !errors.Is(err, ports.ErrTenantNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrTenantNotFound", err)
	}
}

// TestRepositorySaveNil проверяет, что сохранение nil-тенанта отвергается.
func TestRepositorySaveNil(t *testing.T) {
	repository := newRepository(t)
	if err := repository.Save(context.Background(), nil); err == nil {
		t.Fatal("сохранение nil-тенанта не отвергнуто")
	}
}

// TestRepositoryUpsertReplacesSnapshot проверяет, что повторное сохранение того
// же тенанта заменяет прежний снимок, не создавая вторую строку и не оставляя
// осиротевших пользователей.
func TestRepositoryUpsertReplacesSnapshot(t *testing.T) {
	repository := newRepository(t)
	tenant := newTenant(t)
	ctx := context.Background()
	if err := repository.Save(ctx, tenant); err != nil {
		t.Fatalf("Save #1: %v", err)
	}
	if err := repository.Save(ctx, tenant); err != nil {
		t.Fatalf("Save #2: %v", err)
	}

	var tenantRows, userRows int
	if err := testPool(t).QueryRow(ctx, `SELECT count(*) FROM "platform-governance".tenant`).Scan(&tenantRows); err != nil {
		t.Fatalf("подсчёт тенантов: %v", err)
	}
	if err := testPool(t).QueryRow(ctx, `SELECT count(*) FROM "platform-governance".tenant_user`).Scan(&userRows); err != nil {
		t.Fatalf("подсчёт пользователей: %v", err)
	}
	if tenantRows != 1 || userRows != 1 {
		t.Fatalf("после upsert тенантов = %d, пользователей = %d", tenantRows, userRows)
	}
}

// newTenant собирает тенант для проверок репозитория.
func newTenant(t *testing.T) *domain.Tenant {
	return newTenantNamed(t, "tenant-1", "ООО Ромашка")
}

// newTenantNamed собирает тенант с заданными идентификатором и компанией.
func newTenantNamed(t *testing.T, id, company string) *domain.Tenant {
	t.Helper()
	roles, err := domain.NewRoleSet(domain.RoleClient)
	if err != nil {
		t.Fatalf("NewRoleSet: %v", err)
	}
	tenant, err := domain.RegisterTenant(domain.RegisterTenantParams{
		ID:          domain.TenantID(id),
		CompanyName: company,
		Description: "Поставщик промышленного оборудования",
		At:          time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Actor:       "user-1",
		EventID:     domain.EventID("evt-" + id),
		AuditID:     domain.AuditRecordID("audit-" + id),
		User:        &domain.NewUser{ID: "user-1", Login: "client-1", Roles: roles},
	})
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	return tenant
}

// TestRepositoryList проверяет выборку тенантов: только платформенные атрибуты,
// устойчивый порядок по компании и состоянию, включая отключённый тенант.
func TestRepositoryList(t *testing.T) {
	repository := newRepository(t)
	ctx := context.Background()

	second := newTenantNamed(t, "tenant-2", "ООО Василёк")
	first := newTenantNamed(t, "tenant-3", "АО Альфа")
	if err := first.Disable(domain.ChangeContext{
		At:      time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
		Actor:   "admin-1",
		EventID: "evt-disable",
		AuditID: "audit-disable",
	}); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	for _, tenant := range []*domain.Tenant{second, first} {
		if err := repository.Save(ctx, tenant); err != nil {
			t.Fatalf("Save %s: %v", tenant.ID(), err)
		}
	}

	summaries, err := repository.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("тенантов = %d, ожидалось 2", len(summaries))
	}
	if summaries[0].ID != "tenant-3" || summaries[0].CompanyName != "АО Альфа" || summaries[0].State != domain.TenantDisabled {
		t.Fatalf("первый тенант = %+v", summaries[0])
	}
	if summaries[1].ID != "tenant-2" || summaries[1].CompanyName != "ООО Василёк" || summaries[1].State != domain.TenantActive {
		t.Fatalf("второй тенант = %+v", summaries[1])
	}
}

// newRepository возвращает репозиторий с пустым состоянием: тест ожидает
// изоляцию прогонов.
func newRepository(t *testing.T) *postgres.Repository {
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

// applyMigrations применяет миграции схемы сервиса. Принадлежность схеме,
// межпроцессную сериализацию и учёт уже применённых версий обеспечивает
// migrate.Apply, поэтому тест прогоняет реальный путь применения.
func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	return migrate.Apply(ctx, pool, migrationsDir, migrate.OwnSchema(postgres.Schema))
}

// TRUNCATE ... CASCADE очищает тенантов и подчинённые строки пользователей,
// сохраняя схему и таблицы.
const truncateTables = `TRUNCATE "platform-governance".tenant CASCADE`
