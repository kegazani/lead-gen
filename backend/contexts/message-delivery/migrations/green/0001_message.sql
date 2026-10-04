-- schema: message-delivery
-- Миграция принадлежит схеме сервиса message-delivery (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Состояние контекста: сообщения (message), репутация отправляющих доменов
-- (sender_reputation) и журнал transactional outbox (outbox). Снимок агрегата
-- хранится в JSONB, а денормализованные колонки обслуживают запросы:
--   * idempotency_key — уникальный ключ повторной команды;
--   * status, last_outcome, attempts_count — выборка сообщений для повторных
--     окон (role schedule) без разбора JSONB;
--   * sender_domain — репутация домена.
-- Outbox хранит доменные события до подтверждения публикации; запись события
-- атомарна с изменением агрегата (transactional outbox).

CREATE SCHEMA IF NOT EXISTS "message-delivery";

CREATE TABLE IF NOT EXISTS "message-delivery".message (
    id                 text        PRIMARY KEY,
    tenant_id          text        NOT NULL,
    approved_action_id text        NOT NULL,
    idempotency_key    text        NOT NULL,
    sender_domain      text        NOT NULL,
    status             text        NOT NULL,
    attempts_count     integer     NOT NULL DEFAULT 0,
    last_outcome       text        NOT NULL DEFAULT '',
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL DEFAULT now(),
    snapshot           jsonb       NOT NULL
);

-- Ключ идемпотентности уникален: повторная команда с тем же ключом не создаёт
-- второго сообщения и второго внешнего эффекта.
CREATE UNIQUE INDEX IF NOT EXISTS message_idempotency_key_idx
    ON "message-delivery".message (idempotency_key);

-- Выборка сообщений с временным исходом для повторных окон.
CREATE INDEX IF NOT EXISTS message_retry_idx
    ON "message-delivery".message (status, last_outcome, created_at);

CREATE TABLE IF NOT EXISTS "message-delivery".sender_reputation (
    domain       text        PRIMARY KEY,
    delivered    integer     NOT NULL DEFAULT 0,
    hard_bounces integer     NOT NULL DEFAULT 0,
    soft_bounces integer     NOT NULL DEFAULT 0,
    updated_at   timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS "message-delivery".outbox (
    id           text        PRIMARY KEY,
    event_name   text        NOT NULL,
    payload      jsonb       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);

-- Частичный индекс обслуживает выдачу неподтверждённых записей в порядке записи.
CREATE INDEX IF NOT EXISTS outbox_pending_idx
    ON "message-delivery".outbox (created_at, id) WHERE published_at IS NULL;
