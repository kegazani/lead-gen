-- schema: lead-management
-- Миграция принадлежит схеме сервиса lead-management (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Зелёная зона: оценка релевантности компании относительно конкретного задания
-- поиска (lead_assessment). Оценка — не постоянное свойство компании, поэтому
-- ключ пары (job_id, company_id): одна компания оценивается отдельно в каждом
-- задании, и оценка из одного задания не переносится в другое. evidence —
-- JSONB-снимок списка свидетельств (критерий, факт, источник, обоснование, тип
-- сигнала, недостающее свидетельство / установленное несоответствие).

CREATE TABLE IF NOT EXISTS "lead-management".lead_assessment (
    job_id      text        NOT NULL,
    company_id  text        NOT NULL,
    verdict     text        NOT NULL,
    signal_kind text        NOT NULL,
    evidence    jsonb       NOT NULL,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    PRIMARY KEY (job_id, company_id)
);

-- Колонка интерпретации запроса у задания: критерии релевантности, выработанные
-- ассистентом и хранимые отдельно от технического плана (plan). Колонка
-- nullable: старые задания и задания без интерпретации хранят NULL и читаются
-- без ошибки; пустая интерпретация не подменяется выдуманной.

ALTER TABLE "lead-management".search_job
    ADD COLUMN IF NOT EXISTS interpretation jsonb;
