# 8. Источники и выводы исследования

Дата чтения: **15 октября 2026 года**. Изучались публичные первичные материалы проектов: README, API guides, исходные спецификации и лицензии. Google/Bing/DuckDuckGo и часть отдельных сайтов документации были недоступны из облачной среды; использованы доступные официальные GitHub/raw источники. Полный веб-поиск, платные API и достоверность заявлений поставщиков экспериментально не проверялись.

Для community datasets/клиентов отдельно отмечено, что это не официальный регистр или нормативный источник. Ссылки на main/master подвижны; [машиночитаемый журнал](research-sources.json) фиксирует URL, дату чтения, размер и SHA-256 прочитанного содержимого, но не подменяет pin release/commit при реализации. Полные чужие тексты в репозиторий не копируются.

## Основные выводы

1. Crawlee подходит как управляемый acquisition runtime, Crawl4AI — как сильная база извлечения/адаптивного обхода; выбор решает наш benchmark, а не GitHub stars. Firecrawl остаётся до acceptance.
2. Self-hosted метапоиск и собственный web index — разные уровни независимости. Нужны и discovery, и накопление собственного тематического корпуса.
3. OCR/document parsers, browser actions, идентичность, contact ownership и память — отдельные функции. Ни одна библиотека не решает их все с доказанной точностью.
4. Temporal уже покрывает durable execution; паттерны памяти/graph retrieval можно внедрять без одновременного перехода на LangGraph, Graphiti, Mem0 и Letta.
5. Входящая почта требует реального mailbox, устойчивого ingest и корреляции. Наличие SMTP sender, event endpoint или Mailpit не означает готового приёма ответов.
6. Код, model weights и hosted service могут иметь разные лицензии. Это особенно существенно для Surya, Docling и собственного mail server.

## Прочитанные материалы

<a id="s01"></a>
### S01. Firecrawl: публичный README

[Первичный материал](https://raw.githubusercontent.com/firecrawl/firecrawl/main/README.md).

**Что берём:** Crawl/scrape/map/search и несколько форматов результата; есть дополнительные браузерные механизмы.

**Предел вывода:** Наш текущий adapter использует узкий контракт; наличие endpoint не доказывает поддержку страны/DOM в нашем пути.

<a id="s02"></a>
### S02. Firecrawl: лицензия репозитория

[Первичный материал](https://raw.githubusercontent.com/firecrawl/firecrawl/main/LICENSE).

**Что берём:** Основной репозиторий публикует AGPL; лицензия SDK/компонента и условия hosted service рассматриваются отдельно.

**Предел вывода:** Не переносить условия одного репозитория на любой API или весь наш продукт; self-hosted adoption требует проверки конкретного состава.

<a id="s03"></a>
### S03. Crawl4AI: основной README

[Первичный материал](https://raw.githubusercontent.com/unclecode/crawl4ai/main/README.md).

**Что берём:** Self-hosted web acquisition, Markdown/structured extraction, hooks, deep/adaptive crawl; есть отдельная hosted платформа.

**Предел вывода:** В таблице README web search отсутствует у self-hosted library/Docker и перечислен у cloud. Это не собственный глобальный поисковый индекс.

<a id="s04"></a>
### S04. Crawl4AI: Adaptive Crawling

[Первичный материал](https://raw.githubusercontent.com/unclecode/crawl4ai/main/docs/md_v2/core/adaptive-crawling.md).

**Что берём:** Обход можно направлять query relevance/information gain и останавливать при насыщении.

**Предел вывода:** Coverage/стоп-порог обхода не является вероятностью юридического тождества или полноты рынка.

<a id="s05"></a>
### S05. Crawl4AI: параметры crawler/browser

[Первичный материал](https://raw.githubusercontent.com/unclecode/crawl4ai/main/docs/md_v2/api/parameters.md).

**Что берём:** Профили browser/session, ProxyConfig, locale/timezone/geolocation и параметры получения.

**Предел вывода:** Проверить конкретную версию/контейнер; browser geolocation не меняет IP-выход.

<a id="s06"></a>
### S06. Crawl4AI: LLM extraction

[Первичный материал](https://raw.githubusercontent.com/unclecode/crawl4ai/main/docs/md_v2/extraction/llm-strategies.md).

**Что берём:** Структурированное извлечение в схему через LLMExtractionStrategy.

**Предел вывода:** Не запускать скрыто второй LLM pipeline сверх общего model gateway; извлечённый JSON ещё не доказанный факт.

<a id="s07"></a>
### S07. Splink: record linkage

[Первичный материал](https://raw.githubusercontent.com/moj-analytical-services/splink/master/README.md).

**Что берём:** Candidate matching, сравнения полей и вероятностное связывание записей.

**Предел вывода:** Алгоритм требует качественных признаков и калибровки; не решает автоматически нашу юрисдикционную идентичность.

<a id="s08"></a>
### S08. Splink: Fellegi–Sunter

[Первичный материал](https://raw.githubusercontent.com/moj-analytical-services/splink/master/docs/topic_guides/theory/fellegi_sunter.md).

**Что берём:** Полезны частоты совпадений и различение сильных/слабых сигналов.

**Предел вывода:** Self-score LLM и вручную выбранный вес нельзя выдавать за калиброванную вероятность.

<a id="s09"></a>
### S09. Graphiti: temporal knowledge graph

[Первичный материал](https://raw.githubusercontent.com/getzep/graphiti/main/README.md).

**Что берём:** Эпизоды, временные факты, hybrid retrieval и работа с изменениями знаний.

**Предел вывода:** Идеи temporal graph полезны; автоматически построенные LLM связи нуждаются в subject/evidence checks и tenant isolation.

<a id="s10"></a>
### S10. Microsoft GraphRAG: обзор

[Первичный материал](https://raw.githubusercontent.com/microsoft/graphrag/main/docs/index.md).

**Что берём:** Иерархия/сообщества и summaries помогают обзорным вопросам по корпусу.

**Предел вывода:** Обзор корпуса не заменяет exact ID lookup, проверку контактов и source evidence; индексирование имеет стоимость.

<a id="s11"></a>
### S11. LangGraph: framework overview

[Первичный материал](https://raw.githubusercontent.com/langchain-ai/langgraph/main/README.md).

**Что берём:** Паттерны stateful/durable agent execution и human-in-the-loop.

**Предел вывода:** Проект уже использует Temporal; второй workflow runtime не выбран.

<a id="s12"></a>
### S12. Temporal Go SDK

[Первичный материал](https://raw.githubusercontent.com/temporalio/sdk-go/master/README.md).

**Что берём:** Долговечное выполнение workflows/activities и существующая база оркестрации.

**Предел вывода:** Долговечность не обеспечивает exactly-once у произвольного внешнего API или SMTP.

<a id="s13"></a>
### S13. Mem0: memory layer

[Первичный материал](https://raw.githubusercontent.com/mem0ai/mem0/main/README.md).

**Что берём:** Разные области памяти и выбор/обновление полезных наблюдений.

**Предел вывода:** Память нельзя превращать в несопровождаемое резюме без исходных refs; маркетинговые benchmarks не наши результаты.

<a id="s14"></a>
### S14. Reconciliation API: overview

[Первичный материал](https://raw.githubusercontent.com/reconciliation-api/specs/master/README.md).

**Что берём:** Полезен протокол предложения candidate identity matches с явным выбором.

**Предел вывода:** Прочитан обзор community проекта, не полный стандарт и не готовая интеграция OpenRefine с нашим доменом.

<a id="s15"></a>
### S15. Schema.org: словарь и проект

[Первичный материал](https://raw.githubusercontent.com/schemaorg/schemaorg/main/README.md).

**Что берём:** Organization/LocalBusiness и другие структурированные представления полезны как вход extraction.

**Предел вывода:** Schema markup — заявление издателя, а не удостоверенный государственный реестр.

<a id="s16"></a>
### S16. DaData Python SDK

[Первичный материал](https://raw.githubusercontent.com/hflabs/dadata-py/master/README.md).

**Что берём:** Пример find_by_id для party с регистрационным номером/КПП полезен для RU seed lookup.

**Предел вывода:** Документация SDK не гарантирует актуальность, наличие contacts и право на нужный объём/тариф.

<a id="s17"></a>
### S17. Companies House API SDK

[Первичный материал](https://raw.githubusercontent.com/companieshouse/api-sdk-node/master/README.md).

**Что берём:** Lookup company profile по company number; пример сохраняет ведущие нули.

**Предел вывода:** Не проверены реальные credentials/квоты и доступность полей; SDK не является универсальным источником контактов.

<a id="s18"></a>
### S18. VIES: community PHP client

[Первичный материал](https://raw.githubusercontent.com/DragonBe/vies/master/README.md).

**Что берём:** Клиент показывает модель отдельной проверки VAT ID.

**Предел вывода:** Это не первичный источник юридических правил ЕС; VAT validation не заменяет entity registry и не покрывает все компании.

<a id="s19"></a>
### S19. Brave Search: официальный MCP server

[Первичный материал](https://raw.githubusercontent.com/brave/brave-search-mcp-server/main/README.md).

**Что берём:** Поля country, search_lang, ui_lang позволяют разделять параметры выдачи.

**Предел вывода:** Поддерживаемые страны/планы и фактическое покрытие проверяются; не обещаем доступ ко всем рынкам.

<a id="s20"></a>
### S20. SerpAPI: официальный Python client

[Первичный материал](https://raw.githubusercontent.com/serpapi/google-search-results-python/master/README.md).

**Что берём:** В примерах используются location, gl, hl и отдельные search engines.

**Предел вывода:** Нет реального trial или сравнения полноты; proxy API-вызова не заменяет эти параметры.

<a id="s21"></a>
### S21. SearXNG: Search API

[Первичный материал](https://raw.githubusercontent.com/searxng/searxng/master/docs/dev/search_api.rst).

**Что берём:** Self-hosted federation API с параметрами языка/движков/форматов.

**Предел вывода:** Метапоиск зависит от upstream engines, их лимитов/условий и доступности; не независимый глобальный индекс.

<a id="s22"></a>
### S22. SearXNG: outgoing settings

[Первичный материал](https://raw.githubusercontent.com/searxng/searxng/master/docs/admin/settings/settings_outgoing.rst).

**Что берём:** Прокси и network settings отдельно управляют исходящими соединениями; SOCKS5h может переносить DNS resolution.

**Предел вывода:** Наличие proxy config не означает доступную региональную выдачу или SLA выбранного engine.

<a id="s23"></a>
### S23. iso-3166: open-source dataset

[Первичный материал](https://raw.githubusercontent.com/wooorm/iso-3166/main/readme.md).

**Что берём:** Машиночитаемые country codes пригодны как исходник проверяемого справочника.

**Предел вывода:** Это community implementation, не официальный регистр ISO; версии и disputed/exceptional entries требуют явной политики.

<a id="s24"></a>
### S24. Unicode CLDR: русские названия территорий

[Первичный материал](https://raw.githubusercontent.com/unicode-org/cldr-json/main/cldr-json/cldr-localenames-full/main/ru/territories.json).

**Что берём:** Официальные Unicode locale labels/aliases полезны для UI.

**Предел вывода:** Набор включает регионы/группы; multiselect стран фильтруется по отдельному country registry.

<a id="s25"></a>
### S25. Google libphonenumber

[Первичный материал](https://raw.githubusercontent.com/google/libphonenumber/master/README.md).

**Что берём:** Страновые parsing/formatting/validation на metadata национальных планов.

**Предел вывода:** Валидный формат не доказывает активность, принадлежность компании или допустимость канала.

<a id="s26"></a>
### S26. Playwright: BrowserContext API

[Первичный материал](https://raw.githubusercontent.com/microsoft/playwright/main/docs/src/api/class-browsercontext.md).

**Что берём:** Изолированные контексты и настройки геолокации/состояния.

**Предел вывода:** Изоляцию tenant нужно обеспечить приложением; геопозиция JS API не равна сетевому egress.

<a id="s27"></a>
### S27. Scrapy: jobs/resume

[Первичный материал](https://raw.githubusercontent.com/scrapy/scrapy/master/docs/topics/jobs.rst).

**Что берём:** JOBDIR и сохранение состояния очереди помогают durable crawling.

**Предел вывода:** State конкретного spider не заменяет бизнес-идемпотентность и платный action ledger.

<a id="s28"></a>
### S28. Scrapy: AutoThrottle

[Первичный материал](https://raw.githubusercontent.com/scrapy/scrapy/master/docs/topics/autothrottle.rst).

**Что берём:** Адаптивное управление нагрузкой по наблюдаемой latency.

**Предел вывода:** Local worker throttle не ограничивает автоматически весь наш распределённый pool.

<a id="s29"></a>
### S29. Crawlee TypeScript: README

[Первичный материал](https://raw.githubusercontent.com/apify/crawlee/master/README.md).

**Что берём:** HTTP/headless interface, queue/storage, sessions/proxy, retries, hooks, autoscaling.

**Предел вывода:** Не переносить каждую возможность TS на Python; fingerprint функции не дают гарантию доступа.

<a id="s30"></a>
### S30. pgvector: README

[Первичный материал](https://raw.githubusercontent.com/pgvector/pgvector/master/README.md).

**Что берём:** Векторный поиск в PostgreSQL и варианты индексации дают начальный semantic retrieval.

**Предел вывода:** Approximate retrieval и фильтрация требуют измерений; recall не предметный вердикт и не обход ACL.

<a id="s31"></a>
### S31. JSON-LD: проект и документация

[Первичный материал](https://raw.githubusercontent.com/json-ld/json-ld.org/main/README.md).

**Что берём:** Машиночитаемое представление связанных данных полезно для parser input.

**Предел вывода:** JSON-LD синтаксис сам по себе не делает отношения достоверными.

<a id="s32"></a>
### S32. Letta: memory agents

[Первичный материал](https://raw.githubusercontent.com/letta-ai/letta/main/README.md).

**Что берём:** Разделение agent context и долговременной памяти полезно как паттерн.

**Предел вывода:** Миграция на платформу Letta не выбрана; не требуется запускать все memory frameworks одновременно.

<a id="s33"></a>
### S33. scrapy-playwright: официальный adapter

[Первичный материал](https://raw.githubusercontent.com/scrapy-plugins/scrapy-playwright/master/README.md).

**Что берём:** Рендер и browser interaction могут быть добавлены в Scrapy download pipeline.

**Предел вывода:** Отдельный набор lifecycle/resource traps и версий; не обязательный третий runtime.

<a id="s34"></a>
### S34. Browser Use: README

[Первичный материал](https://raw.githubusercontent.com/browser-use/browser-use/main/README.md).

**Что берём:** Open-source browser agent, local/cloud browser, tools и structured output.

**Предел вывода:** Inference/managed browser оплачиваются отдельно; список действий и доступность модели должны ограничиваться нашей системой.

<a id="s35"></a>
### S35. Stagehand: README

[Первичный материал](https://raw.githubusercontent.com/browserbase/stagehand/main/README.md).

**Что берём:** observe/act/extract, schema validated output и local browser полезны для рецептов.

**Предел вывода:** Наличие SDK примера не доказывает replay/correctness на наших сайтах; не запускаем вместе с Browser Use без причины.

<a id="s36"></a>
### S36. Docling: README

[Первичный материал](https://raw.githubusercontent.com/docling-project/docling/main/README.md).

**Что берём:** Многократные форматы, PDF layout/reading order/tables, OCR и структурированный документ.

**Предел вывода:** Лицензия кода MIT отдельно от лицензий моделей; конкретный backend/координаты и ресурсы проверяются.

<a id="s37"></a>
### S37. PaddleOCR: README

[Первичный материал](https://raw.githubusercontent.com/PaddlePaddle/PaddleOCR/main/README.md).

**Что берём:** Multilingual OCR, scene text, document parsing и варианты outputs/моделей.

**Предел вывода:** Поддержка языка и точность зависят от конкретной модели/версии; число языков семейства не гарантия каждого checkpoint.

<a id="s38"></a>
### S38. Surya: README

[Первичный материал](https://raw.githubusercontent.com/datalab-to/surya/master/README.md).

**Что берём:** OCR/layout/reading order/table recognition как альтернативный document stack.

**Предел вывода:** README различает Apache-код и ограничения коммерческого использования весов. Upstream GPU/accuracy цифры не переносим на наш корпус.

<a id="s39"></a>
### S39. Tesseract: README

[Первичный материал](https://raw.githubusercontent.com/tesseract-ocr/tesseract/main/README.md).

**Что берём:** CPU OCR baseline, language data и structured outputs полезны для простых изображений.

**Предел вывода:** Не обещаем надёжность сложной структуры, handwriting или мелких контактов без corpus benchmark.

<a id="s40"></a>
### S40. pdfplumber: README

[Первичный материал](https://raw.githubusercontent.com/jsvine/pdfplumber/stable/README.md).

**Что берём:** Доступ к символам/координатам и таблицам machine-generated PDF.

**Предел вывода:** В README прямо выделены machine-generated, а не scanned PDF; OCR engine отдельно.

<a id="s41"></a>
### S41. Unstructured: README

[Первичный материал](https://raw.githubusercontent.com/Unstructured-IO/unstructured/main/README.md).

**Что берём:** Partitioning документов и local parsing дают альтернативу Docling.

**Предел вывода:** Open-source library и коммерческий Transform API имеют разные качества/функции; рекламные цифры не наши тесты.

<a id="s42"></a>
### S42. Trafilatura: README

[Первичный материал](https://raw.githubusercontent.com/adbar/trafilatura/master/README.md).

**Что берём:** Main-text/metadata extraction, discovery и очистка boilerplate.

**Предел вывода:** Удаление footer/header может удалить нужные контакты/реквизиты; raw DOM и contact scopes обязательны.

<a id="s43"></a>
### S43. YaCy: README

[Первичный материал](https://raw.githubusercontent.com/yacy/yacy_search_server/master/README.md).

**Что берём:** Собственный crawler/index/search и optional P2P режим.

**Предел вывода:** Корпус/актуальность надо строить; P2P не подходит для private tenant data по умолчанию.

<a id="s44"></a>
### S44. Apache Nutch: README

[Первичный материал](https://raw.githubusercontent.com/apache/nutch/master/README.md).

**Что берём:** Подтверждены проект и developer/build ecosystem как кандидат крупного crawling.

**Предел вывода:** Прочитанный README не даёт достаточного основания обещать готовый современный deploy/index integration; отдельная проверка перед выбором.

<a id="s45"></a>
### S45. OpenSearch: README

[Первичный материал](https://raw.githubusercontent.com/opensearch-project/OpenSearch/main/README.md).

**Что берём:** Search/observability платформа — кандидат масштабирования собственного индекса.

**Предел вывода:** README не доказывает подходящую конфигурацию multilingual hybrid retrieval или необходимость cluster на пилоте.

<a id="s46"></a>
### S46. Vespa: README

[Первичный материал](https://raw.githubusercontent.com/vespa-engine/vespa/master/README.md).

**Что берём:** Text/vector/tensor/structured serving и ranking для сложного поиска.

**Предел вывода:** Операционную стоимость и выгоду относительно PostgreSQL/OpenSearch надо доказать.

<a id="s47"></a>
### S47. Meilisearch: README

[Первичный материал](https://raw.githubusercontent.com/meilisearch/meilisearch/main/README.md).

**Что берём:** Поиск внутри приложений, UI-oriented retrieval.

**Предел вывода:** Не получает сам весь интернет и не заменяет discovery gateway/crawler.

<a id="s48"></a>
### S48. Common Crawl Index Server: README

[Первичный материал](https://raw.githubusercontent.com/commoncrawl/cc-index-server/master/README.md).

**Что берём:** Доступ к URL indexes отдельных архивных collections.

**Предел вывода:** Это lookup URL/снимков, не гарантированно свежий full-text search по всему интернету; contacts требуют актуальной проверки.

<a id="s49"></a>
### S49. Crawlee Python: README

[Первичный материал](https://raw.githubusercontent.com/apify/crawlee-python/master/README.md).

**Что берём:** HTTP/BeautifulSoup и Playwright crawler, queue/storage, sessions/proxy, retries; self-hosted запуск.

**Предел вывода:** Рекомендуемый первый прототип, не уже проверенный победитель. Benchmark отдельно от TS, версии фиксируются.

<a id="s50"></a>
### S50. Playwright: Page API

[Первичный материал](https://raw.githubusercontent.com/microsoft/playwright/main/docs/src/api/class-page.md).

**Что берём:** Навигация, locators, ожидание состояний и screenshots для адресных сценариев.

**Предел вывода:** Успешный вызов click не означает, что получен нужный контакт или разрешён внешний эффект.

<a id="s51"></a>
### S51. Crawlee: proxy management guide

[Первичный материал](https://raw.githubusercontent.com/apify/crawlee/master/docs/guides/proxy-management.mdx).

**Что берём:** Собственные proxy URLs и session-oriented proxy management.

**Предел вывода:** ProxyConfiguration не создаёт сам пул адресов нужной страны и не доказывает его фактическую географию.

<a id="s52"></a>
### S52. Scrapy: architecture/features overview

[Первичный материал](https://raw.githubusercontent.com/scrapy/scrapy/master/docs/intro/overview.rst).

**Что берём:** Spider/scheduler/item pipeline и расширяемость полезны для стабильных каталогов.

**Предел вывода:** Необходимость отдельного Scrapy deployment должна подтверждаться классом нагрузки, а не общим списком функций.

<a id="s53"></a>
### S53. go-imap: README

[Первичный материал](https://raw.githubusercontent.com/emersion/go-imap/master/README.md).

**Что берём:** IMAP library для Go — кандидат подключения рабочего ящика.

**Предел вывода:** README v2 говорит, что major ещё в разработке; release/support и cursor semantics проверяются до adoption.

<a id="s54"></a>
### S54. go-message: README

[Первичный материал](https://raw.githubusercontent.com/emersion/go-message/master/README.md).

**Что берём:** Internet Message Format/MIME, streaming, charset и mail subpackage.

**Предел вывода:** Parser не заменяет spam verdict, tenant routing, threading и intent classification.

<a id="s55"></a>
### S55. Rspamd: README

[Первичный материал](https://raw.githubusercontent.com/rspamd/rspamd/master/README.md).

**Что берём:** Spam filtering/email processing framework с правилами и Milter.

**Предел вывода:** Spam verdict не устанавливает юрлицо, деловой интерес или разрешение отправки.

<a id="s56"></a>
### S56. Stalwart: README

[Первичный материал](https://raw.githubusercontent.com/stalwartlabs/mail-server/main/README.md).

**Что берём:** Mail server с SMTP/IMAP, inbound processing, hooks/webhooks и filtering.

**Предел вывода:** Отдельная инфраструктура; dual licensing AGPL/SEL и file-level notices требуют проверки состава. Не выбран для пилота.

<a id="s57"></a>
### S57. Mailpit: README

[Первичный материал](https://raw.githubusercontent.com/axllent/mailpit/develop/README.md).

**Что берём:** Development SMTP capture/UI/testing для контролируемых прогонов.

**Предел вывода:** Не используется как доказательство production приёма внешних писем или полной замены рабочего mailbox.

## Дополнительно прочитанные лицензии

- [crawl4ai-license](https://raw.githubusercontent.com/unclecode/crawl4ai/main/LICENSE).
- [crawlee-license](https://raw.githubusercontent.com/apify/crawlee/master/LICENSE.md).
- [docling-license](https://raw.githubusercontent.com/docling-project/docling/main/LICENSE).
- [paddleocr-license](https://raw.githubusercontent.com/PaddlePaddle/PaddleOCR/main/LICENSE).
- [surya-license](https://raw.githubusercontent.com/datalab-to/surya/master/LICENSE).
- [browser-use-license](https://raw.githubusercontent.com/browser-use/browser-use/main/LICENSE).
- [stagehand-license](https://raw.githubusercontent.com/browserbase/stagehand/main/LICENSE).
- [graphiti-license](https://raw.githubusercontent.com/getzep/graphiti/main/LICENSE).
- [splink-license](https://raw.githubusercontent.com/moj-analytical-services/splink/master/LICENSE).

## Что нужно подтвердить следующими испытаниями

- Фактическое покрытие поисковых providers/metasearch по странам и языкам; текущие тарифы, квоты и условия использования.
- Региональный proxy egress и контент сайтов, сессии и стоимость трафика; никакие адреса/страны здесь не выдаются за работающий купленный пул.
- Crawl4AI/Crawlee на одинаковом размеченном корпусе с CPU/browser/egress учётом; отдельная добавочная полезность второго движка.
- Docling/PaddleOCR/Tesseract по точным email/номерам и связям с субъектом, включая кириллицу/латиницу и последующие рынки; конкретные checkpoints и лицензии.
- Text/tool/vision capabilities выбранных LLM. Совместимый base URL не доказывает vision, structured tool use или нужную accuracy.
- Реальный mailbox ingest, form red sink, threading и pause/suppression по ответам; разбор provider IDs и восстановление cursor.
- Правила/доступность национальных реестров каждого rollout рынка: таблица международных ID в концепции является проектным профилем, а не правовой справкой или отчётом о подключении.
- Effective source policy при пересечении действующих и pending OpenSpec: browser autonomy, robots, manual form sending и прежнее исключение IMAP.

## Дополнение: аудит реализации 2026-10-15

Первоначальные 66 источников выше сохранены. Полный аудит добавил [69 receipts первичных источников](audit-sources.json), [тематические факты/гипотезы](audit-findings.json) и [планы 94 задач](task-blueprints.json). [Сводка](12-change-audit.md) отличает опубликованные возможности, фактический код и требующие эксперимента решения. Денежный бюджет приложения уточнён пользователем: только Firecrawl, остальные инструменты через существующий proxy.
