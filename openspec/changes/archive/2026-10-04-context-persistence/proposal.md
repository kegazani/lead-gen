## Why

Семь контекстов (`docs/implementation-matrix.md` §0, пункт B) хранят состояние в памяти процесса: оно не переживает рестарт и не разделяется между ролями. Это ставит заглушку персистентности на путь уже собранных сервисов. Требования продукта при этом не меняются — меняется носитель состояния, поэтому изменение помечено `skip_specs: true` (новых и изменённых capability нет).

## What Changes

- Для каждого из семи контекстов добавляется PostgreSQL-адаптер к уже объявленным портам хранения (репозитории, outbox, unit-of-work) и миграции в `backend/contexts/<context>/migrations/`.
- Схема и роль СУБД — по правилам `service-per-context` (`schema-per-service`): отдельная схема на сервис, общий пул по DSN, образец — `lead-management`, `outreach-orchestration`, `request-fulfillment`.
- In-memory адаптеры сохраняются только для тестов и dev; на продуктовом пути заглушек не остаётся.
- Точки сборки (`cmd/lead-gen`) и цели проверки (`Makefile`) обновляются централизованно.

## Capabilities

### New Capabilities

Нет: изменение не вводит и не меняет требования capability (`skip_specs: true`).

### Modified Capabilities

Нет.

## Impact

- **Affected bounded contexts:** `analytics`, `audience`, `content-production`, `data-governance`, `engagement-observation`, `integration-platform`, `platform-governance`. Контекст `message-delivery` вне этого изменения — персистентность там закрывается изменением `message-delivery`.
- **Домен:** агрегаты, события и контракты не меняются; границы и владение — без изменений (`architecture/domain-catalog.yaml` не правится).
- **Код:** `backend/contexts/<context>/internal/adapters/persistence/postgres/**`, `backend/contexts/<context>/migrations/**`, сборка соответствующего сервиса; `backend/cmd/lead-gen`, `Makefile` — централизованно.
- **Данные:** новый носитель — PostgreSQL (зелёная/красная зона); внешних сервисов не добавляется.
