-- schema: integration-platform
-- Только технические очереди/receipts proxy-инструментов. Денежных полей,
-- foreign key на Firecrawl account/grant и monetary admission здесь нет.
CREATE SCHEMA IF NOT EXISTS "integration-platform";
CREATE TABLE IF NOT EXISTS "integration-platform".research_source_policy (
 source_id text PRIMARY KEY,
 max_concurrent integer NOT NULL CHECK (max_concurrent>0),
 max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 10),
 min_interval_ms bigint NOT NULL CHECK (min_interval_ms>=0),
 next_allowed_at timestamptz NOT NULL DEFAULT 'epoch'
);
CREATE TABLE IF NOT EXISTS "integration-platform".research_source_attempt (
 operation_id text PRIMARY KEY,
 source_id text NOT NULL REFERENCES "integration-platform".research_source_policy(source_id),
 tenant_id text NOT NULL,
 purpose_id text NOT NULL,
 action_id text NOT NULL,
 attempt_no integer NOT NULL CHECK (attempt_no>0),
 command_hash text NOT NULL,
 state text NOT NULL CHECK (state IN ('running','completed','unknown')),
 lease_epoch bigint NOT NULL DEFAULT 1,
 lease_until timestamptz NOT NULL,
 receipt jsonb,
 receipt_hash text,
 late_receipt jsonb,
 created_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz,
 UNIQUE(source_id,tenant_id,purpose_id,action_id,attempt_no)
);
CREATE INDEX IF NOT EXISTS research_source_active ON "integration-platform".research_source_attempt(source_id,state,lease_until);
