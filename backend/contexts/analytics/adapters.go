// Адаптеры хранилища сервиса analytics, публикуемые composition root'ом.
//
// Публичная поверхность модуля — корневой пакет сборки сервиса: cmd/lead-gen не
// импортирует internal-пакеты напрямую, а получает адаптеры через эти
// конструкторы, как lead-management публикует NewPostgresRepositories.
package analytics

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/analytics/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/analytics/internal/ports"
)

// Persistence — набор адаптеров хранилища сервиса analytics, впрыскиваемых в
// Config. Нулевое значение означает dev-режим: сборка берёт in-memory адаптер.
type Persistence struct {
	// Projection — хранилище эксплуатационной проекции дашборда (PostgreSQL).
	Projection ports.ProjectionStore
}

// NewPostgresPersistence собирает PostgreSQL-адаптеры хранилища аналитики поверх
// пула зелёной зоны: проекция дашборда хранится в схеме analytics.
func NewPostgresPersistence(pool *pgxpool.Pool) Persistence {
	return Persistence{
		Projection: postgres.NewProjectionStore(pool),
	}
}
