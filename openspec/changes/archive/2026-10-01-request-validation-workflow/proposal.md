## Why

Заявка клиента не должна уходить в агентов «как есть»: перед запуском админ обязан проверить оффер — прочитать, при плохом описании позвонить и уточнить, внести правки — и только затем выпустить заявку в работу. Агрегат `request` (change №1) уже содержит статус `validating` и переход `validated`, но workflow, который ведёт валидацию человеком, не определён. Этот change добавляет поведение валидации: от взятия в работу до одобрения или отклонения с причиной, включая звонок-уточнение.

## What Changes

- Добавляется capability `request-validation` — поведение валидации заявки админом.
- Валидатор берёт заявку из `submitted` в `validating`, читает оффер и принимает решение: одобрить (`validated`) или отклонить (`rejected`) с обязательной причиной.
- При недостаточном качестве оффера валидатор инициирует звонок-уточнение (через существующий агрегат `call` контекста `outreach-orchestration`) и вносит правки в оффер до принятия решения.
- Правки оффера до состояния `validated` — на месте; заморозка наступает только после `validated` (инвариант из `client-request`).
- Решение валидации фиксируется внутри агрегата `request` (решение, актор, время, причина) — это не `operator-decision` для черновиков действий.

## Capabilities

### New Capabilities

- `request-validation`: валидация оффера заявки админом перед запуском агентов — взятие в работу, чтение оффера, звонок-уточнение, правки, одобрение или отклонение с причиной.

### Modified Capabilities

Нет. Инварианты статусной машины (`client-request`) не меняются: этот change задаёт workflow, который выполняет переходы `submitted → validating → validated | rejected`.

## Impact

**Затрагиваемые bounded contexts.** `request-fulfillment` — владелец workflow валидации и решения внутри агрегата `request`; `outreach-orchestration` — звонок-уточнение через `call`; `platform-governance` — роли и доступ валидатора (audit на границе API).

**Каталог и модель.** Новая capability `request-validation` добавляется в `architecture/domain-catalog.yaml` (владелец `request-fulfillment`, сервис `request-fulfillment`, роли `api`, `worker`); `docs/domain-model.md` дополняется описанием workflow валидации (без нового агрегата — решение живёт в `request`). **Новых агрегатов нет**; ссылка на `call` — по идентификатору.

**Код.** Прикладной сервис валидации в `backend/contexts/request-fulfillment`; экран валидации — эксплуатационная проекция роли `api` (HTMX + серверный рендеринг, как в `web-frontend-architecture`); звонок — через контракт к агрегату `call` (`outreach-orchestration`).

## Roadmap

Change №3 из шести. См. `openspec/changes/client-request-domain/proposal.md` → Roadmap.
