## Why

Админские консоли разбросаны по портам: операторские разделы живут в
`lead-management` (:8082), `request-fulfillment` (:8085), `analytics` (:8081),
`outreach-orchestration` (:8080), а платформенные — в `platform-governance`
(:8087). Общая навигация консоли (`components.ConsoleSections` + `ConsoleBases`)
строит абсолютные ссылки вида `http://localhost:8085/validation`, поэтому клик с
платформенной консоли (или с консоли другого сервиса) уводит браузер на другой
порт: там нет платформенных вкладок, теряется общий каркас и выбранный тенант
(общий `consoleauth` хранит сессию в cookie, но порты — разные origins, а
`ConsoleBases` зашивают `localhost:<port>`).

Нужно, чтобы весь админский фронт жил на одном origin: один вход (обратный
прокси, один порт/хост), под которым доступны все админские консоли, а навигация
(операторские разделы + платформенные) не покидает хост.

## What Changes

- **Один вход `admin-proxy`.** В `compose.yaml` добавляется сервис `admin-proxy`
  (nginx), публикующий один порт `ADMIN_HTTP_PORT` (по умолчанию 8000) и
   маршрутизирующий по префиксам путей: `/admin/` → `platform-governance`,
   `/leads/` → `lead-management`, `/intake/` → `request-fulfillment`,
   `/reports/` → `analytics`, `/mailing/` → `outreach-orchestration`. Карта — в
   `deploy/nginx/admin-proxy.conf`.
- **Общий механизм префикса.** В `frontend/components` появляется
  `PrefixMiddleware` (и помощник `BasePath`/`JoinPrefix`): он снимает префикс из
  входящего пути, переписывает заголовок `Location` и локальные URL в HTML
  (ссылки, формы, HTMX-атрибуты, статика) обратно под префикс, вставляет
  `<meta name="admin-base-path">` для клиентских скриптов. Ответы не-HTML (SSE,
  JSON, CSV) и скачиваемые документы не трогаются; запросы вне префикса (портал
  клиента) проходят без изменений.
- **Префиксы в точке сборки.** `ConsoleBases` теперь хранят префиксы путей одного
  origin, а не разные origins; базовый префикс пробрасывается в каждый из пяти
  сервисов (`BasePath` в `Config`), навигация склеивается в пределах одного хоста.
- **SSE/HTMX.** Клиентские скрипты (`portal.js`, `company_requests.js`,
  `console.js`, `assistant.js`) читают префикс из `<meta name="admin-base-path">`
  и строят адреса SSE/fetch без дублирования префикса.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `console-appearance`: админские консоли доступны под одним origin через
  префиксы путей; навигация и переходы между разделами не покидают хост.

## Impact

- **Затрагиваемые bounded contexts:** `lead-management`, `request-fulfillment`,
  `analytics`, `outreach-orchestration`, `platform-governance` (HTTP-адаптеры и
  конфигурация сервисов); общий слой `frontend/components` переиспользуется.
- **Код:** `frontend/components/{basepath.go,console.go}`,
  `backend/cmd/lead-gen/{main.go,runners.go}`, `service.go` и
  `internal/adapters/http/server.go` пяти контекстов, статические скрипты
  (`portal.js`, `company_requests.js`, `console.js`, `assistant.js`).
- **Инфраструктура:** `compose.yaml`, `deploy/nginx/admin-proxy.conf`,
  `deploy/README.md`, `.env.example`.
- **Доменная модель:** не меняется (presentation/infrastructure-only).
