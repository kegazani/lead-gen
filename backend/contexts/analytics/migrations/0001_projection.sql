-- schema: analytics
-- Миграция принадлежит схеме сервиса analytics (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Проекция дашборда хранится снимком состояния компании: строка описывает
-- стадию воронки и отметки времени, а не поток событий. Текст ответов компаний
-- (reply_text) не хранится ни в одном столбце — маскирование обеспечивается
-- носителем состояния, а не только кодом чтения. Ключ (tenant_id, company_id)
-- делает запись идемпотентной: повторное применение события заменяет снимок.

CREATE SCHEMA IF NOT EXISTS "analytics";

CREATE TABLE IF NOT EXISTS "analytics".company_projection (
    tenant_id  text        NOT NULL,
    company_id text        NOT NULL,
    stage      text        NOT NULL DEFAULT '',
    found_at   timestamptz,
    sent_at    timestamptz,
    replied_at timestamptz,
    has_sent   boolean     NOT NULL DEFAULT false,
    has_reply  boolean     NOT NULL DEFAULT false,
    PRIMARY KEY (tenant_id, company_id)
);
