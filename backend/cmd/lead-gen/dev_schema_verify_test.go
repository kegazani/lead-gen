package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/platform/migrate"
)

// TestDevHarnessVerifySchemas выполняет только read-only migrate.Verify для
// пустой локальной demo-БД после штатного Apply при старте сервисов. DSN читаются
// из закрытых временных файлов и никогда не включаются в сообщения теста.
func TestDevHarnessVerifySchemas(t *testing.T) {
	greenDSN := devDSNFromFile(t, "LEADGEN_DEV_VERIFY_GREEN_DSN_FILE")
	redDSN := devDSNFromFile(t, "LEADGEN_DEV_VERIFY_RED_DSN_FILE")
	migrationRoot := strings.TrimSpace(os.Getenv("LEADGEN_DEV_VERIFY_MIGRATION_ROOT"))
	if migrationRoot == "" {
		migrationRoot = "../.."
	}

	green := devVerifyPool(t, greenDSN)
	red := devVerifyPool(t, redDSN)
	checks := []struct {
		name   string
		pool   *pgxpool.Pool
		dir    string
		schema migrate.Schema
	}{
		{"lead-management:green", green, "contexts/lead-management/migrations/green", migrate.Schema("lead-management")},
		{"request-fulfillment", green, "contexts/request-fulfillment/migrations/green", migrate.Schema("request-fulfillment")},
		{"platform-governance", green, "contexts/platform-governance/migrations", migrate.Schema("platform-governance")},
		{"consoleauth", green, "platform/consoleauth/migrations/green", migrate.Schema("consoleauth")},
		{"integration-platform", green, "contexts/integration-platform/migrations", migrate.Schema("integration-platform")},
		{"lead-management:red", red, "contexts/lead-management/migrations/red", migrate.Schema("lead-management")},
	}
	for _, check := range checks {
		check := check
		t.Run(check.name, func(t *testing.T) {
			migrationDir := filepath.Join(migrationRoot, filepath.Clean(check.dir))
			if err := migrate.Verify(context.Background(), check.pool, migrationDir, check.schema); err != nil {
				t.Fatalf("read-only проверка схемы %s не пройдена", check.name)
			}
			migrations, err := migrate.Load(migrationDir, check.schema)
			if err != nil {
				t.Fatalf("не удалось перечислить проверенные версии схемы %s", check.name)
			}
			versions := make([]string, 0, len(migrations))
			for _, migration := range migrations {
				versions = append(versions, migration.Name)
			}
			t.Logf("проверены версии схемы %s: %s", check.name, strings.Join(versions, ", "))
		})
	}
}

func devDSNFromFile(t *testing.T, envName string) string {
	t.Helper()
	path := strings.TrimSpace(os.Getenv(envName))
	if path == "" {
		t.Skip("dev harness DSN file не задан")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("не удалось прочитать закрытый DSN-файл для %s", envName)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		t.Fatalf("закрытый DSN-файл для %s пуст", envName)
	}
	return value
}

func devVerifyPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("не удалось открыть пул для read-only проверки схемы")
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatal("не удалось проверить соединение для read-only проверки схемы")
	}
	return pool
}
