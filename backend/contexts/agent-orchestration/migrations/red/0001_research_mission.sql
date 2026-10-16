-- schema: agent-orchestration
CREATE SCHEMA IF NOT EXISTS "agent-orchestration";
-- Исходные критерии, query/URL и receipts допускаются только в клиентской red БД.
CREATE TABLE IF NOT EXISTS "agent-orchestration".research_missions (
    tenant_id text NOT NULL,
    purpose_id text NOT NULL,
    mission_id text NOT NULL,
    intent_id text NOT NULL,
    criteria_version integer NOT NULL CHECK (criteria_version > 0),
    generation text NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    state text NOT NULL,
    runnable boolean NOT NULL,
    updated_at timestamptz NOT NULL,
    payload jsonb NOT NULL,
    PRIMARY KEY (tenant_id, purpose_id, mission_id),
    UNIQUE (tenant_id, purpose_id, intent_id, criteria_version, generation),
    CHECK (tenant_id <> '' AND purpose_id <> ''),
    CHECK (octet_length(payload::text) <= 67108864)
);
CREATE INDEX IF NOT EXISTS research_missions_intent ON "agent-orchestration".research_missions(tenant_id,purpose_id,intent_id,updated_at DESC);
CREATE INDEX IF NOT EXISTS research_missions_ready ON "agent-orchestration".research_missions(updated_at) WHERE runnable;
