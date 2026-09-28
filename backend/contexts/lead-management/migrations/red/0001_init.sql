-- schema: lead-management
-- Миграция принадлежит схеме сервиса lead-management (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Красная зона: контакт (contact). Контакт хранится как JSONB-снимок агрегата
-- с денормализованным company_id для выборки контактов компании (ListByCompany).

CREATE SCHEMA IF NOT EXISTS "lead-management";

CREATE TABLE IF NOT EXISTS "lead-management".contact (
    id         text        PRIMARY KEY,
    company_id text        NOT NULL,
    snapshot   jsonb       NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
