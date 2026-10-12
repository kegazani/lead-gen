## Why

В раздельном контуре `demand` (сервисы `lead-management`/`audience`/
`content-production` в собственном контейнере) логируется
`lead-gen: решение о доступе недоступно — потребители отвечают отказом: владелец platform-governance не выбран к запуску`.
Change `platform-governance` (M4) опубликовал контракт
`contracts/platform-governance/accessdecision` и связал владельца с
потребителями, но **только in-proc** (`assembleAccessDecision` в `cmd/lead-gen`):
решение связывается прямым вызовом, когда владелец и потребитель в одном
процессе. При разделении сервисов владелец оказывается в другом контейнере, и
сетевого транспорта у контракта нет — поэтому в раздельном контуре доступ
честно недоступен, хотя оба сервиса запущены. Это не заглушка, но и не
работающий контур: контракт объявлен, а его сетевая реализация отсутствует.

## What Changes

- **HTTP-поверхность владельца.** `platform-governance` (роль `api`) получает
  эндпоинт `POST /access-decision` — транспорт синхронного решения контракта
  `access-decision`. Обработчик переиспользует use case `DecideAccess`, не
  воспроизводя RBAC; незамонтированный use case честно отвечает `503`.
- **Сетевой клиент контракта.** В `contracts/platform-governance/accessdecision`
  добавляется subpackage `httpport`: клиент реализует `accessdecision.Decider`
  поверх `net/http` по образцу `lead-management/leadsearch/httpport`. Форма
  контракта (DTO запроса/ответа и путь) объявлена в самом контракте, чтобы
  владелец и потребитель не расходились.
- **Выбор транспорта по топологии в `cmd/lead-gen`.** `assembleAccessDecision`
  при заданном адресе владельца собирает сетевой клиент; иначе связывает
  владельца in-proc, как прежде. Адрес задаётся флагом/ENV
  (`-access-decision-url` / `LEADGEN_ACCESS_DECISION_URL`).
- **Потребители не меняются.** `outreach-orchestration` и `data-governance`
  держат `accessdecision.Decider`; недоступность владельца по-прежнему даёт
  честный отказ, а не разрешение. Меняется только реализация, подставленная в
  `cmd`.
- **Каталог, модель и диаграммы.** Синхронная проверка решения добавляется в
  каталог как связь `query`/`sync`/`http-json` (policy-снимок остаётся
  async/amqp); обновляются `docs/domain-model.md` и
  `architecture/diagrams/context-relationships.puml`.

## Capabilities

### Modified Capabilities

- `platform-access`: добавляется требование о сетевом транспорте решения
  доступа и выборе транспорта по топологии (ADDED). Capability создаётся
  активным change `platform-governance`; существующие требования не меняются.

## Impact

- **Affected bounded contexts:** владелец — `platform-governance`; потребители —
  `outreach-orchestration`, `data-governance`. Доменные сущности, агрегаты и
  события не меняются.
- **Каталог и модель:** `architecture/domain-catalog.yaml` — две новые связи
  `query`/`sync`/`http-json` для контракта `access-decision` (связей станет 34);
  `docs/domain-model.md` (число связей и пояснение транспорта);
  `architecture/diagrams/context-relationships.puml` (рёбра синхронного
  решения).
- **Код:** `contracts/platform-governance/accessdecision` (DTO и `httpport`),
  `backend/contexts/platform-governance/internal/adapters/http` (эндпоинт),
  `backend/cmd/lead-gen` (флаг/ENV и выбор транспорта).
- **Dockerfile:** новых Go-модулей не добавляется — `httpport` живёт внутри уже
  встроенного в образ модуля `contracts/platform-governance`, поэтому правка
  встроенного `go.work` не требуется (проверено).
- **Спеки:** дельта `specs/platform-access/spec.md` (ADDED) в этом изменении.
- **Ограничения:** внешний брокер для policy-снимка остаётся отложенным
  (`platform-governance`, «Отложено 2»); этот change даёт синхронный сетевой
  транспорт решения, а не доставку снимков.
