-- schema: agent-orchestration
-- Миграция принадлежит схеме сервиса agent-orchestration (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Зелёная зона: версии пайплайна (agent_pipeline), запуски агента (agent_run) и
-- журнал transactional outbox (outbox). Пайплайн хранится как JSONB-снимок версии
-- с денормализованными tenant_id, version, published и revision: published
-- обслуживает выборку последней опубликованной версии, revision — оптимистичную
-- блокировку (UPDATE ... WHERE revision = $expected). Запуск хранится как
-- JSONB-снимок агрегата с денормализованными tenant_id, status и revision: status
-- обслуживает очередь супервизора и подсчёт занятых слотов, revision — оптимистичную
-- блокировку. Outbox хранит записи платформы в форме публикации
-- (platform/messaging/outbox) до подтверждения relay; публикация следует только за
-- commit транзакции. Все операторы идемпотентны: повторное применение не меняет
-- схему.

CREATE SCHEMA IF NOT EXISTS "agent-orchestration";

CREATE TABLE IF NOT EXISTS "agent-orchestration".agent_pipeline (
    pipeline_id text        NOT NULL,
    tenant_id   text        NOT NULL,
    version     integer     NOT NULL,
    published   boolean     NOT NULL DEFAULT false,
    revision    integer     NOT NULL,
    snapshot    jsonb       NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, pipeline_id, version)
);

-- Выборка последней опубликованной версии пайплайна тенанта: частичный индекс
-- покрывает фильтр по published.
CREATE INDEX IF NOT EXISTS agent_pipeline_published_idx
    ON "agent-orchestration".agent_pipeline (tenant_id, pipeline_id, version DESC)
    WHERE published;

CREATE TABLE IF NOT EXISTS "agent-orchestration".agent_run (
    id         text        PRIMARY KEY,
    tenant_id  text        NOT NULL,
    status     text        NOT NULL,
    revision   integer     NOT NULL,
    snapshot   jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Очередь супервизора читается по (тенант, статус) и упорядочена по идентификатору;
-- тот же индекс обслуживает подсчёт занятых слотов по активным статусам.
CREATE INDEX IF NOT EXISTS agent_run_status_idx
    ON "agent-orchestration".agent_run (tenant_id, status, id);

CREATE TABLE IF NOT EXISTS "agent-orchestration".outbox (
    id           text        PRIMARY KEY,
    exchange     text        NOT NULL,
    routing_key  text        NOT NULL,
    body         bytea       NOT NULL,
    headers      jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);

-- Частичный индекс обслуживает выдачу неподтверждённых записей в порядке записи.
CREATE INDEX IF NOT EXISTS agent_orchestration_outbox_pending_idx
    ON "agent-orchestration".outbox (created_at, id) WHERE published_at IS NULL;
