## 1. Контракты и примитив заданий

- [x] 1.1 Расширить примитив `backend/platform/jobs`: добавить интерфейс `CodedError` (`Code() string`, `Message() string`), поле `ErrorCode` в `Job`, параметр `code` в `Store.UpdateStatus`, и в `Runner.RunOnce` извлекать код/сообщение из ошибки обработчика. Проверить: `go test ./platform/jobs/...` проходит, memory/postgres-реализации собираются.
- [x] 1.2 Расширить контракт `contracts/integration-platform/llm`: `Plan` и `Suggest` возвращают `Usage` (`PromptTokens`/`CompletionTokens`/`TotalTokens`). Проверить: `go test ./contracts/integration-platform/llm/...` проходит.

## 2. Адаптеры integration-platform

- [x] 2.1 В `firecrawl.Client.pollCrawl` при отмене контекста отправлять движку запрос остановки обхода (`DELETE /v1/crawl/{id}` отсоединённым контекстом с коротким дедлайном) перед best-effort `finalSnapshot`. Проверить: `go test ./internal/adapters/firecrawl/...`; подтвердить точный эндпоинт отмены Firecrawl v1 по документации.
- [x] 2.2 В адаптере `openai` парсить поле `usage` ответа chat completion и возвращать токены. Проверить: `go test ./internal/adapters/openai/...` покрывает наличие/отсутствие `usage`.

## 3. Прикладной слой lead-management

- [x] 3.1 Ввести типизированную ошибку `SearchJobError` (`Code`/`Message`/`Detail`) и константы кодов (`search.failed`, `crawl.failed`, `parse.failed`, `budget.exhausted`, `payload.invalid`) с бизнес-формулировками. Проверить: юнит-тест маппинга кода в сообщение.
- [x] 3.2 Расширить `ports.SearchJobProgress` счётчиками `PlannedTargets`, `PlannedURLs`, `ParseErrors`, `TokensSpent` и фазой `extracting`; дополнить `progressReporter`. Проверить: тесты репортера прогресса проходят.
- [x] 3.3 В `RunSearch` обернуть ошибки шагов кодами, накапливать `planned` (цели + URL), `found`, `parse_errors` с ретраем через эскалацию и `tokens_spent` из `Plan`. Проверить: `go test ./internal/app/...` покрывает счётчики и ретрай разбора.
- [x] 3.4 В `RunSearch.handle` фиксировать `ResultRef` при отмене (в `finishCancelled` вызвать `RecordResult` отсоединённым контекстом). Проверить: e2e-тест «отмена после частичного обхода показывает найденные компании в журнале».

## 4. Хранилище и миграции

- [x] 4.1 Добавить миграцию зелёной зоны `0006_search_job_status_events.sql`: колонка `error_code` в `search_job` и таблица `search_job_event`. Проверить: `go test ./migrations/...` проходит, миграция применяется на чистой базе.
- [x] 4.2 Ввести `ports.SearchJobEvent` и метод `RecordEvent` в `SearchJobStore`; в postgres-адаптере реализовать `UpdateStatus` с `code`, запись `error_code`, вставку событий и вычисление позиции в очереди в `ListSearchJobs`. Проверить: `go test ./internal/adapters/persistence/postgres/...` покрывает события и позицию.
- [x] 4.3 Привести memory-адаптер `SearchJobStore` к той же сигнатуре (`UpdateStatus` с `code`, `RecordEvent`, позиция в очереди). Проверить: `go test ./internal/adapters/persistence/memory/...`.

## 5. Консоль и оформление

- [x] 5.1 В `views.go` показывать бизнес-формулировку ошибки вместо сырого текста, добавить hover-строку статуса и данные очереди (позиция/причина ожидания). Проверить: golden-тесты представлений обновлены и проходят.
- [x] 5.2 Убрать расхождение лимитов: единый предел для `handleDashboard` и `handleJobs` (например, 100) и прокрутка журнала через CSS (`max-height` + `overflow-y: auto`). Проверить: `go test ./internal/adapters/http/...` и визуальная проверка журнала.
- [x] 5.3 Добавить hover-детализацию статуса (`title` на бейдже) в шаблон журнала. Проверить: разметка фрагмента журнала содержит подпись статуса.

## 6. Каталог, документация и проверка границ

- [x] 6.1 Обновить `architecture/domain-catalog.yaml` (capability `search-job-error-codes` и `search-job-events` в контексте `lead-management`) и `docs/domain-model.md`. Проверить: `go -C tools/archlint run .` проходит.
- [x] 6.2 Прогнать полный набор тестов затронутых модулей (`go test ./...` в `backend` и `contracts`) и `openspec validate "search-job-status-and-events" --strict`. Проверить: все тесты и валидация зелёные.
