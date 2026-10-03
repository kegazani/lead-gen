-- schema: request-fulfillment
-- Миграция принадлежит схеме сервиса request-fulfillment (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Зелёная зона: агрегат заявки (request), черновики писем оператора (email_draft)
-- и журнал transactional outbox (outbox). Заявка хранится как JSONB-снимок
-- агрегата с денормализованными tenant_id, status и version: status обслуживает
-- чтение очередей (ListByStatus), version — оптимистичную блокировку
-- (UPDATE ... WHERE version = $expected). Черновик — производный материал
-- оператора, хранится построчно в порядке найденных лидов (position): повторный
-- черновик по тому же лиду заменяет прежний, сохраняя место в порядке.
-- Outbox хранит записи платформы в форме публикации (platform/messaging/outbox)
-- до подтверждения relay; публикация следует только за commit транзакции.

CREATE SCHEMA IF NOT EXISTS "request-fulfillment";

CREATE TABLE IF NOT EXISTS "request-fulfillment".request (
    id         text        PRIMARY KEY,
    tenant_id  text        NOT NULL,
    status     text        NOT NULL,
    version    integer     NOT NULL,
    snapshot   jsonb       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Очереди экрана (валидация — submitted) читаются по статусу и упорядочены по
-- идентификатору, поэтому индекс покрывает и фильтр, и порядок выдачи.
CREATE INDEX IF NOT EXISTS request_status_idx
    ON "request-fulfillment".request (status, id);

CREATE TABLE IF NOT EXISTS "request-fulfillment".email_draft (
    request_id text    NOT NULL,
    tenant_id  text    NOT NULL,
    lead_id    text    NOT NULL,
    subject    text    NOT NULL,
    body       text    NOT NULL,
    language   text    NOT NULL DEFAULT '',
    position   integer NOT NULL,
    PRIMARY KEY (request_id, lead_id)
);

-- Чтение черновиков заявки изолировано тенантом и упорядочено найденными лидами.
CREATE INDEX IF NOT EXISTS email_draft_request_idx
    ON "request-fulfillment".email_draft (request_id, tenant_id, position);

CREATE TABLE IF NOT EXISTS "request-fulfillment".outbox (
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
    ON "request-fulfillment".outbox (created_at, id) WHERE published_at IS NULL;
