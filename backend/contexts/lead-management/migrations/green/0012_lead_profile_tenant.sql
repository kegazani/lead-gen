-- schema: lead-management
-- Тенант-скоупное хранилище карточек (tenant-scoped-lead-storage): колонка
-- тенанта-владельца и индекс под выборку по тенанту.
--
-- Значение по умолчанию — пустая строка: записи, сохранённые до миграции,
-- читаются в тенанте по умолчанию (''), поэтому миграция обратно совместима
-- (expand/contract). Колонка денормализована рядом со снимком, как флаг hidden:
-- тенант — операционная область доступа, а не атрибут агрегата компании.

ALTER TABLE "lead-management".lead_profile
    ADD COLUMN IF NOT EXISTS tenant_id text NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS lead_profile_tenant_created_idx
    ON "lead-management".lead_profile (tenant_id, created_at DESC, id DESC);
