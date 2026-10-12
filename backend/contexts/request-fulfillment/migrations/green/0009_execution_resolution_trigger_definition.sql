-- schema: request-fulfillment
-- 0008 checked trigger names. Rebuild any missing/misdefined trigger so the
-- catalog proves BEFORE STATEMENT TRUNCATE calls the rejecting function.

CREATE OR REPLACE FUNCTION "request-fulfillment".reject_execution_resolution_truncate()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME USING ERRCODE = '55000';
END;
$$;

DO $$
DECLARE
    target_table regclass;
    trigger_type smallint;
    trigger_function oid;
    trigger_enabled "char";
BEGIN
    target_table := to_regclass('"request-fulfillment".execution_resolution');
    IF target_table IS NOT NULL THEN
        SELECT tgtype, tgfoid, tgenabled
          INTO trigger_type, trigger_function, trigger_enabled
          FROM pg_trigger
         WHERE tgrelid=target_table AND tgname='execution_resolution_no_truncate'
           AND NOT tgisinternal;
        IF NOT FOUND OR trigger_type <> 34
           OR trigger_function <> '"request-fulfillment".reject_execution_resolution_truncate()'::regprocedure::oid THEN
            EXECUTE 'DROP TRIGGER IF EXISTS execution_resolution_no_truncate ON "request-fulfillment".execution_resolution';
            EXECUTE 'CREATE TRIGGER execution_resolution_no_truncate
                     BEFORE TRUNCATE ON "request-fulfillment".execution_resolution
                     FOR EACH STATEMENT
                     EXECUTE FUNCTION "request-fulfillment".reject_execution_resolution_truncate()';
            trigger_enabled := 'O';
        END IF;
        IF trigger_enabled NOT IN ('O','A') THEN
            EXECUTE 'ALTER TABLE "request-fulfillment".execution_resolution ENABLE TRIGGER execution_resolution_no_truncate';
        END IF;
    END IF;

    target_table := '"request-fulfillment".execution_resolution_revision'::regclass;
    SELECT tgtype, tgfoid, tgenabled
      INTO trigger_type, trigger_function, trigger_enabled
      FROM pg_trigger
     WHERE tgrelid=target_table AND tgname='execution_resolution_revision_no_truncate'
       AND NOT tgisinternal;
    IF NOT FOUND OR trigger_type <> 34
       OR trigger_function <> '"request-fulfillment".reject_execution_resolution_truncate()'::regprocedure::oid THEN
        EXECUTE 'DROP TRIGGER IF EXISTS execution_resolution_revision_no_truncate ON "request-fulfillment".execution_resolution_revision';
        EXECUTE 'CREATE TRIGGER execution_resolution_revision_no_truncate
                 BEFORE TRUNCATE ON "request-fulfillment".execution_resolution_revision
                 FOR EACH STATEMENT
                 EXECUTE FUNCTION "request-fulfillment".reject_execution_resolution_truncate()';
        trigger_enabled := 'O';
    END IF;
    IF trigger_enabled NOT IN ('O','A') THEN
        EXECUTE 'ALTER TABLE "request-fulfillment".execution_resolution_revision ENABLE TRIGGER execution_resolution_revision_no_truncate';
    END IF;
END;
$$;

-- Triggers do not restrict the table owner or a superuser; role grants belong
-- to the separate service-per-context infrastructure change.
