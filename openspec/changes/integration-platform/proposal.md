## Why

Вся внешняя работа платформы — сбор, обогащение, верификация, отправка, CRM, публикация — обязана входить через `provider-port`: это зафиксировано каталогом (семь из 28 связей — `provider-port`) и спекой `provider-agnostic-integrations`. Но доменные агрегаты `adapter-registration` и `integration-invocation` не реализованы: сервис `integration-platform` поднимается заглушкой, а механизмы из `migrate-platform-to-go` §3 (манифест, реестр, нормализация ошибок) существуют только как задачи без кода. ЭТАП 1 (`lead-management`) упрётся в этот пробел первым же обогащением: OSINT-источники идут через `provider-port`, и к этому моменту порт должен не проектироваться, а исполняться.

## What Changes

- **Вводится первый вертикальный срез** контекста `integration-platform` по образцу `outreach-approval-slice`: слои `domain`/`app`/`ports`/`adapters`, тесты инвариантов, контрактные suite, in-memory и тестовые адаптеры; PostgreSQL-адаптер — следующим шагом.
- **Реализуются агрегаты каталога.** `adapter-registration` (AR; VO `manifest-version`; политика `adapter-selection`; событие `adapter-registered`; инварианты: возможности адаптера проверяются до вызова; секреты не входят в доменные события и ошибки). `integration-invocation` (AR; VO `correlation-ref`; политика `error-normalization`; события `integration-failed`, `integration-recovered`; инварианты: вызов идёт через provider port, а не через SDK поставщика; ответы и ошибки поставщика нормализуются до доменных значений).
- **Публикуются контракты владельца** в новом модуле `contracts/integration-platform`: `provider-port` (command, `sync`/`http-json`), `adapter-capability`, `integration-error`. Потребители (`lead-management`, `content-production`, `message-delivery`, `sales-workflow`, `community-intelligence`, `publishing`, `outreach-orchestration`) получают контракт и не импортируют SDK.
- **Размежевание с `migrate-platform-to-go` фиксируется явно.** Доменные правила и агрегаты — в `backend/contexts/integration-platform`; технические примитивы без доменных терминов (оболочка вызова порта: тайм-ауты, отмена, ретраи, телеметрия; реестр фабрик; валидация конфигурации) — в `backend/platform`. Задачи §3 изменения `migrate-platform-to-go` размечаются принадлежностью: манифест, нейтральные ошибки, нейтральные идентификаторы — домен этого изменения; реестр, конфигурация, оболочка — `backend/platform`. Двойного владения нет, задачи не дублируются.
- **Создаётся Go-модуль `backend/contexts/integration-platform`** (context-first); в `backend/cmd/lead-gen` заглушка сервиса заменяется сборкой ролей.
- **Вне среза:** SDK реальных поставщиков — срез проверяется только на тестовых адаптерах; поставка production-адаптеров (появляется с первым бизнес-сценарием каждого потребителя); сетевые адаптеры контракта.

## Capabilities

### New Capabilities

Нет — срез реализует существующую capability `provider-agnostic-integrations` (спека в `migrate-platform-to-go/specs/provider-agnostic-integrations/`), не меняя её требований. Изменение помечается `skip_specs: true` в `.openspec.yaml`, как `outreach-approval-slice`.

### Modified Capabilities

Нет.

**Обоснование.** Спека `provider-agnostic-integrations` уже покрывает изоляцию от поставщика, явный выбор адаптера, подключение новых поставщиков, контрактные тесты, нейтральные данные и безопасную конфигурацию — всё, что реализует срез. Выдумывать новую capability ради прохождения валидации не следует; поведение системы на уровне спек не меняется.

## Impact

- **Affected bounded contexts:** первичный — `integration-platform`; потребители `provider-port` — `lead-management`, `content-production`, `message-delivery`, `sales-workflow`, `community-intelligence`, `publishing`, `outreach-orchestration` (связи каталога; код потребителей не меняется).
- **Доменная модель не затрагивается:** агрегаты, события, политики, value objects и связи уже объявлены в `architecture/domain-catalog.yaml`; каталог, `docs/domain-model.md` и диаграммы остаются без изменений.
- **Код:** новые модули `backend/contexts/integration-platform` и `contracts/integration-platform`; технические примитивы в `backend/platform`; регистрация в `go.work`; замена заглушки сборкой сервиса в `backend/cmd/lead-gen`.
- **Смежные изменения:** `openspec/changes/migrate-platform-to-go/tasks.md` — задачи §3 размечаются как покрытые этим изменением (без переноса текстов спек).
- **Спеки:** без изменений (`skip_specs: true`).
- **Ограничения:** только синтетические данные и тестовые адаптеры; драйвер PostgreSQL и внешние зависимости не добавляются; миграций существующих данных нет (сервис был заглушкой).
