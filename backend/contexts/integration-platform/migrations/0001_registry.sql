-- schema: integration-platform
-- Миграция принадлежит схеме сервиса integration-platform (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Агрегаты adapter-registration и integration-invocation сохраняются снимком
-- целиком: регистрация — заголовком и подчинённой таблицей объявленных
-- возможностей (manifest.adapter-capability), вызов — одной строкой. Порядок
-- возможностей задаёт колонка position: manifest хранит их в порядке объявления,
-- а порядок вызовов в correlation-ref — колонка seq (монотонная
-- последовательность вставки), поэтому FindLatestByCorrelation детерминирован
-- даже при совпадающих моментах начала.
--
-- Адаптер открывает схему только сервиса integration-platform и не обращается к
-- схемам других сервисов.

CREATE SCHEMA IF NOT EXISTS "integration-platform";

CREATE TABLE IF NOT EXISTS "integration-platform".adapter_registration (
    id               text PRIMARY KEY,
    adapter_name     text NOT NULL DEFAULT '',
    manifest_version text NOT NULL DEFAULT ''
);

-- Имя адаптера уникально по правилу use case; индекс обслуживает FindByName.
CREATE INDEX IF NOT EXISTS adapter_registration_name_idx
    ON "integration-platform".adapter_registration (adapter_name);

CREATE TABLE IF NOT EXISTS "integration-platform".adapter_capability (
    adapter_id text    NOT NULL REFERENCES "integration-platform".adapter_registration (id) ON DELETE CASCADE,
    position   integer NOT NULL,
    name       text    NOT NULL,
    PRIMARY KEY (adapter_id, position)
);

CREATE TABLE IF NOT EXISTS "integration-platform".integration_invocation (
    id                        text        PRIMARY KEY,
    seq                       bigserial   NOT NULL,
    tenant_id                 text        NOT NULL,
    correlation_ref           text        NOT NULL,
    integration_type          text        NOT NULL,
    adapter_name              text        NOT NULL,
    operation                 text        NOT NULL,
    command_key               text        NOT NULL,
    idempotency_key           text        NOT NULL,
    state                     text        NOT NULL,
    category                  text        NOT NULL DEFAULT '',
    external_integration_type text,
    external_adapter_name     text,
    external_id               text,
    started_at                timestamptz NOT NULL,
    finished_at               timestamptz
);

-- Ключ идемпотентности — производный от (tenant, тип, операция, команда);
-- последняя по порядку вставки запись обслуживает FindByIdempotencyKey.
CREATE INDEX IF NOT EXISTS integration_invocation_idempotency_idx
    ON "integration-platform".integration_invocation (idempotency_key, seq DESC);

-- Последний по времени вызов в correlation-ref; при равных started_at порядок
-- разрешает seq, поэтому выбор детерминирован.
CREATE INDEX IF NOT EXISTS integration_invocation_correlation_idx
    ON "integration-platform".integration_invocation (correlation_ref, started_at DESC, seq DESC);
