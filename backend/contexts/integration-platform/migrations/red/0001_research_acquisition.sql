-- schema: integration-platform
-- Клиентская зона: receipt содержит исходные URL и содержимое страницы.
CREATE SCHEMA IF NOT EXISTS "integration-platform";
CREATE TABLE IF NOT EXISTS "integration-platform".research_fc_account (
 account_id text PRIMARY KEY,
 max_credits bigint NOT NULL CHECK (max_credits >= 0),
 spent_credits bigint NOT NULL DEFAULT 0 CHECK (spent_credits >= 0),
 reserved_credits bigint NOT NULL DEFAULT 0 CHECK (reserved_credits >= 0),
 unknown_credits bigint NOT NULL DEFAULT 0 CHECK (unknown_credits >= 0 AND unknown_credits <= reserved_credits),
 stopped boolean NOT NULL DEFAULT false,
 max_concurrent integer NOT NULL CHECK (max_concurrent > 0),
 min_interval_ms bigint NOT NULL CHECK (min_interval_ms >= 0),
 next_allowed_at timestamptz NOT NULL DEFAULT 'epoch',
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS "integration-platform".research_fc_grant (
 grant_id text PRIMARY KEY,
 account_id text NOT NULL REFERENCES "integration-platform".research_fc_account(account_id),
 tenant_id text NOT NULL,
 purpose_id text NOT NULL,
 intent_id text NOT NULL,
 intent_kind text NOT NULL CHECK (intent_kind IN ('mission','market_study')),
 max_credits bigint NOT NULL CHECK (max_credits >= 0),
 spent_credits bigint NOT NULL DEFAULT 0 CHECK (spent_credits >= 0),
 reserved_credits bigint NOT NULL DEFAULT 0 CHECK (reserved_credits >= 0),
 unknown_credits bigint NOT NULL DEFAULT 0 CHECK (unknown_credits >= 0 AND unknown_credits <= reserved_credits),
 UNIQUE (tenant_id,purpose_id,intent_kind,intent_id)
);
CREATE TABLE IF NOT EXISTS "integration-platform".research_acquisition_attempt (
 operation_id text PRIMARY KEY,
 account_id text NOT NULL REFERENCES "integration-platform".research_fc_account(account_id),
 grant_id text NOT NULL REFERENCES "integration-platform".research_fc_grant(grant_id),
 tenant_id text NOT NULL,
 purpose_id text NOT NULL,
 action_id text NOT NULL,
 attempt_no integer NOT NULL CHECK (attempt_no > 0),
 command_hash text NOT NULL,
 operation_kind text NOT NULL CHECK (operation_kind IN ('search','fetch')),
 state text NOT NULL CHECK (state IN ('running','completed','unknown')),
 reserved_credits bigint NOT NULL CHECK (reserved_credits > 0),
 charged_credits bigint CHECK (charged_credits >= 0),
 lease_epoch bigint NOT NULL DEFAULT 1,
 lease_until timestamptz NOT NULL,
 receipt jsonb,
 receipt_hash text,
 created_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz,
 UNIQUE(tenant_id,purpose_id,action_id,attempt_no)
);
CREATE INDEX IF NOT EXISTS research_acquisition_active ON "integration-platform".research_acquisition_attempt(account_id,state,lease_until);
CREATE TABLE IF NOT EXISTS "integration-platform".research_fc_reconciliation (
 operation_id text PRIMARY KEY REFERENCES "integration-platform".research_acquisition_attempt(operation_id),
 actual_credits bigint NOT NULL CHECK (actual_credits >= 0),
 evidence_ref text NOT NULL,
 actor text NOT NULL,
 reconciled_at timestamptz NOT NULL DEFAULT now()
);
