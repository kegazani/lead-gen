-- schema: outreach-orchestration
-- Миграция принадлежит схеме сервиса outreach-orchestration (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Правки оператора и отложение решения хранятся на подчинённой таблице
-- action_draft, потому что принадлежат черновику. Правки — отдельный от
-- сгенерированного состава документ (автор, время, прежний и новый состав),
-- поэтому вынесены в jsonb, а не переписаны в колонки текущего состава.
-- Оба поля необязательны: у черновиков без правок и отложений сохраняется
-- прежнее поведение.

ALTER TABLE "outreach-orchestration".action_draft
    ADD COLUMN IF NOT EXISTS edits           jsonb       NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS postponed_until timestamptz;
