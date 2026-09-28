## Why

Примитив наблюдаемости `backend/platform/observability/observ` (`Logger`, `Metrics`, `Tracer`, `Fields`) написан и покрыт тестами, но нигде не подключён: сервисы `lead-management` и `outreach-orchestration` логируют через сырой `slog` только на старте/останове, а рабочий путь задания поиска («взято → готово → упало») вообще молчит. Архитектура (`docs/architecture.md` §9) уже требует структурированные логи с идентификаторами тенанта, задания и трассировки, RED-метрики и сквозную трассировку, а `service-topology-console` уже читает метрики из Prometheus — но эмиссии сегодня нет, поэтому «в логах пусто», а узлы консоли покажут `unknown`.

## What Changes

- Подключается существующий примитив `observ` в composition root `backend/cmd/lead-gen` и пробрасывается в `Config`/`New` обоих реализованных сервисов (`lead-management`, `outreach-orchestration`); сырые `slog.*` на границах заменяются на `observ.Logger`.
- Добавляется middleware входящего HTTP-запроса: генерация `correlation_id` (приём заголовка, иначе новый), посев `observ.Fields` в контекст, лог начала/завершения запроса (метод, путь, статус, длительность), RED-метрики и спан на запрос.
- Добавляется обвязка фонового задания: `task_id` = идентификатор задания, логи «задание взято / готово / упало» с причиной, RED-метрики по исходам заданий, спаны на операциях поиска/обхода/экстракции.
- Наблюдаемость не содержит доменных терминов и не меняет модель: только технические точки на границах запроса и задания.
- В каталог `architecture/domain-catalog.yaml` добавляется capability `observability` у контекста `platform-governance`.
- Без новых зависимостей и без экспортёров: `Metrics` — in-memory (`MemoryMetrics`), `Tracer` — `noop` (контекст несёт сведения о спане), `Logger` — JSON в stderr. Экспортёры (OpenTelemetry SDK + Prometheus + `/metrics` + OTLP-трейсер) — отдельное последующее изменение, точки вызова уже готовы под него.

## Capabilities

### New Capabilities

- `observability`: структурированные логи с корреляцией (`tenant_id`, `correlation_id`, `causation_id`, `task_id`), RED-метрики и спаны на границах входящего HTTP-запроса и фонового задания; примитив наблюдаемости без доменных терминов, эмиссия через порты `Logger`/`Metrics`/`Tracer` без зависимости от конкретного бэкенда.

### Modified Capabilities

Нет.

**Обоснование пустого Modified.** Наблюдаемость — новый кросс-платформенный capability, а не изменение существующих требований. `domain-architecture`, `deployment-topology`, `lead-workflow` и `lead-email` не меняют своих требований: доменная модель, владение агрегатами и границы развёртывания не пересматриваются. Изменение только добавляет эмиссию наблюдаемости на уже существующих границах запроса и задания.

## Impact

**Затрагиваемые bounded contexts.** `lead-management` и `outreach-orchestration` — места подключения (composition, HTTP-адаптеры, app-слой); `platform-governance` — владелец новой capability `observability` в каталоге. Остальные контексты не затрагиваются.

- **Каноническая доменная модель НЕ меняется.** Ни одного нового агрегата, сущности, value object, domain event, policy или межконтекстного контракта. Наблюдаемость — техническая эмиссия на границах, не доменное правило.
- **Каталог:** `architecture/domain-catalog.yaml` — в `capabilities` контекста `platform-governance` добавляется `observability`. Состав контекстов, связи и контракты не меняются; `docs/domain-model.md` и обязательные диаграммы не меняются (новых сущностей и рёбер нет), проверка `go -C tools/archlint run .` остаётся зелёной.
- **Код:** `backend/cmd/lead-gen` (построение `Logger`/`Metrics`/`Tracer` и прокидывание в `Config`), `backend/contexts/lead-management` (`service.go`, `internal/adapters/http`, `internal/app/run_search.go` и смежные use cases), `backend/contexts/outreach-orchestration` (`service.go`, `internal/adapters/http`), при необходимости — небольшие расширения `backend/platform/observability/observ` (middleware запроса, генератор `correlation_id`); сам примитив остаётся без доменных терминов.
- **Зависимости:** новых внешних зависимостей нет; OpenTelemetry и Prometheus-экспортёр сознательно отложены в отдельное изменение.
- **Развёртывание:** не меняется; вывод `Logger` — в stderr (уже собирается контейнером), метрики/спаны остаются in-memory/noop до появления экспортёров.
