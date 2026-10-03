package consoleauth_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/migrate"
)

// consoleauthDSNEnv — переменная окружения со строкой подключения к PostgreSQL
// для интеграционных тестов стора. Если она не задана, тесты пропускаются,
// поэтому обычная сборка не требует базы. Пример ручного запуска: задайте
// LEADGEN_POSTGRES_TEST_DSN на свою базу.
const consoleauthDSNEnv = "LEADGEN_POSTGRES_TEST_DSN"

// consoleauthMigrationsDir — каталог миграций схемы consoleauth относительно
// каталога теста (backend/platform/consoleauth).
var consoleauthMigrationsDir = filepath.Join("migrations", "green")

// testPostgresStore подключается к базе из окружения, применяет миграции схемы
// consoleauth и очищает таблицу сессий. Без заданной строки подключения тест
// пропускается.
func testPostgresStore(t *testing.T) *consoleauth.PostgresStore {
	t.Helper()
	dsn := os.Getenv(consoleauthDSNEnv)
	if dsn == "" {
		t.Skipf("%s не задан: интеграционные тесты стора пропущены", consoleauthDSNEnv)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("подключение к PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)

	migrations, err := migrate.Load(consoleauthMigrationsDir, migrate.OwnSchema(consoleauth.Schema))
	if err != nil {
		t.Fatalf("загрузка миграций: %v", err)
	}
	for _, m := range migrations {
		if _, err := pool.Exec(ctx, m.SQL); err != nil {
			t.Fatalf("миграция %s: %v", m.Name, err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM consoleauth.console_sessions`); err != nil {
		t.Fatalf("очистка таблицы сессий: %v", err)
	}
	return consoleauth.NewPostgresStore(pool)
}

// TestPostgresStoreContract проверяет контракт стора против PostgreSQL:
// сохранение, чтение, отсутствие записи, перезапись и удаление.
func TestPostgresStoreContract(t *testing.T) {
	store := testPostgresStore(t)
	ctx := context.Background()
	at := time.Date(2026, time.March, 4, 10, 30, 0, 0, time.UTC)

	if _, ok, err := store.Load(ctx, "нет-такой"); err != nil || ok {
		t.Fatalf("Load отсутствующей сессии = ok %v, err %v", ok, err)
	}

	rec := consoleauth.Record{ID: "s1", Username: "operator", CSRF: "csrf-1", LastActive: at}
	if err := store.Save(ctx, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, ok, err := store.Load(ctx, "s1")
	if err != nil || !ok {
		t.Fatalf("Load сохранённой сессии = ok %v, err %v", ok, err)
	}
	if got.ID != rec.ID || got.Username != rec.Username || got.CSRF != rec.CSRF || !got.LastActive.Equal(at) {
		t.Fatalf("Load = %+v, ожидалось %+v", got, rec)
	}

	// Повторный Save по тому же ID обновляет запись (upsert).
	updated := rec
	updated.CSRF = "csrf-2"
	updated.LastActive = at.Add(time.Minute)
	if err := store.Save(ctx, updated); err != nil {
		t.Fatalf("повторный Save: %v", err)
	}
	got, _, _ = store.Load(ctx, "s1")
	if got.CSRF != "csrf-2" || !got.LastActive.Equal(updated.LastActive) {
		t.Fatalf("после перезаписи = %+v", got)
	}

	if err := store.Delete(ctx, "s1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, err := store.Load(ctx, "s1"); err != nil || ok {
		t.Fatalf("Load после Delete = ok %v, err %v", ok, err)
	}
	if err := store.Delete(ctx, "s1"); err != nil {
		t.Fatalf("повторный Delete: %v", err)
	}
}

// TestPostgresStoreBacksManager проверяет, что менеджер работает поверх
// PostgreSQL-стора: вход сохраняет сессию, проверка её находит, выход удаляет.
func TestPostgresStoreBacksManager(t *testing.T) {
	store := testPostgresStore(t)
	mgr := consoleauth.New(consoleauth.Config{
		Username: "operator", Password: "secret", Store: store,
	})

	id, err := mgr.Login("operator", "secret")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	sess, ok := mgr.Authenticate(id)
	if !ok || sess.Username() != "operator" {
		t.Fatalf("Authenticate через PostgreSQL-стор = ok %v", ok)
	}

	mgr.Logout(id)
	if _, ok := mgr.Authenticate(id); ok {
		t.Fatalf("Authenticate после Logout через PostgreSQL-стор = true")
	}
}
