-- schema: lead-management
-- Статусная модель задания поиска: машинный код отказа (error_code) и
-- персистентный журнал событий жизненного цикла (search_job_event).
--
-- Колонка error_code nullable: старые задания и задания без кода хранят NULL.
-- search_job_event — append-only операционная запись (как search_job), не доменный
-- агрегат; событие несёт идентификатор задания, тип, код (для error/failed) и
-- JSONB-снимок счётчиков (для phase/terminal).

ALTER TABLE "lead-management".search_job
    ADD COLUMN IF NOT EXISTS error_code text;

CREATE TABLE IF NOT EXISTS "lead-management".search_job_event (
    seq        bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id     text        NOT NULL,
    type       text        NOT NULL,
    code       text,
    payload    jsonb,
    created_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS search_job_event_job_id_idx
    ON "lead-management".search_job_event (job_id, seq);
