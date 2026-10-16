-- schema: lead-management
-- Исходные entity и observations сохраняются; тождество — версионируемая проекция.
CREATE TABLE IF NOT EXISTS "lead-management".research_identity_scope (
 tenant_id text NOT NULL,purpose text NOT NULL,revision bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(tenant_id,purpose)
);
CREATE TABLE IF NOT EXISTS "lead-management".research_identity_decision (
 tenant_id text NOT NULL,purpose text NOT NULL,decision_id text NOT NULL,
 action text NOT NULL CHECK(action IN ('link','reject','revoke')),
 left_entity_id text NOT NULL,right_entity_id text NOT NULL,
 revoke_decision_id text NOT NULL DEFAULT '',payload_hash text NOT NULL,
 snapshot jsonb NOT NULL,recorded_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,purpose,decision_id),
 FOREIGN KEY(tenant_id,purpose,left_entity_id) REFERENCES "lead-management".research_entity,
 FOREIGN KEY(tenant_id,purpose,right_entity_id) REFERENCES "lead-management".research_entity
);
CREATE TABLE IF NOT EXISTS "lead-management".research_identity_evidence (
 tenant_id text NOT NULL,purpose text NOT NULL,decision_id text NOT NULL,assertion_id text NOT NULL,
 PRIMARY KEY(tenant_id,purpose,decision_id,assertion_id),
 FOREIGN KEY(tenant_id,purpose,decision_id) REFERENCES "lead-management".research_identity_decision,
 FOREIGN KEY(tenant_id,purpose,assertion_id) REFERENCES "lead-management".research_assertion
);
CREATE TABLE IF NOT EXISTS "lead-management".research_identity_state (
 tenant_id text NOT NULL,purpose text NOT NULL,decision_id text NOT NULL,
 state text NOT NULL CHECK(state IN ('active','revoked','unavailable','conflict')),
 revision bigint NOT NULL,
 PRIMARY KEY(tenant_id,purpose,decision_id),
 FOREIGN KEY(tenant_id,purpose,decision_id) REFERENCES "lead-management".research_identity_decision
);
CREATE TABLE IF NOT EXISTS "lead-management".research_identity_member (
 tenant_id text NOT NULL,purpose text NOT NULL,entity_id text NOT NULL,
 canonical_entity_id text NOT NULL,revision bigint NOT NULL,
 PRIMARY KEY(tenant_id,purpose,entity_id),
 FOREIGN KEY(tenant_id,purpose,entity_id) REFERENCES "lead-management".research_entity,
 FOREIGN KEY(tenant_id,purpose,canonical_entity_id) REFERENCES "lead-management".research_entity
);
CREATE INDEX IF NOT EXISTS research_identity_canonical ON "lead-management".research_identity_member(tenant_id,purpose,canonical_entity_id);
CREATE TABLE IF NOT EXISTS "lead-management".research_identity_change (
 tenant_id text NOT NULL,purpose text NOT NULL,revision bigint NOT NULL,event_id text NOT NULL,
 cause text NOT NULL,entity_ids text[] NOT NULL,created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,purpose,revision),UNIQUE(tenant_id,purpose,event_id)
);
