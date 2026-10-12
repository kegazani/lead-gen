package migrate

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Verify проверяет read-only, что в БД применены все версии из каталога миграций
// и нет неизвестных runtime-образу версий. Функция не создаёт схему/таблицы и не
// вызывает Apply; её можно безопасно выполнять runtime credential.
func Verify(ctx context.Context, pool *pgxpool.Pool, dir string, owned Schema) error {
	if pool == nil {
		return errors.New("migrate: verify: пул соединений не задан")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	migrations, err := Load(dir, owned)
	if err != nil {
		return err
	}
	table := pgx.Identifier{owned.String(), migrationsTable}.Sanitize()
	rows, err := pool.Query(ctx, "SELECT version FROM "+table+" ORDER BY version")
	if err != nil {
		return fmt.Errorf("migrate: verify: схема %q не инициализирована или runtime не имеет SELECT schema_version: %w", owned, err)
	}
	defer rows.Close()
	var applied []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return fmt.Errorf("migrate: verify: схема %q: чтение версии: %w", owned, err)
		}
		applied = append(applied, version)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("migrate: verify: схема %q: перечисление версий: %w", owned, err)
	}
	if len(applied) != len(migrations) {
		return fmt.Errorf("migrate: verify: схема %q имеет %d применённых версий; бинарный файл ожидает %d", owned, len(applied), len(migrations))
	}
	for i := range migrations {
		if applied[i] != migrations[i].Name {
			return fmt.Errorf("migrate: verify: схема %q: версия %q отсутствует или неизвестна (получена %q)", owned, migrations[i].Name, applied[i])
		}
	}
	return nil
}
