-- schema: analytics
-- Миграция принадлежит схеме сервиса analytics (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Read-модель качества поиска (capability lead-quality-insights): снимок
-- проекции (исходы, watermark, версия формул) для воспроизводимости и
-- зафиксированные применения рекомендаций со снимком «до». Персональных данных
-- и содержимого красной зоны здесь нет: исходы несут только непрозрачные ссылки
-- на компании, вердикты и обезличенные измерения.

CREATE TABLE IF NOT EXISTS "analytics".quality_insight_snapshot (
    tenant_id       text        NOT NULL,
    watermark       timestamptz,
    formula_version text        NOT NULL DEFAULT '',
    outcomes        jsonb       NOT NULL DEFAULT '[]'::jsonb,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id)
);

CREATE TABLE IF NOT EXISTS "analytics".quality_insight_application (
    tenant_id                text        NOT NULL,
    scope_kind               text        NOT NULL DEFAULT '',
    scope_key                text        NOT NULL DEFAULT '',
    applied_at               timestamptz NOT NULL,
    reference                text        NOT NULL DEFAULT '',
    action                   text        NOT NULL DEFAULT '',
    reason                   text        NOT NULL DEFAULT '',
    basis                    text        NOT NULL DEFAULT '',
    before_watermark         timestamptz,
    before_total             integer     NOT NULL DEFAULT 0,
    before_fits              integer     NOT NULL DEFAULT 0,
    before_hypothesis        integer     NOT NULL DEFAULT 0,
    before_unsuitable        integer     NOT NULL DEFAULT 0,
    before_extraction_errors integer     NOT NULL DEFAULT 0,
    before_reasons           jsonb       NOT NULL DEFAULT '[]'::jsonb,
    PRIMARY KEY (tenant_id, scope_kind, scope_key, applied_at)
);
