## Context

Общая навигация консоли строится `components.ConsoleNavMessagesWithContext` по
каноническому списку `ConsoleSections` и базовым адресам `ConsoleBases`
(конфигурация точки входа). Сейчас `ConsoleBases` содержит разные origins
(`http://localhost:8082` и т. п.), поэтому ссылки навигации абсолютны и уводят на
другие порты.

Каждый консольный сервис отдаёт серверный `html/template` с абсолютными путями
(`href="/jobs"`, `action="/search"`, `hx-get="/jobs"`, `src="/static/…"`),
редиректы через `http.Redirect` с локальными путями, статику по `/static/` и
SSE-потоки (`/portal/stream`, `/company/stream`, `/assistant/stream`,
`/dashboard/stream`). Клиентский портал `platform-governance` (`/login`,
`/portal`, `/profile`) живёт на том же сервере, что и платформенная консоль
(`/console`, `/platform`), но не является админским фронтом.

## Goals / Non-Goals

- **Цель:** все админские консоли доступны под одним origin по префиксам путей;
  навигация, формы, редиректы, статика и SSE не покидают origin.
- **Цель:** минимально трогать шаблоны — переписывание ссылок делает один общий
  middleware, а не точечные правки каждой ссылки.
- **Цель:** не сломать клиентский портал (он остаётся без префикса).
- **Не цель:** менять домен, права, контракты; объединять сервисы в один процесс.

## Decisions

### Решение 1. Карта префиксов

| Префикс | Сервис | Внутренние маршруты |
|---|---|---|
| `/admin/` | `platform-governance` (консоль) | `/console/*`, `/platform/*` |
| `/lead-console/` | `lead-management` | `/`, `/jobs`, `/cards`, `/leads`, … |
| `/intake/` | `request-fulfillment` | `/validation`, `/approvals`, … |
| `/reporting/` | `analytics` | `/dashboard`, `/reports`, … |
| `/mailing/` | `outreach-orchestration` | `/outreach/*`, `/campaigns/*` |

Префиксы выбираются так, чтобы не конфликтовать с путями `/login`, `/logout`,
`/`, `/dashboard`, `/static`: каждый сервис живёт под своим префиксом. Для
платформенной консоли выбран `/admin` (а не `/platform`), чтобы не давать двойной
сегмент `/platform/platform/topology` — внутри `platform-governance` уже есть
namespace `/platform/*`. Для `request-fulfillment` выбран `/intake`, а не
`/requests`: внешний префикс `/requests` совпал бы с контрактными маршрутами
request-intent (`POST /requests`, `GET /requests`, `GET /requests/{id}`), и
`PrefixMiddleware` срезал бы их у прямых межсервисных вызовов, ломая контракт.
Для `outreach-orchestration` выбран `/mailing`, а не `/outreach`: внутренние
консольные маршруты уже используют namespace `/outreach/*`, и префикс `/outreach`
дал бы двойной сегмент `/outreach/outreach/drafts`. По той же причине
`lead-management` получает `/lead-console`, а не `/leads` (внутренние консольные
маршруты `GET /leads`, `/leads/export`, `/leads/export.csv` давали бы
`/leads/leads…`), а `analytics` — `/reporting`, а не `/reports` (внутренние
`/reports*` давали бы `/reports/reports…`). Контрактные маршруты не затронуты:
`/lead-search`, `/lead-relevance`, `/operations/*` (контракт `campaign-report`)
остаются как есть — это консольные read-пути, а не межсервисные вызовы.

### Решение 2. Префикс снимает и переписывает сервис, а не nginx

nginx передаёт полный путь (`proxy_pass http://SERVICE:PORT;` без URI),
сервис снимает префикс общим `components.PrefixMiddleware` и добавляет его
обратно в ответы. Вся логика префикса — в одном месте, nginx-конфиг остаётся
тривиальной таблицей путей.

### Решение 3. `PrefixMiddleware` переписывает ответы, не трогая шаблоны

Middleware:

- снимает префикс из `r.URL.Path` (маршруты остаются корневыми);
- переписывает `Location` в редиректах;
- буферизует `text/html` и переписывает локальные URL в атрибутах `href`, `src`,
  `action`, `formaction`, `hx-*`, `data-url` под префикс, вставляет
  `<meta name="admin-base-path">` в `<head>`;
- не трогает не-HTML (SSE, JSON, CSV), скачиваемые документы
  (`Content-Disposition: attachment`) и запросы вне префикса.

Так ссылки, формы, HTMX-атрибуты и статика переписываются единообразно, без
правки сотен мест в шаблонах.

### Решение 4. Префикс для клиентских скриптов — через meta

SSE-адреса и `fetch` в статических скриптах (`portal.js`, `company_requests.js`,
`console.js`, `assistant.js`) читают префикс из `<meta name="admin-base-path">`,
которую вставляет middleware. Скрипты не дублируют префикс в исходниках.

### Решение 5. Портал клиента не переписывается

`platform-governance` обслуживает и портал, и консоль. `PrefixMiddleware`
применяется только к запросам, пришедшим под префиксом (консоль через
`/admin/…`); запросы портала (`/login`, `/portal`) приходят без префикса и
проходят без изменений. Портал остаётся на `platform:8087` и через admin-proxy не
проксируется.

### Решение 6. `ConsoleBases` — префиксы одного origin

`ConsoleBases` теперь хранят префиксы путей (`/lead-console`, `/intake`,
`/reporting`, `/mailing`) вместо разных origins; `consoleHref` склеивает их в
пределах одного хоста. `ConsoleServiceOutreach` добавляется в карту (раньше
отсутствовал).

## Risks / Trade-offs

- **[Двойной сегмент `/outreach/outreach/*`]** → `outreach-orchestration` уже
  использует namespace `/outreach/*` в своих маршрутах, поэтому под префиксом
  `/outreach` получались бы пути `/outreach/outreach/drafts`. Снято выбором
  неконфликтующего префикса `/mailing`: консольные маршруты рассылок доступны как
  `/mailing/outreach/drafts`, контрактные (`/outreach-handoff`, `/campaign-command`,
  `/approved-action`, `/campaigns*`) не затронуты.
- **[Переписывание HTML]** → Регулярное переписывание атрибутов покрывает
  шаблонную разметку и HTMX; покрыто тестами. Не переписываются JS-файлы (они
  читают префикс из meta) и не-HTML ответы.
- **[Портал и консоль на одном сервере]** → Разделение по признаку «запрос под
  префиксом»; тест на passthrough портала есть.

## Migration Plan

Миграций нет. Порядок: `PrefixMiddleware` + тесты → проброс `BasePath` в пять
сервисов → правка скриптов (meta) → префиксы в `ConsoleBases`/точке сборки →
`admin-proxy` в compose → документация → тесты и `make check` → живой прогон.
