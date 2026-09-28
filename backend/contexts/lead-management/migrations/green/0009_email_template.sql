-- schema: lead-management
-- Миграция принадлежит схеме сервиса lead-management (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Зелёная зона: библиотека шаблонов письма (email_template). Шаблон хранится как
-- JSONB-снимок агрегата email-template (имя, тема, тело, объявленные переменные,
-- ревизия); денормализованы name (порядок выдачи списка) и revision (ссылка на
-- использованную редакцию). Текст шаблона и объявленные переменные — данные о
-- компании и письме, а не персональные данные физлица, поэтому зона зелёная.
-- Backfill не требуется: персистентных шаблонов до этой миграции не было.

CREATE TABLE IF NOT EXISTS "lead-management".email_template (
    id         text        PRIMARY KEY,
    name       text        NOT NULL,
    revision   integer     NOT NULL,
    snapshot   jsonb       NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS email_template_name_idx
    ON "lead-management".email_template (name, id);
