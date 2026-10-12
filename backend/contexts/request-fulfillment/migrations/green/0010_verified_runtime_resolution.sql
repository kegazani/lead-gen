-- schema: request-fulfillment
-- Verified runtime history is a distinct append-only stream. Rows imported from
-- execution_resolution_revision remain observations and are not backfilled here.
CREATE UNIQUE INDEX execution_attempt_verified_identity_idx
    ON "request-fulfillment".execution_attempt
       (request_id, tenant_id, generation, attempt_id, run_id);

CREATE TABLE "request-fulfillment".execution_verified_runtime_resolution (
    resolution_id text PRIMARY KEY CHECK (btrim(resolution_id) <> ''),
    tenant_id text NOT NULL CHECK (btrim(tenant_id) <> ''),
    request_id text NOT NULL CHECK (btrim(request_id) <> ''),
    generation bigint NOT NULL CHECK (generation > 0),
    attempt_id text NOT NULL CHECK (btrim(attempt_id) <> ''),
    command_id text NOT NULL CHECK (btrim(command_id) <> ''),
    workflow_id text NOT NULL CHECK (workflow_id = 'fulfillment-' || request_id),
    run_id text NOT NULL CHECK (btrim(run_id) <> ''),
    runtime_outcome text NOT NULL CHECK (runtime_outcome IN ('failed','canceled')),
    reason_code text NOT NULL CHECK (reason_code IN ('search_failed','draft_failed','handoff_failed','transition_rejected','workflow_timed_out')),
    original_reason text NOT NULL CHECK (btrim(original_reason) <> ''),
    terminal_intent jsonb NOT NULL CHECK (jsonb_typeof(terminal_intent) = 'object'),
    evidence_fingerprint text NOT NULL CHECK (evidence_fingerprint ~ '^[0-9a-f]{64}$'),
    revision bigint NOT NULL CHECK (revision > 0),
    expected_revision bigint NOT NULL CHECK (expected_revision >= 0),
    supersedes_id text,
    actor text NOT NULL CHECK (actor = 'temporal-history-verifier'),
    resolved_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (revision = expected_revision + 1),
    CHECK ((expected_revision = 0 AND supersedes_id IS NULL) OR
           (expected_revision > 0 AND supersedes_id IS NOT NULL AND btrim(supersedes_id) <> '')),
    CHECK (terminal_intent->>'Version' = '1'),
    CHECK (terminal_intent->>'WorkflowID' = workflow_id),
    CHECK (terminal_intent->>'RunID' = run_id),
    CHECK (terminal_intent->>'RequestID' = request_id),
    CHECK (terminal_intent->>'TenantID' = tenant_id),
    CHECK (terminal_intent->>'AttemptID' = attempt_id),
    CHECK ((terminal_intent->>'Generation')::bigint = generation),
    CHECK (terminal_intent->>'CommandID' = command_id),
    CHECK (terminal_intent->>'ReasonCode' = reason_code),
    CHECK (terminal_intent->>'OriginalReason' = original_reason),
    FOREIGN KEY (request_id, tenant_id)
        REFERENCES "request-fulfillment".request(id, tenant_id) ON DELETE RESTRICT,
    FOREIGN KEY (request_id, tenant_id, generation, attempt_id, run_id)
        REFERENCES "request-fulfillment".execution_attempt(request_id, tenant_id, generation, attempt_id, run_id)
        ON DELETE RESTRICT,
    UNIQUE (tenant_id, request_id, revision)
);

CREATE INDEX execution_verified_runtime_current_idx
    ON "request-fulfillment".execution_verified_runtime_resolution
       (tenant_id, request_id, revision DESC);

CREATE FUNCTION "request-fulfillment".validate_verified_runtime_resolution()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    current_revision bigint;
    current_id text;
    stored_command_id text;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.tenant_id || ':' || NEW.request_id || ':verified-runtime', 0));
    SELECT command_id INTO stored_command_id
      FROM "request-fulfillment".execution_attempt
     WHERE request_id=NEW.request_id AND tenant_id=NEW.tenant_id
       AND generation=NEW.generation AND attempt_id=NEW.attempt_id AND run_id=NEW.run_id
     FOR KEY SHARE;
    IF NOT FOUND OR stored_command_id IS DISTINCT FROM NEW.command_id THEN
        RAISE EXCEPTION 'verified runtime resolution attempt identity mismatch'
            USING ERRCODE='23514', CONSTRAINT='execution_verified_runtime_identity';
    END IF;
    SELECT revision,resolution_id INTO current_revision,current_id
      FROM "request-fulfillment".execution_verified_runtime_resolution
     WHERE tenant_id=NEW.tenant_id AND request_id=NEW.request_id
     ORDER BY revision DESC LIMIT 1;
    IF NOT FOUND THEN
        current_revision := 0;
        current_id := NULL;
    END IF;
    IF NEW.expected_revision <> current_revision OR NEW.revision <> current_revision + 1
       OR NEW.supersedes_id IS DISTINCT FROM current_id THEN
        RAISE EXCEPTION 'verified runtime resolution revision CAS conflict'
            USING ERRCODE='40001', CONSTRAINT='execution_verified_runtime_revision_cas';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_verified_runtime_validate
    BEFORE INSERT ON "request-fulfillment".execution_verified_runtime_resolution
    FOR EACH ROW EXECUTE FUNCTION "request-fulfillment".validate_verified_runtime_resolution();

CREATE FUNCTION "request-fulfillment".reject_verified_runtime_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'execution_verified_runtime_resolution is append-only'
        USING ERRCODE='55000';
END;
$$;

CREATE TRIGGER execution_verified_runtime_immutable
    BEFORE UPDATE OR DELETE ON "request-fulfillment".execution_verified_runtime_resolution
    FOR EACH ROW EXECUTE FUNCTION "request-fulfillment".reject_verified_runtime_mutation();
CREATE TRIGGER execution_verified_runtime_no_truncate
    BEFORE TRUNCATE ON "request-fulfillment".execution_verified_runtime_resolution
    FOR EACH STATEMENT EXECUTE FUNCTION "request-fulfillment".reject_verified_runtime_mutation();

-- Only the trusted app verifier calls the dedicated writer. DB triggers protect
-- revisions and identity; they do not make runtime observations authentic.
-- Table owner/superuser can bypass these triggers; role grants are external.
