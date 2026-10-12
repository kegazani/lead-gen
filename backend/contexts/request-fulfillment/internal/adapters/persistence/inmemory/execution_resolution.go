package inmemory

import (
	"context"
	"sort"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

func (r *Repository) ReadOwnerState(ctx context.Context, tenantID domain.TenantID, requestID domain.RequestID) (ports.ExecutionOwnerState, error) {
	if err := r.gate.Acquire(ctx); err != nil {
		return ports.ExecutionOwnerState{}, err
	}
	defer r.gate.Release()
	snapshot, ok := r.requests[requestID]
	if !ok || snapshot.TenantID != tenantID {
		return ports.ExecutionOwnerState{}, ports.ErrRequestNotFound
	}
	state := ports.ExecutionOwnerState{TenantID: tenantID, RequestID: requestID, Target: domain.LeadTarget{State: domain.LeadTargetUnknownLegacy}, Runtime: ports.AttemptUnknown}
	state.ExecutionHistoryKnown = snapshot.ExecutionHistoryKnown
	if snapshot.ExecutionHistoryKnown {
		state.Target = domain.LeadTarget{State: domain.LeadTargetUnspecified}
	}
	var latest ports.ExecutionAttempt
	for _, attempt := range r.attempts[requestID] {
		if !state.HasAttempt || attempt.Intent.Generation() > latest.Intent.Generation() {
			latest, state.HasAttempt = attempt, true
		}
	}
	if state.HasAttempt {
		state.Attempt = func() *ports.ExecutionAttempt { copy := copyAttempt(latest); return &copy }()
		state.Target = latest.Intent.Parameters().LeadTarget
		// AttemptTerminal is merely the mutable execution row state. Until an
		// authenticated verified-resolution record exists, it is not terminal
		// evidence and must not unblock admission.
		if latest.State != ports.AttemptTerminal {
			state.Runtime = latest.State
		}
		var currentVerified *domain.VerifiedRuntimeResolution
		for _, verified := range r.verifiedRuntime {
			resolution := verified.Resolution()
			if resolution.TenantID == tenantID && resolution.RequestID == requestID &&
				(currentVerified == nil || resolution.Revision > currentVerified.Resolution().Revision) {
				copy := verified
				currentVerified = &copy
			}
		}
		if currentVerified != nil {
			resolution := currentVerified.Resolution()
			if resolution.Generation == latest.Intent.Generation() && resolution.AttemptID == latest.AttemptID &&
				resolution.ObservedRunID == latest.RunID && currentVerified.Evidence().WorkflowID == "fulfillment-"+string(requestID) {
				state.Runtime = ports.AttemptTerminal
			}
		}
	}
	var latestTarget *domain.ExecutionResolution
	for _, resolution := range r.resolutions {
		if resolution.TenantID != tenantID || resolution.RequestID != requestID {
			continue
		}
		if resolution.Kind == domain.ResolutionTarget && (latestTarget == nil || resolution.Revision > latestTarget.Revision) {
			copy := resolution
			latestTarget = &copy
		}
	}
	if !state.HasAttempt && latestTarget != nil {
		state.Target = latestTarget.Target
	}
	return state, nil
}

func (r *Repository) AppendVerifiedRuntimeResolution(ctx context.Context, verified domain.VerifiedRuntimeResolution) error {
	resolution := verified.Resolution()
	if resolution.Actor != "temporal-history-verifier" {
		return ports.ErrExecutionFenceConflict
	}
	if err := r.gate.Acquire(ctx); err != nil {
		return err
	}
	defer r.gate.Release()
	snapshot, ok := r.requests[resolution.RequestID]
	if !ok || snapshot.TenantID != resolution.TenantID {
		return ports.ErrRequestNotFound
	}
	attempt, ok := r.attempts[resolution.RequestID][resolution.Generation]
	if !ok || attempt.Intent.TenantID() != resolution.TenantID || attempt.AttemptID != resolution.AttemptID ||
		attempt.RunID != resolution.ObservedRunID || string(attempt.Intent.CommandID()) != verified.Evidence().CommandID {
		return ports.ErrExecutionFenceConflict
	}
	if existing, ok := r.verifiedRuntime[resolution.ResolutionID]; ok {
		if sameVerifiedRuntimeResolution(existing, verified) {
			return nil
		}
		return ports.ErrExecutionResolutionConflict
	}
	var current *domain.ExecutionResolution
	for _, stored := range r.verifiedRuntime {
		item := stored.Resolution()
		if item.TenantID == resolution.TenantID && item.RequestID == resolution.RequestID &&
			(current == nil || item.Revision > current.Revision) {
			copy := item
			current = &copy
		}
	}
	currentRevision, currentID := int64(0), ""
	if current != nil {
		currentRevision, currentID = current.Revision, current.ResolutionID
	}
	if resolution.ExpectedRevision != currentRevision || resolution.SupersedesID != currentID {
		return ports.ErrExecutionResolutionConflict
	}
	verified = verified.WithRevision(currentRevision+1, currentID)
	r.verifiedRuntime[resolution.ResolutionID] = verified
	return nil
}

func sameVerifiedRuntimeResolution(a, b domain.VerifiedRuntimeResolution) bool {
	stored, candidate := a.Resolution(), b.Resolution()
	candidate.Revision = stored.Revision
	candidate.SupersedesID = stored.SupersedesID
	return stored == candidate && a.Evidence() == b.Evidence()
}

func (r *Repository) AppendResolution(ctx context.Context, resolution domain.ExecutionResolution) error {
	resolution.ResolvedAt = resolution.ResolvedAt.UTC().Truncate(time.Microsecond)
	if err := resolution.Validate(); err != nil {
		return err
	}
	if err := r.gate.Acquire(ctx); err != nil {
		return err
	}
	defer r.gate.Release()
	if existing, ok := r.resolutions[resolution.ResolutionID]; ok {
		if sameResolution(existing, resolution) {
			return nil
		}
		return ports.ErrExecutionResolutionConflict
	}
	snapshot, ok := r.requests[resolution.RequestID]
	if !ok || snapshot.TenantID != resolution.TenantID {
		return ports.ErrRequestNotFound
	}
	if resolution.Kind == domain.ResolutionTarget && len(r.attempts[resolution.RequestID]) != 0 {
		return ports.ErrExecutionFenceConflict
	}
	if resolution.Kind == domain.ResolutionRuntimeObservation {
		attempt, ok := r.attempts[resolution.RequestID][resolution.Generation]
		if !ok || attempt.Intent.TenantID() != resolution.TenantID || attempt.AttemptID != resolution.AttemptID || attempt.RunID != resolution.ObservedRunID {
			return ports.ErrExecutionFenceConflict
		}
	}
	var current *domain.ExecutionResolution
	for _, stored := range r.resolutions {
		if stored.TenantID == resolution.TenantID && stored.RequestID == resolution.RequestID && stored.Kind == resolution.Kind &&
			(current == nil || stored.Revision > current.Revision) {
			copy := stored
			current = &copy
		}
	}
	currentRevision := int64(0)
	currentID := ""
	if current != nil {
		currentRevision, currentID = current.Revision, current.ResolutionID
	}
	if resolution.ExpectedRevision != currentRevision || resolution.SupersedesID != currentID {
		return ports.ErrExecutionResolutionConflict
	}
	resolution.Revision = currentRevision + 1
	r.resolutions[resolution.ResolutionID] = resolution
	return nil
}

func (r *Repository) ListResolutions(ctx context.Context, tenantID domain.TenantID, requestID domain.RequestID) ([]domain.ExecutionResolution, error) {
	if err := r.gate.Acquire(ctx); err != nil {
		return nil, err
	}
	defer r.gate.Release()
	snapshot, ok := r.requests[requestID]
	if !ok || snapshot.TenantID != tenantID {
		return nil, ports.ErrRequestNotFound
	}
	result := make([]domain.ExecutionResolution, 0)
	for _, resolution := range r.resolutions {
		if resolution.TenantID == tenantID && resolution.RequestID == requestID {
			result = append(result, resolution)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Revision < result[j].Revision
	})
	return result, nil
}

func sameResolution(a, b domain.ExecutionResolution) bool {
	return a.ResolutionID == b.ResolutionID && a.ExpectedRevision == b.ExpectedRevision && a.SupersedesID == b.SupersedesID && a.TenantID == b.TenantID && a.RequestID == b.RequestID &&
		a.Kind == b.Kind && a.Generation == b.Generation && a.AttemptID == b.AttemptID && a.Target == b.Target &&
		a.ObservedOutcome == b.ObservedOutcome && a.ObservedRunID == b.ObservedRunID && a.Actor == b.Actor &&
		a.ResolvedAt.Equal(b.ResolvedAt) && a.EvidenceFingerprint == b.EvidenceFingerprint
}
