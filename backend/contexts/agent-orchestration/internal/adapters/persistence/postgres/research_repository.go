package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	rm "lead-gen/contracts/agent-orchestration/researchmission"
)

type ResearchRepository struct{ pool *pgxpool.Pool }

func NewResearchRepository(redPool *pgxpool.Pool) *ResearchRepository {
	if redPool == nil {
		panic("postgres: research требует red pool")
	}
	return &ResearchRepository{pool: redPool}
}

var _ ports.ResearchRepository = (*ResearchRepository)(nil)

// Проверяет не только socket PostgreSQL, но и доступность relation миссий.
func (r *ResearchRepository) CheckHealth(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `SELECT mission_id FROM "agent-orchestration".research_missions LIMIT 0`)
	return err
}

func researchRunnable(m *domain.ResearchMission) bool {
	if m.State == "running" || m.State == "queued" || m.State == "paused" {
		return true
	}
	for _, a := range m.Actions {
		if a.SettlementState == "pending" || a.SettlementState == "running" {
			return true
		}
		if a.State == "running" || a.State == "received" || a.State == "analyzed" || a.State == "analyzing" || a.State == "ingesting" {
			return true
		}
	}
	return false
}
func researchDecode(raw []byte) (*domain.ResearchMission, error) {
	var m domain.ResearchMission
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
func (r *ResearchRepository) Create(ctx context.Context, m *domain.ResearchMission) (*domain.ResearchMission, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO "agent-orchestration".research_missions(tenant_id,purpose_id,mission_id,intent_id,criteria_version,generation,revision,state,runnable,updated_at,payload) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT DO NOTHING`, m.Request.Scope.TenantID, m.Request.Scope.PurposeID, m.ID, m.Request.IntentID, m.Request.CriteriaVersion, m.Request.Generation, m.Revision, m.State, researchRunnable(m), m.UpdatedAt, raw)
	if err != nil {
		return nil, fmt.Errorf("research create: %w", err)
	}
	existing, err := r.Get(ctx, m.Request.Scope, m.ID)
	if err != nil {
		return nil, err
	}
	if existing.InputHash != m.InputHash {
		return nil, rm.ErrConflict
	}
	return existing, nil
}
func (r *ResearchRepository) Get(ctx context.Context, scope rm.Scope, id string) (*domain.ResearchMission, error) {
	if scope.Validate() != nil || id == "" {
		return nil, rm.ErrInvalid
	}
	var raw []byte
	err := r.pool.QueryRow(ctx, `SELECT payload FROM "agent-orchestration".research_missions WHERE tenant_id=$1 AND purpose_id=$2 AND mission_id=$3`, scope.TenantID, scope.PurposeID, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rm.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return researchDecode(raw)
}
func (r *ResearchRepository) FindByIntent(ctx context.Context, scope rm.Scope, id string) ([]*domain.ResearchMission, error) {
	if scope.Validate() != nil || id == "" {
		return nil, rm.ErrInvalid
	}
	rows, err := r.pool.Query(ctx, `SELECT payload FROM "agent-orchestration".research_missions WHERE tenant_id=$1 AND purpose_id=$2 AND intent_id=$3 ORDER BY updated_at DESC,mission_id`, scope.TenantID, scope.PurposeID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*domain.ResearchMission{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		m, err := researchDecode(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (r *ResearchRepository) Update(ctx context.Context, scope rm.Scope, id string, fn func(*domain.ResearchMission) error) (*domain.ResearchMission, error) {
	if scope.Validate() != nil || id == "" {
		return nil, rm.ErrInvalid
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT payload FROM "agent-orchestration".research_missions WHERE tenant_id=$1 AND purpose_id=$2 AND mission_id=$3 FOR UPDATE`, scope.TenantID, scope.PurposeID, id).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, rm.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m, err := researchDecode(raw)
	if err != nil {
		return nil, err
	}
	revision := m.Revision
	if err = fn(m); err != nil {
		return nil, err
	}
	if m.Revision != revision {
		if m.Revision != revision+1 {
			return nil, fmt.Errorf("%w: некорректный revision transition", rm.ErrConflict)
		}
		raw, err = json.Marshal(m)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE "agent-orchestration".research_missions SET revision=$4,state=$5,runnable=$6,updated_at=$7,payload=$8 WHERE tenant_id=$1 AND purpose_id=$2 AND mission_id=$3 AND revision=$9`, scope.TenantID, scope.PurposeID, id, m.Revision, m.State, researchRunnable(m), m.UpdatedAt, raw, revision)
		if err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return m, nil
}
func (r *ResearchRepository) Ready(ctx context.Context, limit int) ([]ports.ResearchRef, error) {
	if limit < 1 || limit > 1000 {
		return nil, rm.ErrInvalid
	}
	rows, err := r.pool.Query(ctx, `SELECT tenant_id,purpose_id,mission_id FROM "agent-orchestration".research_missions WHERE runnable ORDER BY updated_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ports.ResearchRef{}
	for rows.Next() {
		var ref ports.ResearchRef
		if err := rows.Scan(&ref.Scope.TenantID, &ref.Scope.PurposeID, &ref.ID); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}
