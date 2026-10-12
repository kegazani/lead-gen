-- schema: platform-governance
-- Миграция принадлежит схеме сервиса platform-governance. Добавляет
-- идентифицирующие данные и профиль компании, состояние заявки на регистрацию и
-- хеш пароля пользователя. Колонки идентификации могут быть пустыми у тенантов,
-- созданных администратором напрямую, поэтому уникальность задаётся частичными
-- индексами по непустым значениям.

ALTER TABLE "platform-governance".tenant
    ADD COLUMN IF NOT EXISTS inn text NOT NULL DEFAULT '';
ALTER TABLE "platform-governance".tenant
    ADD COLUMN IF NOT EXISTS company_email text NOT NULL DEFAULT '';
ALTER TABLE "platform-governance".tenant
    ADD COLUMN IF NOT EXISTS website text NOT NULL DEFAULT '';
ALTER TABLE "platform-governance".tenant
    ADD COLUMN IF NOT EXISTS legal_data text NOT NULL DEFAULT '';
ALTER TABLE "platform-governance".tenant
    ADD COLUMN IF NOT EXISTS registration_reason text NOT NULL DEFAULT '';
ALTER TABLE "platform-governance".tenant
    ADD COLUMN IF NOT EXISTS profile jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE "platform-governance".tenant
    ADD COLUMN IF NOT EXISTS profile_status text NOT NULL DEFAULT 'empty';

ALTER TABLE "platform-governance".tenant_user
    ADD COLUMN IF NOT EXISTS password_hash text NOT NULL DEFAULT '';

-- Уникальность идентифицирующих данных: среди непустых ИНН и email компании
-- повторов быть не должно. Частичные индексы не затрагивают пустые значения.
CREATE UNIQUE INDEX IF NOT EXISTS tenant_inn_unique_idx
    ON "platform-governance".tenant (inn)
    WHERE inn <> '';
CREATE UNIQUE INDEX IF NOT EXISTS tenant_company_email_unique_idx
    ON "platform-governance".tenant (company_email)
    WHERE company_email <> '';
