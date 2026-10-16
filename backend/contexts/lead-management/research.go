package leadmanagement

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/postgres"
	researchapp "lead-gen/backend/contexts/lead-management/internal/app/researchmemory"
	contract "lead-gen/contracts/lead-management/researchmemory"
)

// NewPostgresResearchMemory собирает память поверх пула красной зоны.
// Миграции lead-management/red применяются до подключения нового пути.
func NewPostgresResearchMemory(redPool *pgxpool.Pool) contract.Port {
	return researchapp.New(postgres.NewResearchMemoryRepository(redPool))
}

// NewPostgresResearchIdentity открывает человеческие команды тождества и чтение
// текущей проекции; право на команду проверяет вызывающий boundary.
func NewPostgresResearchIdentity(redPool *pgxpool.Pool) contract.IdentityPort {
	return researchapp.New(postgres.NewResearchMemoryRepository(redPool))
}
