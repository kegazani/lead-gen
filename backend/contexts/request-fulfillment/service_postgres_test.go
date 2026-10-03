package requestfulfillment

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// stubRequestRepository — минимальная реализация порта репозитория заявок,
// отличимая от in-memory только по типу: проверяет выбор адаптера при сборке.
type stubRequestRepository struct{}

func (stubRequestRepository) Save(context.Context, *domain.Request, int) error { return nil }

func (stubRequestRepository) FindByID(context.Context, domain.RequestID) (*domain.Request, error) {
	return nil, ports.ErrRequestNotFound
}

func (stubRequestRepository) ListByStatus(context.Context, domain.RequestStatus) ([]*domain.Request, error) {
	return nil, nil
}

// TestBuildRequiresUnitOfWorkForPersistentRepository проверяет, что сборка не
// подменяет персистентный репозиторий in-memory unit-of-work: без явного
// unit-of-work такая комбинация отвергается, а не даёт молча неверную проводку.
func TestBuildRequiresUnitOfWorkForPersistentRepository(t *testing.T) {
	if _, err := Build(Config{Repository: stubRequestRepository{}}); err == nil {
		t.Fatal("ожидалась ошибка: персистентный репозиторий без unit-of-work")
	}
}
