# 5. Поиск, собственный краулер и proxy

## 5.1. База и актуальная рекомендация

Ранее в `docs/crawl4ai-playbook.md` и `openspec/changes/self-hosted-crawler/` выбран Crawl4AI. Пользователь разрешил пересмотреть выбор и использовать несколько open-source инструментов, если это оправдано задачей.

**Первый кандидат собственного acquisition runtime — Crawlee for Python с HTTP/Playwright. Crawl4AI участвует в обязательном сравнении; минимальная собственная обвязка HTTP + Playwright служит дополнительной базой сравнения.** Окончательный production cutover зависит от acceptance по одинаковому корпусу, стоимости и качеству свидетельств. Подробное сравнение, browser/OCR и состав платформы — [в документе 9](09-browser-ocr-and-tool-selection.md).

Общий денежный/кредитный бюджет приложения действует **только для Firecrawl Search/Crawl/Extract**. Остальные инструменты работают через существующий proxy без второго денежного бюджета приложения. Технические пределы времени, попыток, размера и параллелизма сохраняются; доступная статистика proxy помогает измерениям и не становится общим денежным hard stop. Само наличие proxy не подтверждает географический выход или browser/OCR capabilities.

Firecrawl используется как временный рабочий путь до реализации и приёмки собственного контура. Hosted Crawl4AI не является автоматически выбранной заменой. Бизнес-логика исследования и долгосрочный acquisition contract не зависят от победившей библиотеки.

## 5.2. Три независимых инструмента

- **Discovery:** внешний поиск или реестр находит URL/организацию.
- **Acquisition:** Firecrawl сейчас, затем наш self-hosted контур получает конкретный источник.
- **Reasoning:** предметный extractor и проверяющие правила устанавливают субъект и отношения.

Связи между компаниями, память и LLM-планирование можно развивать до завершения собственного краулера. Замена приобретения страниц не должна переписывать предметную идентичность.

## 5.3. Сравнение возможностей

| Возможность | Наш старый контракт | Опубликовано Firecrawl | Целевой собственный контур |
|---|---|---|---|
| Web search | text + maxResults | Hosted Search | Отдельный search backend, не мировой индекс внутри Crawl4AI |
| Получение страниц | Crawl с лимитами | Crawl/Scrape/Map | Контролируемый HTTP/browser fetch за общим контрактом |
| Представления | Markdown + Metadata | Markdown/HTML/JSON и др. | Raw/clean HTML, Markdown, links, snapshots |
| Предметные locators | Не описаны | Зависит от endpoint/формата | DOM scopes, spans, PDF/OCR locators |
| Сессии и действия | Не описаны старым портом | Actions/interaction опубликованы | Собственные read-only site profiles и контексты |
| Страна выдачи | Нет параметра | Проверить точный endpoint/SDK | Параметр отдельного search provider |
| IP-выход страны | Не гарантирован контрактом | Требует проверки hosted service | Только проверенный egress profile; наличие proxy само по себе не гарантия |
| Денежный бюджет | Firecrawl guard/credits | Тариф и квоты | Budget только для Firecrawl; для остальных existing proxy и технические пределы |

Основания: [S01](08-research.md#s01), [S03–S06](08-research.md#s03). Наличие функции в документации поставщика не означает её присутствия в нашем адаптере. До пробного запуска не утверждается, что собственный контур лучше работает на всех сайтах.

## 5.4. Что развиваем в своём краулере

1. URL/frontier management по исследовательской задаче и пробелам dossier.
2. Недорогой fetch простых страниц и browser rendering при необходимости.
3. Разбор отдельных карточек/таблиц/разделов: субъект, контакты, реквизиты, footer.
4. Снимки, hashes, redirect chain, версии parser/нормализации и доступные представления.
5. PDF/text/OCR с указанием страницы и координат.
6. Country/locale/timezone/session/egress profiles с наблюдаемым применением.
7. Источниковые recipes и селекторы, проверяемые на сохранённых fixtures.
8. Rate limiting, приоритеты, ограниченные ретраи, resume и content-diff.
9. Метрики качества, технического расхода и доступная статистика стоимости результата без нового общего денежного лимита.
10. Версионируемую capability manifest и контрактные тесты.

`agent-orchestration` в Go выбирает цель и действие. `integration-platform` владеет техническим frontier, URL/attempt/lease ledger, общим состоянием источников и receipt. Python runtime исполняет ограниченную задачу/сценарий; найденные ссылки возвращает кандидатами. Предметные связи и принадлежность контакта проверяет `lead-management`. Прямое изменение выбранного upstream оформляется небольшими поддерживаемыми patches/fork, если задача не решается extensions. Зависимости и версия фиксируются; upstream update проходит корпус регрессий.

## 5.5. Собственный поисковик

### Внешний regional API для discovery

Brave Search и SerpAPI — кандидаты поискового поставщика; выбор crawler runtime оценивается отдельно. Их опубликованные интерфейсы содержат country/language/location параметры ([S19–S20](08-research.md#s19)). Нужны сравнение покрытия стран, условий использования, квот, стоимости и повторяемости результатов.

Proxy для HTTP-вызова API не заменяет параметры выдачи. Например, SDK SerpAPI описывает `location`, `gl`, `hl`; Brave — `country`, `search_lang`, `ui_lang`. Фактический mapping и поддерживаемые значения проверяются адаптером.

### Собственный метапоиск

SearXNG даёт API и конфигурацию внешних движков, включая proxy ([S21–S22](08-research.md#s21)). Он остаётся зависимым от upstream search engines, их условий и доступности. Public community instance не является production SLA и не обеспечивает безлимитный доступ.

Этот вариант возможен отдельным исследуемым источником. Он не обязателен для первого этапа и не заменяет собой внутренний глобальный веб-индекс.

### Собственный вертикальный индекс

Индекс известных компаний, реестровых записей, разрешённых сайтов и свидетельств. Сначала PostgreSQL exact/alias/FTS и существующий vector recall. Дополнительный search cluster — по измерениям.

Собственный корпус ускоряет повторные заявки и поиск по накопленным связям. Отсутствие результата в нём не означает отсутствия компании в интернете. Общий мировой поисковый индекс не является задачей первого этапа.

**Целевая комбинация:** региональный discovery backend + собственный company/evidence index + наш acquisition runtime.

## 5.6. Предлагаемая acquisition boundary

Старые `crawl.Port`/`search.Port` остаются совместимыми. Новый контракт отделяет команду, техническую попытку и неизменяемый результат. Пример собственного маршрута:

```yaml
request:
  tenant_ref: tenant-fixture
  purpose_ref: research-fixture
  mission_ref: mission-fixture
  action_id: action-fixture
  attempt_id: attempt-fixture
  idempotency_key: acquire-fixture
  command_hash: sha256-fixture
  fencing_token: 1
  requested_url: https://company.example/contact
  source_profile_ref: company-contact-v1
  effective_policy_ref: source-policy-v1
  fetch_profile: http-then-render
  required_capabilities: [html, links]
  language_hints: [de, en]
  browser_locale: de-DE
  egress: {country: DE, mode: required, session_ref: session-fixture}
  technical_limits_ref: bounded-site-flow-v1
  deadline: <timestamp>
result:
  receipt_ref: receipt-fixture
  action_id: action-fixture
  attempt_id: attempt-fixture
  outcome: acquired
  final_url: https://company.example/kontakt
  artifact_ref: artifact-fixture
  observed_at: <timestamp>
  applied_geography_ref: geo-receipt-fixture
  available_representations: [html, links]
  unavailable_capabilities: []
  subject_blocks: [block-fixture]
  discovered_links: [link-fixture]
  technical_usage_ref: usage-fixture
```

`technical_limits_ref` фиксирует предел top-level страниц, навигаций, запросов ресурсов, переданных/распакованных байтов, общего числа попыток и времени. Для Firecrawl дополнительно передаются `firecrawl_credit_reservation_ref` и итоговый credit receipt; собственный route этих денежных резервов не требует. `required` запрещает тихий fallback страны. `preferred` допускает только предусмотренное policy отклонение с фактическим receipt. Неизвестная возможность existing proxy остаётся неизвестной.

Receipt сохраняет версии runtime/browser/parser, policy, исходный и конечный URL, доступную цепочку redirect, MIME/status, hashes исходных и производных представлений, частичный результат и stop reason. Неизвестные redirect hops/DOM/географию нельзя достраивать догадкой. `SubjectBlockCandidate` содержит локальный блок, locator и исходные company/ID/contact значения; он ещё не утверждение о принадлежности. `LinkCandidate` содержит anchor, href, блок-источник и тип связи: `company-home`, `contact`, `legal`, `entity-profile`, `pagination`, `document`, `external-reference`.

Полезная страница без email имеет успешный результат получения: название, ИНН, предметный блок и ссылки передаются для сохранения entity/evidence и открытого пробела. Число fetched pages, сохранённых субъектов и зачтённых результатов заявки — разные показатели. Legacy wrapper честно публикует Markdown capabilities; придуманный selector не является DOM locator.

Предлагаемый API worker: idempotent `POST /v1/acquisition-attempts`, `GET /v1/acquisition-attempts/{id}`, команда cancel и идемпотентная доставка receipt. Повтор ключа с другим `command_hash` возвращает conflict. Сетевой вызов выполняется вне DB transaction; сохранение receipt, CAS по lease/fencing token и outbox — атомарно. Истёкший worker не переписывает актуальный outcome; late receipt сохраняет происхождение. Для Firecrawl crash после оплаты требует сверки provider job/credit, без слепого оплаченного повтора.

## 5.7. Frontier и предметный приоритет

Единственный долговечный технический frontier находится в `integration-platform`. Запись содержит scoped URL/profile/purpose key, тип страницы, источник обнаружения, цель посещения, technical attempt history, due time, lease и ссылку на родительское исследовательское действие. `agent-orchestration` задаёт предметный приоритет и разрешённые следующие действия; runtime не создаёт второй самостоятельный поиск.

1. По seed/названию/ИНН найти память, прошлые evidence и открытые пробелы.
2. Разделить discovery новых субъектов и enrichment известного субъекта. Каталог полезен для первого, contact/legal/profile — для второго.
3. Нормализовать URL без потери параметров карточки и локали. Canonical/redirect — подсказка, не тождество организаций. Отсекать календарные/filter/sort/session ловушки по source profile.
4. Получить технический lease и общий host slot, выбрать HTTP/JS/document profile по MIME, known recipe и недостающему содержимому. Короткий текст сам по себе не требует браузера/LLM.
5. Сохранить bytes/DOM, предметные блоки и ссылки. Контакт footer каталога принадлежит издателю, пока нет основания связать его с отдельной карточкой.
6. Вернуть новые URL/ID/name candidates планировщику. Компания без email остаётся в памяти с узким результатом «не найдено в этом источнике» и `OpenNeed`.
7. При следующем разрешённом исследовании/обновлении проверить новый источник и дополнить прежнюю entity. `OpenNeed` сам по себе не создаёт новую человеческую заявку.

HTTP redirects, JS subresources и шаги принятого recipe могут выполняться автоматически внутри объявленных технических caps. Переход к новому внешнему субъекту/источнику, расширение маршрута или новый LLM-вызов проходят инструментальную policy планировщика. Library queue — ограниченная проекция уже допущенной работы; `enqueue_links`/deep crawl не запускают независимый бесконечный frontier.

Общий host limiter охватывает весь worker pool и tenant: смена profile/tenant не создаёт обходной лимит. Tenant fairness учитывается отдельно. Повторы, proxy rotations, bytes и навигации включаются в технический ledger. В Crawlee `max_requests_per_crawl` допускает превышение при concurrency, а session rotations не входят в `max_request_retries`; поэтому эти настройки не заменяют платформенный предел ([BasicCrawler source](https://raw.githubusercontent.com/apify/crawlee-python/master/src/crawlee/crawlers/_basic/_basic_crawler.py)).

Статья может дать discovery evidence и ссылки, даже если контактов нет. При recrawl сохраняются предыдущий artifact/profile, ETag/Last-Modified, changed/unchanged/partial outcomes. `304` относится к конкретному представлению и прежнему снимку; он не подтверждает все сведения компании навсегда. Новый artifact запускает адресный пересмотр зависимых утверждений, сохраняя исторические итоги завершённых заявок.

## 5.8. Когда нужен proxy

| Маршрут | Назначение | Проверки |
|---|---|---|
| Direct | Публичный географически нейтральный источник | Успех, latency, наблюдаемый регион |
| Региональный datacenter | Нужен country egress | Страна/ASN, согласованность содержимого, session lifetime |
| Разрешённый региональный ISP/residential | Datacenter недостаточен и условия источника/поставщика допускают маршрут | Происхождение пула, регион, трафик, цена, sticky sessions |
| Managed acquisition | Внешнее получение экономически оправдано | Лицензия, стоимость результата, необходимые артефакты |

Это исследуемые классы маршрутов, а не подтверждённые функции существующего proxy. Сначала проверяются его протокол, маршрутизация, country support, sticky semantics и доступные receipts; недостающая возможность имеет явный исход `unsupported`/`geo-unavailable`. Proxy не добавляет права доступа к авторизованному/закрытому источнику. Отказ/CAPTCHA фиксируется как ограничение, а не превращается в бесконечную эскалацию маршрутов.

Sticky key: `(tenant, source, country, session-purpose)`. Cookies/storage клиентов не смешиваются. Смена IP в середине сценария может изменить содержимое; это новая попытка с отдельным receipt.

Здоровье различает отказ proxy, ошибку credentials, timeout, ограничение источника, неправильную географию, технический предел и отдельно кредитный лимит Firecrawl. Ни один исход не равен «компании не существует».

## 5.9. Подтверждение географии

Хранить `requested_country`, `provider_claimed_country`, `observed_egress_ip`, `observed_egress_country`, время и способ проверки. Sticky proxy URL не гарантирует тот же IP: проверка использует тот же маршрут/сессию, а её актуальность ограничена. Геобазы могут ошибаться. Нужны признаки уверенности и проверка полезного содержимого целевого сайта.

Health check выхода не передаёт клиентские контакты или текст оффера стороннему IP-сервису. Locale/timezone браузера согласуются с маршрутом там, где источник этого требует, но не заменяют IP и SERP location.

URL/DNS policy проверяется до получения и при каждом фактическом сетевом соединении, включая redirects, subresources и downloads. Egress gateway проверяет A/AAAA/public-address и не допускает обхода через прямой выход; browser routes — дополнительный слой. Официальные [Playwright network docs](https://raw.githubusercontent.com/microsoft/playwright/main/docs/src/network.md) указывают ограничения перехвата redirects и ServiceWorkers: `page.route` одного стартового URL недостаточно. Приёмка включает public→private redirect, DNS rebinding, IPv4-mapped IPv6, ServiceWorker и cross-tenant cookies. LLM не получает localhost/private network или credentials; TLS-проверка сохраняется.

## 5.10. Доступ и пересечения старых планов

Перед реализацией согласовать четыре конкретных пересечения:

- В `self-hosted-crawler` задача реализации `search.Port` через crawler должна быть разделена: self-hosted acquisition не подтверждает наличие внешнего поискового индекса. Hosted Search в документации Crawl4AI не решает это автоматически.
- Старый план помещает LLMExtractionStrategy внутрь Crawl4AI. Для нового evidence pipeline рекомендуется общий управляемый model gateway и предметный extractor; этот выбор требует ADR, проверки единого action lineage и отсутствия скрытых повторных LLM вызовов. Экстракция идёт по сохранённому artifact через existing proxy; денежные проверки относятся только к Firecrawl. Рабочий Firecrawl не меняется.
- Pending self-hosted design выбирает robots default `false`, действующая `osint-enrichment` содержит безусловное соблюдение. История pending решения сохраняется; активный canonical действует до согласованной MODIFIED-дельты. Defaults библиотеки не становятся продуктовым решением. В частности, исследованные Crawlee/Crawl4AI source snapshots выключают robots по умолчанию; wrapper задаёт effective policy явно. В Crawlee соблюдение crawl-delay требует отдельного ThrottlingRequestManager, а общий host limiter всё равно нужен.
- Действующая спека ограничивает автономные клики. Первая версия использует проверяемые URL и заранее описанные read-only site profiles. Общий LLM-driven browser mode — отдельная будущая дельта и acceptance, без обхода авторизации/ограничений доступа.

## 5.11. Переход с Firecrawl

1. Сравнить Crawlee Python, Crawl4AI, минимальную HTTP/Playwright обвязку и действующий Firecrawl на одном корпусе; зафиксировать версии, capability manifest и решение для собственного runtime. Реализовать соответствующий adapter/service.
2. Проверить на одинаковом разрешённом корпусе HTML/DOM, контакты, PDF, локали, географию, resume, лимиты и стоимость.
3. Сравнить наблюдения с Firecrawl; сравнение не считает число строк доказательством качества.
4. Включить наш движок для ограниченного research cohort с явной версией acquisition profile.
5. Расширять нагрузку по подтверждённым source/country capabilities.
6. Сменить основной путь после acceptance и наблюдения. Firecrawl остаётся временным средством отката на период перехода, а не обязательной постоянной зависимостью.

Перед выпуском фиксируются image/library/browser versions, ресурсы, полный LICENSE/NOTICE и лицензии выбранных моделей. В исследованном [Crawl4AI LICENSE](https://raw.githubusercontent.com/unclecode/crawl4ai/main/LICENSE) есть дополнительный attribution текст поверх Apache-2.0; одного короткого SPDX-названия недостаточно. Эти source snapshots описывают прочитанные возможности и условия, а не проверенный release. Денежный budget приложения остаётся Firecrawl-only; статистика proxy/ресурсов используется для сравнения вариантов и эксплуатации, если доступна.
