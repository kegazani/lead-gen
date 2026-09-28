-- schema: lead-management
-- Согласованный план обхода задания поиска (цели и лимиты), выработанный
-- диалогом ассистента и подтверждённый оператором. Колонка nullable: старые
-- задания и задания без плана хранят NULL и обрабатываются фолбэком на
-- планирование по запросу.

ALTER TABLE "lead-management".search_job
    ADD COLUMN IF NOT EXISTS plan jsonb;
