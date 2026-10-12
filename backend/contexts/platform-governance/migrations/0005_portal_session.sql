-- schema: platform-governance
-- Миграция принадлежит схеме сервиса platform-governance. Создаёт таблицу
-- серверных сессий портала клиента: cookie хранит только случайный токен,
-- а соответствие токена тенанту и пользователю живёт в базе.

CREATE TABLE IF NOT EXISTS "platform-governance".portal_session (
    id          text        PRIMARY KEY,
    tenant_id   text        NOT NULL,
    user_id     text        NOT NULL,
    last_active timestamptz NOT NULL
);
