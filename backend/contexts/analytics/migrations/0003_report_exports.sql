-- schema: analytics
-- Миграция принадлежит схеме сервиса analytics (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Длительная выгрузка отчёта: таблица заданий экспорта в модели platform/jobs.
-- Строка задаёт жизненный цикл (queued → running → done/failed/cancelled) и хранит
-- непрозрачный Payload (зафиксированный фильтр среза), а также готовый файл —
-- тип, имя и тело. Результат отделён от общих колонок примитива, чтобы jobs.Store
-- оставался непрозрачным к содержимому. Порядок и имя колонок согласованы с
-- адаптером internal/adapters/persistence/postgres/export_store.go.

CREATE TABLE IF NOT EXISTS "analytics".report_export_job (
    id                  text        PRIMARY KEY,
    payload             bytea       NOT NULL,
    status              text        NOT NULL DEFAULT 'queued',
    error_text          text        NOT NULL DEFAULT '',
    error_code          text        NOT NULL DEFAULT '',
    attempts            integer     NOT NULL DEFAULT 0,
    cancel_requested    boolean     NOT NULL DEFAULT false,
    result_content_type text        NOT NULL DEFAULT '',
    result_filename     text        NOT NULL DEFAULT '',
    result_body         bytea,
    created_at          timestamptz NOT NULL,
    updated_at          timestamptz NOT NULL
);

-- Взятие из очереди идёт по старейшему queued-заданию, согласование сирот — по
-- активности running-заданий: индекс ускоряет оба запроса.
CREATE INDEX IF NOT EXISTS report_export_job_status_created_idx
    ON "analytics".report_export_job (status, created_at);
