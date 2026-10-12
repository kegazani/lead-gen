## Why

Физическая модель данных платформы (12 bounded contexts + общая платформенная схема `consoleauth`) описана только разрозненно: 37 SQL-миграций в `backend/contexts/*/migrations` и `backend/platform/consoleauth/migrations`, обзорная таблица сущностей в `docs/architecture.md` §5 и раздел «Персистентность» в `docs/implementation-matrix.md` §4. Единого справочника «что где лежит, почему и как связано» нет, а три расхождения между декларацией и кодом (роли schema-per-service не выданы; RLS по тенанту — только декларация; PII красной зоны, кроме `contact`, не материализованы) не зафиксированы в одном месте. Оператору, аналитику и агенту приходится реконструировать схему по миграциям.

## What Changes

- Добавить справочник физической модели данных `docs/database-schema-reference.md`: принципы хранения, карта схем по зонам, разбор по контекстам, связи (FK внутри схемы и логические между контекстами), сквозные механизмы и раздел «Известные расхождения».
- Сослаться на справочник из `docs/architecture.md` §5 и `docs/implementation-matrix.md` §4, чтобы обзорные документы не дублировали детали.
- Зафиксировать (не закрывать) три расхождения с атрибуцией владельцам: роли schema-per-service → change `service-per-context`; RLS по тенанту → открытый факт; PII красной зоны помимо `contact` → открытый вопрос.
- **Изменений поведения, кода и схемы БД нет.** Миграции не меняются.

## Capabilities

### New Capabilities

- Нет: изменение документационное, поведение продукта не меняется — в `.openspec.yaml` установлен `skip_specs: true`. Спека описывает поведение, а поведение здесь не меняется, поэтому требование под справочник не выдумывается.

### Modified Capabilities

- Нет.

## Impact

- **Bounded contexts:** справочник охватывает все контексты каталога — `lead-management`, `request-fulfillment`, `outreach-orchestration`, `message-delivery`, `engagement-observation`, `analytics`, `audience`, `content-production`, `data-governance`, `integration-platform`, `platform-governance`, `agent-orchestration` — и платформенную схему `consoleauth` (не контекст).
- **Доменная модель не меняется:** владение и границы остаются за `architecture/domain-catalog.yaml` и `docs/domain-model.md`; справочник им подчинён и не назначает владельцев.
- **Документация:** новый `docs/database-schema-reference.md`; кросс-ссылки в `docs/architecture.md` (§5) и `docs/implementation-matrix.md` (§4); запись в `docs/progress-log.md`.
- **Код, контракты, `deploy`:** не затрагиваются.
