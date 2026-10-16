package agentorchestration

import "lead-gen/backend/contexts/agent-orchestration/internal/app/researchplanner"

// ResearchPlanner компилирует red brief в поисковый портфель без одобрения заявки.
type ResearchPlanner = researchplanner.Planner
type ResearchPlannerConfig = researchplanner.Config
type ResearchPlannerLimits = researchplanner.Limits

func NewResearchPlanner(cfg ResearchPlannerConfig) (*ResearchPlanner, error) {
	return researchplanner.New(cfg)
}
