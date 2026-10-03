package inmemory

import (
	"testing"

	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports/contracttest"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// TestUnitOfWorkContract прогоняет общий contract suite порта UnitOfWork против
// in-memory-реализации. Тот же suite проходит и против PostgreSQL-реализации:
// запись агрегата и события атомарны в обеих.
func TestUnitOfWorkContract(t *testing.T) {
	contracttest.RunUnitOfWorkContract(t, func(t *testing.T) contracttest.UnitOfWorkHarness {
		t.Helper()
		repo := persistence.New()
		journal := platformoutbox.NewMemoryJournal()
		return contracttest.UnitOfWorkHarness{
			UnitOfWork: NewUnitOfWorkWith(repo, journal),
			Repository: repo,
			Journal:    journal,
		}
	})
}
