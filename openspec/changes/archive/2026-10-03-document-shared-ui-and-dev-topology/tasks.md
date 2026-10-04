# Задачи: догоняющая документация общего веб-слоя и dev-топологии

Код уже реализован; изменение носит документационный характер. Каждый пункт выполнен и содержит способ проверки. Ни одна задача не меняет код и тесты.

## 1. Сверка реализованного поведения

- [x] 1.1 Сверить общий слой статики: `frontend/components/components.go` (`baseFS`, `staticFS`, `ConsoleStylesheets`, `ConsoleBootstrapScripts`, `StaticFS`, `Layout`) и `frontend/components/static/{console.css,theme.js}`; проверить чтением этих файлов и тестов `static_test.go`
- [x] 1.2 Сверить слияние слоёв статики и подключение: `backend/contexts/lead-management/internal/adapters/http/templates.go` (`staticLayers`, приоритет общего слоя), `frontend/components/templates/{layout.html,nav.html}` (бутстрап-скрипты, `#theme-toggle`); проверить чтением этих файлов
- [x] 1.3 Сверить распространение темы на портал и дашборд: `backend/contexts/platform-governance/internal/adapters/http/server.go` (`render` подставляет `components.ConsoleStylesheets`/`ConsoleBootstrapScripts`, `portalNav` рендерит общую навигацию) и `backend/contexts/analytics/internal/adapters/http/server.go` (`render` подставляет те же общие ресурсы, навигации нет); проверить чтением этих файлов
- [x] 1.4 Сверить self-hosted Temporal: `compose.yaml` (`postgres-temporal`, `temporalio/auto-setup` с монтированием `deploy/temporal/dynamicconfig/development.yaml`, `temporal-ui`), `deploy/infrastructure.yaml` (узел `temporal`); сверить расхождение с архивными `2026-10-01-temporal-execution-platform` и `2026-10-03-harden-fulfillment-execution`; проверить чтением этих файлов
- [x] 1.5 Сверить исполняемую dev-топологию: `compose.yaml` (сетевой адаптер `request-intent` у `platform-governance`, публикация `analytics-event` и подписка воркера `analytics`, порты 8082–8083, отдельные контейнеры клиентских экранов), `backend/contexts/analytics/internal/adapters/transport/consumer.go` (очередь `analytics.analytics-event`); проверить чтением этих файлов
- [x] 1.6 Сверить dev-приём событий: `backend/contexts/analytics/internal/adapters/http/server.go` (`POST /dashboard/events`, `handleEvents`, `eventDTO`, `DisallowUnknownFields`) и `internal/app/command/apply_event.go`; убедиться, что `event_id` и дедупликация — атрибуты очереди, а не dev-приёма; проверить чтением этих файлов и теста `server_test.go`

## 2. Планировочные артефакты

- [x] 2.1 Написать `proposal.md` (Why/What/Capabilities/Impact, affected bounded contexts `lead-management`, `platform-governance`, `analytics`, `request-fulfillment`); проверить `openspec validate document-shared-ui-and-dev-topology --strict`
- [x] 2.2 Написать дельту `specs/console-appearance/spec.md` (`## MODIFIED Requirements` для «Переключатель темы в консоли» и `## ADDED Requirements` для общего слоя оформления); проверить `openspec validate document-shared-ui-and-dev-topology --strict`
- [x] 2.3 Написать дельту `specs/deployment-topology/spec.md` (`## ADDED Requirements`: self-hosted рантайм для dev-контура, исполняемая dev-топология); проверить `openspec validate document-shared-ui-and-dev-topology --strict`
- [x] 2.4 Написать дельту `specs/client-dashboard/spec.md` (`## ADDED Requirements`: dev-приём событий `POST /dashboard/events`); проверить `openspec validate document-shared-ui-and-dev-topology --strict`
- [x] 2.5 Написать `design.md` (решения по общему слою, распространению темы, self-hosted как dev-рантайму, границам dev-эндпоинта; компромиссы); проверить `openspec validate document-shared-ui-and-dev-topology --strict`
- [x] 2.6 Написать `tasks.md` с выполненными пунктами и способом проверки каждого; проверить `openspec validate document-shared-ui-and-dev-topology --strict`

## 3. Документация доменной модели

- [x] 3.1 Добавить в `docs/domain-model.md` строки capability-матрицы `lead-search` и `email-generation` (владелец `lead-management`); проверить чтением матрицы
- [x] 3.2 Уточнить заметки о покрытии `console-appearance` (общий слой серверных экранов) и `client-dashboard` (dev-приём событий), не меняя границы и владение; проверить чтением раздела
- [x] 3.3 Убедиться, что `architecture/domain-catalog.yaml` и обязательные диаграммы не меняются (границы, агрегаты, события, policy и relationships те же); проверить `go -C tools/archlint run .`

## 4. Проверка и архивация

- [x] 4.1 Прогнать `openspec validate --all --strict`; проверить, что все change и spec валидны
- [x] 4.2 Синхронизировать дельты в `openspec/specs/console-appearance/spec.md`, `openspec/specs/deployment-topology/spec.md`, `openspec/specs/client-dashboard/spec.md` и проверить отсутствие плейсхолдеров `TBD`
- [x] 4.3 Прогнать `go -C tools/archlint run .`; проверить код возврата `0` и сводку без нарушений
- [x] 4.4 Архивировать change в `openspec/changes/archive/<дата>-document-shared-ui-and-dev-topology/`; проверить наличие каталога и отсутствие активного change
- [x] 4.5 Повторно прогнать `openspec validate --all --strict` и `go -C tools/archlint run .`; проверить оба зелёными
