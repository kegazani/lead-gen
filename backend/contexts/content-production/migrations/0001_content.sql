-- schema: content-production
-- Миграция принадлежит схеме сервиса content-production (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Агрегаты content-package и render-job сохраняются целиком как JSONB-снимки
-- (снимок содержит версии, наполнение лендинга, документ и артефакт), поэтому
-- снимок хранится в одной колонке и меняется вместе с кодом снимка. Колонки
-- version и state денормализованы: version обслуживает оптимистичную блокировку
-- (UPDATE ... WHERE version = $expected), state — выборку очереди рендеринга
-- (ListPending). Outbox хранит записи платформы в форме публикации
-- (platform/messaging/outbox) до подтверждения relay; публикация следует только
-- за commit транзакции.

CREATE SCHEMA IF NOT EXISTS "content-production";

CREATE TABLE IF NOT EXISTS "content-production".content_package (
    id         text        PRIMARY KEY,
    tenant_id  text        NOT NULL,
    version    integer     NOT NULL,
    snapshot   jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS "content-production".render_job (
    id         text        PRIMARY KEY,
    tenant_id  text        NOT NULL,
    state      text        NOT NULL,
    version    integer     NOT NULL,
    snapshot   jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Очередь рендеринга (requested, rendering) читается по состоянию и упорядочена
-- по идентификатору, поэтому индекс покрывает и фильтр, и порядок выдачи.
CREATE INDEX IF NOT EXISTS render_job_pending_idx
    ON "content-production".render_job (state, id);

CREATE TABLE IF NOT EXISTS "content-production".outbox (
    id           text        PRIMARY KEY,
    exchange     text        NOT NULL,
    routing_key  text        NOT NULL,
    body         bytea       NOT NULL,
    headers      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);

-- Частичный индекс обслуживает выдачу неподтверждённых записей в порядке записи.
CREATE INDEX IF NOT EXISTS outbox_pending_idx
    ON "content-production".outbox (created_at, id) WHERE published_at IS NULL;
