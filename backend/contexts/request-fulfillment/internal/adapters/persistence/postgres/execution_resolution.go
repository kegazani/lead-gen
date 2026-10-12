package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

type ExecutionResolutionRepository struct{ db execer }

var _ ports.ExecutionResolutionRepository = (*ExecutionResolutionRepository)(nil)
var _ ports.VerifiedRuntimeResolutionWriter = (*ExecutionResolutionRepository)(nil)

func NewExecutionResolutionRepository(db execer) *ExecutionResolutionRepository {
	if db == nil {
		panic("postgres: NewExecutionResolutionRepository с nil db")
	}
	return &ExecutionResolutionRepository{db: db}
}

func (r *ExecutionResolutionRepository) AppendResolution(ctx context.Context, resolution domain.ExecutionResolution) error {
	resolution.ResolvedAt = resolution.ResolvedAt.UTC().Truncate(time.Microsecond)
	if err := resolution.Validate(); err != nil {
		return err
	}
	var generation, target any
	var attemptID, targetState, runtimeOutcome, runID any
	if resolution.Kind == domain.ResolutionTarget {
		targetState, target = string(resolution.Target.State), resolution.Target.Value
	} else {
		generation, attemptID, runtimeOutcome, runID = resolution.Generation, resolution.AttemptID, string(resolution.ObservedOutcome), resolution.ObservedRunID
	}
	tag, err := r.db.Exec(ctx, `INSERT INTO "request-fulfillment".execution_resolution_revision
(resolution_id,tenant_id,request_id,resolution_kind,revision,expected_revision,supersedes_id,generation,attempt_id,target_state,lead_target,runtime_outcome,observed_run_id,actor,resolved_at,evidence_fingerprint)
VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT(resolution_id) DO NOTHING`,
		resolution.ResolutionID, resolution.TenantID.String(), resolution.RequestID.String(), string(resolution.Kind), resolution.ExpectedRevision+1,
		resolution.ExpectedRevision, resolution.SupersedesID, generation, attemptID, targetState, target, runtimeOutcome, runID, resolution.Actor,
		resolution.ResolvedAt.UTC(), resolution.EvidenceFingerprint)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "40001" || pgErr.ConstraintName == "execution_resolution_revision_check" {
				return r.resolveInsertConflict(ctx, resolution)
			}
			if pgErr.Code == "23505" {
				return r.resolveInsertConflict(ctx, resolution)
			}
			if pgErr.Code == "23503" || pgErr.Code == "23514" {
				return ports.ErrExecutionFenceConflict
			}
		}
		return fmt.Errorf("postgres: добавить execution resolution: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	stored, err := r.readByID(ctx, resolution.ResolutionID)
	if err != nil {
		return err
	}
	if sameResolution(stored, resolution) {
		return nil
	}
	return ports.ErrExecutionResolutionConflict
}

// AppendVerifiedRuntimeResolution is a separate trusted-application capability.
// It is normally obtained only from the transaction passed to terminal
// reconciliation; HTTP dependencies expose only ExecutionResolutionRepository.
func (r *ExecutionResolutionRepository) AppendVerifiedRuntimeResolution(ctx context.Context, verified domain.VerifiedRuntimeResolution) error {
	resolution := verified.Resolution()
	evidence := verified.Evidence()
	if resolution.Actor != "temporal-history-verifier" || resolution.Revision != 0 || resolution.Kind != domain.ResolutionRuntimeObservation {
		return ports.ErrExecutionFenceConflict
	}
	if err := resolution.Validate(); err != nil {
		return err
	}
	var commandID string
	err := r.db.QueryRow(ctx, `SELECT command_id FROM "request-fulfillment".execution_attempt
WHERE tenant_id=$1 AND request_id=$2 AND generation=$3 AND attempt_id=$4 AND run_id=$5`,
		resolution.TenantID.String(), resolution.RequestID.String(), resolution.Generation, resolution.AttemptID, resolution.ObservedRunID).Scan(&commandID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && commandID != evidence.CommandID) {
		return ports.ErrExecutionFenceConflict
	}
	if err != nil {
		return fmt.Errorf("postgres: проверить verified runtime attempt: %w", err)
	}
	intent, err := json.Marshal(evidence)
	if err != nil {
		return fmt.Errorf("postgres: encode terminal intent evidence: %w", err)
	}
	if exists, err := r.sameVerifiedRuntimeRow(ctx, resolution, evidence); err != nil {
		return err
	} else if exists {
		return nil
	}
	tag, err := r.db.Exec(ctx, `INSERT INTO "request-fulfillment".execution_verified_runtime_resolution
(resolution_id,tenant_id,request_id,generation,attempt_id,command_id,workflow_id,run_id,runtime_outcome,
 reason_code,original_reason,terminal_intent,evidence_fingerprint,revision,expected_revision,supersedes_id,actor,resolved_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12::jsonb,$13,$14,$15,NULLIF($16,''),$17,$18)
ON CONFLICT(resolution_id) DO NOTHING`, resolution.ResolutionID, resolution.TenantID.String(), resolution.RequestID.String(),
		resolution.Generation, resolution.AttemptID, evidence.CommandID, evidence.WorkflowID, evidence.RunID,
		string(resolution.ObservedOutcome), evidence.ReasonCode, evidence.OriginalReason, string(intent),
		resolution.EvidenceFingerprint, resolution.ExpectedRevision+1, resolution.ExpectedRevision,
		resolution.SupersedesID, resolution.Actor, resolution.ResolvedAt.UTC().Truncate(time.Microsecond))
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			if pgErr.Code == "40001" || pgErr.ConstraintName == "execution_verified_runtime_revision_cas" {
				if exists, readErr := r.sameVerifiedRuntimeRow(ctx, resolution, evidence); readErr == nil && exists {
					return nil
				}
				return ports.ErrExecutionResolutionConflict
			}
			if pgErr.Code == "23503" || pgErr.Code == "23514" {
				return ports.ErrExecutionFenceConflict
			}
			if pgErr.Code == "23505" {
				return ports.ErrExecutionResolutionConflict
			}
		}
		return fmt.Errorf("postgres: добавить verified runtime resolution: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	exists, err := r.sameVerifiedRuntimeRow(ctx, resolution, evidence)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return ports.ErrExecutionResolutionConflict
}

func (r *ExecutionResolutionRepository) sameVerifiedRuntimeRow(ctx context.Context, resolution domain.ExecutionResolution, evidence domain.TerminalIntentEvidence) (bool, error) {
	var tenant, request, attempt, command, workflow, run, outcome, reasonCode, reason, fingerprint, actor string
	var storedEvidence []byte
	var generation, revision, expected int64
	var supersedes string
	var resolvedAt time.Time
	err := r.db.QueryRow(ctx, `SELECT tenant_id,request_id,generation,attempt_id,command_id,workflow_id,run_id,runtime_outcome,
reason_code,original_reason,terminal_intent,evidence_fingerprint,revision,expected_revision,COALESCE(supersedes_id,''),actor,resolved_at
FROM "request-fulfillment".execution_verified_runtime_resolution WHERE resolution_id=$1`, resolution.ResolutionID).
		Scan(&tenant, &request, &generation, &attempt, &command, &workflow, &run, &outcome, &reasonCode, &reason,
			&storedEvidence, &fingerprint, &revision, &expected, &supersedes, &actor, &resolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("postgres: проверить повтор verified resolution: %w", err)
	}
	var stored domain.TerminalIntentEvidence
	if err := json.Unmarshal(storedEvidence, &stored); err != nil {
		return false, fmt.Errorf("postgres: разобрать повтор verified resolution: %w", err)
	}
	storedCanonical, err := json.Marshal(stored)
	if err != nil {
		return false, fmt.Errorf("postgres: канонизировать terminal payload: %w", err)
	}
	return tenant == resolution.TenantID.String() && request == resolution.RequestID.String() && generation == resolution.Generation &&
		attempt == resolution.AttemptID && command == evidence.CommandID && workflow == evidence.WorkflowID && run == resolution.ObservedRunID &&
		outcome == string(resolution.ObservedOutcome) && reasonCode == evidence.ReasonCode && reason == evidence.OriginalReason &&
		json.Valid(storedEvidence) && domain.FingerprintSafeEvidence(storedCanonical) == fingerprint && fingerprint == resolution.EvidenceFingerprint &&
		revision == resolution.ExpectedRevision+1 && expected == resolution.ExpectedRevision && supersedes == resolution.SupersedesID &&
		actor == resolution.Actor && resolvedAt.UTC().Truncate(time.Microsecond).Equal(resolution.ResolvedAt.UTC().Truncate(time.Microsecond)) && stored == evidence, nil
}

func (r *ExecutionResolutionRepository) resolveInsertConflict(ctx context.Context, resolution domain.ExecutionResolution) error {
	stored, err := r.readByID(ctx, resolution.ResolutionID)
	if err == nil && sameResolution(stored, resolution) {
		return nil
	}
	return ports.ErrExecutionResolutionConflict
}

func (r *ExecutionResolutionRepository) ListResolutions(ctx context.Context, tenantID domain.TenantID, requestID domain.RequestID) ([]domain.ExecutionResolution, error) {
	var exists bool
	if err := r.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "request-fulfillment".request WHERE tenant_id=$1 AND id=$2)`, tenantID.String(), requestID.String()).Scan(&exists); err != nil {
		return nil, fmt.Errorf("postgres: проверить scope заявки для resolutions: %w", err)
	}
	if !exists {
		return nil, ports.ErrRequestNotFound
	}
	result := make([]domain.ExecutionResolution, 0)
	rows, err := r.db.Query(ctx, `SELECT resolution_id,tenant_id,request_id,resolution_kind,generation,attempt_id,target_state,lead_target,runtime_outcome,observed_run_id,actor,resolved_at,evidence_fingerprint,revision,expected_revision,COALESCE(supersedes_id,'')
FROM "request-fulfillment".execution_resolution_revision WHERE tenant_id=$1 AND request_id=$2
ORDER BY resolution_kind,revision`, tenantID.String(), requestID.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: прочитать revisions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var resolution domain.ExecutionResolution
		var tenant, request, kind string
		var generation *int64
		var attempt, targetState, runtimeOutcome, runID *string
		var target *int
		if err := rows.Scan(&resolution.ResolutionID, &tenant, &request, &kind, &generation, &attempt, &targetState, &target, &runtimeOutcome, &runID, &resolution.Actor, &resolution.ResolvedAt, &resolution.EvidenceFingerprint, &resolution.Revision, &resolution.ExpectedRevision, &resolution.SupersedesID); err != nil {
			return nil, fmt.Errorf("postgres: разобрать resolution revision: %w", err)
		}
		resolution.TenantID, resolution.RequestID, resolution.Kind = domain.TenantID(tenant), domain.RequestID(request), domain.ExecutionResolutionKind(kind)
		if generation != nil {
			resolution.Generation = *generation
		}
		if attempt != nil {
			resolution.AttemptID = *attempt
		}
		if targetState != nil {
			resolution.Target.State = domain.LeadTargetState(*targetState)
		}
		if target != nil {
			resolution.Target.Value = *target
		}
		if runtimeOutcome != nil {
			resolution.ObservedOutcome = domain.RuntimeResolutionOutcome(*runtimeOutcome)
		}
		if runID != nil {
			resolution.ObservedRunID = *runID
		}
		result = append(result, resolution)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: пройти revisions: %w", err)
	}
	return result, nil
}

func (r *ExecutionResolutionRepository) readByID(ctx context.Context, id string) (domain.ExecutionResolution, error) {
	var resolution domain.ExecutionResolution
	var tenant, request, kind string
	var generation *int64
	var attempt, targetState, runtimeOutcome, runID *string
	var target *int
	err := r.db.QueryRow(ctx, `SELECT resolution_id,tenant_id,request_id,resolution_kind,generation,attempt_id,target_state,lead_target,runtime_outcome,observed_run_id,actor,resolved_at,evidence_fingerprint,revision,expected_revision,COALESCE(supersedes_id,'') FROM "request-fulfillment".execution_resolution_revision WHERE resolution_id=$1`, id).Scan(&resolution.ResolutionID, &tenant, &request, &kind, &generation, &attempt, &targetState, &target, &runtimeOutcome, &runID, &resolution.Actor, &resolution.ResolvedAt, &resolution.EvidenceFingerprint, &resolution.Revision, &resolution.ExpectedRevision, &resolution.SupersedesID)
	if err != nil {
		return resolution, fmt.Errorf("postgres: прочитать resolution revision: %w", err)
	}
	resolution.TenantID, resolution.RequestID, resolution.Kind = domain.TenantID(tenant), domain.RequestID(request), domain.ExecutionResolutionKind(kind)
	if generation != nil {
		resolution.Generation = *generation
	}
	if attempt != nil {
		resolution.AttemptID = *attempt
	}
	if targetState != nil {
		resolution.Target.State = domain.LeadTargetState(*targetState)
	}
	if target != nil {
		resolution.Target.Value = *target
	}
	if runtimeOutcome != nil {
		resolution.ObservedOutcome = domain.RuntimeResolutionOutcome(*runtimeOutcome)
	}
	if runID != nil {
		resolution.ObservedRunID = *runID
	}
	return resolution, nil
}

type resolutionScanner interface{ Scan(...any) error }

func scanResolution(row resolutionScanner) (domain.ExecutionResolution, error) {
	var r domain.ExecutionResolution
	var tenant, request, kind string
	var generation *int64
	var attempt, targetState, runtimeOutcome, runID *string
	var target *int
	var resolvedAt time.Time
	err := row.Scan(&r.ResolutionID, &tenant, &request, &kind, &generation, &attempt, &targetState, &target,
		&runtimeOutcome, &runID, &r.Actor, &resolvedAt, &r.EvidenceFingerprint)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return r, ports.ErrExecutionAttemptNotFound
		}
		return r, fmt.Errorf("postgres: разобрать execution resolution: %w", err)
	}
	r.TenantID, r.RequestID, r.Kind, r.ResolvedAt = domain.TenantID(tenant), domain.RequestID(request), domain.ExecutionResolutionKind(kind), resolvedAt
	if generation != nil {
		r.Generation = *generation
	}
	if attempt != nil {
		r.AttemptID = *attempt
	}
	if targetState != nil {
		r.Target.State = domain.LeadTargetState(*targetState)
	}
	if target != nil {
		r.Target.Value = *target
	}
	if runtimeOutcome != nil {
		r.ObservedOutcome = domain.RuntimeResolutionOutcome(*runtimeOutcome)
	}
	if runID != nil {
		r.ObservedRunID = *runID
	}
	return r, nil
}

func sameResolution(a, b domain.ExecutionResolution) bool {
	return a.ResolutionID == b.ResolutionID && a.ExpectedRevision == b.ExpectedRevision && a.SupersedesID == b.SupersedesID && a.TenantID == b.TenantID && a.RequestID == b.RequestID && a.Kind == b.Kind &&
		a.Generation == b.Generation && a.AttemptID == b.AttemptID && a.Target == b.Target && a.ObservedOutcome == b.ObservedOutcome &&
		a.ObservedRunID == b.ObservedRunID && a.Actor == b.Actor && a.ResolvedAt.Equal(b.ResolvedAt) && a.EvidenceFingerprint == b.EvidenceFingerprint
}

func executionAttemptID(requestID domain.RequestID, generation int64) string {
	return fmt.Sprintf("%s#%d", requestID, generation)
}
