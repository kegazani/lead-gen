-- schema: platform-governance
-- Миграция принадлежит схеме сервиса platform-governance (граница данных —
-- schema-per-service). Расширяет схему контекста до полной модели доступа:
-- состояние и версия тенанта, наборы ролей пользователей, журнал аудита и
-- transactional outbox с ключами идемпотентности команд.

ALTER TABLE "platform-governance".tenant
    ADD COLUMN IF NOT EXISTS state text NOT NULL DEFAULT 'active';
ALTER TABLE "platform-governance".tenant
    ADD COLUMN IF NOT EXISTS version bigint NOT NULL DEFAULT 0;

-- Набор ролей пользователя неизменяем: хранится массивом. Старая одиночная
-- колонка role переносится в массив и удаляется.
ALTER TABLE "platform-governance".tenant_user
    ADD COLUMN IF NOT EXISTS roles text[] NOT NULL DEFAULT '{}';
UPDATE "platform-governance".tenant_user
    SET roles = ARRAY[role]
    WHERE roles = '{}' AND role IS NOT NULL AND role <> '';
ALTER TABLE "platform-governance".tenant_user
    DROP COLUMN IF EXISTS role;

-- Запись аудита неизменяема: подчинена тенанту, порядок задаёт position.
-- correction_of пуст для исходной записи и ссылается на исходную у корректирующей.
CREATE TABLE IF NOT EXISTS "platform-governance".audit_record (
    tenant_id     text        NOT NULL REFERENCES "platform-governance".tenant (id) ON DELETE CASCADE,
    position      integer     NOT NULL,
    id            text        NOT NULL,
    actor         text        NOT NULL,
    action        text        NOT NULL,
    object        text        NOT NULL,
    at            timestamptz NOT NULL,
    outcome       text        NOT NULL,
    correction_of text        NOT NULL DEFAULT '',
    PRIMARY KEY (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS audit_record_position_idx
    ON "platform-governance".audit_record (tenant_id, position);

-- Transactional outbox: сообщение коммитится в одной транзакции с агрегатом.
CREATE TABLE IF NOT EXISTS "platform-governance".outbox_message (
    id               bigserial   PRIMARY KEY,
    message_id       text        NOT NULL UNIQUE,
    contract         text        NOT NULL,
    schema_version   text        NOT NULL,
    event_id         text        NOT NULL,
    tenant_id        text        NOT NULL,
    occurred_at      timestamptz NOT NULL,
    snapshot_version bigint      NOT NULL DEFAULT 0,
    correlation_id   text        NOT NULL DEFAULT '',
    causation_id     text        NOT NULL DEFAULT '',
    payload          jsonb       NOT NULL,
    published        boolean     NOT NULL DEFAULT false
);

CREATE INDEX IF NOT EXISTS outbox_message_pending_idx
    ON "platform-governance".outbox_message (published, id);

-- Ключ идемпотентности изменяющей команды: пара (tenant_id, command_id)
-- записывается в одной транзакции с эффектом команды.
CREATE TABLE IF NOT EXISTS "platform-governance".command_key (
    tenant_id  text NOT NULL,
    command_id text NOT NULL,
    PRIMARY KEY (tenant_id, command_id)
);
