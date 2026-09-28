-- schema: lead-management
-- Гарантии переходов состояния задания поиска: допустимые значения статуса
-- фиксируются ограничением CHECK, а выбор задания из очереди поддерживается
-- частичным индексом по queued-заданиям. Терминальные состояния защищены от
-- перезаписи прикладным слоем (охраняемый переход); CHECK закрывает попадание
-- неизвестного значения состояния в данные.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint c
        JOIN pg_class t ON t.oid = c.conrelid
        JOIN pg_namespace n ON n.oid = t.relnamespace
        WHERE c.conname = 'search_job_status_check'
          AND t.relname = 'search_job'
          AND n.nspname = 'lead-management'
    ) THEN
        ALTER TABLE "lead-management".search_job
            ADD CONSTRAINT search_job_status_check
            CHECK (status IN ('queued', 'running', 'done', 'failed', 'cancelled'));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS search_job_queue_idx
    ON "lead-management".search_job (created_at, id)
    WHERE status = 'queued';
