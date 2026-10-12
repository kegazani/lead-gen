## Why

Наблюдаемость платформы объявлена в спецификации `observability`, но на
исполнении она не замкнута: метрики пишутся в in-memory `MemoryMetrics` и никуда
не экспортируются (`/metrics` нет), Prometheus в контуре не поднят, трейсинг —
noop, уровень логирования жёстко `Info`, корреляция не сквозная, а outbox relay
глотает ошибки публикации (`_ = publishPending`). Из-за этого оператор не видит,
что происходит с сервисами: нет скрейпа, нет уровня детализации логов, фоновая
публикация может молча терять сбои.

Нужно довести примитив наблюдаемости до реального экспорта, не ломая его
вендор-нейтральность: порт `Metrics{Incr, Observe}` остаётся неизменным и без
зависимостей, а конкретный бэкенд (`prometheus/client_golang`) живёт только в
адаптере.

## What Changes

- **Экспорт метрик.** Процесс отдаёт эндпоинт `/metrics` (Prometheus exposition,
  `text/plain; version=0.0.4`), агрегирующий метрики всех сервисов группы
  процесса; образцы несут метку `service`. Реализация — `prometheus/client_golang`
  строго за портом `Metrics{Incr, Observe}`
  (`backend/platform/observability/observ/metrics.go`); типы SDK не выходят за
  пределы адаптера. Имена метрик и бакеты гистограмм задаются в одном месте.
- **Prometheus в контуре.** Сервис `prometheus` в `compose.yaml` с healthcheck и
  целями на контейнеры групп; `-prometheus-url http://prometheus:9090`
  пробрасывается в группу `platform`. `deploy/tofu/modules/observability/`
  приводится в соответствие (шаблон `prometheus.yml.tftpl`).
- **Настраиваемый уровень логирования.** Флаг `-log-level` управляет `slog`
  через `slog.LevelVar` (`backend/platform/observability/observ/logger.go`).
- **Сквозная корреляция.** Поле `Fields.WorkflowID`; корреляция протягивается через
  `jobs.Runner` (`task_id`), `outbox.Relay`, `transport` и workflow; фоновые
  задания логируют свой жизненный цикл.
- **Relay-логи.** `backend/platform/messaging/outbox/relay.go`: убрать
  `_ = publishPending`, логировать ошибку публикации и учитывать её счётчиком
  отказов вместо проглатывания.
- **Защита от взрыва кардинальности.** Общая композиционная неизменяемая политика
  конечных значений проверяет каждую метку на входе `MemoryMetrics` и
  `prommetrics`; неизвестные значения, включая короткие email/UUID/token,
  отвергаются до создания серии. Runtime-авторегистрация запрещена.
- **Безопасная нормализация ошибок.** Узкий внедряемый `FailureNormalizer`
  использует конечный каталог владельца, сохраняет действующие коды
  `SearchJobError`/`ExportJobError` и безопасные технические категории; неизвестные
  ошибки получают фиксированный fallback. Сырые `err.Error()` не попадают в
  `LastError`, JSON-лог jobs или логи relay, даже при пустом `CodedError.Message()`.
- **Полный путь логирования поиска.** Один эффективный `FailureNormalizer`
  (внедрённый из composition root или безопасный общий fallback сервиса)
  передаётся `jobs.Runner`, `RunSearch` и `progressReporter`. Фатальный путь
  `RunSearch.Handle` сохраняет безопасные `error_code`, `reason`, `diagnostic` и
  `task_id`, не передавая исходную ошибку в `Logger.Error`; возвращаемая ошибка,
  цепочка `Unwrap` и `EventError.Code` остаются прежними. Нефатальные сбои поиска,
  обхода, извлечения, оценки, сохранения и прогресса логируются только конечными
  `diagnostic_code` и безопасными полями. Из логов исключаются запрос, URL и
  производные от URL домен/название компании, включая сообщение об обработанной
  странице.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `observability`: добавляются требования экспорта метрик процесса,
  настраиваемого уровня логирования, сквозной корреляции фоновых контуров и
  защиты от взрыва кардинальности меток; требование RED-метрик расширяется
  доступностью наружу через `/metrics` и валидацией кардинальности.

## Impact

- **Затрагиваемые bounded contexts:** техническая capability `observability`
  принадлежит `platform-governance`; работа идёт в платформенном слое
  наблюдаемости и точках сборки. **Доменная модель не меняется:** агрегаты,
  domain events, владение и связи не затрагиваются, `architecture/domain-catalog.yaml`
  и `docs/domain-model.md` не правятся.
- **Код:** `backend/platform/observability/observ/{metrics.go,logger.go,observ.go,context.go}`,
  `backend/platform/messaging/outbox/relay.go`, `backend/platform/jobs/jobs.go`,
  `backend/platform/transport/`, `backend/cmd/lead-gen/{main.go,runners.go}`;
  `backend/contexts/lead-management/{service.go,internal/app/}` и новый адаптер под
  порт `Metrics`. Изменения `main.go`/runners для этой части не требуются.
- **Инфраструктура:** `compose.yaml`,
  `deploy/tofu/modules/observability/`.
- **Новая внешняя зависимость:** `github.com/prometheus/client_golang` —
  только в адаптере, вне порта `Metrics`.
- **Риски:** взрыв кардинальности меток, будущая миграция на OTel — снимаются
  решениями change (см. `design.md`).
