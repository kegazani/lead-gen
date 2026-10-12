package migrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	provisionTestAdminDatabase = "leadgen_provision_test_admin"
	provisionTestAdminMarker   = "lead-gen-provision-test-admin-v1"
	provisionTestChildPrefix   = "leadgen_provision_test_db_"
	provisionTestChildMarker   = "lead-gen-provision-test-child-v1:"
)

type isolatedProvisionDB struct {
	pool   *pgxpool.Pool
	parent *pgxpool.Pool
	name   string
	once   sync.Once
}

// provisionTestDatabase fails closed: test DSN must point at an explicitly marked,
// separately prepared sacrificial admin DB. Only this helper-created child DB is
// destroyed; request-fulfillment in the admin DB or any pre-existing database is
// never dropped.
func provisionTestDatabase(t *testing.T) (*isolatedProvisionDB, string) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("LEADGEN_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("LEADGEN_POSTGRES_TEST_DSN не задан")
	}
	if os.Getenv("LEADGEN_POSTGRES_TEST_ALLOW_DATABASE_CREATE") != "1" {
		t.Fatal("интеграционный тест требует явного LEADGEN_POSTGRES_TEST_ALLOW_DATABASE_CREATE=1")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("разбор PostgreSQL test DSN:", err)
	}
	if err := validateTestDatabaseTarget(config.Database, os.Getenv("LEADGEN_POSTGRES_TEST_DATABASE"), os.Getenv("LEADGEN_POSTGRES_TEST_ALLOW_DATABASE_CREATE")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	adminPoolConfig, err := pgxpool.ParseConfig(config.ConnString())
	if err != nil {
		t.Fatal("разбор pool config sacrificial DB:", err)
	}
	adminPoolConfig.ConnConfig = config
	admin, err := pgxpool.NewWithConfig(ctx, adminPoolConfig)
	if err != nil {
		t.Fatal("подключение к sacrificial admin DB:", err)
	}
	if err := admin.Ping(ctx); err != nil {
		admin.Close()
		t.Fatal("ping sacrificial admin DB:", err)
	}
	var marker string
	if err := admin.QueryRow(ctx, `SELECT shobj_description(oid,'pg_database') FROM pg_database WHERE datname=current_database()`).Scan(&marker); err != nil || marker != provisionTestAdminMarker {
		admin.Close()
		t.Fatalf("test admin DB не имеет обязательного marker %q: got=%q err=%v", provisionTestAdminMarker, marker, err)
	}
	var canCreateDB bool
	if err := admin.QueryRow(ctx, `SELECT rolsuper OR rolcreatedb FROM pg_roles WHERE rolname=current_user`).Scan(&canCreateDB); err != nil || !canCreateDB {
		admin.Close()
		t.Fatalf("test identity должна иметь CREATEDB только в выделенном тестовом кластере: allowed=%v err=%v", canCreateDB, err)
	}
	name := fmt.Sprintf("%s%d", provisionTestChildPrefix, time.Now().UnixNano())
	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&exists); err != nil || exists {
		admin.Close()
		t.Fatalf("child database name collision/preflight failed for %q: exists=%v err=%v", name, exists, err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close()
		t.Fatal("создание одноразовой test database:", err)
	}
	if _, err := admin.Exec(ctx, "COMMENT ON DATABASE "+pgx.Identifier{name}.Sanitize()+" IS '"+provisionTestChildMarker+name+"'"); err != nil {
		// Не удаляем DB без установленного marker: она останется явно именованной,
		// а администратор тестового кластера сможет безопасно проверить её вручную.
		admin.Close()
		t.Fatal("установка child database marker; база оставлена, cleanup запрещён:", err)
	}
	fixture := &isolatedProvisionDB{parent: admin, name: name}
	t.Cleanup(func() { fixture.cleanup(t) })
	childConfig := config.Copy()
	childConfig.Database = name
	childPoolConfig, err := pgxpool.ParseConfig(config.ConnString())
	if err != nil {
		t.Fatal("разбор pool config child database:", err)
	}
	childPoolConfig.ConnConfig = childConfig
	child, err := pgxpool.NewWithConfig(ctx, childPoolConfig)
	if err != nil {
		t.Fatal("создание connection pool test database:", err)
	}
	if err := child.Ping(ctx); err != nil {
		child.Close()
		t.Fatal("ping test database:", err)
	}
	fixture.pool = child
	return fixture, name
}

func (db *isolatedProvisionDB) cleanup(t *testing.T, roles ...string) {
	t.Helper()
	db.once.Do(func() {
		if db.pool != nil {
			db.pool.Close()
		}
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := dropMarkedProvisionTestDatabase(cleanupCtx, db.parent, db.name); err != nil {
			t.Errorf("child database %q не удалена безопасно: %v", db.name, err)
			db.parent.Close()
			return
		}
		for _, role := range roles {
			if _, err := db.parent.Exec(cleanupCtx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Errorf("удаление test-only роли %q после удаления child database: %v", role, err)
			}
		}
		db.parent.Close()
	})
}

func validateTestDatabaseTarget(dsnDatabase, envDatabase, allowCreate string) error {
	if dsnDatabase != provisionTestAdminDatabase || envDatabase != provisionTestAdminDatabase {
		return fmt.Errorf("test DSN обязан указывать только на sacrificial DB %q и совпадать с LEADGEN_POSTGRES_TEST_DATABASE", provisionTestAdminDatabase)
	}
	if allowCreate != "1" {
		return errors.New("интеграционный тест требует явного LEADGEN_POSTGRES_TEST_ALLOW_DATABASE_CREATE=1")
	}
	return nil
}

func dropMarkedProvisionTestDatabase(ctx context.Context, parent *pgxpool.Pool, name string) error {
	if !strings.HasPrefix(name, provisionTestChildPrefix) {
		return errors.New("имя database не соответствует выделенному test prefix")
	}
	var marker string
	err := parent.QueryRow(ctx, `SELECT shobj_description(oid,'pg_database') FROM pg_database WHERE datname=$1`, name).Scan(&marker)
	if err != nil || marker != provisionTestChildMarker+name {
		return fmt.Errorf("marker database не совпал; DROP запрещён: marker=%q err=%v", marker, err)
	}
	if _, err := parent.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("DROP DATABASE: %w", err)
	}
	return nil
}

func TestProvisionTestDatabaseTargetFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name, dsn, env, optIn string
	}{
		{"normal database refused", "postgres", "postgres", "1"},
		{"mismatched DSN refused", provisionTestAdminDatabase, "another-db", "1"},
		{"database creation needs explicit opt-in", provisionTestAdminDatabase, provisionTestAdminDatabase, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateTestDatabaseTarget(test.dsn, test.env, test.optIn); err == nil {
				t.Fatal("опасная тестовая цель прошла safety preflight")
			}
		})
	}
}

func TestProvisionTestDatabaseCleanupRefusesUnmarkedDatabase(t *testing.T) {
	fixture, _ := provisionTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	preexistingName := fmt.Sprintf("%sunmarked_%d", provisionTestChildPrefix, time.Now().UnixNano())
	if _, err := fixture.pool.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{preexistingName}.Sanitize()); err != nil {
		t.Fatal("создание test-only pre-existing DB fixture:", err)
	}
	if _, err := fixture.pool.Exec(ctx, "COMMENT ON DATABASE "+pgx.Identifier{preexistingName}.Sanitize()+" IS 'not-owned-by-cleanup'"); err != nil {
		t.Fatal("marker pre-existing DB fixture:", err)
	}
	if err := dropMarkedProvisionTestDatabase(ctx, fixture.parent, preexistingName); err == nil {
		t.Fatal("cleanup удалил бы database без своего ownership marker")
	}
	var exists bool
	if err := fixture.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)", preexistingName).Scan(&exists); err != nil || !exists {
		t.Fatalf("pre-existing test fixture должна остаться на месте: exists=%v err=%v", exists, err)
	}
	// Эта конкретная база была создана данным тестом; после подтверждения, что
	// safety helper её не удалил, тест удаляет её по сохранённому точному имени.
	if _, err := fixture.pool.Exec(ctx, "DROP DATABASE "+pgx.Identifier{preexistingName}.Sanitize()+" WITH (FORCE)"); err != nil {
		t.Fatal("cleanup собственной контрольной DB:", err)
	}
}

func TestProvisionCleanupDoesNotDropUnmarkedPreexistingDatabase(t *testing.T) {
	fixture, _ := provisionTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	name := fmt.Sprintf("%sforeign_%d", provisionTestChildPrefix, time.Now().UnixNano())
	if _, err := fixture.parent.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal("создание контрольной pre-existing database:", err)
	}
	if _, err := fixture.parent.Exec(ctx, "COMMENT ON DATABASE "+pgx.Identifier{name}.Sanitize()+" IS 'owned-by-other-test'"); err != nil {
		t.Fatal("marker контрольной database:", err)
	}
	if err := dropMarkedProvisionTestDatabase(ctx, fixture.parent, name); err == nil {
		t.Fatal("cleanup принял database без своего marker")
	}
	var exists bool
	if err := fixture.parent.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&exists); err != nil || !exists {
		t.Fatalf("pre-existing database была удалена/не найдена: exists=%v err=%v", exists, err)
	}
	// Эту контрольную DB создал сам тест, поэтому удаляет её напрямую по своему
	// точному имени; проверяемый cleanup helper её удалить не смог.
	if _, err := fixture.parent.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
		t.Fatal("очистка собственной контрольной database:", err)
	}
}
