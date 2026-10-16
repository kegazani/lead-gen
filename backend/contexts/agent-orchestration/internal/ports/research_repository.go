package ports

import (
	"context"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	rm "lead-gen/contracts/agent-orchestration/researchmission"
)

// ResearchRepository.Update выполняет только локальную короткую транзакцию.
// Callback не имеет права вызывать сеть или чужой bounded context.
type ResearchRepository interface {
	CheckHealth(context.Context) error
	Create(context.Context, *domain.ResearchMission) (*domain.ResearchMission, error)
	Get(context.Context, rm.Scope, string) (*domain.ResearchMission, error)
	FindByIntent(context.Context, rm.Scope, string) ([]*domain.ResearchMission, error)
	Update(context.Context, rm.Scope, string, func(*domain.ResearchMission) error) (*domain.ResearchMission, error)
	Ready(context.Context, int) ([]ResearchRef, error)
}
type ResearchRef struct {
	Scope rm.Scope
	ID    string
}
