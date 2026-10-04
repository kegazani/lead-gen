-- schema: engagement-observation
-- Миграция принадлежит схеме сервиса engagement-observation (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Агрегат engagement-event хранится снимком (по строке на событие) с
-- денормализованными tenant_id и version: tenant_id обслуживает чтение событий
-- тенанта (ListByTenant), version — оптимистичную блокировку
-- (UPDATE ... WHERE version = $expected). Журнал дедупликации сигналов
-- (signal_ledger) хранит ключ обработанного входящего webhook, реестр
-- трекинг-ссылок (transition_link) — адрес назначения по непрозрачной ссылке.
-- Пиксельных метрик здесь нет по построению: контекст наблюдает только доставку,
-- переход и ответ.
--
-- Outbox хранит записи платформы в форме публикации (platform/messaging/outbox)
-- до подтверждения relay; публикация следует только за commit транзакции, иначе
-- transactional outbox потерял бы атомарность записи агрегата и события.

CREATE SCHEMA IF NOT EXISTS "engagement-observation";

CREATE TABLE IF NOT EXISTS "engagement-observation".engagement_event (
    id             text        PRIMARY KEY,
    signal_id      text        NOT NULL,
    tenant_id      text        NOT NULL,
    company_id     text        NOT NULL,
    kind           text        NOT NULL,
    confidence     text        NOT NULL,
    outcome        text        NOT NULL,
    link_ref       text        NOT NULL DEFAULT '',
    target         text        NOT NULL DEFAULT '',
    occurred_at    timestamptz NOT NULL,
    correlation_id text        NOT NULL,
    causation_id   text        NOT NULL DEFAULT '',
    version        integer     NOT NULL
);

-- Чтение событий тенанта упорядочено по идентификатору, поэтому индекс покрывает
-- и фильтр, и порядок выдачи.
CREATE INDEX IF NOT EXISTS engagement_event_tenant_idx
    ON "engagement-observation".engagement_event (tenant_id, id);

CREATE TABLE IF NOT EXISTS "engagement-observation".signal_ledger (
    key       text        PRIMARY KEY,
    marked_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS "engagement-observation".transition_link (
    ref        text PRIMARY KEY,
    target     text NOT NULL,
    tenant_id  text NOT NULL,
    company_id text NOT NULL
);

CREATE TABLE IF NOT EXISTS "engagement-observation".outbox (
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
    ON "engagement-observation".outbox (created_at, id) WHERE published_at IS NULL;
