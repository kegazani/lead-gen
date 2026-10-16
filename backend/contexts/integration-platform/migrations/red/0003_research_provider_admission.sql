-- schema: integration-platform
-- Резерв локального cap и допуск по свежему provider balance различаются.
-- Флаг ставится до платного HTTP в короткой транзакции общего account.
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM information_schema.columns
  WHERE table_schema='integration-platform' AND table_name='research_acquisition_attempt'
   AND column_name='provider_admitted') THEN
  ALTER TABLE "integration-platform".research_acquisition_attempt
   ADD COLUMN IF NOT EXISTS provider_admitted boolean NOT NULL DEFAULT false;
  -- Незавершённые вызовы предыдущей версии могли уже уйти провайдеру.
  UPDATE "integration-platform".research_acquisition_attempt
   SET provider_admitted=true WHERE state IN ('running','unknown');
 END IF;
END $$;
