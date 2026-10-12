-- schema: analytics
-- Миграция принадлежит схеме сервиса analytics (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Длительные операции консоли (web-frontend-architecture, срез 6): задание
-- выгрузки отчёта сообщает ход выполнения. Колонки согласованы с расширениями
-- примитива platform/jobs — jobs.ProgressStore (progress, step), а также
-- started_at для показа времени начала. Порядок и имена согласованы с адаптером
-- internal/adapters/persistence/postgres/export_store.go.

ALTER TABLE "analytics".report_export_job
    ADD COLUMN IF NOT EXISTS progress   integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS step       text    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS started_at timestamptz;

-- Видимость DLQ и список заданий (jobs.JobLister) упорядочены по активности:
-- индекс ускоряет отбор «failed с исчерпанными попытками».
CREATE INDEX IF NOT EXISTS report_export_job_status_attempts_idx
    ON "analytics".report_export_job (status, attempts);
