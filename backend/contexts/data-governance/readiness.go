package datagovernance

import (
	"context"
	"encoding/json"
	"net/http"

	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
)

// Readiness оценивает только зависимости, подключённые к указанной роли.
func (s *Service) Readiness(ctx context.Context, role composition.Role) readiness.Snapshot {
	repository := readiness.Probe(nil)
	if check, ok := s.slice.Repository.(interface{ Readiness(context.Context) error }); ok {
		repository = func(ctx context.Context) (readiness.ProbeResult, error) {
			if err := check.Readiness(ctx); err != nil {
				return readiness.ProbeResult{Status: readiness.StatusDown, Reason: readiness.ReasonProbeFailed}, nil
			}
			return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
		}
	}
	switch role {
	case composition.RoleAPI:
		return s.readiness.Evaluate(ctx, readiness.Profile{Capability: "data-governance-api", Checks: []readiness.CheckSpec{
			{ID: "processing_basis_repository", Key: "data-governance.repository", Required: true, Probe: repository},
			// Access is a mandatory API dependency. Its contract accepts subject
			// decisions, not health requests, so it is never probed with fake actors.
			{ID: "access_decision", Key: "data-governance.access", Required: true},
		}})
	case composition.RoleWorker:
		return s.readiness.Evaluate(ctx, readiness.Profile{Capability: "data-governance-expiry-worker", Checks: []readiness.CheckSpec{
			{ID: "processing_basis_repository", Key: "data-governance.repository", Required: true, Probe: repository},
		}})
	case composition.RoleChains:
		return readiness.Snapshot{Capability: "data-governance-chains", Status: readiness.StatusUnknown}
	default:
		return readiness.Snapshot{Capability: "data-governance-role", Status: readiness.StatusUnknown}
	}
}

func writeReadiness(w http.ResponseWriter, snapshot readiness.Snapshot) {
	status := http.StatusOK
	if !snapshot.Ready {
		status = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(snapshot)
}
