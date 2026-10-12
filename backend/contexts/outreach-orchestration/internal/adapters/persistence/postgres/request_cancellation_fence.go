package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

type requestCancellationFence struct{ db execer }

var _ ports.RequestCancellationFence = (*requestCancellationFence)(nil)

func (f *requestCancellationFence) Cancel(ctx context.Context, tenantID, requestID string) error {
	_, err := f.db.Exec(ctx, `
INSERT INTO "outreach-orchestration".request_cancellation_fence (tenant_id, request_id, cancelled)
VALUES ($1, $2, true)
ON CONFLICT (tenant_id, request_id) DO UPDATE SET cancelled = true`, tenantID, requestID)
	if err != nil {
		return fmt.Errorf("postgres: фиксация отмены заявки: %w", err)
	}
	return nil
}

func (f *requestCancellationFence) RegisterHandoff(ctx context.Context, tenantID, requestID, campaignID string) (bool, error) {
	var cancelled bool
	err := f.db.QueryRow(ctx, `
INSERT INTO "outreach-orchestration".request_cancellation_fence (tenant_id, request_id, campaign_id)
VALUES ($1, $2, $3)
ON CONFLICT (tenant_id, request_id) DO UPDATE SET campaign_id = EXCLUDED.campaign_id
RETURNING cancelled`, tenantID, requestID, campaignID).Scan(&cancelled)
	if err != nil {
		return false, fmt.Errorf("postgres: регистрация handoff заявки: %w", err)
	}
	return cancelled, nil
}

func (f *requestCancellationFence) CampaignCancelled(ctx context.Context, tenantID, campaignID string) (bool, error) {
	var cancelled bool
	err := f.db.QueryRow(ctx, `
SELECT cancelled
FROM "outreach-orchestration".request_cancellation_fence
WHERE tenant_id = $1 AND campaign_id = $2
FOR UPDATE`, tenantID, campaignID).Scan(&cancelled)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("postgres: проверка отмены кампании: %w", err)
	}
	return cancelled, nil
}
