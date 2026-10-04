// PostgreSQL-адаптеры хранилища сервиса integration-platform, публикуемые
// composition root'ом и собираемые внутри контекста по DSN.
//
// Публичная поверхность модуля — корневой пакет сборки сервиса: cmd/lead-gen не
// импортирует internal-пакеты напрямую, а получает PostgreSQL-адаптеры либо через
// NewPostgresPersistence, либо через сборку контекста по заданному DSN.
package integrationplatform

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/integration-platform/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
	"lead-gen/backend/platform/migrate"
)

// defaultMigrationsDir — каталог миграций сервиса относительно рабочего каталога
// процесса (запуск из корня репозитория), применяемых при сборке по DSN.
const defaultMigrationsDir = "backend/contexts/integration-platform/migrations"

// Persistence — набор PostgreSQL-адаптеров хранилища контекста, впрыскиваемых в
// Config. Нулевое значение означает, что сборка берёт адаптеры по DSN: PostgreSQL
// при заданном DSN, иначе in-memory (dev).
type Persistence struct {
	// Registrations — репозиторий регистраций адаптеров.
	Registrations ports.AdapterRegistrationRepository
	// Invocations — репозиторий вызовов интеграций.
	Invocations ports.IntegrationInvocationRepository
}

// NewPostgresPersistence собирает PostgreSQL-адаптеры хранилища контекста поверх
// пула зелёной зоны: репозитории регистраций и вызовов работают в схеме
// integration-platform.
func NewPostgresPersistence(pool *pgxpool.Pool) Persistence {
	return Persistence{
		Registrations: postgres.NewAdapterRegistrationRepository(pool),
		Invocations:   postgres.NewIntegrationInvocationRepository(pool),
	}
}

// buildPostgresPersistence создаёт пул по DSN, применяет миграции сервиса и
// собирает PostgreSQL-адаптеры. Пул возвращается вызывающему для закрытия при
// остановке. Миграции читаются загрузчиком backend/platform/migrate, который до
// выполнения проверяет принадлежность схеме сервиса.
func buildPostgresPersistence(dsn, migrationsDir string) (Persistence, *pgxpool.Pool, error) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return Persistence{}, nil, fmt.Errorf("integration-platform: пул PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return Persistence{}, nil, fmt.Errorf("integration-platform: подключение к PostgreSQL: %w", err)
	}
	if strings.TrimSpace(migrationsDir) == "" {
		migrationsDir = defaultMigrationsDir
	}
	migrations, err := migrate.Load(migrationsDir, migrate.OwnSchema(ServiceID))
	if err != nil {
		pool.Close()
		return Persistence{}, nil, fmt.Errorf("integration-platform: загрузка миграций: %w", err)
	}
	for _, migration := range migrations {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			pool.Close()
			return Persistence{}, nil, fmt.Errorf("integration-platform: применение миграции %s: %w", migration.Name, err)
		}
	}
	return NewPostgresPersistence(pool), pool, nil
}
