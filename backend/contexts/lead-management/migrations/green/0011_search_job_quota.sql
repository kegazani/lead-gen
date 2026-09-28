-- schema: lead-management
-- Целевое число компаний и лимит результатов поиска задания: операторские
-- параметры запуска «искать, пока не наберётся N компаний» и «сколько
-- результатов на запрос». Колонки nullable: старые задания и задания без новых
-- параметров хранят NULL и читаются как 0 (цель не задана, лимит адаптера).

ALTER TABLE "lead-management".search_job
    ADD COLUMN IF NOT EXISTS target_companies integer;

ALTER TABLE "lead-management".search_job
    ADD COLUMN IF NOT EXISTS max_results integer;
