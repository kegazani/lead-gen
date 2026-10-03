package consoleauth_test

import (
	"strings"
	"testing"

	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/migrate"
)

// TestConsoleauthMigrationsLoad проверяет, что миграция общей схемы сессий
// загружается без ошибки и объявляет схему consoleauth. Загрузчик migrate.Load
// отвергает чужую схему, поэтому отсутствие ошибки подтверждает и принадлежность.
func TestConsoleauthMigrationsLoad(t *testing.T) {
	migrations, err := migrate.Load(consoleauthMigrationsDir, migrate.OwnSchema(consoleauth.Schema))
	if err != nil {
		t.Fatalf("загрузка миграций consoleauth: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatalf("миграции consoleauth не найдены в %s", consoleauthMigrationsDir)
	}
	for _, migration := range migrations {
		if !strings.Contains(migration.SQL, "-- schema: "+consoleauth.Schema) {
			t.Fatalf("миграция %s не объявляет схему %q", migration.Name, consoleauth.Schema)
		}
		if !strings.Contains(migration.SQL, "console_sessions") {
			t.Fatalf("миграция %s не создаёт таблицу console_sessions", migration.Name)
		}
	}
}
