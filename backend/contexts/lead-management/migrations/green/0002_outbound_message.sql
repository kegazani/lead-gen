-- schema: lead-management
-- Миграция принадлежит схеме сервиса lead-management (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Зелёная зона: журнал исходящих писем (outbound_message). Сообщение хранится как
-- JSONB-снимок агрегата outbound-message; денормализованы lead_id (выборка
-- истории переписки по лиду и вычисление подавленных лидов) и status (запрос
-- подавленных исходов bounced/unsubscribed/rejected для исключения из рассылки).
--
-- Value object lead-status (stage, snooze_until) добавлен к агрегату lead-profile
-- и сохраняется внутри JSONB-снимка lead_profile (миграция 0001): отдельной
-- колонки не требует, так как витрина и воронка группируют лидов по стадиям в
-- прикладном слое.

CREATE TABLE IF NOT EXISTS "lead-management".outbound_message (
    id         text        PRIMARY KEY,
    lead_id    text        NOT NULL,
    status     text        NOT NULL,
    snapshot   jsonb       NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS outbound_message_lead_id_idx
    ON "lead-management".outbound_message (lead_id);
