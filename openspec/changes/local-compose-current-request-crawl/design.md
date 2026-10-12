## Context

См. мотивацию в `proposal.md` и поведенческие контракты в `specs/client-request/spec.md` и `specs/operations-cockpit/spec.md`. В `backend/cmd/lead-gen/main.go` CLI по умолчанию использует `db-policy=legacy`, принимает `-console-password-file` и поддерживает выбор сервисов; legacy runner применяет миграции request и consoleauth через `migrate.Apply`, но этот путь сам по себе не доказывает полноту схемы через `migrate.Verify`. Альтернативный `runtime-only` intake требует подписанный active candidate manifest и не является готовым упрощённым локальным путём.

В коде request lifecycle различает `submitted`, `processing`, `cancelled`, `fulfilled` и другие состояния; обработка Temporal завершения и editorial draft generation не эквивалентна доменному `fulfilled`. У request-fulfillment уже есть PostgreSQL persistence, workflow/Temporal, UI заявок и provider contracts; `platform-governance` владеет регистрацией и операторским доступом. Текущий раздел «Операции» аутентифицирован, но для части данных отвечает «Данные процесса недоступны».

План — временный dev harness из актуальных исходников с новой пустой PostgreSQL 17, отдельными RabbitMQ и Temporal namespace/task queue. Не копировать основную БД или backup без зафиксированной блокирующей необходимости. Пользовательский `compose.yaml`, его данные и volumes не являются целевым runtime и не меняются. Существующий `console-password` подключается только для чтения через `console-password-file`; не генерировать и не записывать его заново. Секреты не добавлять в tracked artifacts.

## Goals / Non-Goals

**Goals:**
- Провести регистрацию → административное одобрение профиля → вход → создание заявки с отдельными offer/desired leads → одобрение/старт → настоящий provider crawl через UI.
- Обеспечить операторскую историю четырёх UI-статусов, сохранение отменённых/выполненных заявок и реальные связанные leads.
- Привязать фактические request/execution данные к области существующей вкладки «Операции», не заменяя её.
- Выбрать harness roles/ports из реально поддерживаемых service filter и доступных источников данных; не занимать известные старые demo ports `19082`, `19085`, `19086` и любые уже занятые порты.
- До приёмки проверить точный набор миграций/схемы свежей demo-БД посредством `migrate.Verify` или эквивалентной проверки; запуск с `Apply` без независимого Verify недостаточен.

**Non-Goals:**
- Изменение основного Compose, существующих пользовательских данных/volumes, credentials, auth/CSRF/secrets, схемных проверок или release-state.
- Утверждение, что legacy/dev путь получает гарантии `runtime-only`.
- Genesis/IPC/release-state integration, rollout основной БД, whole v2, recovery/hardening, новая модель lifecycle или отдельный crawl mode.
- SMTP, доставка писем и согласование писем; editorial drafts допустимы как побочная внутренняя зависимость.

## Decisions

1. **Изолированный harness, не overlay пользовательского Compose.** Выделить dev-only Compose/runner inputs отдельно от `compose.yaml`; использовать новый пустой именованный PG17 volume и собственные RabbitMQ/Temporal queue/namespace. Сначала проверить доступность выбранных адресов/портов, при конфликте подобрать другие; старые порты 19082/19085/19086 являются историческими, не резервируются и не считаются доказательством нового запуска. Альтернатива — поднять текущий основной Compose — отвергнута из-за его связи с существующими данными и secrets.

2. **Честная граница legacy/dev startup.** Использовать поддерживаемые `main.go` service selection и legacy DB policy, где это нужно для нескольких процессов; не пытаться обходить требования `runtime-only` поддельным manifest. Поскольку `runners.go` применяет миграции без `Verify`, в процедуру harness включить явный read-only verify точной актуальной схемы после Apply и до UI acceptance. При несовпадении миграций или невозможности проверки запуск не принимается; security checks не отключать.

3. **Минимальный состав процессов устанавливается из wiring, не из прежнего прогона.** На реализации проверить текущие `deploy/services.yaml`, `main.go` service filter и builder wiring, определить роли `platform-governance`, `request-fulfillment`, `lead-management` и необходимые integration/provider зависимости. Исторический запуск трёх процессов на 19086/19085/19082 удалён и не является доказательством; порты из него не копировать. Если отдельный operations source требует процесса/контракта, включить только реально необходимую роль.

4. **Не сводить workflow termination к fulfilled.** UI-проекция основана на request domain status: submitted/validating/validated → «Создана», processing → «В работе», cancelled → «Отменена», fulfilled → «Выполнена». Если Temporal workflow завершился, а request остался `processing` (например, editorial workflow `Completed`/`drafts-generated`), показывать «В работе» и фактическую фазу; не вводить переход lifecycle ради красивого UI.

5. **Операции — узкая проводка данных в существующую вкладку.** Подключить подтверждённые реальные read sources текущего request/execution model; сохранить оболочку и иные панели. Недоступность показывать только в затронутой области. Не выдумывать агрегаты, workflow counts или статусы, не заменять весь экран новым UI.

6. **Регрессии фокусируются на наблюдаемом риске.** HTTP/UI тесты покрывают все четыре labels, видимость terminal history и привязку lead; отмена проверяется при обычном outcome и `WorkflowNotFound`, чтобы UI не маскировал состояние заявки. Security regressions проверяют действующие auth/CSRF без изменения механизмов.

## Risks / Trade-offs

- [Legacy startup мигрирует, но не проверяет схему] → отдельный read-only `migrate.Verify`/эквивалент до допуска UI-сценария; зафиксировать версии схемы.
- [Текущая незакоммиченная рабочая копия существенно изменена] → до реализации повторно сверить выбранные роли, provider wiring, ports и операции с применяемым diff; не считать план доказательством наличия уже подключённого кода.
- [Провайдер Firecrawl/embeddings/LLM или их credentials недоступны] → не заменять fake/deterministic; demo acceptance остаётся незавершённой с конкретно указанной недостающей внешней зависимостью.
- [Temporal workflow может стать Completed без request fulfilled] → статус отображать из request aggregate; фазу workflow показывать отдельно, lifecycle не форсировать.
- [Operations source может не предоставлять нужную проекцию] → локальное «недоступно» и открытая задача; не подменять экран или метрики.

## Migration Plan

Продуктовой миграции данных нет. Для каждого запуска создать отдельное пустое PostgreSQL 17 хранилище и отдельные messaging/workflow ресурсы; выполнить штатные migrations и проверить точное состояние схемы read-only. Пользовательские volumes/данные и password file не изменять. Остановка и удаление harness-ресурсов выполняются только по отдельному явному запросу пользователя; автоматическая очистка после фиксации результата запрещена, чтобы не удалить данные живого демо. Это правило ограничено ресурсами данного harness и не распространяется на пользовательский Compose.
