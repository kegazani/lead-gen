-- schema: lead-management
-- Бюджет Firecrawl задачи: JSONB-снимок бюджета (max_credits, расход, резерв,
-- доступно, статус). Колонка nullable: задания, запущенные без бюджета, хранят NULL.

ALTER TABLE "lead-management".search_job
    ADD COLUMN IF NOT EXISTS budget jsonb;
