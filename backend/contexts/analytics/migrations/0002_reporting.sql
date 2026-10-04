-- schema: analytics
-- Миграция принадлежит схеме сервиса analytics (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Расширение проекции дашборда измерениями среза и фактами отчётности:
-- источник, отрасль, сегмент, кампания, менеджер, сроки доставки/перехода/
-- отписки/отказа/сделки и признаки качества базы. Столбцы аддитивны, чтобы
-- существующая проекция пережила миграцию без перезаписи. Текст ответов
-- компаний (reply_text) по-прежнему не хранится ни в одном столбце.

ALTER TABLE "analytics".company_projection
    ADD COLUMN IF NOT EXISTS source           text        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS industry         text        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS segment          text        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS campaign_id      text        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS manager_id       text        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS delivered_at     timestamptz,
    ADD COLUMN IF NOT EXISTS transitioned_at  timestamptz,
    ADD COLUMN IF NOT EXISTS unsubscribed_at  timestamptz,
    ADD COLUMN IF NOT EXISTS bounced_at       timestamptz,
    ADD COLUMN IF NOT EXISTS won_at           timestamptz,
    ADD COLUMN IF NOT EXISTS has_delivered    boolean     NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS has_transition   boolean     NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS has_unsubscribed boolean     NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS has_bounce       boolean     NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS has_won          boolean     NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS validity         text        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS duplicate        boolean     NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS pending_drafts   integer     NOT NULL DEFAULT 0;
