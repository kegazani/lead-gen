# Журнал прогресса (handoff для агентов)

**Зачем этот файл.** Контекстное окно агента конечно. Этот журнал — точка подхвата:
новый агент читает его первым и понимает, где остановилась работа и какой следующий
шаг. Он дополняет, а не заменяет источники истины: `docs/core-roadmap.md` (план),
`openspec/changes/<name>/` (артефакты), `tasks.md` (статус задач чекбоксами).

**Правила ведения.**

1. После каждого завершённого шага обновляй секцию «Где мы остановились» и секцию
   соответствующего change'а. Дата — формат ISO.
2. Сюда пишутся вехи, решения, предупреждения и открытые вопросы. Статус отдельных
   задач живёт только в `tasks.md` соответствующего change'а.
3. Не удаляй историю записей — новые записи добавляются сверху, старые остаются.
4. Файл пишется на русском, как и все артефакты проекта.

---

## Где мы остановились

**2026-09-25 (фикс поиска контактов).** Эскалация экстракции на LLM не работала: `firecrawl.Client.Extract` слал в `/v1/scrape` несуществующий формат `formats:["extract"]` и читал `data` как сам объект контактов. Firecrawl v1 такого формата не знает — структурированный результат запрашивается `formats:["json"]` с `jsonOptions{schema, systemPrompt}` и приходит под `data.json` (устаревшее — `data.llm_extraction`). Из-за этого контакты, извлекаемые только моделью (нестандартная вёрстка, «отдел продаж — Иван Петров», адрес не в явном виде), не находились, а ошибка глушилась в `processPage` (метрика `downstream_errors_total` и всё). Исправлено: корректный запрос `json`, разбор `extractionPayload` (json → llm_extraction → сам `data`). Добавлена диагностика в `RunSearch.processPage`: лог на каждую страницу (`cheap_emails`/`cheap_phones`, `escalated`, `extracted_contacts`, `contacts`) и `Warn` при провале эскалации — чтобы «email не ищется» отличалось от «на странице его нет». Тесты: `TestExtractRequestsJSONFormat` (форма запроса + разбор `data.json`), мок `/v1/scrape` приведён к v1-форме. `go build/vet/test` для `integration-platform` и `lead-management` зелёные. **Не подтверждено живьём** — прогон против реального Firecrawl не делался (ключа нет).

**2026-09-25.** Реализованы два change'а редизайна и развёртки консоли оператора.

- `console-thermal-relight` (представление, `skip_specs`): консоль переведена на направление «термальная прозрачность» — единый насыщенный канал только у температуры лида, стекло как слоистость. `console.css` переписан на токены с двумя темами (`prefers-color-scheme`), оформлено боковое меню (залипание, стекло, термальный маркер активного пункта), введены роли поверхностей, анимация раскрытия через `<details>`/grid-rows, прямой таймлайн статусов с раскрытием деталей при наведении. Воронка превращена в доску с переносом лида перетаскиванием (JS-«остров» `console.js` на существующем `POST /funnel/{id}/stage`, с доступным с клавиатуры fallback-формой и уважением `prefers-reduced-motion`). В `contentSecurityPolicy` добавлена `connect-src 'self'` — до этого `fetch`/HTMX к своему источнику резались, из-за чего авто-обновление `hx-get="/jobs"` было латентно сломано. Исправлены дефекты разметки в `send.html`/`funnel.html`/`dashboard.html`. Golden-файлы и тесты обновлены; браузерная приёмка пройдена (обе темы, меню, таймлайн, перенос, reduced-motion).

- `assistant-streaming-console` (контракт + представление; MODIFIED-дельта `search-strategy`): ответ ассистента выводится потоком. В контракте `llm` появились `ConverseDelta` и `ConverseStream(ctx, req) iter.Seq2[...]`, `ConverseReply` расширен лимитами `PlanLimits`; контрактный suite дополнен `RunLLMConverseStreamContract`. Адаптер `openai` реализует поток: сначала текст реплики (с удержанием разделителя `===PLAN===`), затем JSON-хвост плана. Прикладной метод `ConverseDialogue.StreamReply` соблюдает предел ходов и недоступность LLM. Добавлен SSE-эндпоинт `POST /assistant/stream` (кадры `delta`/`plan`/`done`/`error`, `Flush` после кадра, `no-store`, `X-Accel-Buffering: no`) и JS-«остров» `assistant.js` (`fetch` + `ReadableStream`, `aria-live`). Форма лимитов убрана: параметры согласуются в диалоге и приходят чипами; без JavaScript экран работает через обычный `POST /assistant`. Открытый вопрос №2 канона `web-frontend-architecture` закрыт в пользу SSE.

**Дальше.** Change `email-template-library` (новая capability + MODIFIED `lead-email`) создан и не реализован: библиотека шаблонов писем с поиском и выбором переменных. До его применения `archlint` падает на capability `email-template-library` (спека есть, в `architecture/domain-catalog.yaml` ещё нет) — это ожидаемо для фазы apply.

**2026-09-25.** Реализован change `search-strategy-dialogue` (capability `search-strategy`, контекст `lead-management`): экран ассистента переделан из формы одного выстрела в диалог. В контракте `llm` появилась операция `Converse` (`Message`/`ConverseRequest`/`ConverseReply{Text, Targets, Ready}` + контрактный suite `RunLLMConverseContract`), адаптер `openai` её реализует. Согласованный план обхода (цели + лимиты) едет в задании: `ports.SearchJobPayload.plan`, метод `CreateSearchJobWithPlan`, колонка `plan` в `search_job` (миграция `0007_search_job_plan.sql`); раннер использует план из payload, а при пустых целях откатывается на `Plan` (лимиты — `effectiveLimits`). Состояние диалога — у клиента (`history`/`plan` в скрытых полях), сервер stateless; запуск подтверждает оператор; предел ходов — флаг `-dialogue-max-turns` (по умолчанию 8). Обновлено описание capability в `docs/domain-model.md`. `archlint` зелёный. **Дальше:** архив change'а по запросу пользователя.

**2026-09-24.** Реализован change `firecrawl-credit-budget` (capability в `integration-platform`): модуль безопасного использования Firecrawl на бесплатном плане. Бюджет задачи с атомарным резервированием (`TaskBudget`/`Registry`), остаток аккаунта из `GET /v2/team/credit-usage` (и информационное поле токенов из `token-usage`), общий лимитер частоты/параллелизма, обход с явным `limit`, вычисленным из бюджета, обработка 429 (backoff с jitter, ограниченные повторы) и 402 (остановка новых затратных операций). Контракт `contracts/integration-platform/budget`, впрыск в `cmd/lead-gen` (флаг `firecrawl-max-credits`, по умолчанию 20) и создание бюджета на задание в `RunSearch`. Добавлено отображение бюджета в консоли: «Остаток кредитов Firecrawl» (с временем обновления и «Недоступен» при ошибке) и «Токены Firecrawl» на главной; расход/резерв/доступно в журнале заданий (снимок сохраняется в `search_job.budget`). `make check` зелёный (build/test/vet/importlint/archlint/openspec), `make test-postgres` и race-тесты зелёные. Открытые вопросы — в `design.md` (стоимость extract, персистентность бюджета). **Дальше:** по запросу пользователя — архив change'а (`openspec archive`).

**2026-09-23.** Уточнено ТЗ: операционный CRM-слой (3 вкладки, воронка статусов, шаблоны с merge-полями, ручной диалог, двухфазный краулинг, своя БД без Asana) запланирован **после MVP** — в MVP не входит. Рамка зафиксирована в proposal `mvp-lead-search` (раздел «Следующий срез (после MVP)» в What Changes); в MVP добавлены требования готовности под расширение (единый job-механизм со статусами, компонентная витрина, схема, расширяемая миграциями, адаптер краулера без привязки к режиму discovery/targeted). Также снят `skip_specs: true` в `.openspec.yaml` change'а: дельта `osint-enrichment` (обход robots.txt как осознанное отступление) оформляется первой спекой capability в фазе specs. **Следующий шаг не меняется:** specs (дельта) → design → tasks, затем apply. После MVP — отдельный change на CRM-слой (имя и объём — отдельным запросом).

**2026-09-22 (поздно вечером).** Допущения MVP подтверждены пользователем: тестовых
данных нет (идём сразу на реальный поиск), отправка писем — позже (креды потом),
LLM — любая (тестировать от маленьких моделей к большим), драйвер БД — добавляем
без оговорок. Проведён ресёрч краулеров (katana, Crawl4AI, chromedp, colly — см.
proposal). Создан change `mvp-lead-search`, proposal готов (skip_specs: true,
validate valid). **Следующий шаг:** артефакты design → tasks (specs пропущены),
затем apply. Параллелить нечего — change один; внутри apply можно разбить на
двух агентов: (а) порты/адаптеры integration-platform (краулер, поиск, LLM),
(б) lead-management + консоль + БД, с координацией общих файлов
(`backend/cmd/lead-gen`, `go.work`, `backend/platform`) централизованно.

**2026-09-22 (вечер).** Новая цель пользователя: **MVP-срез для запуска и тестирования** —
собственный краулер (вместо FireCrawl), UI лидов с CRUD, кампании с поисковым
запросом, шаблоны писем с переменными, отправка, стоп-лист; опционально — LLM
для интеллектуального поиска. Роадмап и артефакты ЭТАПА 0 остаются ориентиром;
MVP оформляется отдельным change'ем (рабочее имя `mvp-lead-search-and-outreach`)
как вертикальный срез через 6–7 контекстов. **Следующий шаг:** подтвердить
допущения (данные для теста, источник поиска, SMTP, LLM, драйвер БД) и создать
change через `openspec new change`, затем proposal → specs → design → tasks → apply.

**2026-09-22 (день).** ЭТАП 0 роадмапа (`docs/core-roadmap.md` §6) полностью
спланирован: у трёх change'ей (`platform-governance`, `data-governance`,
`integration-platform`) готовы все артефакты планирования (proposal → specs →
design → tasks), `openspec validate --all --strict` — 23 passed, 0 failed,
`go -C tools/archlint run .` зелёный (29 capability). Каталог, `docs/domain-model.md`
и `docs/system-map.md` уже объявляют новые capability `platform-access` и
`contact-suppression` (сделано при планировании ради зелёного archlint).
Эти change'и переходят в режим ожидания: MVP-срез переиспользует их минимальные
куски (порты, стоп-лист, актор), полный объём — по роадмапу.

## Бюджет кредитов Firecrawl

### `firecrawl-credit-budget`

- **2026-09-24:** срез реализован полностью (17/17 задач). Каталог: `firecrawl-credit-budget` добавлена в capability контекста `integration-platform`. Модуль — в `backend/contexts/integration-platform/internal/adapters/firecrawl` (`credit.go`, `budget.go`, `usage.go`, `limiter.go`, `errors.go`, `guard.go` + `Map`/`Scrape` в `firecrawl.go`). Контракт — `contracts/integration-platform/budget` (плюс `Balance`/`AccountReader`). Впрыск: `NewFirecrawlGuard` в корневом пакете, флаг `-firecrawl-max-credits` (20) в `cmd/lead-gen`, бюджет на задание в `RunSearch` (при `nil`-фабрике поведение не меняется). Консоль: остаток и токены на главной, расход/резерв/доступно в журнале; снимок сохраняется в `search_job.budget` (`RecordBudget`, миграция `green/0004`). Приёмки 1–6 закрыты unit-тестами: атомарное резервирование (`TestTaskBudgetParallelReserve`), обход по бюджету (`TestCrawlCapsLimitByBudget`/`TestCrawlRequiresExplicitLimit`), остаток только из `credit-usage` (`TestUsageCacheUnavailableOnError`), токены только из `token-usage` (`TestTokensAreInformational`), 429/402 (`TestRateLimit*`, `TestPaymentRequiredStops`), освобождение резерва и различение остатка/расхода/резерва (`TestCrawlSettlesActualSpend`, `TestSnapshotDistinguishesValues`).

## MVP-срез (текущая цель)

### `mvp-lead-search`

- **2026-09-25 (фикс экстракции контактов):** эскалация `crawl.Port.Extract` не находила контакты — адаптер слал в Firecrawl несуществующий формат `extract` вместо `json` + `jsonOptions` и читал ответ не оттуда. Исправлено в `integration-platform/internal/adapters/firecrawl`; подробности, диагностика в `processPage` и проверки — в записи «Где мы остановились» выше.

- **2026-09-23 (финал среза):** движок краулинга заменён с self-hosted Crawl4AI
  на **Firecrawl** (hosted API: `/search`, `/map`, `/scrape`, `/crawl`, `extract`):
  JS-рендер и ротация IP на стороне провайдера, robots.txt соблюдается. Прокси-пул
  и OCR-шаг убраны. Экстракция: markdown → regex (email/телефон, деобфускация
  Cloudflare) → эскалация на Firecrawl `extract` только при низком качестве.
  Планирование поиска — LLM-порт `Plan` на **DeepSeek** (OpenAI-совместимый).
  **Срез реализован полностью (40/40 задач):** модули `contracts/integration-platform`
  (порты search/crawl/llm + contracttest), `backend/contexts/integration-platform`
  (адаптеры firecrawl/openai), `backend/contexts/lead-management` (домен lead-profile/
  contact, app RunSearch/Showcase, хранилище pgx+in-memory, консоль html/template+HTMX,
  сервис api/worker), примитив `backend/platform/jobs`, сборка в `cmd/lead-gen` и
  компоненты `frontend/components`. Хранение: карточка — зелёная зона, контакты —
  красная. `docs/crawl4ai-playbook.md` устарел (движок сменён).

- **2026-09-23:** proposal дополнен: раздел «Следующий срез (после MVP)» (операционный CRM-слой: своя БД/CRM без Asana, вкладки «разведка сегментов / кампании / CRM-воронка», двухфазный краулинг discovery→targeted, статусы not warmed → email sent → warming → cold со snooze_until, рассылка по сегменту с исключением bounced/unsubscribed/rejected и email_log, шаблоны с merge-полями и fallback, ручной диалог с историей переписки, сегменты — ссылкой на `lead-segmentation`) и требования готовности MVP под расширение. `skip_specs: true` снят: `osint-enrichment` пойдёт первой спекой (дельта ADDED) в фазе specs — спеки в `openspec/specs/` ещё нет, MODIFIED некуда писать. Объём MVP не изменился. **Дальше:** specs (дельта) → design → tasks → apply (по запросу пользователя).

- **2026-09-22 (поздно вечером, финал):** по решению пользователя в краулер берём
  готовый движок из ресёрча: **self-hosted Crawl4AI** (Docker, Apache-2.0) как
  адаптер за `provider-port` — JS-рендер, Markdown, deep-crawl, антибот. Наш
  код — клиент API, нормализация, дедуп. **LLM — этап эскалации: подключается
  в парсинг только если базовый результат плохой.** В proposal внесены приёмы
  Crawl4AI под задачу «контакт не на базовом эндпоинте»: навигация
  `BestFirstCrawlingStrategy` + `KeywordRelevanceScorer` (contact/team/about/
  staff/impressum), `AsyncUrlSeeder` по sitemap с паттернами, `FilterChain`/
  `URLPatternFilter`; экстракция: `JsonCssExtractionStrategy` (mailto:/tel:/
  .contact-info) → нормализатор → `LLMExtractionStrategy` по схеме
  name/email/phone/role как эскалация; OCR (pytesseract + предобработка) для
  email в картинках — текст в тот же слой. Non-goal: автономная кликательная
  навигация (уровень Firecrawl /agent) — отдельный пункт следующего среза;
  «Показать email»/формы — точечными js_code/wait_for. **Полный набор приёмов
  и источников сохранён в `docs/crawl4ai-playbook.md`** (навигация по
  релевантности, слои экстракции, OCR, граница автономности) — читать перед
  design/apply. Запасной путь —
  Go-адаптер colly+chromedp за тем же портом. Docker на машине есть (29.1.2,
  демон запускается по надобности). **Дальше только proposal:** design/tasks
  создаём по отдельному запросу пользователя, apply не начинаем.
- **2026-09-22:** change создан, `skip_specs: true`, validate valid. Поиск — порт
  Search (дефолт DuckDuckGo HTML без ключа); LLM — порт OpenAI-совместимый
  (base_url+model в конфиге; планирование поиска + экстракция + релевантность;
  без ключа — эвристики: regex, JSON-LD/OG/meta, деобфускация Cloudflare); БД —
  PostgreSQL через pgx/v5 (зелёная schema-per-service, красная — отдельная БД;
  локально нужен поднятый PG, контур в deploy/tofu); консоль — канон
  web-frontend-architecture; dev-актор без auth; отправка/шаблоны/кампании —
  вне среза. Контексты: lead-management (первичный), integration-platform
  (порты+адаптеры). Каталог и модель не меняются (задание поиска —
  операционная запись app-слоя).

## ЭТАП 0 — foundation (platform-governance, data-governance, integration-platform)

### `platform-governance`

- **2026-09-22:** планирование завершено (4/4 артефакта, validate valid).
  Спека `specs/platform-access/spec.md` (8 требований, 19 сценариев); design —
  9 решений (инварианты в AR `tenant`, контракты `contracts/platform-governance`
  с формой порта `AccessDecision` из outreach, outbox, Clock, Snapshot/Rehydrate,
  идемпотентность, аутентификация вне среза, in-process адаптер); tasks — 35
  задач, 1/35 (1.1 каталог — сделано при планировании). Открытые вопросы:
  PostgreSQL-драйвер, сетевой слой, аутентификация, read-порт перечислений,
  начальная синхронизация снимка правил.

### `data-governance`

- **2026-09-22:** планирование завершено (4/4 артефакта, validate valid).
  Спека `specs/contact-suppression/spec.md` (7 требований, 14 сценариев); design —
  9 решений (инварианты в AR `processing-basis`, приоритет стоп-листа у владельца
  правила, контракты `contracts/data-governance`, outbox, Clock для истечения
  срока, Snapshot/Rehydrate, идемпотентность, честное ограничение красной зоны
  in-memory, in-process адаптер); tasks — 34 задачи, 1/34 (1.1 каталог — сделано
  при планировании). Открытые вопросы: граница агрегата при росте стоп-листа,
  планировщик истечения срока, драйвер PostgreSQL/кластер красной зоны, режим A,
  сетевой AMQP-адаптер, read-порт сверки снимков.

### `integration-platform`

- **2026-09-22:** планирование завершено (3/3 + specs skipped, validate valid).
  Design — 10 решений (граница домен ↔ `backend/platform`: манифест/выбор
  адаптера/нормализация/нейтральные id — домен; реестр/конфиг/оболочка вызова —
  platform; контракты `contracts/integration-platform`; 6 категорий ошибок из
  migrate-platform-to-go §3.4; contract suite; размежевание с §3 миграции:
  3.1/3.4/3.5 — этот change, 3.2/3.3/3.6 — backend/platform, разметка задач §3 —
  задача 6.1 в apply); tasks — 27 задач, 0/27. Открытые вопросы: PostgreSQL,
  состав возможностей adapter-capability, публикация событий наружу, сетевой
  адаптер, параметры повторов оболочки.

---

## Диалог подбора стратегии поиска

### `search-strategy-dialogue`

- **2026-09-25:** срез реализован полностью (17/17 задач). Контракт: `contracts/integration-platform/llm` (`Converse` + DTO, `Usage`), адаптер `backend/contexts/integration-platform/internal/adapters/openai`. План задания: `ports.SearchJobPayload.plan` и типы `SearchPlan`/`PlanTarget`/`PlanLimits` с валидацией (regex include/exclude, неотрицательные лимиты); `SearchJobStore.CreateSearchJobWithPlan`; миграция `0007` (колонка `plan`); раннер (`RunSearch.handle`) использует план и `effectiveLimits`, `RunSearch.EnqueuePlanned` собирает финальный план через `Plan` перед постановкой. App-слой: `ConverseDialogue` (предел ходов, безопасные логи). Консоль: экран `assistant.html` — лента диалога, панель плана, редактируемые лимиты, единая форма с действиями `turn`/`launch`; `handleAssistantTurn`/`handleAssistantReply`/`handleAssistantLaunch`. Конфигурация: `-dialogue-max-turns`. Тесты: контрактные suite (llm, хранилище), unit-тесты план/диалог/раннер, HTTP-тесты (ход, запуск, отказ лимитов, stateless).

## Решения и договорённости

- **Работаем строго по OpenSpec workflow** (schema `spec-driven`): change →
  proposal → specs → design → tasks → apply → verify → archive. Вручную каталоги
  под `openspec/changes/` не создавать.
- **Параллельность:** три change'а ЭТАПА 0 независимы по файлам (у каждого свой
  каталог). Параллельная работа допустима; точки согласования — общие контракты
  (`access-decision`, `tenant-context`, `data-zone-policy`, `processing-authority`,
  `suppression-policy`, `provider-port`, `adapter-capability`, `integration-error`)
  и уже существующие порты в `outreach-orchestration` (см. `outreach-approval-slice/design.md`).
- **Правило архивации** (`archive-order-and-rule-deltas`): изменение с
  `## MODIFIED Requirements` архивируется после изменения, создавшего capability.
  Спеки capability `data-handling` и `lead-pool-client-side` уже существуют в
  `full-enterprise-core-scope` и ещё не архивированы.
- **Юридический гейт** (`full-enterprise-core-scope` задача 1.1) — заключение юриста
  до реализации ЭТАПА 1. ЭТАП 0 его не блокирует, но на него завязан
  `data-governance` (зоны данных) — следить.
