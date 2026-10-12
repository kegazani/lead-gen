-- schema: request-fulfillment
-- New immutable revision chain. The legacy 0005 table is retained untouched;
-- its runtime rows are observations only and are never terminal evidence.
CREATE TABLE "request-fulfillment".execution_resolution_revision (
    resolution_id text PRIMARY KEY CHECK (btrim(resolution_id) <> ''),
    tenant_id text NOT NULL CHECK (btrim(tenant_id) <> ''),
    request_id text NOT NULL CHECK (btrim(request_id) <> ''),
    resolution_kind text NOT NULL CHECK (resolution_kind IN ('target','runtime')),
    revision bigint NOT NULL CHECK (revision > 0),
    expected_revision bigint NOT NULL CHECK (expected_revision >= 0),
    supersedes_id text,
    generation bigint,
    attempt_id text,
    target_state text,
    lead_target integer,
    runtime_outcome text,
    observed_run_id text,
    actor text NOT NULL CHECK (btrim(actor) <> ''),
    resolved_at timestamptz NOT NULL,
    evidence_fingerprint text NOT NULL CHECK (evidence_fingerprint ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (revision = expected_revision + 1),
    CHECK ((expected_revision = 0 AND supersedes_id IS NULL) OR
           (expected_revision > 0 AND supersedes_id IS NOT NULL AND btrim(supersedes_id) <> '')),
    CHECK (
      (resolution_kind='target' AND generation IS NULL AND attempt_id IS NULL
       AND target_state='explicit' AND lead_target IS NOT NULL
       AND runtime_outcome IS NULL AND observed_run_id IS NULL)
      OR
      (resolution_kind='runtime' AND generation > 0 AND attempt_id IS NOT NULL
       AND btrim(attempt_id) <> '' AND target_state IS NULL AND lead_target IS NULL
       AND runtime_outcome IN ('completed','failed','canceled')
       AND observed_run_id IS NOT NULL AND btrim(observed_run_id) <> '')
    )
);

CREATE UNIQUE INDEX execution_resolution_revision_chain_idx
    ON "request-fulfillment".execution_resolution_revision
       (tenant_id, request_id, resolution_kind, revision);
CREATE INDEX execution_resolution_revision_subject_idx
    ON "request-fulfillment".execution_resolution_revision
       (tenant_id, request_id, resolution_kind, revision DESC);

CREATE FUNCTION "request-fulfillment".validate_execution_resolution_revision()
RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    current_revision bigint;
    current_id text;
    stored_tenant text;
    stored_run_id text;
BEGIN
    -- Serialize writers for the same subject/kind. Hash collisions only
    -- serialize unrelated writes; they cannot weaken the CAS.
    PERFORM pg_advisory_xact_lock(hashtextextended(NEW.tenant_id || ':' || NEW.request_id || ':' || NEW.resolution_kind, 0));
    SELECT tenant_id INTO stored_tenant FROM "request-fulfillment".request
      WHERE id=NEW.request_id FOR KEY SHARE;
    IF stored_tenant IS DISTINCT FROM NEW.tenant_id THEN
        RAISE EXCEPTION 'execution resolution tenant mismatch' USING ERRCODE='23514';
    END IF;
    IF NEW.resolution_kind='target' AND EXISTS (
       SELECT 1 FROM "request-fulfillment".execution_attempt WHERE request_id=NEW.request_id
    ) THEN
        RAISE EXCEPTION 'target resolution only applies to legacy requests' USING ERRCODE='23514';
    END IF;
    IF NEW.resolution_kind='runtime' THEN
       SELECT run_id INTO stored_run_id FROM "request-fulfillment".execution_attempt
        WHERE tenant_id=NEW.tenant_id AND request_id=NEW.request_id
          AND generation=NEW.generation AND attempt_id=NEW.attempt_id
        FOR KEY SHARE;
       IF NOT FOUND OR stored_run_id IS DISTINCT FROM NEW.observed_run_id THEN
          RAISE EXCEPTION 'runtime observation subject mismatch' USING ERRCODE='23514';
       END IF;
    END IF;
    SELECT revision,resolution_id INTO current_revision,current_id
      FROM "request-fulfillment".execution_resolution_revision
     WHERE tenant_id=NEW.tenant_id AND request_id=NEW.request_id
       AND resolution_kind=NEW.resolution_kind
     ORDER BY revision DESC LIMIT 1;
    IF NOT FOUND THEN
        current_revision := 0;
        current_id := NULL;
    END IF;
    IF NEW.expected_revision <> current_revision OR NEW.revision <> current_revision + 1
       OR NEW.supersedes_id IS DISTINCT FROM current_id THEN
        RAISE EXCEPTION 'execution resolution revision CAS conflict' USING ERRCODE='40001';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER execution_resolution_revision_check
    BEFORE INSERT ON "request-fulfillment".execution_resolution_revision
    FOR EACH ROW EXECUTE FUNCTION "request-fulfillment".validate_execution_resolution_revision();

CREATE FUNCTION "request-fulfillment".reject_execution_resolution_revision_mutation()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'execution_resolution_revision is append-only' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER execution_resolution_revision_immutable
    BEFORE UPDATE OR DELETE ON "request-fulfillment".execution_resolution_revision
    FOR EACH ROW EXECUTE FUNCTION "request-fulfillment".reject_execution_resolution_revision_mutation();
CREATE TRIGGER execution_resolution_revision_no_truncate
    BEFORE TRUNCATE ON "request-fulfillment".execution_resolution_revision
    FOR EACH STATEMENT EXECUTE FUNCTION "request-fulfillment".reject_execution_resolution_revision_mutation();

-- Carry forward prior audit rows as append-only revisions. Historical runtime
-- rows are observations regardless of their old column names/fingerprint.
WITH ranked AS (
    SELECT r.*, row_number() OVER (
        PARTITION BY tenant_id,request_id,resolution_kind
        ORDER BY created_at,resolution_id
    ) AS new_revision,
    lag(resolution_id) OVER (
        PARTITION BY tenant_id,request_id,resolution_kind
        ORDER BY created_at,resolution_id
    ) AS prior_id
    FROM "request-fulfillment".execution_resolution r
)
INSERT INTO "request-fulfillment".execution_resolution_revision
(resolution_id,tenant_id,request_id,resolution_kind,revision,expected_revision,supersedes_id,
 generation,attempt_id,target_state,lead_target,runtime_outcome,observed_run_id,actor,resolved_at,evidence_fingerprint)
SELECT resolution_id,tenant_id,request_id,resolution_kind,new_revision,new_revision-1,prior_id,
       generation,attempt_id,target_state,lead_target,runtime_outcome,verified_run_id,actor,resolved_at,evidence_fingerprint
FROM ranked ORDER BY tenant_id,request_id,resolution_kind,new_revision;

CREATE FUNCTION "request-fulfillment".reject_legacy_execution_resolution_insert()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'write execution_resolution_revision with expected_revision CAS' USING ERRCODE='55000';
END;
$$;
CREATE TRIGGER execution_resolution_legacy_insert_rejected
    BEFORE INSERT ON "request-fulfillment".execution_resolution
    FOR EACH ROW EXECUTE FUNCTION "request-fulfillment".reject_legacy_execution_resolution_insert();

-- Triggers do not protect against the table owner or a superuser. Deployment
-- must grant runtime roles table DML only (no TRUNCATE/DDL); role provisioning
-- is outside this migration model and must be enforced by deployment.
