## Why

Durable-исполнение заявки на Temporal введено (`temporal-execution-platform`), но пока не выдерживает штатных сбоёв, из-за чего заявка может «зависнуть», закрыться успехом без лидов или потерять исполнение при рестарте воркера:

- повторная доставка activity-перехода (потеря ack при краше или таймауте воркера) роняет workflow доменной ошибкой, хотя заявка уже в целевом статусе;
- доменные отказы перехода и постоянные отказы поиска ретраятся впустую 3 раза, а не помечаются non-retryable;
- повторный старт исполнения отвечает «рантайм недоступен» (503) вместо идемпотентного «уже запущено»;
- цикл добора лидов считает сумму сырых счётчиков по раундам, а не уникальные лиды;
- длинные activities (`SearchLeads`, `DraftEmails`) не имеют heartbeat и ограничены 30-секундным `StartToCloseTimeout`, а HTTP-клиенты Firecrawl и LLM собраны на `http.DefaultClient` без таймаута;
- пауза утверждения бесконечна, а исход «0 лидов» не наблюдаем и молча ведёт к `fulfilled`;
- репозиторий заявок существует только в памяти, поэтому durable-исполнение не переживает рестарт процесса, а подключение к Temporal не ретраится при старте.

Всё перечисленное уже согласовано как объём работ; это change фиксирует требования и задачи, чтобы исполнение заявки стало действительно надёжным.

## What Changes

- **Этап 1 — корректность workflow:**
  - переходы-activity `StartProcessing` / `FulfillRequest` / `CancelRequest` идемпотентны: повторное применение, когда заявка уже в целевом статусе, — успех (no-op), а не доменная ошибка; причина — потеря ack не должна ронять workflow;
  - доменные отказы переходов (`ErrInvalidTransition`, `ErrTerminalState`, `ErrReasonRequired`) и постоянные ошибки поиска (заявка не найдена, контракт не подключён) — non-retryable;
  - `WorkflowExecutionAlreadyStarted` при повторном старте трактуется идемпотентно (вернуть существующее исполнение / явный «уже запущено»), а не как «рантайм недоступен» (503);
  - цикл добора лидов считает уникальные лиды (дедуп по id), а не сумму сырых счётчиков по раундам.
- **Этап 2 — защита от зависаний и двойного расхода:**
  - длинные activities получают отдельные опции: увеличенный `StartToCloseTimeout` (минуты) и настроенный `HeartbeatTimeout` с периодическими `RecordHeartbeat`; короткие переходы остаются с малым таймаутом;
  - HTTP-клиенты адаптеров Firecrawl и LLM получают явный таймаут вместо `http.DefaultClient` без таймаута и используют контекст запроса.
- **Этап 3 — политики человеческой паузы и «пустого поиска»:**
  - пауза утверждения ограничена сроком (конфигурируемо, по умолчанию 7 дней) через selector с таймером; по истечении — терминальный исход `cancelled` с причиной «утверждение: срок истёк», а не вечное ожидание; `WorkflowExecutionTimeout` / RunTimeout в старте согласован с этим сроком;
  - исход «0 лидов» становится наблюдаемым (отдельная фаза/поле) и **BREAKING поведения**: без явного одобрения оператора заявка не завершается успехом при нуле лидов — молчаливого `fulfilled` нет.
- **Этап 4 — настоящая durability и устойчивость рантайма:**
  - репозиторий заявок `request-fulfillment` получает персистентную реализацию (PostgreSQL, по образцу контекста `lead-management`: адаптер + миграции + контрактный suite под `LEADGEN_POSTGRES_TEST_DSN`); in-memory остаётся dev-реализацией;
  - воркер Temporal получает настройки устойчивости (`WorkerStopTimeout`, лимиты конкурентности, обработчик фатальных ошибок) и graceful drain при остановке;
  - появляется проба готовности `/readyz`, отражающая доступность рантайма Temporal; `/healthz` остаётся liveness;
  - подключение к Temporal ретраится при старте (backoff), а не помечает рантайм выключенным навсегда после транзиентного сбоя.

## Capabilities

### New Capabilities

- `request-fulfillment-execution`: гарантии durable-исполнения заявки — идемпотентность повторной доставки переходов, классификация отказов activity, идемпотентный старт исполнения, уникальный подсчёт лидов при доборе, таймауты и heartbeat длинных activities, ограниченная сроком пауза утверждения, наблюдаемость исхода «0 лидов» и персистентность состояния заявки, переживающая рестарт.
- `integration-call-resilience`: устойчивость исходящих вызовов внешних провайдеров — явный таймаут HTTP-клиента и уважение контекста (дедлайна и отмены) запроса, чтобы внешний вызов не висел бесконечно.

### Modified Capabilities

- `client-request`: добавляется требование идемпотентности повторного применения перехода — повторный переход, когда заявка уже в целевом статусе, завершается успехом без изменения состояния и без нового доменного события; переход из терминального состояния в другой статус по-прежнему отвергается.
- `deployment-topology`: добавляются требования устойчивости рантайма исполнительного воркера (graceful drain, лимиты конкурентности, обработчик фатальных ошибок), проб живости и готовности (`/healthz` — liveness, `/readyz` — готовность с учётом рантайма) и ретрая подключения к Temporal при старте с backoff.

## Impact

**Затрагиваемые bounded contexts.** `request-fulfillment` — владелец workflow фулфилмента и заявки, первичный контекст change (новые capability `request-fulfillment-execution`, изменение `client-request`). `integration-platform` — владелец внешних адаптеров (новая capability `integration-call-resilience`). `platform-governance` — владелец capability `deployment-topology`, получающей требования устойчивости рантайма и проб. Косвенно участвуют `lead-management` (контракты `lead-search` / `email-generation` и образец PostgreSQL-адаптера) и `outreach-orchestration`, `audience`, `content-production` (activities фулфилмента вызывают их application services через контракты).

**Доменная модель.** Ни новый агрегат, ни сущность, ни value object, ни domain event, ни policy, ни межконтекстный контракт не вводится: заявка остаётся aggregate root `request`, workflow — операционным адаптером исполнения. Меняется только **состав capability**: добавляются `request-fulfillment-execution` (владелец `request-fulfillment`) и `integration-call-resilience` (владелец `integration-platform`). Поэтому в том же change обновляются `architecture/domain-catalog.yaml` (списки `capabilities` у `request-fulfillment` и `integration-platform`), `docs/domain-model.md` и прогоняется `go -C tools/archlint run .`; обязательные диаграммы не меняются (контексты и связи те же).

**Код.**
- `backend/contexts/request-fulfillment/internal/adapters/workflow/` — `fulfillment.go` (граф, опции activities, heartbeat, selector с таймером паузы, дедуп лидов, отдельная фаза «0 лидов»), `activities.go` (классификация отказов, `RecordHeartbeat`), `starter.go` (идемпотентный старт, `WorkflowExecutionAlreadyStarted`), `payload.go` (наблюдаемое состояние).
- `backend/contexts/request-fulfillment/internal/domain/request.go` и `errors.go` — идемпотентность повторного применения перехода (`client-request`).
- `backend/contexts/request-fulfillment/internal/ports/` и `internal/adapters/persistence/postgres/` + `migrations/` + `internal/ports/contracttest/` — персистентный репозиторий заявок и контрактный suite.
- `backend/platform/workflow/` — `worker.go` (устойчивость воркера, graceful drain), `client.go` (`Start` возвращает признак «уже запущено»), `config.go` (параметры таймаутов/сроков).
- `backend/contexts/request-fulfillment/internal/adapters/http/server.go` и `service.go` — маршрут и проводка `/readyz`, состояние рантайма.
- `backend/cmd/lead-gen/main.go` — ретрай подключения к Temporal при старте (backoff) и передача состояния доступности рантайма в пробу готовности.
- `backend/contexts/integration-platform/internal/adapters/firecrawl/firecrawl.go` и `openai/openai.go` — явный `*http.Client` с таймаутом вместо `http.DefaultClient`.
- `Makefile` — цель `test-postgres` дополняется прогоном контрактного suite `request-fulfillment`, если он не покрыт.

**Инфраструктура и данные.** Новая схема PostgreSQL в сервисе `request-fulfillment` (таблицы заявок и outbox) с миграциями green-зоны по образцу `lead-management`; подключение к временной тестовой базе — под `LEADGEN_POSTGRES_TEST_DSN`, обычная сборка базы не требует. Конфигурация `deploy/temporal` и `deploy/services.yaml` не меняются по составу; добавляются параметры таймаутов и срока паузы утверждения.

**Риски и зависимости.** Зависит от `temporal-execution-platform` (рантайм уже введён). Ключевые риски и компромиссы — в `design.md`: миграция уже бегущих исполнений при деплое (`GetVersion` → `DefaultVersion` снимает гейт утверждения у старых исполнений), стоимость повторного краулинга при повторе поиска с дедупликацией лидов и персистентность карточек черновиков писем, которая остаётся производной и в персистентном хранилище заявки не гарантируется.
