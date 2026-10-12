-- schema: request-fulfillment
-- Append-only audited resolutions. Evidence payloads/secrets are never persisted,
-- only their SHA-256 fingerprint.
ALTER TABLE "request-fulfillment".execution_attempt
    ADD COLUMN IF NOT EXISTS attempt_id text;

CREATE UNIQUE INDEX IF NOT EXISTS execution_attempt_attempt_id_idx
    ON "request-fulfillment".execution_attempt (attempt_id)
    WHERE attempt_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS "request-fulfillment".execution_resolution (
    resolution_id        text        PRIMARY KEY CHECK (btrim(resolution_id) <> ''),
    tenant_id            text        NOT NULL CHECK (btrim(tenant_id) <> ''),
    request_id           text        NOT NULL REFERENCES "request-fulfillment".request(id),
    resolution_kind      text        NOT NULL CHECK (resolution_kind IN ('target', 'runtime')),
    generation           bigint,
    attempt_id            text,
    target_state         text,
    lead_target          integer,
    runtime_outcome      text,
    verified_run_id      text,
    actor                text        NOT NULL CHECK (btrim(actor) <> ''),
    resolved_at          timestamptz NOT NULL,
    evidence_fingerprint text        NOT NULL CHECK (evidence_fingerprint ~ '^[0-9a-f]{64}$'),
    created_at           timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (
      (resolution_kind = 'target' AND generation IS NULL AND attempt_id IS NULL
       AND target_state = 'explicit' AND lead_target IS NOT NULL
       AND runtime_outcome IS NULL AND verified_run_id IS NULL)
      OR
      (resolution_kind = 'runtime' AND generation IS NOT NULL AND generation > 0
       AND attempt_id IS NOT NULL AND btrim(attempt_id) <> ''
       AND target_state IS NULL AND lead_target IS NULL
       AND runtime_outcome IS NOT NULL AND runtime_outcome IN ('completed', 'failed', 'canceled')
       AND verified_run_id IS NOT NULL AND btrim(verified_run_id) <> '')
    ),
    FOREIGN KEY (request_id, generation)
      REFERENCES "request-fulfillment".execution_attempt(request_id, generation)
      DEFERRABLE INITIALLY IMMEDIATE
);

CREATE INDEX IF NOT EXISTS execution_resolution_subject_idx
    ON "request-fulfillment".execution_resolution
       (tenant_id, request_id, resolution_kind, generation, resolved_at, resolution_id);

CREATE FUNCTION "request-fulfillment".reject_execution_resolution_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'execution_resolution is append-only' USING ERRCODE = '55000';
END;
$$;

DROP TRIGGER IF EXISTS execution_resolution_immutable
    ON "request-fulfillment".execution_resolution;
CREATE TRIGGER execution_resolution_immutable
    BEFORE UPDATE OR DELETE ON "request-fulfillment".execution_resolution
    FOR EACH ROW EXECUTE FUNCTION "request-fulfillment".reject_execution_resolution_mutation();

CREATE FUNCTION "request-fulfillment".validate_execution_resolution_subject()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    stored_tenant text;
    stored_run_id text;
    has_attempt boolean;
BEGIN
    SELECT tenant_id INTO stored_tenant
      FROM "request-fulfillment".request WHERE id = NEW.request_id FOR KEY SHARE;
    IF stored_tenant IS DISTINCT FROM NEW.tenant_id THEN
        RAISE EXCEPTION 'execution_resolution tenant mismatch' USING ERRCODE = '23514';
    END IF;
    IF NEW.resolution_kind = 'target' THEN
        SELECT EXISTS(SELECT 1 FROM "request-fulfillment".execution_attempt
                      WHERE request_id=NEW.request_id) INTO has_attempt;
        IF has_attempt THEN
            RAISE EXCEPTION 'target resolution is only valid for a legacy request without attempts'
                USING ERRCODE = '23514', CONSTRAINT = 'execution_resolution_target_legacy_only';
        END IF;
    ELSE
        SELECT run_id INTO stored_run_id
          FROM "request-fulfillment".execution_attempt
         WHERE request_id = NEW.request_id AND generation = NEW.generation
           AND tenant_id = NEW.tenant_id AND attempt_id = NEW.attempt_id AND run_id = NEW.verified_run_id
         FOR KEY SHARE;
        IF NOT FOUND OR stored_run_id IS DISTINCT FROM NEW.verified_run_id THEN
            RAISE EXCEPTION 'runtime resolution requires matching verified RunID'
                USING ERRCODE = '23514', CONSTRAINT = 'execution_resolution_run_identity';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER execution_resolution_subject_check
    BEFORE INSERT ON "request-fulfillment".execution_resolution
    FOR EACH ROW EXECUTE FUNCTION "request-fulfillment".validate_execution_resolution_subject();
