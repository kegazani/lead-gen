-- schema: request-fulfillment
-- Reassert resolution guards after 0005-0007 without rewriting those migrations.
-- Existing rows are validated before adding constraints; no audit data is removed.

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

DO $$
DECLARE
    request_table regclass := '"request-fulfillment".request'::regclass;
    revision_table regclass := '"request-fulfillment".execution_resolution_revision'::regclass;
    request_id_att smallint;
    tenant_id_att smallint;
    revision_request_id_att smallint;
    revision_tenant_id_att smallint;
BEGIN
    SELECT attnum::smallint INTO request_id_att FROM pg_attribute
     WHERE attrelid=request_table AND attname='id' AND NOT attisdropped;
    SELECT attnum::smallint INTO tenant_id_att FROM pg_attribute
     WHERE attrelid=request_table AND attname='tenant_id' AND NOT attisdropped;
    SELECT attnum::smallint INTO revision_request_id_att FROM pg_attribute
     WHERE attrelid=revision_table AND attname='request_id' AND NOT attisdropped;
    SELECT attnum::smallint INTO revision_tenant_id_att FROM pg_attribute
     WHERE attrelid=revision_table AND attname='tenant_id' AND NOT attisdropped;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid=request_table AND conname='request_id_tenant_unique'
           AND contype='u'
           AND conkey=ARRAY[request_id_att,tenant_id_att]::smallint[]
    ) THEN
        IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=request_table AND conname='request_id_tenant_unique') THEN
            RAISE EXCEPTION 'request_id_tenant_unique exists with an unexpected definition';
        END IF;
        ALTER TABLE "request-fulfillment".request
            ADD CONSTRAINT request_id_tenant_unique UNIQUE (id, tenant_id);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conrelid=revision_table AND conname='execution_resolution_revision_request_tenant_fk'
           AND contype='f'
           AND confrelid=request_table
           AND confdeltype='r'
           AND conkey=ARRAY[revision_request_id_att,revision_tenant_id_att]::smallint[]
           AND confkey=ARRAY[request_id_att,tenant_id_att]::smallint[]
    ) THEN
        IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=revision_table AND conname='execution_resolution_revision_request_tenant_fk') THEN
            RAISE EXCEPTION 'execution_resolution_revision_request_tenant_fk exists with an unexpected definition';
        END IF;
        ALTER TABLE "request-fulfillment".execution_resolution_revision
            ADD CONSTRAINT execution_resolution_revision_request_tenant_fk
            FOREIGN KEY (request_id, tenant_id)
            REFERENCES "request-fulfillment".request (id, tenant_id)
            ON DELETE RESTRICT;
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION "request-fulfillment".reject_execution_resolution_truncate()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE = '55000';
END;
$$;

DO $$
BEGIN
    IF to_regclass('"request-fulfillment".execution_resolution') IS NOT NULL THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_trigger
             WHERE tgrelid='"request-fulfillment".execution_resolution'::regclass
               AND tgname='execution_resolution_no_truncate' AND NOT tgisinternal
        ) THEN
            EXECUTE 'CREATE TRIGGER execution_resolution_no_truncate
                     BEFORE TRUNCATE ON "request-fulfillment".execution_resolution
                     FOR EACH STATEMENT
                     EXECUTE FUNCTION "request-fulfillment".reject_execution_resolution_truncate()';
        END IF;
        IF EXISTS (
            SELECT 1 FROM pg_trigger
             WHERE tgrelid='"request-fulfillment".execution_resolution'::regclass
               AND tgname='execution_resolution_no_truncate' AND tgenabled NOT IN ('O','A')
        ) THEN
            EXECUTE 'ALTER TABLE "request-fulfillment".execution_resolution ENABLE TRIGGER execution_resolution_no_truncate';
        END IF;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
         WHERE tgrelid='"request-fulfillment".execution_resolution_revision'::regclass
           AND tgname='execution_resolution_revision_no_truncate' AND NOT tgisinternal
    ) THEN
        EXECUTE 'CREATE TRIGGER execution_resolution_revision_no_truncate
                 BEFORE TRUNCATE ON "request-fulfillment".execution_resolution_revision
                 FOR EACH STATEMENT
                 EXECUTE FUNCTION "request-fulfillment".reject_execution_resolution_truncate()';
    END IF;
    IF EXISTS (
        SELECT 1 FROM pg_trigger
         WHERE tgrelid='"request-fulfillment".execution_resolution_revision'::regclass
           AND tgname='execution_resolution_revision_no_truncate' AND tgenabled NOT IN ('O','A')
    ) THEN
        EXECUTE 'ALTER TABLE "request-fulfillment".execution_resolution_revision ENABLE TRIGGER execution_resolution_revision_no_truncate';
    END IF;
END;
$$;

-- Triggers do not restrict the table owner or a superuser. Runtime-role grants
-- are a separate infrastructure concern and are not changed or asserted here.
