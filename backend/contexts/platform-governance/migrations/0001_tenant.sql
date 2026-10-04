-- schema: platform-governance
-- Миграция принадлежит схеме сервиса platform-governance (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Агрегат tenant сохраняется целиком вместе с составом пользователей
-- (tenant_user), поэтому пользователи подчинены тенанту и живут в его схеме.
-- Порядок строк в tenant_user задаёт колонка position: снимок агрегата хранит
-- пользователей в порядке добавления, а загрузка восстанавливает тот же состав.

CREATE SCHEMA IF NOT EXISTS "platform-governance";

CREATE TABLE IF NOT EXISTS "platform-governance".tenant (
    id           text        PRIMARY KEY,
    company_name text        NOT NULL,
    description  text        NOT NULL,
    created_at   timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS "platform-governance".tenant_user (
    tenant_id text    NOT NULL REFERENCES "platform-governance".tenant (id) ON DELETE CASCADE,
    position  integer NOT NULL,
    id        text    NOT NULL,
    login     text    NOT NULL,
    role      text    NOT NULL,
    PRIMARY KEY (tenant_id, id)
);

-- Индекс обслуживает загрузку состава в порядке добавления (ORDER BY position).
CREATE INDEX IF NOT EXISTS tenant_user_position_idx
    ON "platform-governance".tenant_user (tenant_id, position);
