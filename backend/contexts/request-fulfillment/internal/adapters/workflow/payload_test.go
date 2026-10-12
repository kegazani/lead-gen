package workflowadapter

import (
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	platformworkflow "lead-gen/backend/platform/workflow"
)

// TestPayloadsCarryIdentifiersOnly проверяет правило payload-гигиены (change,
// задача 1.3/2.2): все payload-типы workflow и activities несут только
// идентификаторы, коды и корреляционные ссылки — без PII и секретов.
func TestPayloadsCarryIdentifiersOnly(t *testing.T) {
	payloads := map[string]any{
		"Params":                 Params{},
		"ExecutionPolicy":        ExecutionPolicy{},
		"ApprovalDecision":       ApprovalDecision{},
		"Status":                 Status{},
		"transitionInput":        transitionInput{},
		"searchInput":            searchInput{},
		"searchResult":           searchResult{},
		"draftInput":             draftInput{},
		"draftResult":            draftResult{},
		"handoffInput":           handoffInput{},
		"handoffOutcome":         handoffOutcome{},
		"SearchInput":            SearchInput{},
		"ports.StartFulfillment": ports.StartFulfillment{},
		"ports.WorkflowRun":      ports.WorkflowRun{},
		"ports.WorkflowStatus":   ports.WorkflowStatus{},
		"ports.WorkflowDecision": ports.WorkflowDecision{},
	}
	for name, payload := range payloads {
		if violations := platformworkflow.CheckIdentifiersOnly(payload); len(violations) != 0 {
			t.Errorf("payload %s нарушает правило гигиены: %+v", name, violations)
		}
	}
}
