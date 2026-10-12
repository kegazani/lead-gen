package inmemory_test

import (
	"testing"

	outboxinmemory "lead-gen/backend/contexts/agent-orchestration/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports/contracttest"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// TestRepositoryContracts прогоняет общие contract suite портов против in-memory
// реализации: тот же набор проверок применяется к PostgreSQL-адаптеру.
func TestRepositoryContracts(t *testing.T) {
	t.Run("запуски", func(t *testing.T) {
		contracttest.RunRunRepositoryContract(t, func(t *testing.T) ports.RunRepository {
			return inmemory.New().Runs
		})
	})
	t.Run("пайплайны", func(t *testing.T) {
		contracttest.RunPipelineRepositoryContract(t, func(t *testing.T) ports.PipelineRepository {
			return inmemory.New().Pipelines
		})
	})
	t.Run("транзакция", func(t *testing.T) {
		contracttest.RunUnitOfWorkContract(t, func(t *testing.T) contracttest.UnitOfWorkHarness {
			repos := inmemory.New()
			journal := platformoutbox.NewMemoryJournal()
			uow := outboxinmemory.NewUnitOfWork(repos.Store(), journal)
			return contracttest.UnitOfWorkHarness{UnitOfWork: uow, Runs: repos.Runs, Journal: journal}
		})
	})
}
