package agentorchestration

import (
	"context"
	"lead-gen/backend/contexts/agent-orchestration/internal/app/research"
	"lead-gen/contracts/agent-orchestration/researchmission"
	"lead-gen/contracts/lead-management/researchmemory"
)

// ResearchCriteriaSupport — текущая доступность оснований оценки данной миссии.
type ResearchCriteriaSupport = research.CriteriaSupport

// CheckResearchCriteriaSupport позволяет composition root проверить complete
// mission-owned assessments через опубликованный порт владельца доказательств.
func CheckResearchCriteriaSupport(ctx context.Context, store researchmemory.Port, scope researchmission.Scope, entityID string, criteriaVersion int, assessments []ResearchAssessment) (ResearchCriteriaSupport, error) {
	return research.CheckCriteriaSupport(ctx, store, scope, entityID, criteriaVersion, assessments)
}
