## Why

Фундамент доступа отсутствует целиком: нет ни строки кода, ни даже требований для тенантов, пользователей, ролей и аудита. Каталог объявляет агрегат `tenant-access` и контракты `access-decision`, `audit-recorded`, `tenant-context`, но в OpenSpec нет ни одной capability, которая их описывает; сервис `platform-governance` поднимается заглушкой `stubService`. При этом консоль оператора и все core-контексты зависят от `access-decision`: `outreach-orchestration` уже подключил порт `AccessDecision` с явной пометкой «полная семантика RBAC — за `platform-governance`». Пока контекст не реализован, эта заглушка выглядит покрытием доступа, которым не является, а консоль остаётся без настоящей авторизации (`docs/core-roadmap.md` §6, ЭТАП 0).

## What Changes

- **Вводится первый вертикальный срез** контекста `platform-governance` по образцу `outreach-approval-slice`: слои `domain`/`app`/`ports`/`adapters`, тесты инвариантов, контрактные suite, in-memory адаптеры; PostgreSQL-адаптер — следующим шагом (драйвера в проекте нет, тот же открытый вопрос 5 у `outreach-approval-slice`).
- **Реализуется агрегат `tenant-access`** (AR `tenant`; E `tenant`, `user-role`, `audit-record`; VO `tenant-id`, `role-set`) строго по каталогу: создание и отключение тенанта, назначение и отзыв ролей, журнал аудита. Инварианты каталога защищаются доменом: роль принадлежит ровно одному тенанту; отключённый тенант не выдаёт новых решений доступа. Политики: `tenant-isolation`, `least-privilege`.
- **Публикуются контракты владельца** в новом модуле `contracts/platform-governance`: `access-decision` (policy, `async`/`amqp`; при co-location — in-process адаптер), `tenant-context`, `audit-recorded` (event). Решение доступа вычисляется по ролям тенанта; потребители не воспроизводят RBAC-семантику локально.
- **Создаётся Go-модуль `backend/contexts/platform-governance`** (context-first: приватные слои под `internal/`, публичная поверхность — сборка сервиса); в `backend/cmd/lead-gen` заглушка сервиса заменяется сборкой ролей `api` и `worker`.
- **Вне среза:** аутентификация (вход, сессии, учётные данные) — срез работает с уже проверенным актором и не выдаёт заглушку аутентификации за готовую; архитектурные capability контекста (`domain-architecture`, `service-topology`, `service-topology-console`, `deployment-topology`) — они реализованы инструментами и документацией либо принадлежат другим изменениям (консоль — ЭТАП 4) и в runtime-код этого среза не превращаются; сетевой HTTP-слой доступа для других сервисов — следующий шаг, сейчас связь идёт in-process адаптером контракта.

## Capabilities

### New Capabilities

- `platform-access`: тенанты и их изоляция, пользователи и роли, решения доступа по принципу наименьших привилегий, неизменяемый аудит значимых действий; публикуемый `access-decision` для всех контекстов-потребителей.

### Modified Capabilities

Нет.

**Обоснование новой capability.** Агрегат `tenant-access` объявлен каталогом, но ни одна существующая спека его не описывает (проверено: ни `тенант`, ни `access-decision`, ни аудит не встречаются в `openspec/`). Реализовывать срез без требований нельзя, а вписывать доступ в `domain-architecture` нельзя по смыслу: та описывает проверяемую архитектуру, а не продуктовый доступ. Поэтому capability добавляется явно, с обновлением каталога и карт в том же изменении.

## Impact

- **Affected bounded contexts:** первичный — `platform-governance`; потребители `access-decision` — `outreach-orchestration` (порт `AccessDecision` уже существует, заглушка среза заменяется реализацией контракта), в перспективе — все контексты и консоль; `audit-recorded` — событие аудита для потребителей журнала.
- **Каталог и модель:** `architecture/domain-catalog.yaml` — `platform-access` добавляется в `capabilities` контекста `platform-governance` (схема не меняется: поле — список строк); `docs/domain-model.md` — строка capability matrix; `docs/system-map.md` — строка кросс-таблицы §5. Состав контекстов, агрегатов и связей не меняется, диаграммы без изменений.
- **Код:** новые модули `backend/contexts/platform-governance` и `contracts/platform-governance`; регистрация модулей в `go.work`; замена заглушки сборкой сервиса в `backend/cmd/lead-gen`.
- **Спеки:** новая дельта `specs/platform-access/spec.md` (ADDED) в этом изменении.
- **Ограничения:** только синтетические данные; драйвер PostgreSQL и внешние зависимости не добавляются; миграций существующих данных нет (сервис был заглушкой).
