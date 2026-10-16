-- schema: request-fulfillment
-- Способ исполнения входит в тот же JSON snapshot/version CAS, что и одобрение.
-- Историческое одобрение сохраняет прежний путь и не разрешает второй бюджет.
UPDATE "request-fulfillment".request
SET snapshot=jsonb_set(snapshot,'{ExecutionMode}','"legacy"'::jsonb,true)
WHERE COALESCE(snapshot->>'ExecutionMode','')=''
  AND (snapshot->'ValidationDecision'->>'Outcome'='approved'
       OR status IN ('validated','processing','fulfilled','failed'));

ALTER TABLE "request-fulfillment".request
ADD CONSTRAINT request_execution_mode_known CHECK (
 COALESCE(snapshot->>'ExecutionMode','') IN ('','legacy','research')
 AND ((snapshot->>'ExecutionMode' IS DISTINCT FROM 'research')
      OR COALESCE((jsonb_typeof(snapshot->'ResearchSelection')='object'
          AND snapshot->'ResearchSelection'->>'Unit' IN ('company','company-with-contact','email-ready')),false))
);

-- Старый бинарник не может стереть выбранный research mode при записи снимка.
CREATE OR REPLACE FUNCTION "request-fulfillment".protect_research_execution_mode()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.snapshot->>'ExecutionMode'='research' THEN
  IF NEW.snapshot->>'ExecutionMode' IS DISTINCT FROM 'research'
     OR NEW.snapshot->'ResearchSelection' IS DISTINCT FROM OLD.snapshot->'ResearchSelection' THEN
   RAISE EXCEPTION 'request research mode and approved selection are immutable' USING ERRCODE='23514';
  END IF;
 ELSIF COALESCE(OLD.snapshot->>'ExecutionMode','')='legacy'
       AND NEW.snapshot->>'ExecutionMode'='research' THEN
  RAISE EXCEPTION 'request legacy mode cannot become research' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER protect_research_execution_mode
BEFORE UPDATE ON "request-fulfillment".request
FOR EACH ROW EXECUTE FUNCTION "request-fulfillment".protect_research_execution_mode();
