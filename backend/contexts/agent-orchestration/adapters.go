// Адаптеры хранилища сервиса agent-orchestration, публикуемые composition root'ом.
//
// Публичная поверхность модуля — корневой пакет сборки сервиса: cmd/lead-gen не
// импортирует internal-пакеты напрямую, а получает адаптеры через эти конструкторы.
package agentorchestration

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// Persistence — набор адаптеров хранилища запусков и пайплайнов, впрыскиваемых в
// Config. Нулевое значение означает dev-режим: сборка берёт in-memory адаптеры.
type Persistence struct {
	// Pool — фактический PostgreSQL pool этого контекста для readiness probe.
	Pool *pgxpool.Pool
	// Pipelines — репозиторий версий пайплайнов (PostgreSQL).
	Pipelines ports.PipelineRepository
	// Runs — репозиторий запусков (PostgreSQL).
	Runs ports.RunRepository
	// UnitOfWork — транзакция: атомарная запись агрегата и outbox.
	UnitOfWork ports.UnitOfWork
	// Journal — журнал outbox: ожидающие публикации события переживают рестарт.
	Journal platformoutbox.Journal
}

// NewPostgresPersistence собирает PostgreSQL-адаптеры хранилища поверх пула зелёной
// зоны: репозитории пайплайнов и запусков, unit-of-work и журнал outbox работают в
// схеме agent-orchestration.
func NewPostgresPersistence(pool *pgxpool.Pool) Persistence {
	return Persistence{
		Pool:       pool,
		Pipelines:  postgres.NewPipelineRepository(pool),
		Runs:       postgres.NewRunRepository(pool),
		UnitOfWork: postgres.NewUnitOfWork(pool),
		Journal:    postgres.NewJournal(pool),
	}
}
