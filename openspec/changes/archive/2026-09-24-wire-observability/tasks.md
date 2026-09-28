## 1. Примитив `observ`

- [x] 1.1 Добавить в `observ` генератор `correlation_id` (`crypto/rand`, hex, ограничение длины) и тест: два вызова дают разные непустые значения, ограничение длины соблюдается — проверить `go -C backend/platform test ./observability/observ`
- [x] 1.2 Добавить в `observ` middleware входящего HTTP-запроса (`Middleware(logger, metrics, tracer, serviceName)`): приём `X-Correlation-Id` или генерация нового, `WithFields` в контекст, спан `http.request`, лог завершения (метод, путь, статус, длительность), метрики `http_requests_total{service,code}` и `http_duration_seconds{service}` — тест на `httptest`-запрос без и с заголовком корреляции проверяет поля лога, инкремент метрик и спан в контексте — проверить `go -C backend/platform test ./observability/observ`
- [x] 1.3 Убедиться, что `observ` не импортирует пакеты контекстов и не содержит доменных терминов — проверить `go -C tools/importlint run .`

## 2. Composition root `cmd/lead-gen`

- [x] 2.1 В `main` построить один `observ.Logger`, один `observ.Metrics` и по одному `observ.NewTracer(serviceID)` на сервис; прокачать через `buildOptions` в `Config` `lead-management` и `outreach-orchestration` — проверить `go -C backend/cmd/lead-gen build ./...`
- [x] 2.2 Заменить `slog.*` в `main.go` и `runners.go` на `observ.Logger` (один формат вывода в процессе) — проверить `go -C backend/cmd/lead-gen test ./...`

## 3. Сервис `lead-management`

- [x] 3.1 Добавить в `Config` обязательные поля `Logger`/`Metrics`/`Tracer` (`observ`) и валидацию не-nil в `New` наряду с существующими обязательными зависимостями — проверить `go -C backend/contexts/lead-management test ./...`
- [x] 3.2 Обернуть консольный обработчик (`httpapi.Server`) middleware в `service.go` на границе `http.Server` — проверить `go -C backend/contexts/lead-management test ./...`
- [x] 3.3 Заменить `slog.*` в `service.go` (`runAPI`/`runWorker`) и `internal/adapters/http` на `observ.Logger` — проверить `go -C backend/contexts/lead-management test ./...`
- [x] 3.4 В `internal/app/run_search.go` обвязать задание: `task_id = job.ID`, логи «задание взято / готово / упало» с причиной, спан `job.search`, метрики `jobs_total{outcome}` и `jobs_duration_seconds`; в `internal/app/send.go` и обращениях к движкам — `downstream_errors_total{service,engine}` (`search`/`crawl`/`llm`/`mail`) — проверить `go -C backend/contexts/lead-management test ./...`
- [x] 3.5 Добавить тесты: успешное и упавшее задание инкрементируют `jobs_total` с нужным исходом и кладут `task_id` в контекст; запрос консоли порождает лог с `correlation_id` и метрики запроса — проверить `go -C backend/contexts/lead-management test ./...`

## 4. Сервис `outreach-orchestration`

- [x] 4.1 Добавить в `Config` обязательные поля `Logger`/`Metrics`/`Tracer` (`observ`) и валидацию не-nil в `New` — проверить `go -C backend/contexts/outreach-orchestration test ./...`
- [x] 4.2 Обернуть `slice.Server` middleware в `runAPI` на границе `http.Server` — проверить `go -C backend/contexts/outreach-orchestration test ./...`
- [x] 4.3 Заменить `slog.*` в `service.go` (`runAPI`/`runWorker`/`runChains`/`runSchedule`/`publishPending`) на `observ.Logger` — проверить `go -C backend/contexts/outreach-orchestration test ./...`
- [x] 4.4 Добавить тест: входящий запрос среза порождает лог с `correlation_id` и инкрементирует метрики запроса — проверить `go -C backend/contexts/outreach-orchestration test ./...`

## 5. Каталог и проверки

- [x] 5.1 Добавить `observability` в список `capabilities` контекста `platform-governance` в `architecture/domain-catalog.yaml` (новых агрегатов, событий и контрактов нет; `docs/domain-model.md` и диаграммы не меняются) — проверить `go -C tools/archlint run .`
- [x] 5.2 Прогнать проверку границ и импортов: `go -C tools/archlint run .` и `go -C tools/importlint run .` — обе зелёные
- [x] 5.3 Прогнать тесты всех затронутых модулей `make test` — зелёные
- [x] 5.4 Прогнать `openspec validate wire-observability --strict` — без ошибок
