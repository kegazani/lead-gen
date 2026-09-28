-- schema: lead-management
-- Миграция принадлежит схеме сервиса lead-management (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Зелёная зона: карточка компании (lead_profile) и журнал заданий поиска
-- (search_job). Карточка хранится как JSONB-снимок агрегата с денормализованным
-- флагом hidden для витрины (ListVisible); задание — с текстовым запросом и
-- JSONB-ссылкой на найденные компании (result_ref).

CREATE SCHEMA IF NOT EXISTS "lead-management";

CREATE TABLE IF NOT EXISTS "lead-management".lead_profile (
    id         text        PRIMARY KEY,
    hidden     boolean     NOT NULL DEFAULT FALSE,
    snapshot   jsonb       NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS "lead-management".search_job (
    id         text        PRIMARY KEY,
    query      text        NOT NULL,
    status     text        NOT NULL,
    result_ref jsonb,
    error      text,
    attempts   integer     NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
