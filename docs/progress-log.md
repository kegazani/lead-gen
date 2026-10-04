# Журнал прогресса (handoff для агентов)

**Фокус сейчас:** **сквозной E2E-продукт + полные фронты.** Волны 1–3 выполнены (backend RAG/недобор→письма/мост, консоль срезы 3–5, генерация лендингов, in-process E2E «утверждено → отправлено»); три change заархивированы. Гейт зелёный: `make check` (OpenSpec 50 passed, archlint 53 capability / 30 связей) и `make test-postgres` (11 suite `ok`). **Список следующих работ для новой сессии — `docs/next-session.md`** (приоритеты: замкнуть `MaterialVersionRef` в approve→relay, до-закрыть срезы 3.4/5.3/6.x/7.x консоли, `service-topology-console`, сверка/архивация прочих changes, live-приёмка). Цель и волны — `docs/e2e-plan.md`.

**Зачем этот файл.** Контекстное окно агента конечно. Этот журнал — точка подхвата:
новый агент читает его первым и понимает, где остановилась работа и какой следующий
шаг. Он дополняет, а не заменяет источники истины: `docs/core-roadmap.md` (план),
`openspec/changes/<name>/` (артефакты), `tasks.md` (статус задач чекбоксами).

**Правила ведения.**

1. **Держи строку «Фокус сейчас» вверху актуальной** — один активный change (или
   явное «нет активного change»). Это первое, что читает агент при старте сессии.
2. После каждого завершённого шага обновляй секцию «Где мы остановились» и секцию
   соответствующего change'а. Дата — формат ISO.
3. Сюда пишутся вехи, решения, предупреждения и открытые вопросы. Статус отдельных
   задач живёт только в `tasks.md` соответствующего change'а.
4. Не удаляй историю записей — новые записи добавляются сверху, старые остаются.
5. Файл пишется на русском, как и все артефакты проекта.

---

## Где мы остановились

**2026-10-04 (волны 2–3: консоль, генерация лендингов, in-process E2E).** Волна 2 (фронты) и волна 3 (приёмка) продвинуты пятью субагентами (по одному на контекст, общий `frontend/components` не трогали) и проводкой головного агента.

*Консоль (`web-frontend-architecture`).* Срез 3: дашборд лидов (`lead-management`) и раздел отчётности/фильтры/живое обновление/дашборд клиента (`analytics`) подтверждены тестами. Срез 4 (очередь утверждения рассылок, `outreach-orchestration`): список с фильтрами и риском, пакетное решение с обязательным предъявлением состава, индивидуальные решения (правка с автором, отклонение, отложение), отделение рисковых, срез зависших — с консольными экранами (сессия/CSRF, PostgreSQL, миграция `0004`). Срез 5 (`platform-governance`): пользователи/роли через владельца, доступ по снимку `access-decision` (матрица роль→раздел→действие, отказ на сервере), переключение тенанта, журнал аудита с CSV-экспортом. Проводка в `cmd/lead-gen`: консоль рассылок (`ConsoleAuth`/`Console` + общий стор сессий).

*Генерация лендингов (`landing-page-generator` + `linguistics`).* В `content-production` реализованы генерация/переформулирование текста через LLM-порт, проверки сведений (числа/сроки/гарантии) и дословных совпадений, исключение персональных данных, сбор с сайта за портом. Проводка `LLM` в `cmd/lead-gen`.

*Волна 3 (in-process E2E).* `backend/cmd/lead-gen/e2e_approved_to_sent_test.go` — сквозной тест замыкания `approve → outbox → relay → мост (delivery-target + content-version) → команда → SMTP-захват` на реальной композиции `buildService`, вкл. идемпотентность и отказ на не-email. **Честная находка:** в реальном пути approve→relay `relay.MapActionApproved` не заполняет `MaterialVersionRef` — материал письма пуст; кандидат в отдельный change (`material-ref-in-approved-action`).

*Открыто.* (1) Срез 5.3 — состояние адаптеров (нужен контракт владельца `integration-platform`). (2) Срезы 6–7 — долгие операции/DLQ и a11y/l10n/безопасность (затрагивают общий `frontend/components`). (3) `service-topology-console` — нужен `backend/platform/topology` + `deploy/infrastructure.yaml` + `tools/archlint`. (4) `MaterialVersionRef` в approve→relay. (5) Live-контур (реальные Temporal/SMTP/сети).

**2026-10-04 (волна 1 backend: три change + семь контекстов; гейт зелёный).** Созданы три недостающих change OpenSpec с артефактами (proposal/specs/design/tasks, `validate --strict` зелёный): `real-vector-recall`, `shortfall-to-letters`, `outreach-delivery-bridge`. Каталог `architecture/domain-catalog.yaml` и `docs/domain-model.md` обновлены в том же change (capability `vector-recall` → `integration-platform`, `request-shortfall` → `request-fulfillment`, `delivery-bridge` → `message-delivery`; контракт `delivery-target` у `lead-management`, связь `leads-for-delivery`).

*Волна 1 (один субагент на контекст, scope строго внутри каталога; общие файлы правил головной агент):*
- `integration-platform` — реальный эмбеддер поверх `/v1/embeddings` и персистентный векторный индекс (`vector/postgres`, миграция `0002`), общий suite `vector.Port`; `HashEmbedder` оставлен тест/dev-двойником.
- `analytics` — отчётность `campaign-reporting` (группы 9–12): дашборды кампаний/лидогенерации/воронки, качество базы, управленческая отчётность, порог минимальной группы, фильтры, CSV/XLSX, время обновления.
- `request-fulfillment` — capability `request-shortfall`: поле `Shortfall` в наблюдаемом состоянии, вычисление недобора, версионированный гейт `shortfall-gate` над шагом писем.
- `lead-management` — контракт `contracts/lead-management/deliverytarget` + резолвер получателя по контактам; сверх того — доменное ядро `email-verification` (реальные DNS/remote адаптеры) и `company-size-assessment`.
- `platform-governance` — вертикальный срез `platform-access` (домен/app/порты/адаптеры/HTTP, PostgreSQL, outbox, идемпотентность) + сохранённый `client-portal`.
- `content-production` — capability `landing-page-generator` (структура из материалов, сборка самодостаточной страницы, форма, архив, история версий) поверх реального Chromium-рендера.
- `message-delivery` — capability `delivery-bridge`: порт-резолвер ссылок и адаптер-источник команд `approvedaction` (приём `approved-action` → `domain.DeliveryCommand`, идемпотентность, отказ на не-email/невалидное).

*Интеграция головным агентом.* В `backend/cmd/lead-gen` векторный порт собран (пул integration-platform + реальный эмбеддер) и передан в `request-fulfillment` (эмбеддинг оффера). Исправлен устаревший тест `backend/platform/deployment` (ожидал полную рамку 15 сервисов/38 рёбер; продукт сведён к 11 контекстам — ожидания приведены к 11/29, `deploy/README.md` согласован). Гейт: `make check` зелёный (OpenSpec 50 passed; archlint 53 capability / 29 связей); `make test-postgres` — 11 suite `ok`.

*Проводка и находки (вторая итерация волны 1).* В `backend/cmd/lead-gen` доделана интеграция: (1) векторный порт `vectorRecallPort` собран из `support-green-dsn` + ключа эмбеддера и подключён и в `request-fulfillment` (`Config.Vector`), и в `lead-management` (`Config.Vector`; добавлены аксессоры `SemanticRecall()`/`EmbedCompany()`, HTTP `POST /semantic-recall` и `POST /cards/{id}/embed` за сессией оператора). (2) Мост `approved-action → delivery-command`: `lead-management` отдаёт `Service.DeliveryTargetResolver(tenant)`, `message-delivery` — обёртку `NewApprovedActionBridge` (+`Bind`/`Sink`), `outreach-orchestration` получил `Config.ApprovedActionSink`; `main` собирает сервисы через кэш (`buildOnce`) и связывает один экземпляр моста со всеми тремя сервисами. Найденное расхождение: активный путь публикации утверждённого действия — outbox → relay → `Sink` (не `ApprovedActionPublisher`, который никто не вызывает); закрыто подменой sink на мост.

*Фиксация контракта материала и приёмка (2026-10-04).* Расхождение с материалом письма закрыто: контракт `contracts/content-production/contentversion` расширен необязательным полем `Subject` (MINOR `1.0.0 → 1.1.0`), `NewContentVersion` заполняет тему из заголовка материала; в `message-delivery` добавлен резолвер `contentversion.Source → ports.MaterialResolver` (+`NewContentMaterialResolver`); в `cmd/lead-gen` материал подключён в мост (`contentSource` от `content-production`). Каталог: добавлена связь `content-to-delivery`; `docs/domain-model.md` обновлён. Все три change **заархивированы**: specs `vector-recall`, `request-shortfall`, `delivery-bridge` созданы в `openspec/specs/`, MODIFIED `request-fulfillment-execution` слита; `archlint` — 53 capability / 30 связей, `openspec validate --all --strict` — 50 passed. **Не коммитил** (сделаю по явной просьбе).

*Осталось (вне закрытых задач).* (1) **Волна 2:** остальные срезы консоли (общий `frontend/components` — один writer); публичный фронт `frontend/public` уже реализован. (2) **Волна 3:** live-E2E на реальном контуре (волна 3 e2e-плана). (3) `content-production`/`email-verification`/`company-size-assessment` требуют внешних провайдеров (`linguistics`, движок 3.1, верификатор) — честно `unavailable`.

**2026-10-04 (план: сквозной E2E-продукт + фронты; параллельные волны).** Зафиксирована цель продукта.

*Целевой поток.* Клиент регистрирует компанию (имя + описание) → создаёт **заявку** (услуги, которые компания предоставляет, + описание искомых лидов) → **админ** видит заявку, при необходимости связывается с клиентом и правит текст, одобряет → **ИИ запускает поиск** (сначала по пулу, при недоборе — дозабор; семантика/RAG) → при недостатке лидов **генерирует персонализированные письма** по каждой компании → **оператор** смотрит, при необходимости правит и **отправляет** → клиент видит **дашборд**.

*Соответствие домену (уже описано и в основном реализовано — эпик `client-request-domain`):* `client-portal`, `client-request`, `request-validation`, `request-fulfillment-execution` (`request-fulfillment`); `lead-search`, `semantic-lead-search`, `lead-relevance`, `company-quota-search`, `email-generation`, `lead-email`, `email-template-library`, `search-strategy` (`lead-management`); `outreach-approval` + `outreach-channel-email`/`domain-deliverability` (`outreach-orchestration` → `message-delivery`); `client-dashboard` (`analytics`); согласие/стоп-лист (`data-governance`). CRM/сделки (`sales-workflow`) и биллинг (`billing`) вырезаны — **вне текущей цели**.

*Пробелы к закрытию:* (1) реальный **векторный стор для RAG** (сейчас in-memory в `integration-platform/.../vector`); (2) **авто-триггер «недобор лидов → генерация писем»** (явной capability нет); (3) **отчётность/дашборды** (`sequences-and-reporting` 9–12); (4) **фронты** (`web-frontend-architecture` 14/41, `service-topology-console` 0/57, `landing-page-generator` 1/36); (5) **мост `approved-action → delivery-command`**.

*Параллельные волны (один writer на bounded context; общие файлы — `cmd/lead-gen`, `Makefile`, `go.work`, каталог, `docs` — централизованно):*
- **Волна 1 (backend, параллельно):** `integration-platform` — векторный стор/RAG; `analytics` — отчётность/дашборды; `request-fulfillment` — валидация + авто-триггер «недобор→письма»; `lead-management` — поиск/RAG-использование, quota, письма; `platform-governance` — регистрация/тенант/портал-backend; `content-production` — генерация лендингов; `message-delivery` — мост `approved-action → delivery-command`.
- **Волна 2 (фронты, сериализовано — общий `frontend/components`):** админская консоль (`web-frontend-architecture`); клиентский портал/дашборд; экран топологии (`service-topology-console`); публичные лендинги (`frontend/public`, `landing-page-generator`).
- **Волна 3:** сквозная приёмка E2E на реальном backend (PG + SMTP + движок цепочек + RAG).

**2026-10-04 (остаются A1/A2/A9/B).** Закрыты/устранены **A1** (`message-delivery` — реальная отправка `mail`/SMTP), **A2** (`content-rendering` — реальный Chromium-рендер), **A9** (`sequences-and-reporting` — движок последовательностей + PG) и **B** (`context-persistence` — PostgreSQL для 7 контекстов). Гейт: `make check` зелёный (OpenSpec **54 passed**), `make test-postgres` — 15 PG-suite `ok`. Вне объёма: мост `approved-action → delivery-command`; выравнивание каталога `provider-port`↔`mail`.

**2026-10-04 (P0: A1 и B закрыты кодом, параллельная разработка по контекстам).** Параллельно по bounded context (один writer на контекст) реализованы: change `message-delivery` (реальная отправка за контрактом `mail`/SMTP вместо `mailer.Unavailable`, стоп-лист через `suppression-policy`, PostgreSQL-персистентность, роль `schedule`) и change `context-persistence` (PG-адаптеры + миграции + contract-тесты для 7 контекстов: `analytics`, `audience`, `content-production`, `data-governance`, `engagement-observation`, `integration-platform`, `platform-governance`). Проводка выполнена централизованно одним агентом в `backend/cmd/lead-gen` (флаг `-support-green-dsn`, SMTP-порты, применение миграций) и `Makefile` (цель `test-postgres` на 11 контекстов). Гейт: `make check` зелёный (OpenSpec 53 passed), `make test-postgres` — все 11 PG-suite `ok`. `docs/implementation-matrix.md` §0/§2/§4 обновлены. Вне объёма `message-delivery` остаётся мост `approved-action → delivery-command` (нужен контракт разрешения ссылок). Выявлен системный разрыв каталога: `architecture/domain-catalog.yaml` объявляет связи `integrations-for-leads`/`integrations-for-delivery` через `provider-port`, тогда как реальные адаптеры используют контракт `mail` (наследовано от `lead-management`) — нужен отдельный change выравнивания каталога и диаграмм. **Дальше:** verify/archive обоих changes; очередь §0 — A2, A9.

**2026-10-04 (старт P0-контура `message-delivery`).** Создан change `message-delivery` (артефакты proposal/specs/design/tasks, `validate --strict` зелёный) — первый по очереди §0 (`docs/implementation-matrix.md`): **A1** (реальная отправка почты за `ports.Mailer`) и **B** (PostgreSQL-персистентность контекста). Решения. (1) Реальный почтовый адаптер использует контракт `mail` (SMTP-адаптер `integration-platform`), а не `provider-port`: у `provider-port` реальный провайдер не подключён (`unconfiguredProvider` возвращает окончательный отказ), тогда как SMTP-адаптер за `mail.Port` уже реально используется в `lead-management`. (2) Мост `approved-action → delivery-command` вынесен из объёма change: `approved-action` несёт только непрозрачные ссылки и не содержит адреса получателя, разрешение `RecipientRef`/`MaterialVersionRef` требует межконтекстного контракта — отдельный change. Замечание: `openspec` предупреждает о некорректных правилах артефакта `tasks` в `openspec/config.yaml` («Rules for 'tasks' must be an array of strings…»), правила игнорируются. **Дальше:** apply по `tasks.md` (группы 1–7).

**2026-10-03 (сверка состояния по коду).** Составлена фактическая матрица реализации — [`docs/implementation-matrix.md`](implementation-matrix.md): по каждому из 15 контекстов сведены домен / app / порты / персистентность / API / внешние адаптеры / контракты / тесты. Вывод: **собраны все 15 сервисов каталога** (заглушек сервисов нет), но зрелость различается: **уровень A** — `lead-management`, `request-fulfillment`, `integration-platform`; **уровень B** — `outreach-orchestration` (PG), `platform-governance`, `analytics`; **уровень C** (домен + app + in-memory, внешние порты — заглушки) — остальные девять. PostgreSQL и миграции есть только у трёх контекстов; реально ходят в сеть только Firecrawl, LLM, SMTP (через `integration-platform`) и Temporal. `docs/core-roadmap.md` и `docs/implementation-matrix.md` приведены к этим фактам.

**OpenSpec приведён к фактам (2026-10-03).** Заархивирован завершённый change `integration-platform` (было 28/28, не заархивирован) → `archive/2026-10-04-integration-platform`. Отметки задач сверены с кодом только там, где критерий «верификация» буквально выполняется существующим кодом и тестами; задачи, требующие реальных внешних интеграций, PostgreSQL или клиентского контура, оставлены открытыми. Отмечено: `data-governance` +13, `migrate-platform-to-go` +9, `archive-order-and-rule-deltas` +3, `lead-segmentation` +2, `platform-governance` +2, `service-per-context` +2, `sequences-and-reporting` +2, по +1 — `company-size-assessment`, `email-verification`, `outreach-channel-form`, `landing-page-generator`; `web-frontend-architecture` — без изменений. `make check` зелёный (51/51 OpenSpec). **Остаётся решить:** `self-hosted-crawler` (вытеснен Firecrawl), `system-map-and-cpp-baseline` (C++-задачи заменены, невыполнимы) и `archive-order-and-rule-deltas` (остаток условный — ждёт архивации рамки); `service-topology-console` и `full-enterprise-core-scope` не реализуются без отдельного решения.

**Дальше:** ближайшие содержательные разрывы — почта-контур, рендер, звонки; персистентность пяти контекстов уровня C и `schema-per-service`.

**2026-10-02 (эпик `client-request-domain` заархивирован).** Завершён и заархивирован эпик `client-request-domain` (6 change'ов: `client-request-domain`, `temporal-execution-platform`, `request-validation-workflow`, `semantic-lead-search`, `client-portal`, `client-dashboard-metrics`; архивация 2026-09-30…2026-10-02). Закрыт 15-й контекст `request-fulfillment`: сервис собран в `buildService` с воркером workflow поверх self-hosted Temporal; capability `client-request` и `request-validation`; связи `vector-for-requests`, `call-intent-for-requests`, `request-intent-from-portal`. В коде собраны 6 сервисов, остальные 9 — заглушка `stubService`. **Дальше:** по `docs/core-roadmap.md` — дозакрыть ЭТАП 0 (`data-governance`) и ЭТАП 1 (`audience`).

**2026-09-28 (этап 1 закоммичен, начат свой краулер).** Первый этап закоммичен одним коммитом `7f186cd` (452 файла): MVP-срез `mvp-lead-search`, foundation (`platform-governance`, `data-governance`, `integration-platform`), `ai-lead-discovery`, `manual-crawl-params-and-company-target`, архивы срезов, спеки и документация. В `.gitignore` добавлены правила под артефакты `backend/tmp/` (бинарник сборки) и `.playwright-mcp/` (логи/скриншоты MCP) — в историю не попали. Создан change **`self-hosted-crawler`** (proposal → specs → design → tasks, все 4 артефакта, `validate --strict` зелёный): self-hosted движок **Crawl4AI** (Apache-2.0, контейнер) за существующим `crawl.Port`/`search.Port`, Firecrawl остаётся альтернативой (`crawl_engine=firecrawl|crawl4ai`). Решения: новая capability `crawler-proxy-pool` (источники-`Source`, уровни `cheap`/`premium`, sticky по домену, карантин, эскалация; комбинирование дешёвых списков с провайдером заложено сразу); robots.txt управляется конфигурацией `crawl_respect_robots`, **по умолчанию выключено** (обход без проверки robots) с фиксацией режима в журнале задания — это MODIFIED `osint-enrichment`; эскалация на LLM решается в Go, вызов — `LLMExtractionStrategy` внутри Crawl4AI к DeepSeek; кредитный бюджет остаётся только у Firecrawl. Задачи (9 групп: каталог/домен → spike → контейнер/конфиг → адаптер → прокси → robots/журнал → LLM/OCR → лимиты/режим → проверки) ещё не выполнены. **Зависимость:** MODIFIED-дельта `osint-enrichment` наложится только после архивации `mvp-lead-search` (там живёт основная спека); `validate` уже предупреждает (INFO), задача 9.6. **Дальше:** выход из explore-режима и apply по `tasks.md`, начиная с групп 1–3.

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

## Эпик `client-request-domain` (request-fulfillment)

Завершён и заархивирован 2026-09-30 … 2026-10-02. Шесть change'ов закрыли 15-й контекст `request-fulfillment` и смежные capability.

### `client-request-domain`

- **2026-09-30 (заархивирован):** capability `client-request` в `request-fulfillment` — жизненный цикл заявки (`submitted` → `validating` → `validated` → `processing` → `fulfilled`; терминальные `rejected`/`cancelled`), оффер и связь с артефактами фулфилмента; агрегат `request` (root) с сущностью `offer`. Спека — `openspec/specs/client-request/spec.md`.

### `temporal-execution-platform`

- **2026-10-01 (заархивирован):** self-hosted Temporal как рантайм исполнения заявок: воркер workflow сервиса `request-fulfillment` (поднимается тем же `docker compose up --build`).

### `request-validation-workflow`

- **2026-10-01 (заархивирован):** capability `request-validation` — валидация оффера админом: взятие в работу, звонок-уточнение через `call-intent`, правки оффера до `validated`, одобрение/отклонение с причиной; решение живёт в агрегате `request`. Спека — `openspec/specs/request-validation/spec.md`.

### `semantic-lead-search`

- **2026-10-01 (заархивирован):** capability `semantic-lead-search` — векторный recall и rich-описание: эмбеддинг компании и оффера заявки, поиск кандидатов по близости. Спека — `openspec/specs/semantic-lead-search/spec.md`.

### `client-portal`

- **2026-10-01 (заархивирован):** capability `client-portal` — самообслуживание клиента: регистрация тенанта, подача заявки с оффером и единое окно портала (роль `api` сервиса `platform-governance`). Спека — `openspec/specs/client-portal/spec.md`.

### `client-dashboard-metrics`

- **2026-10-02 (заархивирован):** capability `client-dashboard` — real-time дашборд метрик клиента (проекция счётчиков прогресса и операций, роль `api` сервиса `analytics`). Спека — `openspec/specs/client-dashboard/spec.md`.

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
