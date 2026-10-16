-- schema: lead-management
-- Исследовательские снимки, утверждения и идентичность: только красная зона.
CREATE TABLE IF NOT EXISTS "lead-management".research_entity (
 tenant_id text NOT NULL, purpose text NOT NULL, entity_id text NOT NULL,
 kind text NOT NULL, country text NOT NULL, revision bigint NOT NULL DEFAULT 1,
 created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id,purpose,entity_id)
);
CREATE TABLE IF NOT EXISTS "lead-management".research_source (
 tenant_id text NOT NULL, purpose text NOT NULL, source_id text NOT NULL,
 source_url text NOT NULL, source_revision text NOT NULL, content_hash text NOT NULL,
 snapshot jsonb NOT NULL, observed_at timestamptz NOT NULL,
 revoked_at timestamptz, revoke_reason text NOT NULL DEFAULT '',
 PRIMARY KEY (tenant_id,purpose,source_id),
 UNIQUE (tenant_id,purpose,source_url,source_revision)
);
CREATE TABLE IF NOT EXISTS "lead-management".research_observation (
 tenant_id text NOT NULL, purpose text NOT NULL, observation_id text NOT NULL,
 payload_hash text NOT NULL, entity_id text NOT NULL, source_id text NOT NULL,
 subject_key text NOT NULL, mission_id text NOT NULL, action_id text NOT NULL,
 query_attempt_id text NOT NULL, result jsonb NOT NULL, recorded_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id,purpose,observation_id),
 FOREIGN KEY (tenant_id,purpose,entity_id) REFERENCES "lead-management".research_entity,
 FOREIGN KEY (tenant_id,purpose,source_id) REFERENCES "lead-management".research_source
);
CREATE INDEX IF NOT EXISTS research_observation_mission ON "lead-management".research_observation (tenant_id,purpose,mission_id,entity_id);
CREATE INDEX IF NOT EXISTS research_observation_subject ON "lead-management".research_observation (tenant_id,purpose,source_id,subject_key);
CREATE TABLE IF NOT EXISTS "lead-management".research_identifier (
 tenant_id text NOT NULL, purpose text NOT NULL, identifier_key text NOT NULL,
 entity_id text NOT NULL, source_id text NOT NULL, kind text NOT NULL,
 jurisdiction text NOT NULL, authority text NOT NULL, normalized text NOT NULL,
 snapshot jsonb NOT NULL,
 PRIMARY KEY (tenant_id,purpose,identifier_key,source_id),
 FOREIGN KEY (tenant_id,purpose,entity_id) REFERENCES "lead-management".research_entity,
 FOREIGN KEY (tenant_id,purpose,source_id) REFERENCES "lead-management".research_source
);
CREATE INDEX IF NOT EXISTS research_identifier_entity ON "lead-management".research_identifier (tenant_id,purpose,entity_id);
CREATE TABLE IF NOT EXISTS "lead-management".research_assertion (
 tenant_id text NOT NULL, purpose text NOT NULL, assertion_id text NOT NULL,
 entity_id text NOT NULL, source_id text NOT NULL, observation_id text NOT NULL,
 predicate text NOT NULL, value text NOT NULL, knowledge text NOT NULL,
 valid_from timestamptz, valid_until timestamptz, fresh_until timestamptz,
 snapshot jsonb NOT NULL, recorded_at timestamptz NOT NULL,
 search_text tsvector GENERATED ALWAYS AS (to_tsvector('simple',value)) STORED,
 PRIMARY KEY (tenant_id,purpose,assertion_id),
 FOREIGN KEY (tenant_id,purpose,entity_id) REFERENCES "lead-management".research_entity,
 FOREIGN KEY (tenant_id,purpose,source_id) REFERENCES "lead-management".research_source,
 FOREIGN KEY (tenant_id,purpose,observation_id) REFERENCES "lead-management".research_observation
);
CREATE INDEX IF NOT EXISTS research_assertion_entity ON "lead-management".research_assertion (tenant_id,purpose,entity_id,predicate);
CREATE INDEX IF NOT EXISTS research_assertion_fts ON "lead-management".research_assertion USING gin(search_text);
CREATE TABLE IF NOT EXISTS "lead-management".research_need (
 tenant_id text NOT NULL, purpose text NOT NULL, need_id text NOT NULL, entity_id text NOT NULL,
 predicate text NOT NULL, criteria_version text NOT NULL,
 knowledge_state text NOT NULL CHECK (knowledge_state IN ('open','satisfied','blocked','obsolete')),
 reason text NOT NULL, updated_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id,purpose,need_id),
 UNIQUE (tenant_id,purpose,entity_id,predicate,criteria_version),
 FOREIGN KEY (tenant_id,purpose,entity_id) REFERENCES "lead-management".research_entity
);
CREATE TABLE IF NOT EXISTS "lead-management".research_need_run (
 tenant_id text NOT NULL, purpose text NOT NULL, need_id text NOT NULL, mission_id text NOT NULL,
 PRIMARY KEY (tenant_id,purpose,need_id,mission_id),
 FOREIGN KEY (tenant_id,purpose,need_id) REFERENCES "lead-management".research_need
);
CREATE TABLE IF NOT EXISTS "lead-management".research_attempt (
 tenant_id text NOT NULL,purpose text NOT NULL,attempt_id text NOT NULL,payload_hash text NOT NULL,
 entity_id text NOT NULL,question text NOT NULL,source_url text NOT NULL,source_revision text NOT NULL,
 locale text NOT NULL,snapshot jsonb NOT NULL,expires_at timestamptz NOT NULL,observed_at timestamptz NOT NULL,
 PRIMARY KEY (tenant_id,purpose,attempt_id)
);
CREATE INDEX IF NOT EXISTS research_attempt_recall ON "lead-management".research_attempt (tenant_id,purpose,question,source_url,source_revision,locale,expires_at);

ALTER TABLE "lead-management".research_observation ADD COLUMN IF NOT EXISTS identity_keys text[] NOT NULL DEFAULT ARRAY[]::text[];
