-- schema: request-fulfillment
-- Durable owner-side intent and fenced dispatcher lease. Rows are additive and
-- do not rewrite the existing request snapshot; absent legacy target stays unknown.
CREATE TABLE IF NOT EXISTS "request-fulfillment".execution_attempt (
    request_id       text        NOT NULL REFERENCES "request-fulfillment".request(id),
    tenant_id        text        NOT NULL,
    generation       bigint      NOT NULL CHECK (generation > 0),
    command_id       text        NOT NULL UNIQUE,
    request_version  integer     NOT NULL,
    intent           jsonb       NOT NULL,
    target_state     text        NOT NULL CHECK (target_state IN ('explicit', 'unspecified', 'unknownLegacy')),
    lead_target      integer,
    state            text        NOT NULL CHECK (state IN ('queued', 'claimed', 'started', 'unknown', 'terminal')),
    run_id           text,
    lease_owner      text,
    lease_until      timestamptz,
    fence            bigint      NOT NULL DEFAULT 0 CHECK (fence >= 0),
    expected_objects jsonb,
    receipts         jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(receipts) = 'object'),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (request_id, generation),
    CHECK ((target_state = 'explicit' AND lead_target IS NOT NULL) OR
           (target_state <> 'explicit' AND lead_target IS NULL))
);

CREATE INDEX IF NOT EXISTS execution_attempt_dispatch_idx
    ON "request-fulfillment".execution_attempt (created_at, request_id, generation)
    WHERE state IN ('queued', 'claimed');

CREATE INDEX IF NOT EXISTS execution_attempt_tenant_idx
    ON "request-fulfillment".execution_attempt (tenant_id, request_id, generation);
