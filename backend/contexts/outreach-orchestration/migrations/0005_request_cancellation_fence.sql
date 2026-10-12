-- schema: outreach-orchestration
-- Owner-owned cancellation tombstones and request-to-campaign association.
CREATE TABLE IF NOT EXISTS "outreach-orchestration".request_cancellation_fence (
    tenant_id   text        NOT NULL,
    request_id  text        NOT NULL,
    campaign_id text,
    cancelled   boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, request_id),
    UNIQUE (tenant_id, campaign_id)
);

CREATE INDEX IF NOT EXISTS request_cancellation_fence_campaign_idx
    ON "outreach-orchestration".request_cancellation_fence (tenant_id, campaign_id)
    WHERE campaign_id IS NOT NULL;

-- Existing request-handoff campaigns use the stable owner identity
-- handoff-<request_id>; backfill their mapping before cancellation is enabled.
INSERT INTO "outreach-orchestration".request_cancellation_fence
    (tenant_id, request_id, campaign_id, cancelled)
SELECT tenant_id, substring(id FROM 8), id, false
FROM "outreach-orchestration".campaign
WHERE id LIKE 'handoff-%'
ON CONFLICT (tenant_id, request_id) DO NOTHING;
