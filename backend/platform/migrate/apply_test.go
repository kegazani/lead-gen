package migrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAdvisoryKeyIsDeterministicAndSchemaScoped проверяет ключ блокировки: он
// стабилен для одной схемы и различен для разных, иначе конкурирующие схемы
// сериализовались бы зря, а одна схема — не сериализовалась.
func TestAdvisoryKeyIsDeterministicAndSchemaScoped(t *testing.T) {
	t.Parallel()
	first := advisoryKey(OwnSchema("consoleauth"))
	second := advisoryKey(OwnSchema("consoleauth"))
	if first != second {
		t.Fatalf("ключ схемы недетерминирован: %d != %d", first, second)
	}
	if first == advisoryKey(OwnSchema("platform-governance")) {
		t.Fatal("разные схемы получили один ключ блокировки")
	}
}

// TestApplyRejectsNilPool проверяет, что применение не паникует без пула.
func TestApplyRejectsNilPool(t *testing.T) {
	t.Parallel()
	if err := Apply(context.Background(), nil, "dir", OwnSchema("s")); err == nil {
		t.Fatal("применение без пула должно быть ошибкой")
	}
}

func TestCleanupAndReleaseMigrationConnDiscardsWhenResetFails(t *testing.T) {
	t.Parallel()
	var executed []string
	discarded, released := false, false
	err := cleanupAndReleaseMigrationConn(context.Background(), func(_ context.Context, sql string, _ ...any) error {
		executed = append(executed, sql)
		return errors.New("injected RESET failure")
	}, true, true, 7, false, func(context.Context) error { discarded = true; return nil }, func() { released = true })
	if err == nil || !discarded || released {
		t.Fatalf("RESET failure must discard, never release: err=%v discarded=%v released=%v", err, discarded, released)
	}
	if !reflect.DeepEqual(executed, []string{"RESET ROLE"}) {
		t.Fatalf("after RESET failure the lock must be released by closing, not reused: %#v", executed)
	}
}

func TestCleanupAndReleaseMigrationConnDiscardsWhenUnlockFails(t *testing.T) {
	t.Parallel()
	var executed []string
	discarded, released := false, false
	err := cleanupAndReleaseMigrationConn(context.Background(), func(_ context.Context, sql string, _ ...any) error {
		executed = append(executed, sql)
		if sql == "SELECT pg_advisory_unlock($1)" {
			return errors.New("injected unlock failure")
		}
		return nil
	}, true, true, 7, false, func(context.Context) error { discarded = true; return nil }, func() { released = true })
	if err == nil || !discarded || released {
		t.Fatalf("unlock failure must discard, never release: err=%v discarded=%v released=%v", err, discarded, released)
	}
	if !reflect.DeepEqual(executed, []string{"RESET ROLE", "SELECT pg_advisory_unlock($1)"}) {
		t.Fatalf("неожиданный порядок session cleanup: %#v", executed)
	}
}

func TestCleanupAndReleaseMigrationConnReturnsOnlyCleanSession(t *testing.T) {
	t.Parallel()
	var executed []string
	discarded, released := false, false
	err := cleanupAndReleaseMigrationConn(context.Background(), func(_ context.Context, sql string, _ ...any) error {
		executed = append(executed, sql)
		return nil
	}, true, true, 7, false, func(context.Context) error { discarded = true; return nil }, func() { released = true })
	if err != nil || discarded || !released {
		t.Fatalf("clean session may be released: err=%v discarded=%v released=%v", err, discarded, released)
	}
	if !reflect.DeepEqual(executed, []string{"RESET ROLE", "SELECT pg_advisory_unlock($1)"}) {
		t.Fatalf("неожиданный порядок session cleanup: %#v", executed)
	}
}

func TestCleanupAndReleaseMigrationConnDiscardsAmbiguousSession(t *testing.T) {
	t.Parallel()
	discarded, released, executed := false, false, false
	err := cleanupAndReleaseMigrationConn(context.Background(), func(context.Context, string, ...any) error {
		executed = true
		return nil
	}, true, true, 7, true, func(context.Context) error { discarded = true; return nil }, func() { released = true })
	if err != nil || !discarded || released || executed {
		t.Fatalf("ambiguous session must be discarded without cleanup attempts: err=%v discarded=%v released=%v executed=%v", err, discarded, released, executed)
	}
}

func TestApplyCleanupResetFailureHijacksRealPGXPoolConnection(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("LEADGEN_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("LEADGEN_POSTGRES_TEST_DSN не задан")
	}
	fixture, database := provisionTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	owner := fmt.Sprintf("cleanup_owner_%d", time.Now().UnixNano())
	assertTestRolesAbsent(t, ctx, fixture.pool, owner)
	if _, err := fixture.pool.Exec(ctx, "CREATE ROLE "+pgx.Identifier{owner}.Sanitize()+" NOLOGIN"); err != nil {
		t.Fatal("создание owner для проверки cleanup:", err)
	}

	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("разбор PostgreSQL test DSN:", err)
	}
	config.Database = database
	poolConfig, err := pgxpool.ParseConfig(config.ConnString())
	if err != nil {
		t.Fatal("pool config cleanup test:", err)
	}
	poolConfig.ConnConfig = config
	poolConfig.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal("создание pool для discard test:", err)
	}
	defer func() {
		pool.Close()
		_, _ = fixture.pool.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{owner}.Sanitize())
	}()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire:", err)
	}
	if _, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{owner}.Sanitize()); err != nil {
		conn.Release()
		t.Fatal("SET ROLE owner:", err)
	}
	var oldPID int32
	if err := conn.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&oldPID); err != nil {
		conn.Release()
		t.Fatal("backend pid before cleanup:", err)
	}
	discarded, released, unlockAttempted := false, false, false
	err = cleanupAndReleaseMigrationConn(ctx, func(_ context.Context, sql string, _ ...any) error {
		if sql == "RESET ROLE" {
			return errors.New("injected RESET ROLE failure")
		}
		if sql == "SELECT pg_advisory_unlock($1)" {
			unlockAttempted = true
		}
		return nil
	}, true, true, 99, false,
		func(ctx context.Context) error {
			discarded = true
			return conn.Hijack().Close(ctx)
		},
		func() { released = true; conn.Release() },
	)
	if err == nil || !discarded || released || unlockAttempted {
		t.Fatalf("cleanup must discard elevated connection: err=%v discarded=%v released=%v unlock=%v", err, discarded, released, unlockAttempted)
	}

	clean, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("pool не смог создать чистое соединение после discard:", err)
	}
	defer clean.Release()
	var newPID int32
	var currentUser, currentRole string
	if err := clean.QueryRow(ctx, "SELECT pg_backend_pid(), current_user, current_role").Scan(&newPID, &currentUser, &currentRole); err != nil {
		t.Fatal("чтение повторно выданного connection:", err)
	}
	if newPID == oldPID || currentRole != currentUser {
		t.Fatalf("pool вернул старую/elevated session: old pid=%d new pid=%d user=%q role=%q", oldPID, newPID, currentUser, currentRole)
	}
}
