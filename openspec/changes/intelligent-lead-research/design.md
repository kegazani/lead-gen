# Design

## Context

Мотивация и семь capability перечислены в [proposal](proposal.md). Полная концепция и исследование — [карта документов](../../../docs/concepts/intelligent-lead-research/README.md).

Текущая архитектура context-first, владельцы — `architecture/domain-catalog.yaml`. Проверяемый baseline `bbe49f6`: domain-based CompanyID, существующий multi-target LLM Plan и bounded refinement (`lead-management/internal/app/run_search.go:520,572`), тонкие search/crawl ports, provenance без полноценного artifact graph, registered company-agent blocks/Temporal, existing exact-version approval/delivery и event qualification. Сохраняемого портфеля discovery и устойчивых semantic attempts в этом пути ещё нет; visited/used targets создаются в памяти исполнения (`run_search.go:377`). Есть form relay в клиентский контур; полноценный mailbox ingest не обнаружен. Crawl4AI выбран прежним pending change, но действующий adapter/acceptance не обнаружен.

Пользователь запросил международную исследовательскую систему, допустил замену Crawl4AI/несколько инструментов и добавил браузерные взаимодействия, OCR, собственный поиск, формы, приём почты и учёт ответов. Это отдельная концептуальная ветка; действующий demo/Firecrawl сохраняется до принятия нового пути. Все формальные клиентские заявки создают люди. Последний приоритет пользователя — прежде всего продумать поиск и краулинг; примерные требования уточняются проверками, а конкретные инструменты/численные пороги остаются решениями по результатам экспериментов. Денежный бюджет приложения нужен только Firecrawl: для остальных инструментов уже используется proxy, второй monetary reserve/deny не вводится.

## Goals / Non-Goals

**Цели:**

- Разделить предметную идентичность, наблюдения, приобретение данных, память и действие, чтобы их можно было независимо менять и проверять.
- Спроектировать discovery от оффера/целевой аудитории, международные scope/country/quota semantics, технически ограниченное исполнение и единый бюджет Firecrawl.
- Сохранить происхождение контакта до исходного DOM/PDF/image и смысл входящего ответа до сообщения.
- Предложить реализуемый первый срез на существующих PostgreSQL/Temporal и владельцах контекстов.

**Границы:**

- Планировочная поставка без runtime, миграций, подключения платных providers и отправки сообщений.
- Нет обещания полного мирового поискового индекса или покрытия всех стран при выборе из справочника.
- Exact approval не заменяется автономным решением LLM. Режим отправки чужих форм не выбран пользователем; прежний ручной путь — pending-предложение, а не окончательный запрет автоматизации всей программы.
- Новые entity IDs не означают немедленное переписывание всех legacy company PK.

## Decisions

### 1. ResearchMission до известной компании

`agent-orchestration` владеет предлагаемым aggregate root `research-mission`, сохраняемыми стратегиями, попытками, составом результатов и stop reasons. Миссия начинается с человеческого SearchBrief без обязательной company identity; после её установления может связать existing company-agent. Firecrawl account/grant/credit ledger принадлежит `integration-platform`; миссия хранит opaque grant_ref и проекцию, не второй денежный счёт.

LLM работает в зарегистрированном research-planner block: один вызов возвращает ограниченный набор типизированных предложений. Детерминированный scheduler проверяет tool/schema/refs/scope/TechnicalLimits и выбирает действие. Исправления невалидного ответа ограничены. Нельзя переносить новый network loop внутрь старого RunStep/UoW.

Existing company-agent требует CompanyID, search block возвращает только `Leads[0]` (`agent-orchestration/internal/domain/agent_run.go:20`, `internal/adapters/blocks/search.go:76`); bulk mission получает отдельный контракт/collection refs. Свободный agent framework как второй orchestration owner не вводится: Temporal уже обеспечивает долговечный executor, а динамичность относится к сохраняемому плану.

### 2. Entity identity независимо от домена

`lead-management` владеет aggregate root `research-entity` с непрозрачным tenant-scoped ID, identifiers/aliases/typed relations; юрлицо, бренд, филиал, сайт и endpoint различаются. Страна/реестр/тип/номер задают область идентификатора. Старый CompanyID сохраняется в mapping до отдельной additive migration.

Альтернатива «домен — универсальный company key» отклонена из-за группы юрлиц на одном сайте и нескольких сайтов одного юрлица. Совпавший домен/телефон/имя даёт сигнал, а не универсальную merge-команду.

### 3. Evidence и identity decision — отдельные версии

`lead-management` владеет `evidence-document` и `identity-decision`. Документ хранит artifact/version/hash/locators/subject/time; assertion различает observed/hypothesis/confirmed/conflicted/stale. Решение о связывании имеет policy/version/основание и обратимость; canonical projection применяет решение с CAS участников.

Копии одного источника не считаются независимыми подтверждениями. Два OCR прочтения одного изображения тоже не независимые факты. Альтернатива «одна confidence float и последний JSON» теряет причины, историю и возможность исправления.

### 4. Scope, память и фактический storage baseline

Первичная память/evidence — `lead-management`; procedural/action history — `agent-orchestration`. Для первого среза **планируются** PostgreSQL typed keys/edges/JSONB/FTS с tenant/purpose scope и refs версий. Это будущая схема, а не обнаруженный готовый evidence store.

Текущий vector adapter `backend/contexts/integration-platform/internal/adapters/vector/postgres/store.go` хранит `double precision[]`, читает индекс целиком и считает cosine в Go. Он не использует pgvector/ANN; существующий `vector_document`/контракт не дают готового tenant/purpose-aware retrieval, deletion и invalidation. Расширение scope/версий/удаления нужно до применения к новой памяти. Exact/lexical путь не зависит от включения embeddings; pgvector/OpenSearch/Graph DB выбираются после измерения необходимости.

Raw artifacts с контактами и переписка должны сохраняться в клиентской red области. Переиспользуемый runtime object-storage contract/adapter для этой evidence модели в просмотренном baseline не обнаружен; topology kind `object-storage` не подтверждает готовую интеграцию. Durable artifact put/get/retention/delete, content hash, локаторы и owner access — обязательная предлагаемая зависимость, требующая своей реализации/приёмки.

Authorisation проверяется до retrieval и при разрешении refs; все производные индексы/резюме отзываются по версиям владельца. Общая vector memory всех tenant и автоматическое принятие GraphRAG/LLM edges не удовлетворяют этим границам.

### 5. Международность в ядре

`request-fulfillment` владеет выбранными странами, версиями критериев и quota policy внутри existing request. Россия и русский UI — defaults, но страна поиска/сообщения не выводится из языка UI. `audience` владеет `market-study`, рекомендациями с источниками и неизвестностями. Исследование запускается человеческой кнопкой как study intent с техническими пределами; человек выбирает страны. Если оно использует Firecrawl, ему назначается grant в той же Firecrawl account policy; отдельный финансовый budget type MarketStudy и денежные caps для иных proxy API не вводятся.

Различаются target market, registration jurisdiction, office/service presence, SERP region, search language, browser locale и IP egress. Применённые возможности возвращаются receipts. Неподдержанная страна не подменяется другой. Альтернатива автоматического определения страны по TLD/IP даёт систематические ошибки.

### 6. Собственный acquisition за capability contract

`integration-platform` владеет adapter-registration/integration-invocation, manifest, acquisition profiles и техническими receipts. Новые contracts: regional-search/web-acquisition/registry-lookup/document-observation. Предметное решение остаётся у `lead-management`; technical workers не владеют корпоративной истиной.

Для будущего прототипа рекомендуется Crawlee Python HTTP/Playwright. Ранее выбранный Crawl4AI сравнивается с ним и Firecrawl на одинаковом корпусе; может остаться основным/специализированным инструментом при доказанной выгоде. Runtime router фиксирован архитектурно; замена библиотеки не меняет бизнес-контракт. Дополнительный runtime вводится только при измеримом полезном покрытии.

Альтернатива «поставить все crawler frameworks» увеличивает эксплуатацию и дубли очередей. Hosted Search в Crawl4AI не означает self-hosted Search. Firecrawl обслуживает текущий demo до capability acceptance и постепенного cutover.

### 7. Три вида поиска

Различаются внешний discovery API, self-hosted metasearch (кандидат SearXNG) и собственный вертикальный index. Внутренний index начинается в PostgreSQL; OpenSearch — следующий кандидат при доказанной необходимости. YaCy/Nutch/Vespa/Meilisearch рассмотрены как инструменты своих классов, не обязательный набор.

Альтернатива «развернуть SearXNG и считать поиск независимым» отклонена: остаются upstream engines, условия и ограничения. Пустой локальный corpus не означает отсутствия компании в интернете. Proxy API-вызова не заменяет country/location параметр выдачи.

### 8. Browser и OCR — ограниченные инструменты

Предпочтительны raw DOM/structured extraction, затем нужный browser render/recipe, document parsing/OCR по нужному представлению, качеству и техническим пределам. Application monetary budgeting на browser/OCR/LLM не добавляется; их optional usage приходит из proxy. Docling — кандидат document adapter, PaddleOCR — OCR adapter; Tesseract baseline, Surya/Unstructured альтернативы по измерениям и лицензиям. Docling/PaddleOCR composition проверяется, не объявляется готовым native integration.

Browser recipes имеют allowlist origin/action, пределы и проверяемые постусловия. Browser Use — первый кандидат ограниченного agent mode при Python-first стеке; Stagehand альтернатива, оба сразу не требуются. Vision/model capabilities проверяются отдельно от совместимого API URL. OCR хранит исходное чтение, bbox/page/crop transform и model version; коррекция цифр/email требует проверки.

Альтернатива «всё читать screenshot+LLM» дорога, теряет DOM и точность символов. Автономные клики не активируются до явной дельты действующей `osint-enrichment`; read-only search/navigation не может выполнить коммуникационное submit.

### 9. Durable effects и единый учёт Firecrawl

Все действия используют устойчивые intent/attempt/receipt и CAS вне длительных DB-транзакций. Existing `RunStep` вызывает executor внутри UoW (`agent-orchestration/internal/app/command/run_step.go:62,122`); новый research workflow не наследует это для browser/OCR/model calls.

Для обычного proxy/local действия: короткая agent Tx intent/outbox → technical admission → external activity → provider/artifact receipt → idempotent owner ingest → persistence receipt → mission CAS/result/outbox. Для Firecrawl между intent и dispatch нужна отдельная saga:

1. Agent сохраняет `awaiting_fc_reservation` и action/attempt ID.
2. `integration-platform.ReserveForAction(account, grant, action, attempt)` в одной owner Tx проверяет account/grant и создаёт **одну** reservation/charge lineage.
3. Reservation receipt переводит action CAS в ready. Потерянное подтверждение восстанавливается `GetReservation` по тому же ключу, без нового резерва.
4. `Settle`/`MarkUnknown` выполняются владельцем Firecrawl идемпотентно; mission хранит проекцию. Общая cross-context DB-транзакция не требуется.

Предлагаемые Firecrawl account/grant/operation ledger tables находятся у `integration-platform`; миссия и human-button MarketStudy получают grant refs из одной account policy. Общий budget охватывает Search/Crawl/Extract и refinement. Grant — область разрешённого расхода, не второе списание поверх account. Current Firecrawl Registry process-local (`integration-platform/internal/adapters/firecrawl/budget.go:294`), поэтому durable accounting ещё предстоит реализовать.

LLM и остальные proxy-инструменты не требуют нового application monetary reserve/deny/обязательного price receipt. Если proxy вернул usage, оно сохраняется для диагностики. Для всех инструментов отдельно действуют timeouts, concurrency, page/depth/bytes, retry/attempt caps и cancellation.

Crash после внешнего эффекта может оставлять unknown. Provider idempotency/job lookup используются по реальной capability; lease expiry не доказывает отсутствие эффекта. Unknown Firecrawl credits удерживаются до точного operation evidence, общий account balance недостаточен. Остальные инструменты восстанавливаются по технической retry policy без второго денежного ledger. Temporal не даёт exactly-once чужому сервису.

Новый fulfillment route запускает одну research mission на approved intent. Existing `criteriaForRound` создаёт разные budget scopes (`request-fulfillment/.../activities.go:136`), а `RunSearch` ещё имеет refine loop: их нельзя оборачивать вокруг нового пути. Query suffix/restart не сбрасывает Firecrawl budget; старый и новый поиск по одному intent не исполняются параллельно.

### 10. Исследование передаётся в существующий outreach

`outreach-orchestration` принимает versioned research-handoff с recipient/material/offer/evidence refs, кампанией, языком и policy. `content-production`, existing lead/content contracts и `message-delivery` сохраняют владельцев материала/адреса/доставки. Existing exact-current-version decision и campaign-aware action ID обязательны; suppression перепроверяется перед отправкой.

Company found/contact found/email-ready/draft/approved/delivered — разные показатели. Неопределённый SMTP outcome не даёт слепого retry. Альтернатива «готовая заявка разрешает отправить любой найденный email» отклонена.

### 11. Входящие обращения и переписка

`lead-management` владеет red aggregates `lead-inquiry` и `conversation`: raw message refs, source/origin, participant/correlation/intent versions. `integration-platform` доставляет технический receipt с bound mailbox/form scope. `engagement-observation` квалифицирует reply/OOO/bounce/отказ и публикует непрозрачный signal; `outreach-orchestration` приостанавливает/останавливает sequence и предлагает действие.

Дедупликация опирается на transport identity; Message-ID не единственный глобальный ключ. Correlation использует контролируемые reply refs, References/In-Reply-To и scoped provider thread IDs, затем слабые сигналы с review. Новое деловое обращение создаёт/дополняет inbound lead; повторный ответ не создаёт дубль, человек без компании не превращается в компанию gmail.com.

Первый реальный transport — существующий рабочий mailbox через поддерживаемый provider API/webhook или IMAP connector; инфраструктурно это взаимозаменяемые adapters. Свой SMTP/IMAP server не нужен первому этапу. `go-imap`/`go-message` кандидаты, версии и cursor recovery проверяются; Stalwart только отдельное future решение с лицензией и эксплуатацией. Альтернатива «Mailpit/event endpoint уже принимает реальные ответы» отклонена.

### 12. Формы и контакты отправителя

Versioned sender profile содержит реальные принимающие каналы. Для чужой формы обязательны подготовка материала/реальных контактных значений и учёт результата. Прежний pending change предлагает ручной task; пользователь не выбирал окончательную степень автоматизации. Отсутствие обязательных данных видно оператору. Для нашей формы — existing relay в red client sink и дальнейшее сохранение/связывание inquiry. Эти направления контрактов называются однозначно.

Альтернатива «встроить send в research browser» нарушает существующую модель и скрывает внешний эффект. Формальное утверждение и фактическая ручная отправка/системная доставка различаются.

### 13. Каталог и выпуск по этапам

Планируемые aggregate roots, entities/VO, события и контракты указаны в [матрице владельцев](../../../docs/concepts/intelligent-lead-research/07-architecture-and-delivery-plan.md#73-предлагаемая-регистрация-в-domain-catalog). Реальные дополнения регистрируются при apply в том же change с domain-model/диаграммами. Сейчас каталог получает только primary owners новых capability; доменная модель и диаграмма помечают их как план. Новые entities/aggregates/contracts ещё не вводятся.

Полный объём P0–P8 поставляется независимыми срезами. По последнему приоритету пользователя старт поиска идёт от SearchBrief/источников до сохраняемого кандидата и cross-site контакта, затем durable target/counts/UI. Почта и дальнейшие каналы сохраняют обязательный scope. Последовательность уточняет решение 17; собственный acquisition сравнивается на том же контракте и корпусе. Каждая стадия имеет реальный acceptance.

### 14. Зачёт по версии цели и сохранённому membership

`research-mission` владеет составом результатов и CriteriaSnapshot: target unit, критерии, goal и quota semantics. Goal содержит целевой объём конкретной человеческой заявки; общей фиксированной числовой цели нет. Варианты unit — company, company-with-contact, email-ready-company; company-with-contact и RU/GB предложены ассистентом для первого пилота, фактический выбор фиксирует человек в поручении. `lead-management` предоставляет доказательства/идентичность, а eligibility действия разрешается владельцами outreach/channel policy.

Membership записывается после owner persistence receipt и принятия по criteria/unit, включая соответствие целевой аудитории; provider fetch receipt сам по себе недостаточен. Canonical entity/mission/unit/criteria revision дедуплицируют повтор. Принятый уровень identity задаёт policy конкретной заявки, он не всегда требует registry-confirmed юридического лица. При merge/revoke до завершения зачёт объяснимо пересчитывается, после — исторический completion snapshot и текущая актуальность различаются. Альтернатива счётчика по числу URL/regex hits отклонена: он не доказывает сохранение или уникальность.

### 15. Детерминированные приоритеты и отдельный lifecycle

Scheduler выбирает между discovery новых кандидатов и пробелами известных субъектов по допустимости, существенным конфликтам используемых фактов, country quota debt, справедливости и измеряемому stage yield. Известные source recipes выполняются без лишнего planner call. Числовые utility/exploration ratios — проверяемые гипотезы, а самооценка LLM не probability truth. Firecrawl credits учитываются только для его действий; остальные инструменты сравниваются по полезности/технической задержке без нового денежного gate.

Attempts нормализованы по QueryFamily/question/source/country/language/input revisions и сохраняются durable; строковая перестановка не новый лимит. Park имеет причину/условие пересмотра, не означает отрицательный fit компании. Source cooldown не блокирует альтернативы. У каждой frontier один владелец (§22), а technical caps не смешиваются с денежным бюджетом.

Execution state, phase, waiting reason и completion outcome раздельны; heartbeat не предметный прогресс. Firecrawl exhausted блокирует его новые действия, но оставляет полезные разрешённые memory/proxy/self-hosted пути. Partial по причине FC budget возникает, когда необходимый остаток требует его вызовов. Global pause имеет owner/condition/deadline; resume не расширяет scope/пределы.

Finalization ограничена сроком: снимок фиксирует membership/canonical/criteria/evidence revisions и обработанный event watermark. Поздний evidence меняет current dossier, не исторический состав; Firecrawl settlement может иметь отдельную финансовую ревизию. Отсутствие optional proxy usage не удерживает completion. Terminal статус формальной заявки устанавливает `request-fulfillment`.

### 16. Pending reply и конфликт correlation

Уверенно связанный устойчивый inbound message создаёт ожидание разбора в применимой sequence до глубокой qualification. Ошибка модели сохраняет hold и видимую задачу; конфликт reply refs/headers у `conversation` требует review, не произвольного приоритета одного поля. Источник лида и количество сообщений остаются разными метриками.

Для раннего hold `lead-management` публикует scope-safe `conversation-signal` и в outreach, и в engagement. Первый потребитель применяет временное ожидание, второй отдельно квалифицирует событие. Необработанное письмо не выдаётся за подтверждённый human reply; будущая прямая relationship и schema регистрируются при apply.

`outreach-orchestration` владеет pause/eligibility revision; `message-delivery` повторно проверяет разрешённость перед внешним вызовом. Уже принятый/неопределённый SMTP эффект не отменяется задним числом. Альтернатива обещания атомарного stop между почтой, БД и провайдером не соответствует внешним системам.

### 17. Приоритетный срез поиска и последующие этапы

Первый приоритет — **человеческий оффер/ICP → discovery portfolio → сохранённые неполные кандидаты → cross-site identity/contact enrichment → проверяемый зачёт/недобор и UI**. Один seed до доказанного контакта остаётся необходимым тестом качества, но не заменяет discovery с нуля. Реальный Firecrawl используется до принятия собственного получения страниц; начальные рынки/движки выбираются по проверкам, RU/GB и Crawlee не являются окончательным пользовательским выбором.

После базовой модели/контрактов и корпуса вводятся durable action lineage и общий Firecrawl grant, затем стратегии/адаптивная очередь, membership/progress/recovery. Собственный acquisition/browser/OCR исследуется параллельно на одинаковом корпусе и подключается за принятым контрактом. Письма, формы, inbound и расширенные рынки остаются обязательными этапами полной программы, но не подменяют текущий фокус поиска.

Markdown-only adapter сохраняет Markdown evidence, не выдуманный DOM. Неподключённый OCR сообщает unsupported/review и не закрывает acceptance. Один успешный поиск либо завершение ранних R1–R4 не закрывает полный change. Подробный план — [поисковый blueprint](../../../docs/concepts/intelligent-lead-research/11-search-implementation-blueprint.md).

### 18. Происхождение пользовательских уточнений

[Реестр исходных реплик](../../../docs/intelligent-lead-platform-roadmap.md#ваши-уточнения-из-переписки) отделяет исходные уточнения пользователя от предложений ассистента. Последние прямые указания о приоритете поиска и Firecrawl-only бюджете имеют приоритет над прежними рекомендациями универсального финансового ledger. Сравнение альтернатив не является выбором Crawlee, собственный поиск не фиксирует SearXNG, а полный объём не означает принятия всех предложенных пилотных настроек. Ручная отправка взята из незавершённого старого change и не исключает будущий иной режим от имени пользователя.

Полный способ исполнения чужих форм остаётся продуктовым решением до активации: подготовка/обратный канал обязательны при любом варианте; техническая реализация эффекта должна иметь отдельную спецификацию, утверждение и проверку. Эта открытая часть фиксируется задачей 9.7, а не скрытым manual-only ограничением. Запрет автономных кликов в canonical osint-enrichment по-прежнему требует явной дельты.

### 19. Полный объём и применение исследования обязательны

Пользователь подтвердил необходимость реализации всего перечисленного. [Единый реестр](../../../docs/intelligent-lead-platform-roadmap.md) связывает REQ-01…REQ-36 с capability/задачами/критериями, USE-01…USE-14 — со всеми 66 изученными материалами и решениями. Начальные срезы ограничивают отдельную поставку, но не сужают итоговую программу; текущий порядок отдаёт приоритет discovery/краулингу.

Международные рынки/MarketStudy, собственные search backends/индекс, self-hosted acquisition, browser/OCR, формы/mailbox/ответы и полный UI остаются обязательными. Выбор конкретного конкурирующего инструмента определяется испытанием; нужны применённый механизм и зафиксированное решение, а не одновременная установка всех изученных frameworks. Открытая внешняя зависимость сохраняет требование открытым.

Полное завершение требует evidence по каждому required требованию и применению исследования, связанных apply-results и полного пользовательского end-to-end. Альтернатива закрыть программу по одному успешному прототипу отклонена. Текущий pending статус означает отсутствие приёмки в новой программе и не стирает существующие результаты demo.

### 20. Межзапусковый граф знаний, потребностей и зависимостей

CL-14 уточняет долгоживущую память: entity сохраняется без контакта; `OpenNeed` у `lead-management` фиксирует недостающее отношение/значение, scope, критерии, историю и условие пересмотра. Предметные отношения отделены от зависимостей вывода от конкретных версий evidence/assertions и policy. Позднее наблюдение, merge/split, отзыв и истечение актуальности вызывают устойчивый идемпотентный пересмотр затронутых выводов с учётом обязательных и альтернативных достаточных оснований.

Обновляется текущее досье/потребность, сохраняется исторический снимок завершённого run и фактически использованный материал/адресат. Знание само не создаёт заявку/Firecrawl grant: `agent-orchestration` продолжает только разрешённую активную миссию в её пределах либо явно разрешённый связанный run. Контракты событий передают владельцам разрешённые refs/версии; source bodies/контакты остаются в клиентской области.

UI объясняет статус конкретного утверждения, пригодность источника и качество извлечения, метод/дату/автора проверки и отдельные identity/link/format/reachability/freshness/action dimensions. Самооценка LLM или единая отметка компании не заменяют эти проверки. Модель/контрольный сценарий — [§2.13–2.14](../../../docs/concepts/intelligent-lead-research/02-identity-and-evidence.md#213-сеть-отношений-и-зависимостей-во-времени), исполнение — [§3.16](../../../docs/concepts/intelligent-lead-research/03-agent-and-memory.md#316-открытые-потребности-и-событийное-дообогащение). Регистрация реально вводимых агрегатов/событий остаётся задачей 1.2 при apply.

### 21. SearchBrief и сохраняемый discovery portfolio

`SearchBrief` у `agent-orchestration` связывает человеческие OfferVersion/TargetAudience/CriteriaVersion, must/should/exclude, неизвестности, страны/квоты, result unit, technical limits и Firecrawl grant_ref. Оффер продавца отделён от отрасли/роли покупателя; human facts и LLM inference имеют происхождение. Новая гипотеза не изменяет hard criteria без разрешённой версии.

`StrategyCell` задаёт рынок/сегмент/source family/язык/вопрос. `QueryFamily` — semantic template и типизированные slots; query string остаётся backend variant. `DiscoveryHit` сохраняет source/document/locator и proposed subject role. Источники выбираются по тому, где представлены целевые компании: corpus, реестры, ассоциации/каталоги, участники выставок, закупки/проекты/новости, прямые сайты и cross-site exact-ID lookup. Статья или страница без email не исключаются из discovery; publisher и названные subjects различаются.

План сохраняет lineage `intent → brief/criteria version → cell/query family → action/attempt → receipt → entity/assertion → acceptance/membership`. Первичная волна ограничена; дальнейший выбор учитывает distinct saved candidates, criteria pass, identity sufficiency, owned contact и accepted unit. Повторные копии источника не создают новые субъекты. Предположения о caps/exploration принимаются на независимом корпусе и реальных ограниченных испытаниях, а не записываются как доказанный оптимум.

### 22. Единственный владелец каждого уровня frontier

`agent-orchestration` владеет одним устойчивым исследовательским планом с работой discovery и entity gaps. `integration-platform` владеет acquisition frontier для допущенного bounded action: URL/host/session leases, runtime retries и receipts. Очередь crawler runtime исполняет эти work items, сохраняя stable IDs и пределы; она не начинает независимый обход поверх mission без lineage.

Выдача/перевыдача lease, cancel и retry согласованы через action/work-item IDs и версии. Source recipe может делегировать ограниченный crawl с page/depth/time caps, но только один owner определяет актуальное состояние каждого item. После рестарта ни mission, ни runtime не создают второй бесконечный обход. Это сохраняет сменяемость Firecrawl/собственного движка без нескольких несогласованных scheduler.

## Risks / Trade-offs

- [Ложное объединение или контакт издателя] → предметные scopes, typed edges, сильные ID, independent holdout, reversible decisions и блокирующие критерии.
- [Потеря OCR символа] → raw/crop lineage, exact-field metrics, второй метод/источник либо review; отсутствие тихих исправлений.
- [Firecrawl credits сгорают в повторных scopes] → общий provider-owner account/grant ledger, operation receipts и единое intent lineage. Для остальных tools — technical loop/timeout caps без второго денежного бюджета.
- [Неполное покрытие страны] → capability manifest/receipts и отдельные метрики рынков; запрет тихого географического fallback.
- [Source/model prompt injection] → данные не инструкции; registered tools, URL/network policy, отсутствие credentials в prompts и ограниченный browser executor.
- [Повтор оплаченного/внешнего эффекта] → stable IDs, receipts, provider reconciliation и unknown status; не обещать exactly-once там, где его нет.
- [Дубли очередей] → единый action lifecycle поверх mission/frontier/runtime queues и crash acceptance.
- [Cross-tenant leakage] → red storage, owner contracts, проверка при retrieval/ref resolve, отзыв всех derivative indexes.
- [Reply прикреплён к чужой компании] → scoped correlation, ambiguity review, сохранение отдельных inquiry и исправляемые связи.
- [Licensing/code-vs-weights] → pin/version/hash для выбранного состава, явная проверка до distribution/deploy; не смешивать open-source library и hosted service.

## Migration Plan

1. P0: оформить владельцев, SearchBrief/result-unit/country/source contracts и независимый corpus. Перед собственным browser/acquisition согласовать robots/browser противоречия existing/pending; перед inbound — mail sync/form contracts. Это зависимости соответствующего пути, а не запрет первого discovery на Firecrawl.
2. Additive entity/evidence/mission/strategy tables и legacy mapping под versioned tenant route. Реализовать artifact storage, scope-safe retrieval, owner Firecrawl ledger/grants и durable action saga; existing vector/object-storage нельзя объявлять готовыми для этой модели.
3. Принять offer/ICP → discovery → incomplete entity → cross-site contact → criteria/unit credit, membership/progress/restart и общий FC cap; new route не запускает legacy top-up. Source/acquisition adapters включать по проверенным capabilities. Неактивная интеграция не закрывает задачи.
4. Включить red inbound intake и один реальный mailbox после scope/transport acceptance; проверить replay/backfill/correlation и остановку follow-up.
5. Выполнить controlled handoff/approval/delivery и form acceptance в явно выбранном режиме с реальным разрешённым каналом ответа.
6. Cutover Firecrawl только после сравнительной приёмки. Откат меняет route/profile, сохраняет receipts/history; не пересылает уже отправленное и не удаляет входящие обращения.

Implementation tasks остаются `[ ]`. Полный `make check`, archlint, strict validation и реальные integrations обязательны перед их закрытием; текущая документация не доказывает runtime readiness.

## Open Questions

- Конкретные search/proxy suppliers и покрытие стран уточняются trial. Firecrawl credit policy остаётся отдельной; proxy tariffs/usage — необязательная диагностика, не новое условие app допуска.
- Поддерживаемые release/checkpoint/hardware версии Crawlee/Crawl4AI/OCR/model выбираются на закреплённом corpus; архитектура router и evidence обязательна при любом результате.
- Страны и отраслевые источники пилота выбираются клиентским спросом; RU/GB предложены для сравнения и не ограничивают международную программу.
- Конкретный mailbox provider первого подключения задаётся имеющейся почтовой инфраструктурой tenant; transport adapters не меняют inquiry/conversation semantics.
