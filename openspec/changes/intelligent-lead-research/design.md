# Design

## Context

Мотивация и семь capability перечислены в [proposal](proposal.md). Полная концепция и исследование — [карта документов](../../../docs/concepts/intelligent-lead-research/README.md).

Текущая архитектура context-first, владельцы — `architecture/domain-catalog.yaml`. Наблюдаемая исходная версия `8977438`: domain-based CompanyID, фиксированный search pipeline, тонкие search/crawl ports, provenance без полноценного artifact graph, registered company-agent blocks/Temporal, existing exact-version approval/delivery и event qualification. Есть form relay в клиентский контур; полноценный mailbox ingest не обнаружен. Crawl4AI выбран прежним pending change, но действующий adapter/acceptance не обнаружен.

Пользователь запросил международную исследовательскую систему, допустил замену Crawl4AI/несколько инструментов и добавил браузерные взаимодействия, OCR, собственный поиск, формы, приём почты и учёт ответов. Это отдельная концептуальная ветка; действующий demo/Firecrawl не модифицируются. Все формальные клиентские заявки создают люди.

## Goals / Non-Goals

**Цели:**

- Разделить предметную идентичность, наблюдения, приобретение данных, память и действие, чтобы их можно было независимо менять и проверять.
- Определить международные scope/country/quota semantics и наблюдаемое исполнение с ограниченной стоимостью.
- Сохранить происхождение контакта до исходного DOM/PDF/image и смысл входящего ответа до сообщения.
- Предложить реализуемый первый срез на существующих PostgreSQL/Temporal и владельцах контекстов.

**Границы:**

- Планировочная поставка без runtime, миграций, подключения платных providers и отправки сообщений.
- Нет обещания полного мирового поискового индекса или покрытия всех стран при выборе из справочника.
- Нет замены exact approval автономным решением LLM и автоматической отправки чужих форм.
- Новые entity IDs не означают немедленное переписывание всех legacy company PK.

## Decisions

### 1. ResearchMission до известной компании

`agent-orchestration` владеет новым aggregate root `research-mission`, действиями, бюджетом и stop reasons. До установленной entity миссия работает с seed; после установления может связать existing company-agent. LLM планирует внутри зарегистрированного research block; детерминированный executor проверяет tool/args/scope/budget и переходы.

Альтернатива «свободный agent framework становится оркестратором» отклонена: противоречит действующей роли LLM внутри blocks и дублирует Temporal. Existing search block возвращает только первую компанию; его недостаточно для bulk mission.

### 2. Entity identity независимо от домена

`lead-management` владеет aggregate root `research-entity` с непрозрачным tenant-scoped ID, identifiers/aliases/typed relations; юрлицо, бренд, филиал, сайт и endpoint различаются. Страна/реестр/тип/номер задают область идентификатора. Старый CompanyID сохраняется в mapping до отдельной additive migration.

Альтернатива «домен — универсальный company key» отклонена из-за группы юрлиц на одном сайте и нескольких сайтов одного юрлица. Совпавший домен/телефон/имя даёт сигнал, а не универсальную merge-команду.

### 3. Evidence и identity decision — отдельные версии

`lead-management` владеет `evidence-document` и `identity-decision`. Документ хранит artifact/version/hash/locators/subject/time; assertion различает observed/hypothesis/confirmed/conflicted/stale. Решение о связывании имеет policy/version/основание и обратимость; canonical projection применяет решение с CAS участников.

Копии одного источника не считаются независимыми подтверждениями. Два OCR прочтения одного изображения тоже не независимые факты. Альтернатива «одна confidence float и последний JSON» теряет причины, историю и возможность исправления.

### 4. Scope и память

Первичная память/evidence — `lead-management`; procedural/action history — `agent-orchestration`. PostgreSQL keys/edge tables/JSONB/FTS и существующий vector infrastructure достаточны для первого среза. Raw artifacts с контактами и переписка — client red storage. Authorisation проверяется при retrieval и разрешении refs; ACL фильтры применяются до/после candidate retrieval.

Graph DB и GraphRAG — варианты последующей оптимизации, не обязательные dependencies. Альтернатива «все tenant facts в общей vector memory» отклонена: неверное владение, утечка и невозможность достоверно отозвать производные данные.

### 5. Международность в ядре

`request-fulfillment` владеет выбранными странами, версиями критериев и quota policy внутри existing request. Россия и русский UI — defaults, но страна поиска/сообщения не выводится из языка UI. `audience` владеет `market-study`, рекомендациями с источниками и неизвестностями. Исследование запускается явно с бюджетом; человек выбирает страны.

Различаются target market, registration jurisdiction, office/service presence, SERP region, search language, browser locale и IP egress. Применённые возможности возвращаются receipts. Неподдержанная страна не подменяется другой. Альтернатива автоматического определения страны по TLD/IP даёт систематические ошибки.

### 6. Собственный acquisition за capability contract

`integration-platform` владеет adapter-registration/integration-invocation, manifest, acquisition profiles и техническими receipts. Новые contracts: regional-search/web-acquisition/registry-lookup/document-observation. Предметное решение остаётся у `lead-management`; technical workers не владеют корпоративной истиной.

Для будущего прототипа рекомендуется Crawlee Python HTTP/Playwright. Ранее выбранный Crawl4AI сравнивается с ним и Firecrawl на одинаковом корпусе; может остаться основным/специализированным инструментом при доказанной выгоде. Runtime router фиксирован архитектурно; замена библиотеки не меняет бизнес-контракт. Дополнительный runtime вводится только при измеримом полезном покрытии.

Альтернатива «поставить все crawler frameworks» увеличивает эксплуатацию и дубли очередей. Hosted Search в Crawl4AI не означает self-hosted Search. Firecrawl обслуживает текущий demo до capability acceptance и постепенного cutover.

### 7. Три вида поиска

Различаются внешний discovery API, self-hosted SearXNG metasearch и собственный вертикальный index. Внутренний index начинается в PostgreSQL; OpenSearch — следующий кандидат при доказанной необходимости. YaCy/Nutch/Vespa/Meilisearch рассмотрены как инструменты своих классов, не обязательный набор.

Альтернатива «развернуть SearXNG и считать поиск независимым» отклонена: остаются upstream engines, условия и ограничения. Пустой локальный corpus не означает отсутствия компании в интернете. Proxy API-вызова не заменяет country/location параметр выдачи.

### 8. Browser и OCR — ограниченные инструменты

Предпочтительны raw DOM/structured extraction, затем нужный browser render/recipe, document parsing/OCR по качеству и бюджету. Docling — кандидат document adapter, PaddleOCR — OCR adapter; Tesseract baseline, Surya/Unstructured альтернативы по измерениям и лицензиям. Docling/PaddleOCR composition проверяется, не объявляется готовым native integration.

Browser recipes имеют allowlist origin/action, пределы и проверяемые постусловия. Browser Use — первый кандидат ограниченного agent mode при Python-first стеке; Stagehand альтернатива, оба сразу не требуются. Vision/model capabilities проверяются отдельно от совместимого API URL. OCR хранит исходное чтение, bbox/page/crop transform и model version; коррекция цифр/email требует проверки.

Альтернатива «всё читать screenshot+LLM» дорога, теряет DOM и точность символов. Автономные клики не активируются до явной дельты действующей `osint-enrichment`; read-only search/navigation не может выполнить коммуникационное submit.

### 9. Durable effects вне DB transaction

Новый путь использует короткий commit intent/reservation/outbox → внешнюю activity → commit receipt/result с revision check. Existing RunStep вызывает executor внутри UoW; новый длительный исследовательский путь не расширяет этот паттерн на browser/OCR/model вызовы.

Retries учитывают реальные возможности provider job lookup/idempotency. Crash после оплаты может требовать reconciliation или явной повторной оплаты в бюджете. Альтернатива «Temporal даёт exactly-once внешнему сервису» неверна.

### 10. Исследование передаётся в существующий outreach

`outreach-orchestration` принимает versioned research-handoff с recipient/material/offer/evidence refs, кампанией, языком и policy. `content-production`, existing lead/content contracts и `message-delivery` сохраняют владельцев материала/адреса/доставки. Existing exact-current-version decision и campaign-aware action ID обязательны; suppression перепроверяется перед отправкой.

Company found/contact found/email-ready/draft/approved/delivered — разные показатели. Неопределённый SMTP outcome не даёт слепого retry. Альтернатива «готовая заявка разрешает отправить любой найденный email» отклонена.

### 11. Входящие обращения и переписка

`lead-management` владеет red aggregates `lead-inquiry` и `conversation`: raw message refs, source/origin, participant/correlation/intent versions. `integration-platform` доставляет технический receipt с bound mailbox/form scope. `engagement-observation` квалифицирует reply/OOO/bounce/отказ и публикует непрозрачный signal; `outreach-orchestration` приостанавливает/останавливает sequence и предлагает действие.

Дедупликация опирается на transport identity; Message-ID не единственный глобальный ключ. Correlation использует контролируемые reply refs, References/In-Reply-To и scoped provider thread IDs, затем слабые сигналы с review. Новое деловое обращение создаёт/дополняет inbound lead; повторный ответ не создаёт дубль, человек без компании не превращается в компанию gmail.com.

Первый реальный transport — существующий рабочий mailbox через поддерживаемый provider API/webhook или IMAP connector; инфраструктурно это взаимозаменяемые adapters. Свой SMTP/IMAP server не нужен первому этапу. `go-imap`/`go-message` кандидаты, версии и cursor recovery проверяются; Stalwart только отдельное future решение с лицензией и эксплуатацией. Альтернатива «Mailpit/event endpoint уже принимает реальные ответы» отклонена.

### 12. Формы и контакты отправителя

Versioned sender profile содержит реальные принимающие каналы. Для чужой формы — существующий ручной task с подготовленным материалом/значениями; отсутствие обязательных данных видно оператору. Для нашей формы — existing relay в red client sink и дальнейшее сохранение/связывание inquiry. Эти направления контрактов называются однозначно.

Альтернатива «встроить send в research browser» нарушает существующую модель и скрывает внешний эффект. Формальное утверждение и фактическая ручная отправка/системная доставка различаются.

### 13. Каталог и выпуск по этапам

Планируемые aggregate roots, entities/VO, события и контракты указаны в [матрице владельцев](../../../docs/concepts/intelligent-lead-research/07-architecture-and-delivery-plan.md#73-предлагаемая-регистрация-в-domain-catalog). Реальные дополнения регистрируются при apply в том же change с domain-model/диаграммами. Сейчас каталог получает только primary owners новых capability; доменная модель и диаграмма помечают их как план. Новые entities/aggregates/contracts ещё не вводятся.

Полный объём P0–P8 поставляется независимыми срезами. Рекомендуемый старт R1–R4 уточнён решением 17: доказанный контакт, затем цель/прогресс и входящая переписка; собственный acquisition исследуется отдельно. Каждая стадия имеет реальный acceptance; зависимости, стоимость и границы прототипа — в документе 7.

### 14. Зачёт по версии цели и сохранённому membership

`research-mission` владеет составом результатов и CriteriaSnapshot: target unit, критерии, goal и quota semantics. Варианты unit — company, company-with-contact, email-ready-company; первый прототип использует company-with-contact. `lead-management` предоставляет доказательства/идентичность, а eligibility действия разрешается владельцами outreach/channel policy.

Membership записывается после устойчивого принятия результата; canonical entity/mission/unit/criteria revision дедуплицируют повтор. При merge/revoke до завершения зачёт объяснимо пересчитывается, после — исторический completion snapshot и текущая актуальность различаются. Альтернатива счётчика по числу URL/regex hits отклонена: он не доказывает сохранение или уникальность.

### 15. Детерминированные приоритеты и отдельный lifecycle

Начальная стратегия использует приоритеты: существенный конфликт → различить субъект → найти контакт → доказать принадлежность → закрыть конкретный пробел качества → зачесть. Information gain формула остаётся оптимизацией по измеренным исходам, не самостоятельной оценкой истинности. Попытки нормализованы по вопросу/источнику/географии; исчерпанная стратегия не начинается заново от перестановки слов.

Execution state, phase, waiting reason и completion outcome раздельны. Heartbeat не предметный прогресс. Результат завершения миссии не присваивает сам terminal status клиентской заявке. Альтернатива единого «в работе/готово» скрывает сохранение, недобор, ожидание и другие фазы.

### 16. Pending reply и конфликт correlation

Уверенно связанный устойчивый inbound message создаёт ожидание разбора в применимой sequence до глубокой qualification. Ошибка модели сохраняет hold и видимую задачу; конфликт reply refs/headers у `conversation` требует review, не произвольного приоритета одного поля. Источник лида и количество сообщений остаются разными метриками.

Для раннего hold `lead-management` публикует scope-safe `conversation-signal` и в outreach, и в engagement. Первый потребитель применяет временное ожидание, второй отдельно квалифицирует событие. Необработанное письмо не выдаётся за подтверждённый human reply; будущая прямая relationship и schema регистрируются при apply.

`outreach-orchestration` владеет pause/eligibility revision; `message-delivery` повторно проверяет разрешённость перед внешним вызовом. Уже принятый/неопределённый SMTP эффект не отменяется задним числом. Альтернатива обещания атомарного stop между почтой, БД и провайдером не соответствует внешним системам.

### 17. Последовательные прототипы R1–R4

Уточнённый старт: R1 один seed RU/GB до доказанного контакта на Firecrawl → R2 durable target/counts/UI → R3 наша форма/реальный mailbox/conversation → R4 собственный acquisition/browser/OCR, который можно исследовать независимо. Это приоритетные срезы P0–P8; расширенные рынки/MarketStudy/индекс не являются условием первого end-to-end.

Markdown-only adapter сохраняет именно Markdown evidence, без выдуманного DOM. Неактивный OCR честно отдаёт unsupported/review. Альтернатива одновременного переписывания движка, предметной модели, поиска и почты затрудняет установление причины ошибок. Контракт и критерии R1–R4 закреплены в документе 7; все реализации остаются открытыми задачами.

### 18. Полный объём и применение исследования обязательны

Пользователь подтвердил необходимость реализации всего перечисленного. [Единый реестр](../../../docs/intelligent-lead-platform-roadmap.md) связывает REQ-01…REQ-36 с capability/задачами/критериями, USE-01…USE-14 — со всеми 66 изученными материалами и решениями. Начальная граница R1–R4 ограничивает отдельную поставку, но не сужает итоговую программу.

Международные рынки/MarketStudy, собственные search backends/индекс, self-hosted acquisition, browser/OCR, формы/mailbox/ответы и полный UI остаются обязательными. Выбор конкретного конкурирующего инструмента определяется испытанием; нужны применённый механизм и зафиксированное решение, а не одновременная установка всех изученных frameworks. Открытая внешняя зависимость сохраняет требование открытым.

Полное завершение требует evidence по каждому required требованию и применению исследования, связанных apply-results и полного пользовательского end-to-end. Альтернатива закрыть программу по одному успешному прототипу отклонена. Текущий pending статус означает отсутствие приёмки в новой программе и не стирает существующие результаты demo.

## Risks / Trade-offs

- [Ложное объединение или контакт издателя] → предметные scopes, typed edges, сильные ID, independent holdout, reversible decisions и блокирующие критерии.
- [Потеря OCR символа] → raw/crop lineage, exact-field metrics, второй метод/источник либо review; отсутствие тихих исправлений.
- [Бюджет сгорает в циклах] → attempts/negative TTL, action caps/reservations, cost receipts, stop reason и partial outcome.
- [Неполное покрытие страны] → capability manifest/receipts и отдельные метрики рынков; запрет тихого географического fallback.
- [Source/model prompt injection] → данные не инструкции; registered tools, URL/network policy, отсутствие credentials в prompts и ограниченный browser executor.
- [Повтор оплаченного/внешнего эффекта] → stable IDs, receipts, provider reconciliation и unknown status; не обещать exactly-once там, где его нет.
- [Дубли очередей] → единый action lifecycle поверх mission/frontier/runtime queues и crash acceptance.
- [Cross-tenant leakage] → red storage, owner contracts, проверка при retrieval/ref resolve, отзыв всех derivative indexes.
- [Reply прикреплён к чужой компании] → scoped correlation, ambiguity review, сохранение отдельных inquiry и исправляемые связи.
- [Licensing/code-vs-weights] → pin/version/hash для выбранного состава, явная проверка до distribution/deploy; не смешивать open-source library и hosted service.

## Migration Plan

1. P0: оформить владельцев, versioned contracts и независимый acceptance corpus. Перед R4 согласовать robots/browser противоречия existing/pending; перед R3 — прежнее исключение mail sync и разные смыслы form-submission. Эти зависимости относятся к активации нужного пути и не блокируют допустимый R1 на действующем Firecrawl.
2. Additive research tables/contracts и legacy mapping под versioned tenant route, без замены старых company IDs/links. Сначала чтение snapshots/quality и ограниченный real seed.
3. Включать memory/actions/markets и acquisition workers отдельно по cohort/source capabilities. Неактивные integrations явно недоступны и не закрывают задачи.
4. Включить red inbound intake и один реальный mailbox после scope/transport acceptance; проверить replay/backfill/correlation и остановку follow-up.
5. Выполнить controlled handoff/approval/delivery и ручной form acceptance с реальным разрешённым каналом ответа.
6. Cutover Firecrawl только после сравнительной приёмки. Откат меняет route/profile, сохраняет receipts/history; не пересылает уже отправленное и не удаляет входящие обращения.

Implementation tasks остаются `[ ]`. Полный `make check`, archlint, strict validation и реальные integrations обязательны перед их закрытием; текущая документация не доказывает runtime readiness.

## Open Questions

- Конкретный search/proxy supplier и тарифы для каждой rollout страны уточняются RFP/trial; fixed capability/cost contract от этого не меняется.
- Поддерживаемые release/checkpoint/hardware версии Crawlee/Crawl4AI/OCR/model выбираются на закреплённом corpus; архитектура router и evidence обязательна при любом результате.
- Состав стран после RU/GB и отраслевые источники пилота выбираются клиентским спросом, без объявления всех стран готовыми.
- Конкретный mailbox provider первого подключения задаётся имеющейся почтовой инфраструктурой tenant; transport adapters не меняют inquiry/conversation semantics.
