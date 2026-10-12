## Why

Текущие health endpoints не дают общего достоверного ответа о готовности: process `/livez` проверяет живость, контекстные `/healthz` поверхностны, `/readyz` есть лишь у двух контекстов, а `integration-platform` считает настроенный адаптер без `Probe` состоянием `up`. Из-за этого наличие ключа может выглядеть как готовая внешняя зависимость, хотя её доступность не проверялась.

## What Changes

- Уточнить в существующей `deployment-topology` общие контракты process liveness (`/livez`), context HTTP liveness (`/healthz`) и readiness (`/readyz`), сохранив совместимость маршрутов.
- Закрепить владение: каждый bounded context задаёт собственную политику readiness и сигналы зависимостей; `integration-platform` владеет provider adapter-status и безопасными пробами в рамках существующего `adapter-status`.
- Требовать для сконфигурированного адаптера без `Probe` статус `unknown` с `probe_not_configured`; конфигурация или наличие ключа не доказывают доступность.
- Планировать только подтверждённые безопасные реальные пробы с ограничениями времени, кэшированием и безопасными кодами; неподтверждённые проверки и отсутствующее покрытие остаются явно открытыми.
- Сохранить покрытие всех 12 bounded contexts в изолированных группах задач. Это перечень контекстов для проверки, а не назначение им общей capability.

## Capabilities

### New Capabilities
- Нет. Общая семантика относится к существующей capability `deployment-topology`.

### Modified Capabilities
- `deployment-topology`: уточнить общую семантику `/livez`, `/healthz` и `/readyz`; readiness policy и сигналы задаются каждым контекстом отдельно.

## Impact

Затрагиваются bounded contexts `lead-management`, `audience`, `outreach-orchestration`, `message-delivery`, `request-fulfillment`, `agent-orchestration`, `platform-governance`, `data-governance`, `integration-platform`, `content-production`, `engagement-observation`, `analytics`; общий process HTTP сервер и composition в `backend/cmd/lead-gen`; Compose probes. Эти 12 контекстов являются охватом проверки, а не владельцами общей capability. Общую семантику топологии задаёт существующая `deployment-topology`, provider adapter-status/probes принадлежат `integration-platform`, readiness dependency policy и сигналы — каждому соответствующему контексту. Новые capability, доменные модели, агрегаты и связи каталога не вводятся. Provider-specific health contracts пока не подтверждены; если безопасная проверка не документирована или недоступна, результат остаётся `unknown`, без подмены реальной пробы.
