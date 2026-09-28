-- schema: outreach-orchestration
-- Миграция принадлежит схеме сервиса outreach-orchestration (граница данных —
-- schema-per-service). Файл применяется загрузчиком backend/platform/migrate,
-- который до выполнения проверяет директиву принадлежности.
--
-- Агрегат campaign сохраняется целиком вместе с сущностями action-draft и
-- operator-decision, поэтому отдельные таблицы подчинены кампании и живут в её
-- схеме. Порядок строк в подчинённых таблицах задаёт колонка position: снимок
-- агрегата хранит черновики в порядке постановки, а решения — в порядке
-- принятия.

CREATE SCHEMA IF NOT EXISTS "outreach-orchestration";

CREATE TABLE IF NOT EXISTS "outreach-orchestration".campaign (
    id           text        PRIMARY KEY,
    tenant_id    text        NOT NULL,
    name         text        NOT NULL,
    window_start timestamptz NOT NULL,
    window_end   timestamptz NOT NULL,
    state        text        NOT NULL
);

CREATE TABLE IF NOT EXISTS "outreach-orchestration".action_draft (
    campaign_id  text    NOT NULL REFERENCES "outreach-orchestration".campaign (id) ON DELETE CASCADE,
    position     integer NOT NULL,
    id           text    NOT NULL,
    material_id  text    NOT NULL,
    segment_id   text    NOT NULL,
    recipient_id text    NOT NULL,
    channel      text    NOT NULL,
    state        text    NOT NULL,
    version      integer NOT NULL,
    PRIMARY KEY (campaign_id, id)
);

-- Ключ окна черновиков: (материал, сегмент, идентификатор). Индекс обслуживает
-- keyset-переход LoadDraftWindow без полной выгрузки кампании.
CREATE INDEX IF NOT EXISTS action_draft_window_idx
    ON "outreach-orchestration".action_draft (campaign_id, material_id, segment_id, id);

CREATE TABLE IF NOT EXISTS "outreach-orchestration".operator_decision (
    campaign_id   text        NOT NULL REFERENCES "outreach-orchestration".campaign (id) ON DELETE CASCADE,
    position      integer     NOT NULL,
    id            text        NOT NULL,
    draft_id      text        NOT NULL,
    draft_version integer     NOT NULL,
    actor_id      text        NOT NULL,
    outcome       text        NOT NULL,
    reason        text        NOT NULL DEFAULT '',
    decided_at    timestamptz NOT NULL,
    batch_digest  text        NOT NULL DEFAULT '',
    PRIMARY KEY (campaign_id, id)
);
