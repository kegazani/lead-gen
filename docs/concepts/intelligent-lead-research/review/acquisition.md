# Аудит получения страниц, браузера и OCR

Baseline `bbe49f6`. Исследование документов и реального кода; ни один новый crawler/model/provider здесь не запускался. 22 официальных source/doc/license файла сохранены локально с URL, временем, размером и SHA256. Их main/master — документированная возможность на дату загрузки, не выбранный проверенный release. Все новые пути/таблицы ниже — предложения.

## Последнее уточнение бюджета пользователя

Общий денежный/кредитный бюджет приложения требуется **только для Firecrawl Search/Crawl/Extract**. Собственный crawler, browser, OCR, LLM и прочие инструменты используют существующий proxy без второго application monetary budget. Для них сохраняются технические ограничения host concurrency/rate, retries/attempts, bytes/pages, timeouts и защита от циклов; это техническая защита исполнения, не денежные резервы. Статистика стоимости из proxy опциональна, не общий hard-stop. Все рекомендации ниже читаются с этим приоритетом.

## Главный вывод для продукта

Собственный краулер должен приносить **доказательства о конкретном субъекте и следующие полезные адреса**, включая страницу без email. Правильный результат страницы — не только список контактов. Мы должны сохранить компанию/ИНН/название/ссылку на профиль, установить какую информацию не нашли именно здесь, вернуть `LinkCandidates`, а затем искать контакт в другом источнике. Footer каталога, номер статьи и контакты издателя не наследуются компаниями его списка. Дешёвый HTTP не обязательно первый: известная JS карточка сразу идёт в browser profile, PDF — в document profile.

Первая инженерная рекомендация: **Crawlee Python + HTTP/Playwright как один bounded acquisition runtime**, один внешний предметный планировщик и technical frontier платформы. Crawl4AI участвует в равном обязательном сравнении, thin HTTP+Playwright — полезная контрольная реализация. Firecrawl остаётся совместимым route. Несколько способов получения нужны; несколько независимых crawler clusters не следуют из этого требования. Второй движок принимаем по добавочным правильно связанным контактам/свидетельствам и полной стоимости сопровождения.

## Что реально обнаружено

- `firecrawl.go:193`: v1 Crawl получает `maxDiscoveryDepth`, `ignoreSitemap=true`, optional `limit`. `Page` (`contracts/.../crawl.go:65`) сохраняет URL/Markdown/Metadata; HTML/DOM/redirect-chain/точных locators нет.
- `firecrawl.go:276`: Extract снова читает URL через `/v1/scrape`, JSON contacts. Нет привязки к тому же неизменяемому снимку, который прочитал regex. Новый pipeline должен извлекать из artifact, а не заново получать страницу для каждого слоя.
- `firecrawl.go:454`: best-effort cancel и final snapshot реализованы; provider job id не публикуется старым портом. Нельзя обещать exactly-once после crash между provider submit и сохранением id.
- `firecrawl.go:544`: `io.ReadAll(resp.Body)` в методе транспорта без локального max bytes. Новый gateway обязан контролировать bytes, decompression, deadlines, partial outcomes.
- `documentparse.go:49`: Text/Metadata без координат. Потребуется новый versioned layout/evidence contract.
- `run_search.go:792–851`: legacy admission/Extract/save связаны с contact-oriented demo. Его фильтры нельзя переносить в новый discovery path; useful incomplete entity сохраняется отдельно от result credit.

## Критические недоработки исходного change

1. **Effective policy не решена.** Canonical `osint-enrichment:38` требует robots; pending self-hosted-crawler:60 пишет false и ссылается на прошлое решение владельца. Crawlee/Crawl4AI также имеют false defaults. Новая библиотека не выбирает продуктовую policy. Нужны явная версия правил и согласованная MODIFIED delta при её изменении; не объявляем pending действующей нормой и не стираем его историю.
2. **Две очереди и повторы.** Crawlee документирует overshoot `max_requests_per_crawl` при concurrency; session rotations не входят в `max_request_retries`. Library knobs не обеспечивают технические пределы исполнения и Firecrawl credit cap.
3. **Page.route не весь network boundary.** Официальные Playwright network docs прямо пишут: handler вызывается на исходный request, redirect браузер следует сам. ServiceWorker тоже может скрыть запрос от route. Нужен обязательный egress enforcement при каждом connect, с проверкой actual A/AAAA/private targets. Одного `page.route` или проверки стартового URL недостаточно.
4. **Встроенная LLM extraction.** C4AI `LLMExtractionStrategy` делает собственные chunk calls/overlap и учитывает usage внутри себя. Это потенциальный второй inference pipeline поверх model gateway. Native LLM выключен в новом fetch route; общий extractor вызывает gateway по immutable artifact.
5. **Docling + PaddleOCR не готовый заявленный connector.** В Docling options есть RapidOCR с inference backend `paddle`. Это не тождественно PaddleOCR pipeline. Первый профиль — поддерживаемый Docling OCR backend; отдельный PaddleOCR сравнивается. Если он выбран, наш adapter композирует его и выключает дублирующий OCR.
6. **Лицензии.** Fetched Crawl4AI LICENSE содержит Apache2 и дополнительный attribution текст. Нельзя писать только Apache и считать review готовым. Docling MIT, PaddleOCR/Tesseract Apache2, BrowserUse MIT — лицензии кода, не обещание о всех модельных весах. При фиксации release нужны полный LICENSE/NOTICE, model cards/hash и dependencies.
7. **Sticky URL не страна.** Crawlee гарантирует mapping session id→proxy URL; это не гарантия неизменного IP/страны. Geo receipt различает requested/claimed/observed и время наблюдения. Страна IP не доказывает страну компании.
8. **Benchmark смешивает причины.** Нужны сначала один общий extractor на artifacts разных crawler runtimes, затем end-to-end. Иначе различие качества extractor ошибочно приписывается краулеру.

## Точная предлагаемая граница

### Владельцы

- `agent-orchestration`: цель исследования, Firecrawl credit cap и технические пределы, выбор типа действия/инструмента, OpenNeed и meaningful progress.
- `integration-platform`: technical frontier, canonical URL/profile keys, host state, rate limiter, capability manifest, acquisition attempts/receipts, доступ к artifacts, собственный Python runtime.
- `lead-management`: subject interpretation, identity/link assertions, контактные проверки и зачёт в целях заявки через согласованные контракты.
- Document parsing/OCR — integration mechanics; итоговая принадлежность контакта не принадлежит OCR.

### `AcquisitionCommand` v1 (новый контракт)

`tenant_ref`, `purpose_ref`, `mission_ref`, `action_id`, `attempt_id`, `idempotency_key`, `command_hash`, `fencing_token`, `requested_url`, `source_profile_ref/version`, `effective_policy_ref/version`, `fetch_profile`, `requested_representations`, `required_capabilities`, `language_hints`, `browser_locale`, `timezone`, `egress_profile_ref`, `egress_country_mode(required|preferred)`, `session_ref`, `firecrawl_credit_reservation_ref` (только Firecrawl route), `max_top_pages`, `max_navigations`, `max_subrequests`, `max_download_bytes`, `max_decoded_bytes`, `max_pixels/pages`, `max_attempts_total`, `deadline`, `allowed_recipe_ref/version`, `prior_artifact_ref/validators`.

Command содержит opaque secret refs, не сами proxy credentials. Idempotency key с другим command_hash — conflict. Технические caps явные. Firecrawl credit cap учитывается только на Firecrawl route. Смена профиля/страны/рецепта — новая учтённая попытка, не невидимый fallback.

### `AcquisitionReceipt`

`receipt_id`, command/action/attempt/version refs, original/final URL, redirect hops (observed or explicitly unavailable), status/content type, timestamps, selected runtime/browser version, effective policy, requested/claimed/observed geography, source session opaque ref, immutable artifact hashes/refs, raw bytes/DOM/Markdown/diff availability, `SubjectBlockCandidate[]`, `LinkCandidate[]`, `DocumentCandidate[]`, physical fetch accounting, retries/session rotations, partial outcome, `termination_reason`, warnings/unavailable capabilities; Firecrawl credit settlement/ref только для Firecrawl, optional proxy usage/cost observations для других инструментов.

`SubjectBlockCandidate` ещё не факт принадлежности: содержит локальный блок, label, company/ID/contact raw candidates и locators. Полезная company-only page — `acquired`, не parse error. `LinkCandidate` имеет href/anchor/context/DOM locator, source block, relation hint (`company-home`, `contact`, `legal`, `profile`, `pagination`, `document`, `external-reference`), confidence category и purpose/need refs.

Статусы исходов: `acquired`, `unchanged`, `partial`, `unsupported`, `policy-denied`, `geo-unavailable`, `rate-limited`, `source-unavailable`, `challenge-required`, `parse-failed`, `timeout`, `cancelled`, `unknown-effect`. Пустой результат не равен отсутствию компании или email во всём мире.

### API/lifecycle

Предлагаемый worker API: `POST /v1/acquisition-attempts` с idempotency key → handle/status; `GET /.../{id}` → immutable completed receipt или revisioned running progress; `POST /.../{id}/cancel` → accepted cancellation intent, не обещание что provider уже остановлен. Callback/outbox идемпотентен. Downstream SDK retries не расширяют технические caps или Firecrawl credit cap.

Новые таблицы integration-platform: `acquisition_attempt` (tenant/id unique; action+attempt unique; command_hash; lease/fence; status; runtime/provider_handle; optional Firecrawl reservation; receipt_ref), `source_host_state` (общий для всех workers/tenant ключ source authority и при необходимости более широкий source-family throttle; token bucket/next_allowed_at/backoff/concurrency; tenant fairness отдельным измерением), `acquisition_receipt` (immutable attempt result), технический frontier (normalized URL+profile+purpose/due generation unique, priority, lease, previous validators). Body/DOM/images — artifact store с tenant scope и retention, в PG только hashes/refs/индексы. `observed_at` и `fetched_at` не заменяют source declared date. Ссылки/ID/субъекты хранят domain context через contracts, без прямого импорта domain.

SQL concurrency: атомарная выдача технических source/action slots под транзакцией, `FOR UPDATE SKIP LOCKED` для frontier; внешний HTTP за пределами транзакции; receipt insertion+attempt CAS+outbox атомарны. Истёкший fencing token не меняет текущий outcome, но late receipt сохраняется с lineage. При timeout после provider submit — reconciliation/unknown, не слепой retry. Completed mission snapshot не меняется от late artifact; dossier может получить новое evidence в допустимой области.

## Один обход от seed до повторного исследования

1. **Memory/source lookup.** Из seed/ИНН/имени/URL планировщик получает known entities, свежие evidence, открытые пробелы, прошлые неудачи с TTL. Existing corpus не требует нового fetch без причины.
2. **Назначение страницы.** Определить discovery или entity enrichment: каталог может дать компании; контактная/legal страница — атрибуты известной компании. Обозначить subject hint, desired assertions, deadline и технические пределы шага; кредитный предел только для Firecrawl.
3. **URL/policy admission.** Нормализовать осторожно: сохранять entity-id/locale query params, убирать только доказанные tracking params. Canonical tag/redirect — подсказка, не merge компаний. Отдельно reject неподдержанный scheme/private path; robots/source access policy по версии.
4. **Frontier.** URL классифицируется по типу и полезности для конкретного пробела; fairness между субъектами/источниками. Не ходить бесконечно в общие каталоги по числу совпадений. Отсечь calendar/filter/sort/session traps и повторное content hash при сохранении path observation.
5. **Dispatch.** Integration владелец frontier выдаёт committed bounded task; runtime не создаёт независимый durable обход. Технический lease ограничивает допустимый flow; credit reservation нужен только Firecrawl. Допустимы только declared HTTP redirects, JS resource loads и проверенный recipe в caps. Нельзя автоматически менять географию, источник/purpose, запускать LLM или внешнюю компанию из найденной ссылки.
6. **HTTP/JS routing.** MIME/source recipe/history решают путь. HTTP сохраняет исходные bytes/headers; проверить содержимое как HTML/text/PDF/image, status challenge и quality. JS escalation по признакам отсутствующего целевого блока/known hydration; короткий текст сам по себе не причина. Проверить before/after semantic gain и стоимость.
7. **Link discovery.** Читать navbar/contact/legal/footer отдельно; JSON-LD Organization/LocalBusiness только publisher assertion. Каталог: card-level links и ID, footer — publisher scope. Sitemap/robots links — URL hints, не подтверждение субъекта. Hreflang/locale candidates сохраняются. External reference отдаётся планировщику, не автоматически бесконечно обходится.
8. **Извлечение.** Сначала mailto/tel/DOM/labelled blocks/structureddata/text-PDF; затем OCR нужного crop. LLM получает ограниченный block с locator IDs, возвращает candidate assertions. Runtime не делает merge компаний и не назначает confidence=verified.
9. **Проверка/сохранение.** Сохранить useful entity без email; assertion/locator/extraction quality/ownership separate. Неудача `email-not-found-on-contact-page@version` узкая и имеет TTL. OpenNeed остаётся доступной для позднего источника/входящего письма.
10. **Recrawl.** Запуск по новому разрешённому действию (кредитный лимит применяется только при Firecrawl), stale evidence, source change или explicit refresh; не по одному существованию OpenNeed. Conditional fetch по ETag/Last-Modified только с prior artifact/profile;304 не создаёт новое прочтённое содержимое. Новый hash пересматривает зависимые assertions; unchanged не стирает ручные проверки и не делает все сведения актуальными навсегда.

## Лимиты и что разрешено runtime автоматически

Crawlee queue — ephemeral проекция committed batch, не независимая долговечная очередь исследования. Не использовать безусловный `enqueue_links`/deep crawl; наши handler возвращают candidates. Session rotation/request retry ограничены как SDK knob **и** общим техническим absolute attempt cap платформы. Physical network accounting включает subresources/redirects, а top-level page count отдельно. Общий host limiter не разбивается на независимые tenant/profile buckets, иначе смена tenant/профиля обойдёт предел источника. Per-tenant fairness идёт вторым уровнем. Origin (scheme/host/port) и при необходимости source-family задают throttle domain; публичный suffix нельзя использовать как общий сайт без корректного PSL. Браузерный flow получает max network/bytes/time lease; если per-resource slot API создаёт накладные расходы, выдаётся ограниченное число технических slots с последующим учётом. Это не денежная резервация. Невыданные технические slots не исполняются.

Automatic retries: bounded transport/5xx,429 по Retry-After внутри deadline.403/challenge не вызывает бесконечное повышение proxy tier; source-unavailable/OpenNeed. Recipe может открыть ровно разрешённую contact tab/pagination/public-search с проверкой результата. Поисковый POST бывает read-only, GET может иметь эффект; смысл проверяется allowlist действия/endpoint, не только методом. Новый source/LLM — supervisor action в пределах технической policy; monetary reservation применяется только если выбран Firecrawl.

## Браузер и document pipeline

Recipe: versioned locators/semantic target/allowed origin+endpoint, before-state hash, action, postcondition, максимум navigation/steps/time, rollback не обещается для внешнего эффекта. B2 известный сценарий разрешён canonical только в соответствующей source policy. B3 самостоятельный выбор кликов требует MODIFIED запрета до активации. BrowserUse инструмент рассматривается как planner предложений, executor применяет собственный узкий registry; default execute/fill/send tools не становятся разрешёнными. Vision capability exact endpoint/model проверяется; BrowserUse source может переводить use_vision вfalse — это не vision success.

Document: Content-type+magic; filesize/decompression/pages/pixels/timeout до heavy parse. PDF text first, column/order/glyph quality checks. OCR только нужных областей, language profile. Сохранять original artifact, page dimensions/cropbox/rotation/DPI, affine transform+inverse, OCR raw string/polygon/confidence, normalized candidates, engine/model/preprocess hashes. Bounding boxes в resized pixels не объявляются PDF coords. Два OCR движка одного image не два независимых источника. Ambiguous rn/m,О/0,Latin/Cyrillic → alternatives/review, не тихое исправление. Contact ownership проверяется отдельно от text confidence.

## Эксперименты и порядок реализации

Два benchmark слоя: acquisition-only с одинаковым extractor; затем end-to-end профили. 120 объектов — manifest, не обещание статистической достоверности для всех стран. Holdout source-family, карточки/footers/multicolumn/JS/reveal/locale/expired contacts. Метрики: artifact acquisition, exact locator recovery, field exact-match, correct subject-link precision/recall, unique correct entities/contacts, ошибочные merge, P50/P95, CPU/RAM/browser seconds/proxy bytes/token cost/manual minutes. Дополнительный runtime оценивается по incremental correct links сверх основного и цене владения. Live source change отделять от parser regression.

Предлагаемый начальный маршрут: policy/contract/gateway ADR → corpus → два bounded prototype runtime + legacy baseline → artifacts/subject blocks → external frontier/leases/host caps/egress → browser recipes → staged cohort. OCR native backend baseline параллельно corpus; B3 позже direct recipes/vision policy. Все 18 assigned tasks разложены в JSON по deps/input/output/implementation/failures/acceptance. Критические ошибки (wrong subject/cross-tenant/uncontrolled side-effect) блокируют route; ноль наблюдений на выборке не равно нулевому риску вообще. Numeric пороги noninferiority/cost замораживаются до независимой контрольной выборки, не выдумываются по отсутствующим измерениям.
