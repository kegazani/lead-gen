## Context

Примитив `backend/platform/observability/observ` уже содержит `Logger` (JSON → stderr, поля корреляции через `Fields.attrs()`), `Metrics` (`Incr`/`Observe`, in-memory `MemoryMetrics`) и `Tracer` (`Start`, noop, спан несёт `service.name` и имя операции). Поля корреляции `Fields{TenantID, CorrelationID, CausationID, TaskID}` совпадают с `contracts.Envelope`, а `transport/http` уже переносит `X-Tenant-Id`/`X-Correlation-Id`/`X-Causation-Id` в заголовках. Сервисы `lead-management` и `outreach-orchestration` примитив не используют: логируют сырым `slog` на старте/останове, а рабочий путь задания молчит. Мотивация — в `proposal.md`.

Границы, которые надо обвязать: консоль `lead-management` (`httpapi.Server` на `html/template`), HTTP-срез `outreach-orchestration` (`httpapi.Server` реализует `http.Handler`), воркер `lead-management` (`jobs.Runner` → `RunSearch.Handle`).

## Goals / Non-Goals

**Goals:**

- Подключить `observ.Logger`/`Metrics`/`Tracer` к обоим сервисам через composition root и `Config`/`New`.
- Добавить middleware входящего запроса: `correlation_id`, посев `Fields` в контекст, лог завершения, RED-метрики, спан.
- Обвязать фоновое задание `task_id` и логи/метрики/спаны жизненного цикла.
- Сохранить примитив без доменных терминов и без импорта контекстов.

**Non-Goals:**

- Экспортёры метрик и трассировки (OpenTelemetry SDK, Prometheus, `/metrics`, OTLP) — отдельный последующий change; `Metrics` остаётся `MemoryMetrics`, `Tracer` — `noop`.
- Политика сбора и хранения логов, ротация, уровни и сэмплирование.
- Метрика «длина очереди» (требует знания очереди в `jobs.Runner`) — отложена.
- Изменение доменной модели, каталога связей и контрактов.

## Decisions

### Решение 1. Место сборки: composition root, общий Logger/Metrics + Tracer на сервис

`cmd/lead-gen` строит один `observ.NewLogger()` и один `observ.NewMetrics()` на процесс (оба потокобезопасны и не несут состояния сервиса) и один `observ.NewTracer(serviceID)` на сервис (несёт `service.name`). Объекты прокачиваются через `buildOptions` в `Config` каждого сервиса.

- **Альтернатива:** строить логгер/метрики в каждом сервисе отдельно — дублирует настройку вывода и размывает единый формат. Отклонено.

### Решение 2. Зависимости наблюдаемости обязательны в Config

`Logger`/`Metrics`/`Tracer` — обязательные поля `Config` обоих сервисов, проверяются на nil в `New` (как репозитории и порты). Расхождение конфигурации видно при сборке, а не при первом запросе.

- **Альтернатива:** noop по умолчанию при nil — скрывает забытое подключение и даёт «тихие» сервисы. Отклонено.

### Решение 3. Middleware запроса — в `observ`, поверх `net/http`

В `observ` добавляется `Middleware(logger, metrics, tracer)` (или конструктор с теми же аргументами): принимает `X-Correlation-Id` (каноничное имя из `transport/http`), иначе генерирует новый через `crypto/rand` (hex, ограничение длины); кладёт `Fields` в контекст через `WithFields`, открывает спан `http.request`, логирует завершение (метод, путь, статус, длительность) и эмитит `http_requests_total{service,code}` и `http_duration_seconds{service}`. Статус захватывается обёрткой `http.ResponseWriter`.

- **Альтернатива A:** middleware в каждом контексте — дублирование. Отклонено.
- **Альтернатива B:** в `transport/http` — это слой конверта контрактов (`Envelope`), семантически иное; консоль не ходит через него. Отклонено.

Примитив остаётся без доменных терминов: middleware не знает имён сущностей и контрактов, только `service.name` (подставляется при сборке).

### Решение 4. Корреляция задания — в app-слое, не в jobs.Runner

`jobs.Runner` остаётся доменно- и observ-нейтральным (только переводы статусов). Обвязка задания — в `RunSearch.Handle`: `task_id = job.ID`, логи «задание взято / готово / упало» с причиной, спан `job.search`, метрики `jobs_total{outcome}` и `jobs_duration_seconds`. Причина ошибки берётся из возвращённой ошибки `Handle` (тот же текст уходит в `LastError` в `jobs.Runner`).

- **Альтернатива:** hooks в `jobs.Runner` — тянет observability в платформенный примитив и размывает границу «без доменных терминов». Отклонено; метрика длины очереди отложена (Non-Goals).

### Решение 5. Имена метрик и спанов

- **Метрики:** `http_requests_total{service,code}`, `http_duration_seconds{service}`, `jobs_total{service,outcome}`, `jobs_duration_seconds{service}`, `downstream_errors_total{service,engine}` (`engine`: `search`/`crawl`/`llm`/`mail`). Первые два имени уже зафиксированы в `observ/metrics_test.go`.
- **Спаны:** `http.request`, `job.search`, `search.call`, `crawl.call`, `llm.plan`, `mail.send`.
- Метка `service` = `ServiceID` контекста (`lead-management`, `outreach-orchestration`).

### Решение 6. Что логировать / не логировать

- **Логировать:** идентификаторы (`correlation_id`, `task_id`, `job.ID`, id карточки), исход и код статуса, длительность, имя операции, имя движка, причину ошибки.
- **НЕ логировать:** тела запросов, полный текст поискового запроса, ключи и токены, данные красной зоны, персональные данные, адреса узлов (guardrail `architecture.md` §6.5 и `service-topology-console` Решение 8).

### Решение 7. Сырые slog в cmd/lead-gen заменяются

Существующие `slog.*` в `cmd/lead-gen` (жизненный цикл процесса, запуск ролей) и в `service.go` (`runAPI`/`runWorker`) переводятся на `observ.Logger`, чтобы в одном процессе был один формат вывода. Внутренние ошибки рендера шаблона в `httpapi` (`slog.Error`) переходят на `observ.Logger` из middleware/сервиса.

## Risks / Trade-offs

- **[Рост объёма логов]** → логируются только границы запроса/задания и ошибки downstream, не каждый шаг; уровни/сэмплирование — отдельным изменением.
- **[MemoryMetrics неограниченно растёт в долгом процессе]** → in-memory только для dev/тестов и локальной диагностики; заменяется экспортёром, не влияя на точки вызова.
- **[correlation_id от клиента недоверен]** → используется только для корреляции, не для авторизации; длина ограничена, значение — непрозрачная строка.
- **[Двойной формат логов в переходный период]** → замена `slog` в `cmd/lead-gen` выполняется в том же change.
- **[Спаны noop невидимы снаружи]** → контекст несёт спан уже сейчас; подключение экспортёра не меняет вызовы (`Tracer` контракт стабилен).

## Migration Plan

Изменение только подключает эмиссию на существующих границах; доменных данных и миграций схем нет.

1. `cmd/lead-gen`: построить `Logger`/`Metrics`/`Tracer` и прокачать через `buildOptions` в `Config` обоих сервисов; заменить `slog.*` на `observ.Logger`.
2. `lead-management` и `outreach-orchestration`: добавить обязательные поля наблюдаемости в `Config`, валидацию в `New`, обернуть HTTP-обработчики middleware.
3. `lead-management/internal/app/run_search.go`: обвязка задания (`task_id`, логи жизненного цикла, метрики, спан) и downstream-ошибки.
4. `architecture/domain-catalog.yaml`: добавить `observability` в `capabilities` контекста `platform-governance`.
5. Проверки: `go test ./...`, `go -C tools/archlint run .`, `go run ./tools/importlint`.

**Откат.** Снять обёртки middleware и поля `Config`; примитив и домен не меняются, откат без миграций.

## Open Questions

1. **Формат `correlation_id`** — случайный hex против UUID; заменяемая деталь, на модель и задачи не влияет.
2. **Логировать ли полный текст поискового запроса** — по умолчанию нет (приватность, guardrail); подтвердить с бизнесом, если понадобится для отладки поиска.
