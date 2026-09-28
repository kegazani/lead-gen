-- schema: lead-management
-- Управление заданиями поиска: бюджет кредитов на запрос (max_credits), признак
-- запроса отмены (cancel_requested) и наблюдаемый прогресс (progress, JSONB-снимок).
-- Колонки nullable или с дефолтом: существующие задания читаются со значением по
-- умолчанию (бюджет 0 — из конфигурации, отмена не запрошена, прогресса нет).

ALTER TABLE "lead-management".search_job
    ADD COLUMN IF NOT EXISTS max_credits integer,
    ADD COLUMN IF NOT EXISTS cancel_requested boolean NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS progress jsonb;
