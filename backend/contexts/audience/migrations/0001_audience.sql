-- schema: audience
-- Миграция принадлежит схеме сервиса audience (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Агрегаты audience-segment и audience-snapshot сохраняются снимком целиком в
-- колонке JSONB: снимок — единица записи агрегата, а восстановление идёт через
-- domain.Rehydrate*. Денормализованные колонки обслуживают запросы порта, не
-- заставляя читать JSONB: tenant_id и version — оптимистичную блокировку и
-- выборку сегментов тенанта (ListByTenant/List), segment_id снимка — выборку
-- снимков сегмента (ListBySegment). Outbox хранит записи платформы в форме
-- публикации (platform/messaging/outbox) до подтверждения relay; публикация
-- следует только за commit транзакции, а запись outbox идёт в той же транзакции,
-- что и снимок агрегата (transactional outbox).

CREATE SCHEMA IF NOT EXISTS "audience";

CREATE TABLE IF NOT EXISTS "audience".segment (
    id         text        PRIMARY KEY,
    tenant_id  text        NOT NULL,
    version    integer     NOT NULL,
    snapshot   jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Сегменты тенанта читаются по tenant_id и упорядочены по идентификатору, а
-- регулярный пересчёт (List) обходит таблицу по первичному ключу.
CREATE INDEX IF NOT EXISTS segment_tenant_idx
    ON "audience".segment (tenant_id, id);

CREATE TABLE IF NOT EXISTS "audience".audience_snapshot (
    id         text        PRIMARY KEY,
    tenant_id  text        NOT NULL,
    segment_id text        NOT NULL,
    version    bigint      NOT NULL,
    snapshot   jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Снимки сегмента читаются по segment_id и упорядочены по идентификатору.
CREATE INDEX IF NOT EXISTS audience_snapshot_segment_idx
    ON "audience".audience_snapshot (segment_id, id);

CREATE TABLE IF NOT EXISTS "audience".outbox (
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
    ON "audience".outbox (created_at, id) WHERE published_at IS NULL;
