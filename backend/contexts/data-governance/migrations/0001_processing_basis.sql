-- schema: data-governance
-- Миграция принадлежит схеме сервиса data-governance (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Aggregate root processing-basis сохраняется целиком вместе с entity
-- suppression-entry, поэтому записи стоп-листа подчинены основанию и живут в его
-- схеме. Порядок строк в подчинённой таблице задаёт колонка position: снимок
-- агрегата хранит записи в порядке внесения. Колонка version обслуживает
-- оптимистичную блокировку, а status с valid_until — выборку действующих
-- оснований с истёкшим окном (роль worker).

CREATE SCHEMA IF NOT EXISTS "data-governance";

CREATE TABLE IF NOT EXISTS "data-governance".processing_basis (
    id             text        PRIMARY KEY,
    tenant_id      text        NOT NULL,
    purpose_name   text        NOT NULL,
    category       text        NOT NULL,
    retention_days integer     NOT NULL,
    valid_from     timestamptz NOT NULL,
    valid_until    timestamptz NOT NULL,
    status         text        NOT NULL,
    version        integer     NOT NULL
);

-- Основания тенанта читаются по tenant_id и упорядочены по идентификатору, а
-- фоновое истечение отбирает действующие основания по status и valid_until.
CREATE INDEX IF NOT EXISTS processing_basis_tenant_idx
    ON "data-governance".processing_basis (tenant_id, id);

CREATE INDEX IF NOT EXISTS processing_basis_expiry_idx
    ON "data-governance".processing_basis (status, valid_until, id);

CREATE TABLE IF NOT EXISTS "data-governance".suppression_entry (
    basis_id   text        NOT NULL REFERENCES "data-governance".processing_basis (id) ON DELETE CASCADE,
    position   integer     NOT NULL,
    id         text        NOT NULL,
    contact    text        NOT NULL,
    reason     text        NOT NULL,
    active     boolean     NOT NULL,
    created_at timestamptz NOT NULL,
    revoked_at timestamptz,
    PRIMARY KEY (basis_id, position)
);

-- Активная запись стоп-листа проверяется по контакту (инвариант I2), поэтому
-- индекс обслуживает выборку активной записи контакта без полной выгрузки.
CREATE INDEX IF NOT EXISTS suppression_entry_contact_idx
    ON "data-governance".suppression_entry (basis_id, contact)
    WHERE active;
