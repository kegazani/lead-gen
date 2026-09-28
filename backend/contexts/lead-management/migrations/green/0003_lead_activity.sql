-- schema: lead-management
-- Миграция принадлежит схеме сервиса lead-management (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Зелёная зона: операционная история переходов лида между стадиями воронки
-- (lead_activity). Это запись app-слоя (как search_job), а не доменный агрегат:
-- фиксируются время, автор и целевая стадия перехода.

CREATE TABLE IF NOT EXISTS "lead-management".lead_activity (
    id      text        PRIMARY KEY,
    lead_id text        NOT NULL,
    stage   text        NOT NULL,
    author  text        NOT NULL,
    at      timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS lead_activity_lead_id_idx
    ON "lead-management".lead_activity (lead_id, at);
