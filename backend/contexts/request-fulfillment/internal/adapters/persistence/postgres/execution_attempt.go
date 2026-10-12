package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// ExecutionAttemptRepository — PostgreSQL-хранилище owner intent и fenced lease.
// Для записи intent экземпляр должен быть получен из активной UoW-транзакции.
type ExecutionAttemptRepository struct{ db execer }

var _ ports.ExecutionAttemptRepository = (*ExecutionAttemptRepository)(nil)

func NewExecutionAttemptRepository(pool execer) *ExecutionAttemptRepository {
	if pool == nil {
		panic("postgres: NewExecutionAttemptRepository с nil db")
	}
	return &ExecutionAttemptRepository{db: pool}
}

func (r *ExecutionAttemptRepository) ReadOwnerState(ctx context.Context, tenantID domain.TenantID, requestID domain.RequestID) (ports.ExecutionOwnerState, error) {
	var raw []byte
	err := r.db.QueryRow(ctx, `SELECT snapshot FROM "request-fulfillment".request WHERE tenant_id=$1 AND id=$2`, tenantID.String(), requestID.String()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ExecutionOwnerState{}, ports.ErrRequestNotFound
	}
	if err != nil {
		return ports.ExecutionOwnerState{}, fmt.Errorf("postgres: проверить request owner scope: %w", err)
	}
	var snapshot domain.RequestSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return ports.ExecutionOwnerState{}, fmt.Errorf("postgres: decode request execution marker: %w", err)
	}
	state := ports.ExecutionOwnerState{TenantID: tenantID, RequestID: requestID, Target: domain.LeadTarget{State: domain.LeadTargetUnknownLegacy}, Runtime: ports.AttemptUnknown}
	state.ExecutionHistoryKnown = snapshot.ExecutionHistoryKnown
	if snapshot.ExecutionHistoryKnown {
		state.Target = domain.LeadTarget{State: domain.LeadTargetUnspecified}
	}
	var commandID string
	err = r.db.QueryRow(ctx, `SELECT command_id FROM "request-fulfillment".execution_attempt WHERE tenant_id=$1 AND request_id=$2 ORDER BY generation DESC LIMIT 1`, tenantID.String(), requestID.String()).Scan(&commandID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return state, fmt.Errorf("postgres: прочитать текущую попытку owner state: %w", err)
	}
	if err == nil {
		attempt, findErr := r.FindByCommand(ctx, tenantID, domain.CommandID(commandID))
		if findErr != nil {
			return state, findErr
		}
		state.HasAttempt = true
		state.Target = attempt.Intent.Parameters().LeadTarget
		state.Attempt = &attempt
		// A mutable attempt row can be marked terminal by an older caller or a
		// partial workflow compensation. Only the dedicated verified-resolution
		// capability may turn that observation into owner terminal state.
		if attempt.State != ports.AttemptTerminal {
			state.Runtime = attempt.State
		}
		var verified bool
		if err := r.db.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM "request-fulfillment".execution_verified_runtime_resolution v
WHERE v.tenant_id=$1 AND v.request_id=$2 AND v.generation=$3 AND v.attempt_id=$4 AND v.run_id=$5
AND v.revision=(SELECT max(current.revision) FROM "request-fulfillment".execution_verified_runtime_resolution current
WHERE current.tenant_id=v.tenant_id AND current.request_id=v.request_id))`, tenantID.String(), requestID.String(), attempt.Intent.Generation(), attempt.AttemptID, attempt.RunID).Scan(&verified); err != nil {
			return state, fmt.Errorf("postgres: проверить verified runtime resolution: %w", err)
		}
		if verified {
			state.Runtime = ports.AttemptTerminal
		}
	}
	resolutions, err := NewExecutionResolutionRepository(r.db).ListResolutions(ctx, tenantID, requestID)
	if err != nil {
		return state, err
	}
	var latestTarget *domain.ExecutionResolution
	for i := range resolutions {
		resolution := resolutions[i]
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

func (r *ExecutionAttemptRepository) SaveIntent(ctx context.Context, intent domain.ExecutionIntent, expectedRequestVersion int) error {
	var existingRaw []byte
	err := r.db.QueryRow(ctx, `SELECT intent FROM "request-fulfillment".execution_attempt WHERE command_id=$1`, string(intent.CommandID())).Scan(&existingRaw)
	if err == nil {
		var snapshot domain.ExecutionIntentSnapshot
		if e := json.Unmarshal(existingRaw, &snapshot); e != nil {
			return fmt.Errorf("postgres: decode intent: %w", e)
		}
		existing, e := domain.RehydrateExecutionIntent(snapshot)
		if e != nil {
			return fmt.Errorf("postgres: rehydrate intent: %w", e)
		}
		if existing.SameCommand(intent) {
			return nil
		}
		return ports.ErrExecutionCommandConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres: поиск CommandID: %w", err)
	}
	var tenant string
	var version int
	err = r.db.QueryRow(ctx, `SELECT tenant_id, version FROM "request-fulfillment".request WHERE id=$1 FOR UPDATE`, intent.RequestID().String()).Scan(&tenant, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.ErrRequestNotFound
	}
	if err != nil {
		return fmt.Errorf("postgres: блокировка заявки для intent: %w", err)
	}
	// Перепроверяем после блокировки request: конкурентный повтор той же команды
	// мог завершиться после первого SELECT, пока текущая транзакция ждала lock.
	err = r.db.QueryRow(ctx, `SELECT intent FROM "request-fulfillment".execution_attempt WHERE command_id=$1`, string(intent.CommandID())).Scan(&existingRaw)
	if err == nil {
		var snapshot domain.ExecutionIntentSnapshot
		if e := json.Unmarshal(existingRaw, &snapshot); e != nil {
			return fmt.Errorf("postgres: decode intent: %w", e)
		}
		existing, e := domain.RehydrateExecutionIntent(snapshot)
		if e != nil {
			return fmt.Errorf("postgres: rehydrate intent: %w", e)
		}
		if existing.SameCommand(intent) {
			return nil
		}
		return ports.ErrExecutionCommandConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres: повторная проверка CommandID: %w", err)
	}
	if tenant != intent.TenantID().String() || (version != expectedRequestVersion && version != expectedRequestVersion+1) {
		return ports.ErrVersionConflict
	}

	var latest int64
	err = r.db.QueryRow(ctx, `SELECT COALESCE(max(generation),0) FROM "request-fulfillment".execution_attempt WHERE request_id=$1`, intent.RequestID().String()).Scan(&latest)
	if err != nil {
		return fmt.Errorf("postgres: чтение поколения попытки: %w", err)
	}
	if intent.Generation() != latest+1 {
		return ports.ErrExecutionFenceConflict
	}
	raw, e := json.Marshal(intent.Snapshot())
	if e != nil {
		return fmt.Errorf("postgres: encode intent: %w", e)
	}
	target := intent.Parameters().LeadTarget
	var value any
	if target.State == domain.LeadTargetExplicit {
		value = target.Value
	}
	_, err = r.db.Exec(ctx, `INSERT INTO "request-fulfillment".execution_attempt
(request_id,tenant_id,generation,attempt_id,command_id,request_version,intent,target_state,lead_target,state)
VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,'queued')`, intent.RequestID().String(), tenant, intent.Generation(), executionAttemptID(intent.RequestID(), intent.Generation()), string(intent.CommandID()), version, string(raw), string(target.State), value)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ports.ErrExecutionCommandConflict
		}
		return fmt.Errorf("postgres: сохранить intent: %w", err)
	}
	return nil
}

func (r *ExecutionAttemptRepository) FindByCommand(ctx context.Context, tenantID domain.TenantID, commandID domain.CommandID) (ports.ExecutionAttempt, error) {
	var a ports.ExecutionAttempt
	var raw, manifest, receipts []byte
	var req, tenant, state, run, owner string
	var generation, fence int64
	var leaseUntil *time.Time
	err := r.db.QueryRow(ctx, `SELECT request_id,tenant_id,generation,COALESCE(attempt_id,''),intent,request_version,state,COALESCE(run_id,''),COALESCE(lease_owner,''),lease_until,fence,expected_objects,receipts FROM "request-fulfillment".execution_attempt WHERE tenant_id=$1 AND command_id=$2`, tenantID.String(), string(commandID)).Scan(&req, &tenant, &generation, &a.AttemptID, &raw, &a.RequestVersion, &state, &run, &owner, &leaseUntil, &fence, &manifest, &receipts)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ports.ErrExecutionAttemptNotFound
	}
	if err != nil {
		return a, fmt.Errorf("postgres: прочитать intent: %w", err)
	}
	var snapshot domain.ExecutionIntentSnapshot
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return a, err
	}
	a.Intent, err = domain.RehydrateExecutionIntent(snapshot)
	if err != nil {
		return a, err
	}
	a.State = ports.AttemptState(state)
	a.RunID = run
	a.LeaseOwner = owner
	a.Fence = uint64(fence)
	if manifest != nil {
		a.ManifestSet = true
		if err = json.Unmarshal(manifest, &a.ExpectedObjects); err != nil {
			return a, fmt.Errorf("postgres: decode manifest: %w", err)
		}
	}
	if len(receipts) > 0 {
		if err = json.Unmarshal(receipts, &a.ReceiptStates); err != nil {
			return a, fmt.Errorf("postgres: decode receipts: %w", err)
		}
	}
	for object, receiptState := range a.ReceiptStates {
		a.Receipts = append(a.Receipts, domain.AttemptReceipt{RequestID: a.Intent.RequestID(), TenantID: a.Intent.TenantID(), Generation: a.Intent.Generation(), ObjectID: object, State: receiptState})
	}
	sort.Slice(a.Receipts, func(i, j int) bool { return a.Receipts[i].ObjectID < a.Receipts[j].ObjectID })
	if leaseUntil != nil {
		a.LeaseUntil = *leaseUntil
	}
	return a, nil
}

func (r *ExecutionAttemptRepository) SetExpectedObjects(ctx context.Context, l ports.ExecutionLease, objectIDs []string) error {
	objects, err := normalizeExecutionObjects(objectIDs)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(objects)
	if err != nil {
		return err
	}
	tag, err := r.db.Exec(ctx, `UPDATE "request-fulfillment".execution_attempt SET expected_objects=$8::jsonb,updated_at=clock_timestamp()
WHERE request_id=$1 AND tenant_id=$2 AND generation=$3 AND command_id=$4 AND lease_owner=$5 AND fence=$6 AND request_version=$7 AND lease_until>clock_timestamp() AND expected_objects IS NULL`, l.RequestID.String(), l.TenantID.String(), l.Generation, string(l.CommandID), l.Owner, int64(l.Fence), l.RequestVersion, string(raw))
	if err != nil {
		return fmt.Errorf("postgres: записать manifest: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var stored []byte
	err = r.db.QueryRow(ctx, `SELECT expected_objects FROM "request-fulfillment".execution_attempt WHERE request_id=$1 AND tenant_id=$2 AND generation=$3 AND command_id=$4 AND lease_owner=$5 AND fence=$6 AND request_version=$7 AND lease_until>clock_timestamp()`, l.RequestID.String(), l.TenantID.String(), l.Generation, string(l.CommandID), l.Owner, int64(l.Fence), l.RequestVersion).Scan(&stored)
	if err != nil {
		return ports.ErrExecutionFenceConflict
	}
	var existing []string
	if err = json.Unmarshal(stored, &existing); err != nil {
		return err
	}
	if equalExecutionObjects(existing, objects) {
		return nil
	}
	return ports.ErrExecutionManifestConflict
}

func (r *ExecutionAttemptRepository) RecordReceipt(ctx context.Context, l ports.ExecutionLease, objectID string, state domain.ReceiptState) error {
	if strings.TrimSpace(objectID) == "" || (state != domain.ReceiptPending && state != domain.ReceiptUnknown && state != domain.ReceiptConfirmed) {
		return ports.ErrExecutionReceiptConflict
	}
	tag, err := r.db.Exec(ctx, `UPDATE "request-fulfillment".execution_attempt
	 SET receipts=jsonb_set(receipts,ARRAY[$8],to_jsonb($9::text),true),updated_at=clock_timestamp()
WHERE request_id=$1 AND tenant_id=$2 AND generation=$3 AND command_id=$4 AND lease_owner=$5 AND fence=$6 AND request_version=$7 AND lease_until>clock_timestamp()
AND expected_objects @> jsonb_build_array($8::text)
AND (COALESCE(receipts->>$8,'') <> 'confirmed' OR $9='confirmed')`, l.RequestID.String(), l.TenantID.String(), l.Generation, string(l.CommandID), l.Owner, int64(l.Fence), l.RequestVersion, objectID, string(state))
	if err != nil {
		return fmt.Errorf("postgres: сохранить receipt: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ports.ErrExecutionReceiptConflict
	}
	return nil
}

func normalizeExecutionObjects(ids []string) ([]string, error) {
	result := append([]string(nil), ids...)
	for _, id := range result {
		if strings.TrimSpace(id) == "" {
			return nil, ports.ErrExecutionManifestConflict
		}
	}
	sort.Strings(result)
	for i := 1; i < len(result); i++ {
		if result[i] == result[i-1] {
			return nil, ports.ErrExecutionManifestConflict
		}
	}
	return result, nil
}
func equalExecutionObjects(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r *ExecutionAttemptRepository) ClaimNext(ctx context.Context, owner string, duration time.Duration) (ports.ExecutionLease, error) {
	if strings.TrimSpace(owner) == "" || duration <= 0 {
		return ports.ExecutionLease{}, ports.ErrExecutionLeaseUnavailable
	}
	var l ports.ExecutionLease
	var until time.Time
	var fence int64
	err := r.db.QueryRow(ctx, `WITH candidate AS (
 SELECT current.request_id,current.generation FROM "request-fulfillment".execution_attempt current
 WHERE current.target_state <> 'unknownLegacy' AND current.state IN ('queued','claimed')
   AND (current.state='queued' OR current.lease_until <= clock_timestamp())
   AND NOT EXISTS (
     SELECT 1 FROM "request-fulfillment".execution_attempt previous
     WHERE previous.request_id=current.request_id
       AND previous.generation<current.generation
       AND previous.state<>'terminal'
   )
 ORDER BY current.created_at,current.request_id,current.generation FOR UPDATE OF current SKIP LOCKED LIMIT 1
)
UPDATE "request-fulfillment".execution_attempt a
SET state='claimed',lease_owner=$1,lease_until=clock_timestamp()+($2 * interval '1 second'),fence=a.fence+1,updated_at=clock_timestamp()
FROM candidate c WHERE a.request_id=c.request_id AND a.generation=c.generation
RETURNING a.request_id,a.tenant_id,a.generation,a.request_version,a.command_id,a.lease_until,a.fence`, owner, duration.Seconds()).Scan(&l.RequestID, &l.TenantID, &l.Generation, &l.RequestVersion, &l.CommandID, &until, &fence)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ports.ErrExecutionLeaseUnavailable
	}
	if err != nil {
		return l, fmt.Errorf("postgres: claim intent: %w", err)
	}
	l.Owner = owner
	l.Until = until
	l.Fence = uint64(fence)
	return l, nil
}

func (r *ExecutionAttemptRepository) RenewLease(ctx context.Context, l ports.ExecutionLease, d time.Duration) (ports.ExecutionLease, error) {
	if d <= 0 {
		return l, ports.ErrExecutionFenceConflict
	}
	var until time.Time
	err := r.db.QueryRow(ctx, `UPDATE "request-fulfillment".execution_attempt SET lease_until=clock_timestamp()+($8 * interval '1 second'),updated_at=clock_timestamp()
WHERE request_id=$1 AND tenant_id=$2 AND generation=$3 AND command_id=$4 AND lease_owner=$5 AND fence=$6 AND request_version=$7 AND state='claimed' AND lease_until>clock_timestamp()
RETURNING lease_until`, l.RequestID.String(), l.TenantID.String(), l.Generation, string(l.CommandID), l.Owner, int64(l.Fence), l.RequestVersion, d.Seconds()).Scan(&until)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, ports.ErrExecutionFenceConflict
	}
	if err != nil {
		return l, fmt.Errorf("postgres: продлить lease: %w", err)
	}
	l.Until = until
	return l, nil
}

func (r *ExecutionAttemptRepository) BindRun(ctx context.Context, l ports.ExecutionLease, runID string) error {
	if strings.TrimSpace(runID) == "" {
		return ports.ErrExecutionFenceConflict
	}
	tag, err := r.db.Exec(ctx, `UPDATE "request-fulfillment".execution_attempt SET run_id=$8,state='started',updated_at=clock_timestamp()
WHERE request_id=$1 AND tenant_id=$2 AND generation=$3 AND command_id=$4 AND lease_owner=$5 AND fence=$6 AND request_version=$7 AND state='claimed' AND lease_until>clock_timestamp() AND (run_id IS NULL OR run_id=$8)`, l.RequestID.String(), l.TenantID.String(), l.Generation, string(l.CommandID), l.Owner, int64(l.Fence), l.RequestVersion, runID)
	if err != nil {
		return fmt.Errorf("postgres: привязать RunID: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ports.ErrExecutionFenceConflict
	}
	return nil
}

func (r *ExecutionAttemptRepository) SetState(ctx context.Context, l ports.ExecutionLease, state ports.AttemptState) error {
	if state != ports.AttemptUnknown && state != ports.AttemptTerminal {
		return ports.ErrExecutionFenceConflict
	}
	tag, err := r.db.Exec(ctx, `UPDATE "request-fulfillment".execution_attempt SET state=$8,updated_at=clock_timestamp() WHERE request_id=$1 AND tenant_id=$2 AND generation=$3 AND command_id=$4 AND lease_owner=$5 AND fence=$6 AND request_version=$7 AND state IN ('claimed','started') AND lease_until>clock_timestamp()`, l.RequestID.String(), l.TenantID.String(), l.Generation, string(l.CommandID), l.Owner, int64(l.Fence), l.RequestVersion, string(state))
	if err != nil {
		return fmt.Errorf("postgres: сменить состояние attempt: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ports.ErrExecutionFenceConflict
	}
	return nil
}
