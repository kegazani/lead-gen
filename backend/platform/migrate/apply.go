package migrate

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationsTable — имя таблицы учёта применённых версий в схеме сервиса.
const migrationsTable = "schema_migrations"

// Apply загружает миграции схемы owned из каталога dir и применяет их к пулу.
//
// Применение безопасно при конкурентном старте нескольких процессов и при
// повторном запуске на существующей базе:
//
//   - на время применения схемы удерживается межпроцессная advisory-блокировка
//     PostgreSQL, ключ которой детерминированно выведен из имени схемы, поэтому
//     конкурирующие процессы одной схемы выстраиваются в очередь и не гоняются за
//     CREATE SCHEMA/объектами;
//   - в схеме ведётся таблица <schema>.schema_migrations, и уже применённые файлы
//     пропускаются;
//   - каждый файл применяется в отдельной транзакции: SQL миграции и запись
//     версии атомарны, ошибка не оставляет частичного эффекта.
//
// Принадлежность миграций схеме проверяет Load до выполнения.
func Apply(ctx context.Context, pool *pgxpool.Pool, dir string, owned Schema) error {
	return apply(ctx, pool, dir, owned, "", nil)
}

// ApplyAs применяет миграции от имени NOLOGIN owner. pool обязан быть подключён
// migration login, которому выдано членство SET только в указанной owner role.
// Схема предварительно создаётся ProvisionSchema; database-level CREATE мигратору
// не требуется. После выхода из функции роль обязательно сбрасывается.
func ApplyAs(ctx context.Context, pool *pgxpool.Pool, dir string, owned Schema, ownerRole string) error {
	if strings.TrimSpace(ownerRole) == "" {
		return errors.New("migrate: owner role не задан")
	}
	return apply(ctx, pool, dir, owned, ownerRole, nil)
}

// ApplyAsAllowlisted applies only the exact, checksum-pinned migration files.
// It is intended for an explicit G1 rollback window; unknown/changed files never
// become eligible implicitly.
func ApplyAsAllowlisted(ctx context.Context, pool *pgxpool.Pool, dir string, owned Schema, ownerRole string, approved []ApprovedMigration) error {
	if strings.TrimSpace(ownerRole) == "" {
		return errors.New("migrate: owner role не задан")
	}
	if len(approved) == 0 {
		return errors.New("migrate: approved migration manifest не задан")
	}
	return apply(ctx, pool, dir, owned, ownerRole, approved)
}

func apply(ctx context.Context, pool *pgxpool.Pool, dir string, owned Schema, ownerRole string, approved []ApprovedMigration) (resultErr error) {
	if pool == nil {
		return errors.New("migrate: пул соединений не задан")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	migrations, err := Load(dir, owned)
	if err != nil {
		return err
	}
	if approved != nil {
		migrations, err = SelectApprovedMigrationFiles(migrations, approved)
		if err != nil {
			return err
		}
	}
	if ownerRole != "" {
		// Validate every file before obtaining a migration session or changing the
		// version ledger, so an unsupported later file cannot follow partial Apply.
		for i := range migrations {
			migrations[i].SQL, err = removeOwnedSchemaBootstrap(migrations[i].SQL, owned)
			if err != nil {
				return fmt.Errorf("%s: migration %s: %w", owned, migrations[i].Name, err)
			}
		}
	}

	// Блокировка действует в пределах сессии, поэтому всё применение идёт на
	// одном выделенном соединении пула: отдельные Exec могли бы получить разные.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("migrate: соединение для применения схемы %q: %w", owned, err)
	}
	key := advisoryKey(owned)
	lockHeld := false
	roleApplied := false
	discard := false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cleanupErr := cleanupAndReleaseMigrationConn(cleanupCtx, func(ctx context.Context, sql string, args ...any) error {
			_, err := conn.Exec(ctx, sql, args...)
			return err
		}, roleApplied, lockHeld, key, discard,
			func(ctx context.Context) error {
				physical := conn.Hijack()
				return physical.Close(ctx)
			}, conn.Release)
		resultErr = errors.Join(resultErr, cleanupErr)
	}()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		discard = true // Неизвестно, был ли lock установлен до потери ответа.
		return fmt.Errorf("migrate: блокировка применения схемы %q: %w", owned, err)
	}
	lockHeld = true
	if ownerRole != "" {
		if _, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{ownerRole}.Sanitize()); err != nil {
			discard = true
			return fmt.Errorf("migrate: установка owner role для схемы %q: %w", owned, err)
		}
		roleApplied = true
		var schemaExists bool
		if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname=$1)", owned.String()).Scan(&schemaExists); err != nil {
			return fmt.Errorf("migrate: проверка подготовленной схемы %q: %w", owned, err)
		}
		if !schemaExists {
			return fmt.Errorf("migrate: схема %q не подготовлена bootstrap provisioner-ом", owned)
		}
		if err := verifyMigrationAuthority(ctx, conn, owned, ownerRole); err != nil {
			return err
		}
	}

	schema := pgx.Identifier{owned.String()}.Sanitize()
	if ownerRole == "" {
		if _, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schema); err != nil {
			return fmt.Errorf("migrate: создание схемы %q: %w", owned, err)
		}
	}
	table := schema + "." + pgx.Identifier{migrationsTable}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+table+
		" (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return fmt.Errorf("migrate: таблица учёта версий схемы %q: %w", owned, err)
	}

	for _, migration := range migrations {
		done, err := applied(ctx, conn, table, migration.Name)
		if err != nil {
			return fmt.Errorf("migrate: состояние схемы %q: %w", owned, err)
		}
		if done {
			continue
		}
		if err := applyOne(ctx, conn, table, migration); err != nil {
			return fmt.Errorf("%s: миграция %s: %w", owned, migration.Name, err)
		}
	}
	return nil
}

type migrationSessionExec func(context.Context, string, ...any) error

func cleanupAndReleaseMigrationConn(ctx context.Context, exec migrationSessionExec, roleApplied, lockHeld bool, key int64, discardRequired bool, discard func(context.Context) error, release func()) error {
	if !discardRequired {
		if err := cleanupMigrationSession(ctx, exec, roleApplied, lockHeld, key); err != nil {
			discardRequired = true
			if discardErr := discard(ctx); discardErr != nil {
				return errors.Join(err, fmt.Errorf("migrate: discard загрязнённого соединения: %w", discardErr))
			}
			return err
		}
	}
	if discardRequired {
		if err := discard(ctx); err != nil {
			return fmt.Errorf("migrate: discard загрязнённого соединения: %w", err)
		}
		return nil
	}
	release()
	return nil
}

func cleanupMigrationSession(ctx context.Context, exec migrationSessionExec, roleApplied, lockHeld bool, key int64) error {
	if roleApplied {
		if err := exec(ctx, "RESET ROLE"); err != nil {
			return fmt.Errorf("migrate: RESET ROLE не подтверждён; соединение необходимо выбросить из пула: %w", err)
		}
	}
	if lockHeld {
		if err := exec(ctx, "SELECT pg_advisory_unlock($1)", key); err != nil {
			return fmt.Errorf("migrate: advisory unlock не подтверждён; соединение необходимо выбросить из пула: %w", err)
		}
	}
	return nil
}

// applyOne применяет один файл в отдельной транзакции: SQL миграции и запись её
// версии коммитятся вместе, поэтому ошибка не оставляет частичного эффекта, а
// повтор не выполнит файл заново.
func applyOne(ctx context.Context, conn *pgxpool.Conn, table string, migration Migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	if _, err := tx.Exec(ctx, migration.SQL); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO "+table+" (version) VALUES ($1) ON CONFLICT (version) DO NOTHING", migration.Name); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// applied сообщает, записана ли версия как применённая.
func applied(ctx context.Context, conn *pgxpool.Conn, table, version string) (bool, error) {
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM "+table+" WHERE version = $1)", version).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// advisoryKey выводит детерминированный ключ advisory-блокировки из имени схемы:
// процессы одной схемы сериализуются между собой, разные схемы блокируют друг
// друга независимо.
func advisoryKey(owned Schema) int64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte("migrate:" + owned.String()))
	return int64(hasher.Sum64())
}
