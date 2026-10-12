package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	tappeditorial "lead-gen/backend/contexts/outreach-orchestration/internal/app/editorialreview"
	contract "lead-gen/contracts/outreach-orchestration/editorialreview"
)

// EditorialStore — durable отдельное хранилище editorial campaign/decision.
// Материал хранится только ссылкой; текст читается из владельца content-production.
type EditorialStore struct{ pool *pgxpool.Pool }

func NewEditorialStore(pool *pgxpool.Pool) *EditorialStore {
	if pool == nil {
		panic("postgres: NewEditorialStore с nil-пулом")
	}
	return &EditorialStore{pool: pool}
}

func (s *EditorialStore) Submit(ctx context.Context, h tappeditorial.Handoff) (tappeditorial.Campaign, bool, error) {
	if strings.TrimSpace(h.TenantID) == "" || strings.TrimSpace(h.RequestID) == "" || len(h.Items) == 0 {
		return tappeditorial.Campaign{}, false, contract.ErrRejected
	}
	items := append([]tappeditorial.Item(nil), h.Items...)
	sort.Slice(items, func(i, j int) bool { return items[i].LeadID < items[j].LeadID })
	seen := map[string]bool{}
	for _, i := range items {
		if strings.TrimSpace(i.LeadID) == "" || strings.TrimSpace(i.MaterialRef) == "" || strings.TrimSpace(i.AssetID) == "" || strings.TrimSpace(i.VersionID) == "" || seen[i.LeadID] {
			return tappeditorial.Campaign{}, false, contract.ErrRejected
		}
		seen[i.LeadID] = true
	}
	raw, err := json.Marshal(items)
	if err != nil {
		return tappeditorial.Campaign{}, false, err
	}
	sum := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(sum[:])
	id := "handoff-" + h.RequestID
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return tappeditorial.Campaign{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `INSERT INTO "outreach-orchestration".editorial_campaign(id,tenant_id,request_id,purpose,items,fingerprint) VALUES($1,$2,$3,'editorial',$4::jsonb,$5) ON CONFLICT(id) DO NOTHING`, id, h.TenantID, h.RequestID, string(raw), fingerprint)
	if err != nil {
		return tappeditorial.Campaign{}, false, err
	}
	repeated := tag.RowsAffected() == 0
	var tenant, request, purpose, storedFingerprint string
	var stored []byte
	err = tx.QueryRow(ctx, `SELECT tenant_id,request_id,purpose,items,fingerprint FROM "outreach-orchestration".editorial_campaign WHERE id=$1 FOR UPDATE`, id).Scan(&tenant, &request, &purpose, &stored, &storedFingerprint)
	if err != nil {
		return tappeditorial.Campaign{}, false, err
	}
	if tenant != h.TenantID || request != h.RequestID || purpose != "editorial" || storedFingerprint != fingerprint {
		return tappeditorial.Campaign{}, false, contract.ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return tappeditorial.Campaign{}, false, err
	}
	var decoded []tappeditorial.Item
	if err := json.Unmarshal(stored, &decoded); err != nil {
		return tappeditorial.Campaign{}, false, err
	}
	c := tappeditorial.Campaign{CampaignID: id, TenantID: tenant, RequestID: request, Purpose: purpose}
	for _, item := range decoded {
		c.Items = append(c.Items, tappeditorial.ItemRecord{Item: item})
	}
	return c, repeated, nil
}

func (s *EditorialStore) Get(ctx context.Context, q contract.Query) (tappeditorial.ItemRecord, string, error) {
	var tenant, purpose string
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT tenant_id,purpose,items FROM "outreach-orchestration".editorial_campaign WHERE id=$1`, q.CampaignID).Scan(&tenant, &purpose, &raw)
	if errors.Is(err, pgx.ErrNoRows) || tenant != q.TenantID || purpose != "editorial" {
		return tappeditorial.ItemRecord{}, "", contract.ErrNotFound
	}
	if err != nil {
		return tappeditorial.ItemRecord{}, "", err
	}
	var items []tappeditorial.Item
	if err := json.Unmarshal(raw, &items); err != nil {
		return tappeditorial.ItemRecord{}, "", err
	}
	for _, item := range items {
		if item.LeadID == q.LeadID && item.MaterialRef == q.MaterialRef && item.VersionID == q.VersionID {
			r := tappeditorial.ItemRecord{Item: item}
			var d tappeditorial.Decision
			err := s.pool.QueryRow(ctx, `SELECT outcome,reason,actor_id,decided_at,version_id FROM "outreach-orchestration".editorial_decision WHERE campaign_id=$1 AND lead_id=$2 AND material_ref=$3`, q.CampaignID, q.LeadID, q.MaterialRef).Scan(&d.Outcome, &d.Reason, &d.Actor, &d.At, &d.VersionID)
			if err == nil {
				r.Decision = &d
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return tappeditorial.ItemRecord{}, "", err
			}
			return r, q.CampaignID, nil
		}
	}
	return tappeditorial.ItemRecord{}, "", contract.ErrNotFound
}

func (s *EditorialStore) Decide(ctx context.Context, d tappeditorial.Decide) (tappeditorial.ItemRecord, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return tappeditorial.ItemRecord{}, "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var tenant, purpose string
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT tenant_id,purpose,items FROM "outreach-orchestration".editorial_campaign WHERE id=$1 FOR UPDATE`, d.CampaignID).Scan(&tenant, &purpose, &raw)
	if errors.Is(err, pgx.ErrNoRows) || tenant != d.TenantID || purpose != "editorial" {
		return tappeditorial.ItemRecord{}, "", contract.ErrNotFound
	}
	if err != nil {
		return tappeditorial.ItemRecord{}, "", err
	}
	var items []tappeditorial.Item
	if err := json.Unmarshal(raw, &items); err != nil {
		return tappeditorial.ItemRecord{}, "", err
	}
	valid := false
	for _, i := range items {
		if i.LeadID == d.LeadID && i.MaterialRef == d.MaterialRef {
			if i.VersionID != d.ExpectedVersion {
				return tappeditorial.ItemRecord{}, "", tappeditorial.ErrConflict
			}
			valid = true
			break
		}
	}
	if !valid {
		return tappeditorial.ItemRecord{}, "", contract.ErrNotFound
	}
	var old tappeditorial.Decision
	err = tx.QueryRow(ctx, `SELECT outcome,reason,actor_id,decided_at,version_id FROM "outreach-orchestration".editorial_decision WHERE campaign_id=$1 AND lead_id=$2 AND material_ref=$3 FOR UPDATE`, d.CampaignID, d.LeadID, d.MaterialRef).Scan(&old.Outcome, &old.Reason, &old.Actor, &old.At, &old.VersionID)
	if err == nil {
		if old.Outcome == d.Outcome && old.Reason == d.Reason && old.Actor == d.Actor && old.VersionID == d.ExpectedVersion {
			if e := tx.Commit(ctx); e != nil {
				return tappeditorial.ItemRecord{}, "", e
			}
			r, _, e := s.Get(ctx, contract.Query{TenantID: d.TenantID, CampaignID: d.CampaignID, LeadID: d.LeadID, MaterialRef: d.MaterialRef, VersionID: d.ExpectedVersion})
			return r, d.CampaignID, e
		}
		return tappeditorial.ItemRecord{}, "", tappeditorial.ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return tappeditorial.ItemRecord{}, "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO "outreach-orchestration".editorial_decision(campaign_id,lead_id,material_ref,version_id,outcome,reason,actor_id,decided_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, d.CampaignID, d.LeadID, d.MaterialRef, d.ExpectedVersion, d.Outcome, d.Reason, d.Actor, d.At)
	if err != nil {
		return tappeditorial.ItemRecord{}, "", fmt.Errorf("insert editorial decision: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO "outreach-orchestration".editorial_decision_event(campaign_id,tenant_id,lead_id,material_ref,version_id,outcome,reason,actor_id,occurred_at,event_type) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, d.CampaignID, d.TenantID, d.LeadID, d.MaterialRef, d.ExpectedVersion, d.Outcome, d.Reason, d.Actor, d.At, tappeditorial.EventTypeDecisionRecorded)
	if err != nil {
		return tappeditorial.ItemRecord{}, "", fmt.Errorf("insert editorial decision event: %w", err)
	}
	if err = tx.Commit(ctx); err != nil {
		return tappeditorial.ItemRecord{}, "", err
	}
	r, _, err := s.Get(ctx, contract.Query{TenantID: d.TenantID, CampaignID: d.CampaignID, LeadID: d.LeadID, MaterialRef: d.MaterialRef, VersionID: d.ExpectedVersion})
	return r, d.CampaignID, err
}

func (s *EditorialStore) Events(ctx context.Context, campaignID string) ([]tappeditorial.DecisionEvent, error) {
	rows, err := s.pool.Query(ctx, `SELECT event_type,campaign_id,tenant_id,lead_id,material_ref,version_id,outcome,reason,actor_id,occurred_at FROM "outreach-orchestration".editorial_decision_event WHERE campaign_id=$1 ORDER BY event_id`, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]tappeditorial.DecisionEvent, 0)
	for rows.Next() {
		var e tappeditorial.DecisionEvent
		if err := rows.Scan(&e.EventType, &e.CampaignID, &e.TenantID, &e.LeadID, &e.MaterialRef, &e.VersionID, &e.Outcome, &e.Reason, &e.Actor, &e.At); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "outreach-orchestration".editorial_campaign WHERE id=$1)`, campaignID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, contract.ErrNotFound
		}
	}
	return events, nil
}

var _ tappeditorial.Store = (*EditorialStore)(nil)
