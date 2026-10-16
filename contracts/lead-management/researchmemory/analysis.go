package researchmemory

import "context"

// AnalysisCriterion — критерий данной миссии; знания компании не получают
// постоянный глобальный вердикт о пригодности.
type AnalysisCriterion struct{ ID, Text, Kind string }

type AnalysisRequest struct {
	Scope                               Scope
	MissionID, ActionID, QueryAttemptID string
	Source                              Source
	CriteriaVersion                     int
	ICP, Country                        string
	Criteria                            []AnalysisCriterion
}

type AnalyzedObservation struct {
	Observation  SaveObservationRequest
	Accepted     bool
	Reason       string
	EvidenceRefs []string
}

type AnalysisResult struct {
	Observations           []AnalyzedObservation
	Diagnostics            []string
	ModelID, MethodVersion string
}

type Analyzer interface {
	Analyze(context.Context, AnalysisRequest) (AnalysisResult, error)
}
