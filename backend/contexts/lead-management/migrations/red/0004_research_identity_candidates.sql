-- schema: lead-management
-- Кандидат тождества — сигнал для проверки, не ребро подтверждения.
CREATE TABLE IF NOT EXISTS "lead-management".research_identity_candidate (
 tenant_id text NOT NULL,purpose text NOT NULL,candidate_id text NOT NULL,
 left_entity_id text NOT NULL,right_entity_id text NOT NULL,
 source_id text NOT NULL,target_source_id text NOT NULL,snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,purpose,candidate_id),
 FOREIGN KEY(tenant_id,purpose,left_entity_id) REFERENCES "lead-management".research_entity,
 FOREIGN KEY(tenant_id,purpose,right_entity_id) REFERENCES "lead-management".research_entity,
 FOREIGN KEY(tenant_id,purpose,source_id) REFERENCES "lead-management".research_source,
 FOREIGN KEY(tenant_id,purpose,target_source_id) REFERENCES "lead-management".research_source
);
CREATE INDEX IF NOT EXISTS research_identity_candidate_pair ON "lead-management".research_identity_candidate(tenant_id,purpose,left_entity_id,right_entity_id);
