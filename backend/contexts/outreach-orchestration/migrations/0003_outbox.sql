-- schema: outreach-orchestration
-- Миграция принадлежит схеме сервиса outreach-orchestration (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Таблица outbox хранит доменные события, записанные атомарно вместе с агрегатом
-- campaign в одной транзакции (transactional outbox). До подтверждения брокером
-- запись остаётся неопубликованной и переживает рестарт процесса, поэтому relay
-- доставляет событие «как минимум один раз»; получатель идемпотентен по event_id.
-- Событие хранится проводимой JSON-формой (payload) и именем-дискриминатором
-- (event_name): value objects домена с закрытыми полями восстанавливаются
-- конструкторами при чтении (см. adapters/persistence/postgres/events.go).

CREATE TABLE IF NOT EXISTS "outreach-orchestration".outbox (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_name   text        NOT NULL,
    payload      jsonb       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);

-- Индекс обслуживает выборку неподтверждённых записей в порядке записи.
CREATE INDEX IF NOT EXISTS outbox_pending_idx
    ON "outreach-orchestration".outbox (id)
    WHERE published_at IS NULL;
