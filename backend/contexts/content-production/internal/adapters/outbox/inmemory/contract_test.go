package inmemory

import (
	"testing"

	persistence "lead-gen/backend/contexts/content-production/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/content-production/internal/ports/contracttest"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// TestUnitOfWorkContract прогоняет общий contract suite порта UnitOfWork против
// in-memory-реализации: запись агрегатов и запись событий фиксируются вместе, а
// ошибка use case откатывает обе. Тот же suite проходит и против PostgreSQL.
func TestUnitOfWorkContract(t *testing.T) {
	contracttest.RunUnitOfWorkContract(t, func(t *testing.T) contracttest.UnitOfWorkHarness {
		t.Helper()
		store := persistence.New()
		journal := platformoutbox.NewMemoryJournal()
		return contracttest.UnitOfWorkHarness{
			UnitOfWork: NewUnitOfWorkWith(store, journal),
			Content:    store.Content(),
			RenderJobs: store.RenderJobs(),
			Journal:    journal,
		}
	})
}
