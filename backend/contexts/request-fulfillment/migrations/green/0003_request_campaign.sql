-- schema: request-fulfillment
-- Миграция принадлежит схеме сервиса request-fulfillment (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Capability request-delivery-conclusion: событие доставки сопоставляется с
-- заявкой по её кампании, поэтому выборка «заявка по кампании в тенанте» должна
-- быть индексирована. Кампания хранится внутри снимка (JSONB), поэтому индекс —
-- выраженный (tenant_id + snapshot->>'CampaignID'). Схема не меняется: новых
-- колонок нет, только индекс; данные совместимы.

CREATE INDEX IF NOT EXISTS request_campaign_idx
    ON "request-fulfillment".request (tenant_id, (snapshot->>'CampaignID'))
    WHERE snapshot->>'CampaignID' <> '';
