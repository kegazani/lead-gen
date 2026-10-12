-- schema: request-fulfillment
-- 0005/0006 are immutable. Restore TRUNCATE protection on the retained legacy
-- table and tie revision rows to their request and tenant without deleting data.

DO $$
BEGIN
    IF to_regclass('"request-fulfillment".execution_resolution') IS NOT NULL
       AND EXISTS (
           SELECT 1
             FROM "request-fulfillment".execution_resolution r
             LEFT JOIN "request-fulfillment".request q ON q.id = r.request_id
            WHERE q.id IS NULL OR q.tenant_id IS DISTINCT FROM r.tenant_id
       ) THEN
        RAISE EXCEPTION 'execution_resolution contains missing request or tenant mismatch; repair data before migration'
            USING ERRCODE = '23503';
    END IF;

    IF EXISTS (
        SELECT 1
          FROM "request-fulfillment".execution_resolution_revision r
          LEFT JOIN "request-fulfillment".request q ON q.id = r.request_id
         WHERE q.id IS NULL OR q.tenant_id IS DISTINCT FROM r.tenant_id
    ) THEN
        RAISE EXCEPTION 'execution_resolution_revision contains missing request or tenant mismatch; repair data before migration'
            USING ERRCODE = '23503';
    END IF;
END;
$$;

ALTER TABLE "request-fulfillment".request
    ADD CONSTRAINT request_id_tenant_unique UNIQUE (id, tenant_id);

ALTER TABLE "request-fulfillment".execution_resolution_revision
    ADD CONSTRAINT execution_resolution_revision_request_tenant_fk
    FOREIGN KEY (request_id, tenant_id)
    REFERENCES "request-fulfillment".request (id, tenant_id)
    ON DELETE RESTRICT;

CREATE FUNCTION "request-fulfillment".reject_legacy_execution_resolution_truncate()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'execution_resolution is append-only' USING ERRCODE = '55000';
END;
$$;

DO $$
BEGIN
    IF to_regclass('"request-fulfillment".execution_resolution') IS NOT NULL THEN
        EXECUTE 'DROP TRIGGER IF EXISTS execution_resolution_no_truncate ON "request-fulfillment".execution_resolution';
        EXECUTE 'CREATE TRIGGER execution_resolution_no_truncate
                 BEFORE TRUNCATE ON "request-fulfillment".execution_resolution
                 FOR EACH STATEMENT
                 EXECUTE FUNCTION "request-fulfillment".reject_legacy_execution_resolution_truncate()';
    END IF;
END;
$$;

-- Triggers do not protect against the table owner or a superuser. Runtime-role
-- privileges are not configured by this migration model and are not asserted.
