# Задачи: бюджет кредитов Firecrawl

Порядок по зависимостям: каталог → модель стоимости и ошибки → бюджет → остаток аккаунта → лимитер → обёртка → контракт → впрыск → проверки.

## 1. Каталог и capability

- [x] 1.1 Добавить capability `firecrawl-credit-budget` в список `capabilities` контекста `integration-platform` в `architecture/domain-catalog.yaml`; проверить `go -C tools/archlint run .` — команда завершается кодом 0 (после создания спеки в п. 2).

## 2. Модель стоимости и ошибки

- [x] 2.1 Реализовать в `backend/contexts/integration-platform/internal/adapters/firecrawl/credit.go` чистые функции стоимости: `scrape` — 1, `crawl` — страницы, `map` — 1, `search` — `2 * ceil(n/10)`; проверить unit-тесты граничных значений (0, 1, 10, 11 результатов).
- [x] 2.2 Реализовать типизированную HTTP-ошибку в `errors.go` с полем `StatusCode` и предикатами `IsRateLimited` (429) и `IsPaymentRequired` (402); заменить в `doJSON` строковую ошибку статуса на типизированную, сохранив текст; проверить существующие contract suite зелёными.

## 3. Бюджет задачи

- [x] 3.1 Реализовать в `budget.go` `TaskBudget` с полями `max_credits`, `reserved_credits`, `confirmed_spent_credits`, статусом и мьютексом; методы `Reserve` (атомарная проверка `reserved+confirmed+amount <= max`) и `Settle` (освобождение резерва и подтверждение фактического расхода); проверить unit-тесты атомарности (параллельные `Reserve` не превышают лимит) и освобождения резерва.
- [x] 3.2 Реализовать `Registry` — реестр бюджетов задач по идентификатору под мьютексом; проверить unit-тесты создания задачи и доступа к ней.

## 4. Остаток аккаунта и токены

- [x] 4.1 Реализовать в `usage.go` клиент `GET /v2/team/credit-usage` и `GET /v2/team/token-usage` (stdlib HTTP, тот же `baseURL`/ключ) с типами `CreditUsage`/`TokenUsage`; проверить unit-тесты декодирования `data.remainingCredits` и `data.remainingTokens` на httptest-сервере.
- [x] 4.2 Реализовать кэш `Usage` с временем последнего успешного обновления и TTL свежести; при ошибке — состояние «недоступен» без подстановки вычисленного значения; проверить unit-тесты успешного обновления, ошибки и TTL-истечения.

## 5. Лимитер

- [x] 5.1 Реализовать в `limiter.go` лимитер: семафор ёмкостью 1 и минимальные паузы (10 с обычные, 40 с `crawl`) из конфигурации; методы `Acquire`/`AcquireCrawl` с уважением `context.Context`; проверить unit-тесты последовательного выполнения и выдержки паузы (с фейковыми часами).

## 6. Обёртка `Guard` и задача `Task`

- [x] 6.1 Реализовать в `guard.go` `Guard` (raw-клиент, лимитер, кэш остатка, реестр) и `Task` (реализует `search.Port`/`crawl.Port`, методы `Map`/`Scrape`); вызов проходит: проверка свежего остатка → оценка стоимости → резервирование → лимитер → вызов → сверка и освобождение; проверить unit-тесты успешного вызова с подтверждением расхода.
- [x] 6.2 Реализовать логику `crawl`: вычисление `limit = min(max_pages, доступный бюджет)`, отказ без явного `max_pages`, отказ при стоимости выше бюджета; проверить unit-тесты: без лимита — отказ до вызова Firecrawl, бюджет 20 не запрашивает обход стоимостью выше 20.
- [x] 6.3 Реализовать обработку 429 (backoff с возрастающей задержкой и случайным разбросом, ограниченное число повторов) и 402 (остановка новых затратных запросов); проверить unit-тесты отсутствия лавины повторов и остановки на 402.

## 7. Контракт `contracts/integration-platform/budget`

- [x] 7.1 Создать пакет `contracts/integration-platform/budget` с `Factory.Begin(taskID, maxCredits) (Task, error)`, `Task` (встраивает `search.Port` и `crawl.Port`, добавляет `Snapshot()`), `Snapshot` (остаток аккаунта, подтверждённый расход, резерв, доступно); проверить `make build` и отсутствие импорта модулей контекстов.

## 8. Впрыск и точка входа

- [x] 8.1 Реализовать в `backend/contexts/integration-platform` конструктор `NewFirecrawlGuard(cfg)` (создаёт `Guard` вокруг raw-клиента с конфигурацией бюджета/лимитера); проверить `make build`.
- [x] 8.2 В `backend/cmd/lead-gen` добавить флаг/env `firecrawl_max_credits` и собрать `Guard`; передать его в `lead-management` как `budget.Factory`; проверить `go build -o /dev/null ./...` в `backend/cmd/lead-gen`.
- [x] 8.3 В `lead-management` `RunSearch` добавить необязательный `budget.Factory` и `max_credits`; при обработке задания создавать бюджет задачи и использовать его как `search.Port`/`crawl.Port`; при `nil`-фабрике поведение не меняется; проверить unit-тесты на фейковых портах и с фейковой фабрикой бюджета.

## 9. Проверка среза

- [x] 9.1 Прогнать `make check` (build, test, vet, importlint, archlint, OpenSpec) — всё зелёное; проверить отсутствие нарушений границ импортов.
- [x] 9.2 Прогнать `openspec validate firecrawl-credit-budget --strict`; проверить proposal, specs, design и tasks валидными.
- [x] 9.3 Обновить `docs/progress-log.md`; проверить `go -C tools/archlint run .` кодом 0.

## 10. Отображение бюджета в консоли

- [x] 10.1 Расширить контракт `contracts/integration-platform/budget` типами `Balance` и `AccountReader`; реализовать `AccountReader` в `Guard`; проверить `make build` и отсутствие импорта модулей контекстов из контракта.
- [x] 10.2 Сохранять снимок бюджета задания: метод `RecordBudget` в `SearchJobStore`, колонка `budget` jsonb (миграция `migrations/green/0004_search_job_budget.sql`), адаптеры memory/postgres; проверить contract suite порта против обеих реализаций и `make test-postgres`.
- [x] 10.3 Отобразить на главной «Остаток кредитов Firecrawl» (с временем обновления и «Недоступен» при ошибке) и «Токены Firecrawl», а в журнале заданий — расход/резерв/доступно; проверить unit-тесты форматирования и рендер главной.
