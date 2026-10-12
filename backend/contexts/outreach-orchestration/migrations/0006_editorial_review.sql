-- schema: outreach-orchestration
ALTER TABLE "outreach-orchestration".campaign
    ADD COLUMN IF NOT EXISTS purpose text NOT NULL DEFAULT 'delivery';

CREATE TABLE IF NOT EXISTS "outreach-orchestration".editorial_campaign (
    id text PRIMARY KEY,
    tenant_id text NOT NULL,
    request_id text NOT NULL,
    purpose text NOT NULL DEFAULT 'editorial' CHECK (purpose = 'editorial'),
    items jsonb NOT NULL,
    fingerprint text NOT NULL,
    UNIQUE (tenant_id, request_id)
);

CREATE TABLE IF NOT EXISTS "outreach-orchestration".editorial_decision (
    campaign_id text NOT NULL REFERENCES "outreach-orchestration".editorial_campaign(id) ON DELETE CASCADE,
    lead_id text NOT NULL,
    material_ref text NOT NULL,
    version_id text NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('approved','rejected')),
    reason text NOT NULL DEFAULT '',
    actor_id text NOT NULL,
    decided_at timestamptz NOT NULL,
    PRIMARY KEY (campaign_id, lead_id, material_ref)
);

CREATE TABLE IF NOT EXISTS "outreach-orchestration".editorial_decision_event (
    event_id bigserial PRIMARY KEY,
    campaign_id text NOT NULL REFERENCES "outreach-orchestration".editorial_campaign(id) ON DELETE CASCADE,
    tenant_id text NOT NULL,
    lead_id text NOT NULL,
    material_ref text NOT NULL,
    version_id text NOT NULL,
    outcome text NOT NULL CHECK (outcome IN ('approved','rejected')),
    reason text NOT NULL DEFAULT '',
    actor_id text NOT NULL,
    occurred_at timestamptz NOT NULL,
    event_type text NOT NULL CHECK (event_type = 'editorial-decision-recorded'),
    UNIQUE(campaign_id,lead_id,material_ref)
);
