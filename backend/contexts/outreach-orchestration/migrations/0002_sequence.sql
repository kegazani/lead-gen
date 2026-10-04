-- schema: outreach-orchestration
-- Миграция принадлежит схеме сервиса outreach-orchestration (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Состояние последовательностей (сценарии и участия) хранится внутри снимка
-- aggregate root campaign вместе с кампанией: сценарий принадлежит кампании, а
-- участие — лиду в её рамках. Снимок цепочек вложен и часто пуст, поэтому
-- хранится документом в jsonb-колонках, а не отдельными таблицами: идентификаторы
-- значений восстанавливаются конструкторами домена при загрузке.

ALTER TABLE "outreach-orchestration".campaign
    ADD COLUMN IF NOT EXISTS sequence_definitions jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS sequence_executions  jsonb NOT NULL DEFAULT '[]'::jsonb;
