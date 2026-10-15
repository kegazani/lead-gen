# Поиск компаний, источники и международная география: предметный аудит

Baseline `bbe49f6`; только исследование и план. Реальные платные discovery, реестровые ключи, regional proxy и поисковый benchmark не запускались. В JSON: 11 назначенных задач, 6 нормативных clauses, 10 findings, 11 facts, 6 hypotheses, 7 decisions, 16 прочитанных первичных материалов с bytes/hash/URL. Из них часть pinned commit sources, часть snapshot текущей upstream ветки; перед поставкой закрепить выбранные версии. Рабочее дерево проекта не менялось.

## Последнее уточнение бюджета (CL-16)

Пользователь уточнил: **денежный бюджет приложения только Firecrawl — единый ledger/account policy для Search/Crawl/Extract**. Остальные API/модели/свой crawler используют уже существующий service proxy, без второго денежного cap/reservation в нашем приложении. Технические attempts/timeouts/concurrency/source rate limits остаются. Proxy-reported usage/cost показываем при наличии, unknown не выдумываем. Existing service proxy не доказывает нужную страну выхода или SERP geography. Предварительный MarketStudy запускается человеком как самостоятельный study intent. Firecrawl разрешён при явном study grant в том же авторитетном ledger/account policy; существующая заявка не обязательна. Для связанного исследования применяется request binding. Это распределение Firecrawl credits в одной системе учёта, без второго денежного бюджета приложения. Прочие инструменты используют existing proxy с техническими пределами.

## Главный вывод

Change правильно описывает identity/evidence и разницу между поиском, приобретением страниц и памятью. Но приоритет пользователя — **найти новые целевые компании с нуля, затем добыть и доказать нужные контакты** — ещё не превращён в исполнимый план. Нынешний текст перечисляет источники и обогащение известной компании. Нужно добавить полноценный discovery portfolio: оффер/ICP → проверяемые критерии → источники/локальные запросы → набор кандидатов → подтверждение субъекта → обогащение → сравнение отдачи стратегий → следующая ограниченная итерация.

Это не требует заменить Firecrawl прежде, чем заработает интеллект. Старый demo сохраняется; новый исследовательский контур может сначала использовать его `query+limit` и приобретение страниц с явным ограничением geography. Новые поисковые adapters и собственный crawler подключаются за независимыми контрактами.

## Что реально есть сейчас

1. `contracts/integration-platform/search/search.go:24`: `Query{Text,MaxResults}`, `Result{URL,Title,Snippet}`. Нет страны/языка/cursor/engine/статуса partial/cost receipt.
2. `backend/contexts/integration-platform/internal/adapters/firecrawl/firecrawl.go:134`: `/v1/search` получает только `query` и `limit`. Нельзя объявить поддержку международной параметризации по возможностям самого поставщика.
3. `lead-management/internal/app/search_leads.go:204–245`: из tenant-visible pool берутся компании с usable contact; подбор — lexical match. Строка348 делает один поиск исходного criteria, затем domain-based посещение сайтов. Это heuristic demo, не полнота discovery.
4. `run_search.go:649`: список query/URL целей исполняется, search error логируется и задача переходит дальше. Нет source portfolio, ожидаемой полезности country/source cell, отдельного географического receipt.
5. **Критичный gap:** `integration-platform/internal/adapters/vector/postgres/store.go:92–127,193–195` читает ВСЕ vectors; `contracts/.../vector/vector.go:34–62` не несёт tenant/purpose/delete/model version. `SemanticRecall.Run` возвращает найденные IDs. Его нельзя включить в новый tenant-safe corpus как готовую реализацию. Это вывод по коду, не утверждение о наблюдённой утечке текущей эксплуатации.

## Предлагаемый поиск от общего к частному

### 1. Зафиксировать SearchBrief

Владелец утверждённых критериев — request-fulfillment; agent-orchestration строит исполнимое представление по версии.

Поля: offer_ref/version, audience/ICP predicates, must/should/exclude, target unit, count, страны и смысл присутствия, языки, technical time/work/source constraints и общий Firecrawl request credit cap. Каждый predicate имеет `observable_via` (registry activity, company service page, completed projects, company size evidence и т.п.) или `unknown`. Отсутствующий критерий нельзя придумать по красивому тексту оффера. Даже правильный отраслевой код не доказывает текущую специализацию/готовность купить.

Пример: оффер «CRM для генподрядчиков коммерческого строительства». Для иллюстрации страны RU/GB, количество компаний с контактом задаётся человеком; это **пример**, не уже утверждённое пользователем поручение. Критерии: строит коммерческие объекты; действует в выбранном рынке; не строительный маркетплейс, не кадровое агентство, не software developer. Размер компании пока unknown. Контакт и website не нужны для самого существования company candidate.

### 2. Стратегии поиска — сетка, а не один запрос

`StrategyCell=(market,segment,source_family,language,query_family,objective)`; source capability profile закреплён. Mission хранит ограниченный portfolio таких cells, status/cost/new candidates/yield/unknowns и причину остановки. `QueryFamily` — версия шаблона, смысловые слоты и ограничения, из которой получаются конкретные локальные запросы. Перестановка слов не новая смысловая попытка.

| Семейство | Пример из SearchBrief | Что извлекаем | Чего не доказывает |
|---|---|---|---|
| Категория + услуга + место | `генподрядчик коммерческое строительство Казань`; `commercial building contractor Manchester`; `Gewerbebau Generalunternehmer Hamburg` | company/domain candidates | название/сниппет ещё не текущая компетенция |
| Вертикальные каталоги | списки членов отраслевой ассоциации/СРО, профили поставщиков | карточки отдельных субъектов, номера, ссылки | footer каталога не контакты компаний |
| Участники рынка | exhibitors, подрядчики проектов, awards, procurement supplier lists | много новых company seeds из одной страницы/документа | участие/победа не готовность купить CRM |
| Реестр | допустимые activity/status/location filters | legal company seeds/ID/status | registry address не реальный офис и не contact coverage |
| Новости/проекты/статьи | `commercial contractor completed warehouse [city]` | именованные компании и события | publisher не субъект каждой новости |
| Entity gap | `"ИНН ..." контакты`; `"company name" "legal address" contact`; exact number+brand | ID/name/domain/contact relation evidence | совпадение строки требует проверки subject scope |
| Собственная память | exact identifier, alias, scoped lexical/semantic | старые компании/OpenNeed/недавние источники | нет в корпусе ≠ нет в интернете |

Поддержка операторов (`site:`, quotes, exclusions, filetype) объявляется конкретным provider/engine; generic meta search не гарантирует переносимость syntax. Локальный смысл проверяется отдельно: «developer» не переводится вслепую. Названия/оригинальные цитаты сохраняются на языке источника, перевод отдельный.

### 3. DiscoveryHit → candidate → entity → credit

Поиск возвращает `DiscoveryHit`, сохраняющий query family/source/rank/URL/received_at. Это ещё не лид. Следом:

1. Дешёвый анализ результата определяет тип источника: company page, directory listing, registry record, document, article.
2. Acquisition читает целевой источник и сохраняет evidence; одна страница может дать несколько subject scopes.
3. Извлечение создаёт company candidates, включая без email. Identity resolver ищет прежнюю entity по scoped identifiers/aliases/relations.
4. Candidate ledger хранит discovered/saved/identity-pending/market-pending/contacts-missing/accepted; counters раскрываются объектами.
5. Если нужен контакт — открывается OpenNeed. Follow-up ищет legal/contact/about/local office pages, точные ID queries, карточки в других источниках. Новый контакт через неделю дополняет прежнюю entity; frozen completion не меняется.
6. Только после persistence receipt и criteria evaluation создаётся ResultMembership/credit соответствующей единицы. Количество SERP/URL/карточек не выдаётся за accepted count.

Domain dedup допустим для ограничения повторного посещения, но один host каталога содержит много организаций, одна company имеет несколько доменов, а статья может упоминать компанию без собственного сайта.

### 4. Управление discovery и enrichment

Две предметные очереди: поиск новых компаний и заполнение пробелов уже найденных. Третий вид работ — повторное чтение/переоценка разрешённого corpus; он не сам себе создаёт формальную заявку или новые полномочия. Action lifecycle и авторитетная frontier едины; внутренняя crawler queue только исполняет lease.

Вначале предлагаем детерминированный round-robin по разрешённым cells с лимитами и приоритетом явных OpenNeed. Потом измерять policy выбора по marginal unique eligible entities/contacts на стоимость с долей исследования новых источников. Фиксированное «70/30» или bandit — гипотезы, не установленный оптимум. Остановка миссии: цель достигнута; technical time/attempt/work limit исчерпан; допустимые стратегии exhausted; долгий no-progress; unsupported required market. Исчерпание Firecrawl request cap/study grant блокирует только новые Firecrawl-действия: другие разрешённые инструменты через existing proxy могут продолжать работу в пределах поручения. Ни один частичный исход не превращается в отсутствие компаний в реальности.

Метрики: new distinct candidate entities, linked entities, accepted unit, usable contact, rejected/ambiguous reasons, source diversity, freshness, cost per accepted result; отдельно по рынкам. Истинный recall всего интернета неизвестен. Для оценки использовать pooled judged set и coverage проверенных source strata, не обещать полноту рынка.

## Источники и география

`SourceProfileRevision` отделяет: search provider; engine; конечный источник документа; host; семью зависимых/перепечатанных данных. Содержит типы субъектов/полей, страны/языки, discovery/filter/pagination способы, rate/concurrency/access/cost, parser/adapter/version, terms/retention refs, status/tested_at/expires_at и owner. Техническая конфигурация provider в integration-platform; смысл пригодности по рынку/сегменту — audience; документы/субъекты/утверждения/индекс — lead-management; планы — agent-orchestration.

### Матрица выбора источников (предложение, не объявленная интеграция)

| Тип рынка | Базовый discovery | Identity evidence | Контакты и дополнительные сигналы | Что проверить перед ready |
|---|---|---|---|---|
| RU | local search, отраслевые каталоги/СРО, списки участников/подрядчиков | кандидат ФНС ЕГРЮЛ/ЕГРИП по ИНН/ОГРН | сайт/реквизиты, карточка с тем же ИНН, документы | официальный доступ/условия/квоты ФНС не проверены в этой сети; default RU ≠ готовность registry |
| GB | regional search, отраслевые ассоциации/проекты/списки поставщиков; candidate CH filters | Companies House profile по number | официальный сайт и внешняя subject-scoped карточка | SDK доказывает форму endpoint, не публичный доступ нашего аккаунта; registry не обещает email |
| DE | local terminology, отраслевые каталоги/выставки/проекты | кандидаты Handelsregister/Unternehmensregister; номер+register/court | Impressum/legal/contact/office pages | API/условия доступа/стоимость ещё исследовать; TLD .de не регистрация |
| FR | local industry directories/projects и доступные registry channels | кандидаты SIRENE/RNE; раздельно юрлицо/площадка | mentions légales/contacts/associations | выбранный актуальный endpoint/licence/access не подтверждён здесь |
| US | state/local trade/licence/directory, regional web | юрисдикция штата+номер; SEC только когда применим к нужному классу субъекта | local office/company website/project evidence | нельзя проектировать единственный федеральный «реестр всех компаний» по одной доступной базе |
| AE/прочие multilingual | местные каталоги/палаты/ассоциации/выставки, локальный+English web | доступный реестр конкретной юрисдикции/зоны | арабские/английские названия, local office evidence | конкретный реестр/доступ/provider country поддержка неизвестны; один перевод/язык не покрывает страну |

GB/RU — кандидаты пилота, не фиксированный выбор. После реального trial выбирать одну иностранную страну с коммерческим спросом и доступными источниками, затем расширять языковой/структурный контраст. CLDR labels отделить от valid ISO codes: dataset включает EU/other groups, они не самостоятельные страны заявки. UI language, search language, provider country, acquisition egress, registration и served market различаются.

### Проверенные свойства поисковых вариантов

- **Firecrawl baseline:** query+limit, metadata только URL/title/snippet. Новый gateway честно маркирует regional parameters unsupported. Не означает отсутствия таких функций у новых upstream endpoint.
- **Brave кандидат:** официальный MCP schema (MS-S11) имеет country/search_lang/ui_lang, count≤20, offset≤9. RU/GB есть, AE в этом enum отсутствует. Это ограничение проверенного SDK, **не доказательство отсутствия AE у backend**. Объём/пагинация не обещают bulk recall; тарифы, terms, качество конкретных стран и live доступ неизвестны.
- **SerpAPI альтернатива:** published legacy SDK показывает location/gl/hl. Новый официальный serpapi-python помечает прежний пакет legacy. Перед интеграцией выбирать текущий endpoint/SDK/plan; разные engines не получают одинаковые capabilities автоматически. Proxy запроса к API не задаёт SERP location.
- **SearXNG кандидат:** primary Search API пересылает q внешним engines, JSON требует настройки, supported syntax/time_range различаются. Engine Google строит язык/регион через locale traits; не универсальная независимая страна поиска. Own instance даёт контроль gateway/backend, но остаётся зависимость от engines. Outgoing multiple proxies round-robin, не гарантия sticky geography. Container up не acceptance.
- **Внутренний corpus:** own exact/FTS/semantic поиск по разрешённым накопленным данным, отдельный от внешней выдачи. Первый вариант PostgreSQL; OpenSearch по нагрузке/качеству, не как обязательный компонент.

## Точная техническая поверхность

### Regional search

Новый `contracts/integration-platform/regionalsearch/` (proposed) рядом с legacy port. Request: action/scope/query-family/source-profile refs, query или typed source filter, требуемые country/search-lang/UI-lang/location, required/preferred semantics, cursor/page/limit, Firecrawl-only reservation ref либо service proxy route. Receipt: requested/applied/unsupported params, provider/engine/config revision, observed_at, status success/empty/partial/throttled/unavailable/invalid/cancelled, result refs, next_cursor, known/unknown cost, attempt lineage.

Один user-level search может состоять из provider pages с отдельными дочерними receipts. Provider timeout после отправки сохраняет outcome/usage ambiguity и bounded retry. Firecrawl повтор остаётся в разрешённом request cap либо study grant того же ledger/account policy; прочие инструменты проходят existing service proxy без нового app money reservation. Shared source limiter и Retry-After/backoff; query cache scoped по query/profile/language/geography/source version/TTL. Raw query с оффером и bodies не пишутся в общую telemetry. `query receipt → persisted result` имеет idempotency/CAS через общий action lifecycle; сетевой вызов вне DB transaction.

### Собственный индекс

`research_search_document` в red владельце (proposed): tenant/purpose/document/revision/entity refs; language/FTS config; tsvector; validity/ACL revision/tombstone; updated_at. Scoped PK, exact identifier B-tree, GIN FTS. Owner evidence transaction пишет outbox; projector применяет только новую revision. До ranking/snippet проверять scope/revocation, при отдаче prompt повторно авторизовать refs. Source-of-truth tombstone исключает запись сразу, даже при отставшем async index.

Первый semantic слой — только после нового scoped contract и раздельных model/dimension versions. Нынешний green all-rows vector store нельзя расширять секретным бэкфиллом red corpus. Сначала exact scoped baseline; pgvector ANN при фильтрах может урезать recall, это прямо описано primary README. Удаление распространяется на vectors/snippets/cached summaries. Local empty означает только отсутствие записи в этом corpus.

### Frontier/refresh

Один authoritative item: scope/run + normalized URL/registry query + purpose/profile/representation revision. Сохранять значимые record/locale параметры. Lease/CAS в action lifecycle; единственный app money ledger — Firecrawl с request cap либо явным study grant, прочие через existing proxy; crawler не второй scheduler. Conditional fetch по ETag/Last-Modified при поддержке источника, content hash, status/redirect lineage.304 — повторное наблюдение прежнего содержимого, не независимое подтверждение факта.404 — отказ URL, не ликвидация компании. Версия source B может заполнить OpenNeed старой entity, не менять historical run snapshot.

## Порядок реализации и принятия

1. Scope/result-unit/country snapshot + contracts; SourceProfileRevision и SearchBrief/query families. Это prerequisite поиска с нуля, который надо выделить явной задачей совместно с orchestration.
2. Gateway поверх legacy Firecrawl; нормальные receipts и partial/cost/error семантика, потом один реально проверенный regional API. Отдельно source registry/directory adapter под выбранный пилот.
3. Discovery candidates сохраняются даже без контакта, evidence scopes и identity resolver передают их в enrichment; адаптивность начинается с понятного deterministic portfolio.
4. Scoped local exact/FTS index и разрешённый recrawl. Старый vector port не считается готовым новым поиском.
5. Per-country acceptance на реальных источниках, then capability badges/quota execution. Ни catalogue, ни SDK README не закрывают поддержку рынка.
6. MarketStudy использует тот же проверенный discovery, explicit start/technical limits и источники; non-Firecrawl через existing proxy, Firecrawl через request cap либо явный study grant того же ledger/account policy, существующая заявка для самостоятельного human study intent не обязательна; comparison protocol показывает unknowns, не только LLM мнение.
7. SearXNG и OpenSearch — сравнительные решения после измерений; собственный crawler развивать независимо. Пользовательская функция search/corpus обязательна даже если конкретный кандидат отклонён.

Полные input/output/dependencies/owners/schema/events/retries/failure/acceptance по КАЖДОЙ задачe5.1–5.5 и6.1–6.6, разбор всех6 Requirements и источники находятся в [планах задач](../task-blueprints.json), [выводах аудита](../audit-findings.json) и [источниках](../audit-sources.json). Полноценная live приемка остаётся задачей реализации, результаты этого аудита её не подменяют.
