# tasks.md — single-admin-host

Порядок — от общего механизма к проводке, инфраструктуре и живой проверке. Статус
задачи меняется только по критерию верификации (§4 AGENTS.md). Все задачи ниже
отмечены `[x]` только после фактической проверки; пометка «Проверено» — это
выполненный критерий верификации, а не намерение.

Попутно (review-дефекты) сменены внешние префиксы консолей: у
`request-fulfillment` `/requests` → `/intake` (коллизия внешнего префикса с
контрактными маршрутами request-intent `POST /requests`, `GET /requests`,
`GET /requests/{id}`), у `outreach-orchestration` `/outreach` → `/mailing`
(двойной сегмент `/outreach/outreach/drafts`), у `lead-management` `/leads` →
`/lead-console` (двойной сегмент `/leads/leads…` из-за внутренних `GET /leads`,
`/leads/export`, `/leads/export.csv`), у `analytics` `/reports` → `/reporting`
(двойной сегмент `/reports/reports…` из-за внутренних `/reports*`). Контрактные
маршруты не тронуты (`/lead-search`, `/lead-relevance`, `/operations/*`). В
`admin-proxy.conf` добавлены редиректы префиксов без завершающего слэша
(`/admin`, `/lead-console`, `/intake`, `/reporting`, `/mailing` → тот же путь со
слэшем).

## 1. Общий механизм префикса

- [x] 1.1 Добавить `frontend/components/basepath.go`: `BasePath`, `JoinPrefix` и
  `PrefixMiddleware` (снятие префикса из пути, переписывание `Location`, буфер
  `text/html` с переписыванием локальных URL в атрибутах и вставкой
  `<meta name="admin-base-path">`; passthrough для не-HTML, скачиваемых документов
  и запросов вне префикса). Проверено: unit-тесты зелёные, включая добавленный
  `TestPrefixMiddlewareIntakeDoesNotStripContractRoutes` (при `BasePath=/intake`
  контрактный `POST /requests` не срезается, консольный `/intake/...` срезается).
- [x] 1.2 Уточнить комментарий `ConsoleBases` в `frontend/components/console.go`:
  под единым origin это префиксы путей, а не разные origins. Проверено: сборка.

## 2. Проброс префикса в сервисы

- [x] 2.1 Добавить `BasePath` в `Config` и `Dependencies` пяти контекстов
  (`lead-management`, `request-fulfillment`, `analytics`, `outreach-orchestration`,
  `platform-governance`) и обернуть `server.handler` в `PrefixMiddleware`.
  Проверено: `go test ./...` затронутых контекстов зелёный.
- [x] 2.2 В `main.go` заменить флаги `console-*-url` на `console-*-base` (пять
  префиксов) и собрать `ConsoleBases` (с `ConsoleServiceOutreach`) и поля
  `console*Base` в `buildOptions`; в `runners.go` пробросить `BasePath` в `Config`
  каждого сервиса. Проверено: `go build ./...` модуля `cmd/lead-gen`.

## 3. Скрипты под префиксом

- [x] 3.1 Научить `portal.js`, `company_requests.js`, `console.js`, `assistant.js`
  читать префикс из `<meta name="admin-base-path">` и строить адреса SSE/fetch.
  Проверено: тесты сервера контекстов зелёные.

## 4. Инфраструктура единого входа

- [x] 4.1 Добавить `deploy/nginx/admin-proxy.conf` и сервис `admin-proxy` в
  `compose.yaml` (порт `ADMIN_HTTP_PORT`, маршрутизация по префиксам, SSE без
  буферизации); обновить `x-app-env` на `CONSOLE_*_BASE_PATH`. Проверено:
  `docker compose config` без ошибок; префиксы `/intake` и `/mailing` видны в
  конфигурации.
- [x] 4.2 Обновить `deploy/README.md` и `.env.example` (новый порт, карта
  префиксов). Проверено: документация согласована с `admin-proxy.conf`
  (`/intake/`, `/mailing/`).

## 5. Гейт и живой прогон

- [x] 5.1 `gofmt`, `make check` и `openspec validate single-admin-host --strict`.
  Проверено: `gofmt` без diff; `make check` exit 0 (build/test/vet/importlint/
  archlint/openspec, 67 passed, 0 failed); `openspec validate single-admin-host
  --strict` valid.
- [x] 5.2 `docker compose build` затронутых сервисов и `docker compose up -d`;
  проверить, что через единый порт открывается вход оператора и все разделы, а
  ссылки навигации остаются на том же хосте (без `:8082`/`:8085`). Проверено:
  живьём через `http://localhost:8000` — `/` → `/admin/console`, `/admin/` →
  `/admin/console/login`, `/intake/validation` (303 на `/intake/login`),
  `/leads/` (303 на `/leads/login`), `/reports/dashboard` (200),
  `/mailing/outreach/drafts` (303 на `/mailing/login`); навигация на
  `/reports/dashboard` — `/intake/validation`, `/intake/approvals`, `/leads/`,
  `/mailing/outreach/drafts` без межпортовых ссылок. Контракт напрямую на
  `:8085`: `POST /requests` (400 по неизвестному полю — маршрут обработан, не
  срезан), `GET /requests?tenant_id=demo` (200).
