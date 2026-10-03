## Why

Решения по общему веб-слою и локальному контуру уже приняты в коде и `compose.yaml`, но не нормированы в спеках и расходятся с тем, что там зафиксировано. Общая статика `console.css`/`theme.js` перенесена в общий модуль серверных экранов (`frontend/components/static`) и раздаётся всем экранам через `ConsoleStylesheets`/`ConsoleBootstrapScripts`/`StaticFS`, но `console-appearance` (и архив `console-theme-switcher`) описывает тему и переключатель как принадлежность только консоли, тогда как тема применяется и на портале, и на дашборде. Локальный контур поднял self-hosted Temporal, тогда как архив `temporal-execution-platform` фиксировал «Cloud first, self-hosted отложен», а `harden-fulfillment-execution` — «self-hosted вне объёма». Исполняемая dev-топология (сетевой адаптер `request-intent`, события дашборда через RabbitMQ, отдельные контейнеры `billing`/`publishing`, порты 8082–8085) и write-эндпоинт `POST /dashboard/events` не описаны в спеках `deployment-topology` и `client-dashboard`.

Change — «догоняющая документация»: он нормирует уже реализованное поведение, ничего не меняя в коде.

## What Changes

- **`console-appearance` (MODIFIED + ADDED).** Общий слой оформления серверных экранов: общая статика в общем модуле, раздача по общему пути `/static/` через объявленные списки и `StaticFS`, слияние слоёв с приоритетом общего; тема применяется ко всем серверным экранам (консоль, портал, дашборд); переключатель — на экранах с общей навигацией (консоль и портал), дашборд применяет тему без переключателя. Снимается дрейф «переключатель — только консоль».
- **`deployment-topology` (ADDED).** Self-hosted рантайм durable-исполнения как dev-рантайм и допустимый выбор для production (Cloud или self-hosted) без изменения кода и контрактов; dev-dynamicconfig ослабляет проверки только локально. Исполняемая dev-топология: сетевые адаптеры синхронных контрактов, публикация событий дашборда в брокер, отдельные контейнеры для сервисов с собственной ролью `api`, клиентские экраны на собственных портах. Снимается расхождение с `temporal-execution-platform` и `harden-fulfillment-execution`.
- **`client-dashboard` (ADDED).** Write-эндпоинт `POST /dashboard/events` — dev-приём нормализованного события дашборда для проверки проекции без брокера; доменные поля совпадают с сообщением очереди; рабочий контур питается воркером из брокера.
- **`docs/domain-model.md`.** Добавляются строки capability-матрицы `lead-search` и `email-generation` (заведены в спеки и каталог ранее, но в матрице отсутствовали) и уточняются заметки о `console-appearance` и `client-dashboard`.

## Capabilities

### New Capabilities

Нет. Изменения не вводят нового поведения за пределами уже описанных capability: общий слой оформления — уточнение `console-appearance`, self-hosted и dev-топология — `deployment-topology`, dev-приём событий — `client-dashboard`.

### Modified Capabilities

- `console-appearance`: общий слой оформления серверных экранов и распространение темы и переключателя за пределы операторской консоли.
- `deployment-topology`: self-hosted рантайм durable-исполнения как dev-рантайм и исполняемая dev-топология.
- `client-dashboard`: write-эндпоинт dev-приёма событий `POST /dashboard/events`.

## Impact

- **Affected bounded contexts:** `lead-management` — владелец `console-appearance` (общий слой оформления ведёт происхождение из консоли); `platform-governance` — владелец `deployment-topology` и контекст клиентского портала, потребляющий общий слой; `analytics` — владелец `client-dashboard` и контекст дашборда, потребляющий общий слой; `request-fulfillment` — потребитель контракта `request-intent`, связываемый с порталом сетевым адаптером в dev-топологии.
- **Доменная модель не меняется:** ни агрегатов, сущностей, value objects, domain events, policies и межконтекстных контрактов не добавляется и не переносится. Канонические границы в `architecture/domain-catalog.yaml` не меняются; `docs/domain-model.md` правится только дозаполнением матрицы (`lead-search`, `email-generation`) и заметками о покрытии, без изменения границ и владения. Обязательные диаграммы не затрагиваются.
- **Код и данные:** не меняются. Изменение носит документационный характер; нормируются уже существующие `frontend/components/{components.go,static/*}`, `backend/contexts/lead-management/internal/adapters/http/templates.go`, `backend/contexts/platform-governance/internal/adapters/http/server.go`, `backend/contexts/analytics/internal/adapters/http/server.go`, `compose.yaml`, `deploy/temporal/dynamicconfig/development.yaml`.
- **Проверка:** сверка дельт с реализацией, `openspec validate --all --strict`, `go -C tools/archlint run .`.
