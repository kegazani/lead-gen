# 5. Поиск, собственный краулер и proxy

## 5.1. База и актуальная рекомендация

Ранее в `docs/crawl4ai-playbook.md` и `openspec/changes/self-hosted-crawler/` выбран Crawl4AI. Пользователь разрешил пересмотреть выбор и использовать несколько open-source инструментов, если это оправдано задачей.

**Для будущего собственного acquisition runtime рекомендуется прототип Crawlee for Python с HTTP/Playwright; Crawl4AI участвует в обязательном сравнении и остаётся возможным специализированным инструментом.** Окончательный production cutover зависит от acceptance по одинаковому корпусу, стоимости и качеству свидетельств. Подробное сравнение, browser/OCR и состав платформы — [в документе 9](09-browser-ocr-and-tool-selection.md).

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
| IP-выход страны | Не гарантирован контрактом | Требует проверки hosted service | Управляется нашим egress/proxy profile |
| Цена | Firecrawl guard/credits | Тариф и квоты | Search + CPU/browser + proxy + LLM + эксплуатация |

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
9. Метрики качества извлечения и фактической стоимости результата.
10. Версионируемую capability manifest и контрактные тесты.

Наш сервис хранит бизнес-логику исследования в Go-контекстах; browser/extraction mechanics живут в crawler adapter/service. Прямое изменение выбранного upstream оформляется небольшими поддерживаемыми patches/fork, если задача не решается extensions. Зависимости и версия фиксируются; upstream update проходит корпус регрессий.

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

Старые `crawl.Port`/`search.Port` остаются совместимыми. Новый исследовательский запрос может иметь такой вид:

```yaml
request:
  tenant_ref: tenant-fixture
  mission_ref: mission-fixture
  requested_url: https://company.example/contact
  purpose: confirm-company-contact
  market_country: DE
  language_hints: [de, en]
  browser_locale: de-DE
  egress: {country: DE, mode: required, session_ref: session-fixture}
  representations: [html, markdown, links]
  capability_profile_ref: acquisition-de-v1
  cost_reservation_ref: reservation-fixture
result:
  final_url: https://company.example/kontakt
  artifact_ref: artifact-fixture
  observed_at: <timestamp>
  applied_geography_ref: geo-receipt-fixture
  available_representations: [html, markdown, links]
  unavailable_capabilities: []
  cost_receipt_ref: cost-fixture
```

`required` запрещает тихий fallback страны. `preferred` допускает только разрешённое отклонение с отображением фактического маршрута. Результат не содержит proxy credentials/provider keys. Доступ к artifact проверяется отдельно.

Legacy wrapper публикует реально доступные Markdown capabilities. Нельзя выдавать придуманный из текста selector за полученный DOM locator.

## 5.7. Frontier и предметный приоритет

Очередь хранит URL, страну/язык, цель посещения, ожидаемую пользу, стоимость и источник обнаружения. Приоритетны legal/contact/impressum/requisites/about/office pages; затем внешние карточки, документы и точные registry lookups.

Нормализация URL учитывает значимые параметры локали/карточки, acquisition profile и версию источника. Canonical и redirect служат подсказками и не доказывают тождество организаций. Лимит источника действует на весь worker pool.

Статья в новом исследовательском пути может дать discovery signal. Она не создаёт автоматически компанию издателя и не отдаёт все свои контакты упоминаемому субъекту. Demo-фильтр статей сохраняется в старом пути; новая логика работает с утверждениями отдельно.

## 5.8. Когда нужен proxy

| Маршрут | Назначение | Проверки |
|---|---|---|
| Direct | Публичный географически нейтральный источник | Успех, latency, наблюдаемый регион |
| Региональный datacenter | Нужен country egress | Страна/ASN, согласованность содержимого, session lifetime |
| Разрешённый региональный ISP/residential | Datacenter недостаточен и условия источника/поставщика допускают маршрут | Происхождение пула, регион, трафик, цена, sticky sessions |
| Managed acquisition | Внешнее получение экономически оправдано | Лицензия, стоимость результата, необходимые артефакты |

Это классы маршрутов для выбора поставщика после пилота. Proxy не добавляет права доступа к авторизованному/закрытому источнику. Отказ/CAPTCHA фиксируется как ограничение, а не превращается в бесконечную эскалацию маршрутов.

Sticky key: `(tenant, source, country, session-purpose)`. Cookies/storage клиентов не смешиваются. Смена IP в середине сценария может изменить содержимое; это новая попытка с отдельным receipt.

Здоровье различает отказ proxy, ошибку его credentials, timeout, ограничение источника, неправильную географию и лимит бюджета. Ни один исход не равен «компании не существует».

## 5.9. Подтверждение географии

Хранить `requested_country`, `provider_claimed_country`, `observed_egress_country`, время и способ проверки. Геобазы могут ошибаться. Нужны признаки уверенности и проверка полезного содержимого целевого сайта.

Health check выхода не передаёт клиентские контакты или текст оффера стороннему IP-сервису. Locale/timezone браузера согласуются с маршрутом там, где источник этого требует, но не заменяют IP и SERP location.

DNS/redirect validation и URL policy действуют до получения и после переходов. LLM не получает свободный доступ к localhost/private network или credentials. TLS-проверка сохраняется.

## 5.10. Доступ и пересечения старых планов

Перед реализацией согласовать четыре конкретных пересечения:

- В `self-hosted-crawler` задача реализации `search.Port` через crawler должна быть разделена: self-hosted acquisition не подтверждает наличие внешнего поискового индекса. Hosted Search в документации Crawl4AI не решает это автоматически.
- Старый план помещает LLMExtractionStrategy внутрь Crawl4AI. Для нового evidence pipeline рекомендуется общий управляемый model gateway и предметный extractor; этот выбор требует отдельного ADR и budget tests. Рабочий Firecrawl не меняется.
- Pending self-hosted design выбирает robots default `false`, действующая `osint-enrichment` содержит безусловное соблюдение. Концепция не меняет ранее выбранное значение, не применяет pending-дельту молча и фиксирует согласование effective source policy как зависимость активации.
- Действующая спека ограничивает автономные клики. Первая версия использует проверяемые URL и заранее описанные read-only site profiles. Общий LLM-driven browser mode — отдельная будущая дельта и acceptance, без обхода авторизации/ограничений доступа.

## 5.11. Переход с Firecrawl

1. Сравнить Crawlee Python, Crawl4AI и действующий Firecrawl на одном корпусе; зафиксировать версии, capability manifest и решение для собственного runtime. Реализовать соответствующий adapter/service.
2. Проверить на одинаковом разрешённом корпусе HTML/DOM, контакты, PDF, локали, географию, resume, лимиты и стоимость.
3. Сравнить наблюдения с Firecrawl; сравнение не считает число строк доказательством качества.
4. Включить наш движок для ограниченного research cohort с явной версией acquisition profile.
5. Расширять нагрузку по подтверждённым source/country capabilities.
6. Сменить основной путь после acceptance и наблюдения. Firecrawl остаётся временным средством отката на период перехода, а не обязательной постоянной зависимостью.

Тарифы proxy/search не зафиксированы: нужен актуальный trial/RFP по странам, объёму и условиям доступа. Полная стоимость включает search API, proxy GB, browser/CPU, LLM/OCR, storage, ретраи и сопровождение. Open-source библиотека не делает весь pipeline бесплатным.
