// Адаптеры хранилища сервиса data-governance, публикуемые composition root'ом и
// собираемые внутри контекста по DSN.
//
// Публичная поверхность модуля — корневой пакет сборки сервиса: cmd/lead-gen не
// импортирует internal-пакеты напрямую, а получает PostgreSQL-адаптер либо через
// NewPostgresRepository, либо через сборку контекста по заданному DSN.
package datagovernance

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/data-governance/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/data-governance/internal/ports"
	"lead-gen/backend/platform/migrate"
)

// defaultMigrationsDir — каталог миграций сервиса относительно рабочего каталога
// процесса (запуск из корня репозитория), применяемых при сборке по DSN.
const defaultMigrationsDir = "backend/contexts/data-governance/migrations"

// NewPostgresRepository собирает PostgreSQL-адаптер хранилища контекста поверх
// пула зелёной зоны: репозиторий оснований обработки работает в схеме
// data-governance.
func NewPostgresRepository(pool *pgxpool.Pool) ports.ProcessingBasisRepository {
	return postgres.New(pool)
}

// buildPostgresRepository создаёт пул по DSN, применяет миграции сервиса и
// собирает PostgreSQL-репозиторий. Пул возвращается вызывающему для закрытия при
// остановке. Миграции читаются загрузчиком backend/platform/migrate, который до
// выполнения проверяет принадлежность схеме сервиса.
func buildPostgresRepository(dsn, migrationsDir string) (ports.ProcessingBasisRepository, *pgxpool.Pool, error) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("data-governance: пул PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("data-governance: подключение к PostgreSQL: %w", err)
	}
	if strings.TrimSpace(migrationsDir) == "" {
		migrationsDir = defaultMigrationsDir
	}
	migrations, err := migrate.Load(migrationsDir, migrate.OwnSchema(ServiceID))
	if err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("data-governance: загрузка миграций: %w", err)
	}
	for _, migration := range migrations {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			pool.Close()
			return nil, nil, fmt.Errorf("data-governance: применение миграции %s: %w", migration.Name, err)
		}
	}
	return NewPostgresRepository(pool), pool, nil
}
