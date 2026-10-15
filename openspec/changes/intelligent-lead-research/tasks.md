# Tasks

Это **план будущей реализации**, не отчёт об исполнении. Все задачи открыты. Карта P0–P8, владельцы и критерии — [документ 7](../../../docs/concepts/intelligent-lead-research/07-architecture-and-delivery-plan.md). Для каждого apply-среза перед отметкой выполненного обязательны проектные гейты из `AGENTS.md`; проверки в пунктах ниже дополняют их. Для параллельных продуктовых приоритетов допустим отдельный согласованный apply-change с сохранением контрактов/зависимостей.

## 1. Зависимости требований и владение

- [ ] 1.1 Зафиксировать единицу результата заявки: компании/контакты/email-ready и quota semantics; верификация: примеры 25 candidates, 8 confirmed, 5 email-ready имеют разные ожидаемые дефициты и UI labels.
- [ ] 1.2 При вводе первой entity/evidence/mission дополнить уже зарегистрированные назначения capability агрегатами, VO, событиями, policies и relationships из design в `architecture/domain-catalog.yaml`, `docs/domain-model.md` и диаграммах; верификация: archlint/importlint и согласованная owner matrix без прямых чужих domain imports.
- [ ] 1.3 Оформить явное согласование existing `osint-enrichment` и pending `self-hosted-crawler` по effective source/robots policy; верификация: отсутствуют несовместимые требования/defaults на активируемом маршруте, legacy demo default не изменён скрыто.
- [ ] 1.4 Зафиксировать ADR единого model gateway/extractor против pending LLMExtractionStrategy в Crawl4AI; верификация: стоимость одного извлечения имеет один action lineage и не дублируется двумя неучтёнными LLM вызовами.
- [ ] 1.5 Разделить технические понятия internet search, local index, acquisition, document/OCR и subject verification в versioned contracts; верификация: adapter manifest не обещает неподдержанные DOM/географию/search.

## 2. Независимый корпус и измерения

- [ ] 2.1 Подготовить разрешённый размеченный корпус identity/contact/evidence с footer-trap, articles, multi-entity pages, shared domains/phones и временными конфликтами; верификация: manifest фиксирует ожидаемые субъект/значение/связь и separate holdout без source-family leakage.
- [ ] 2.2 Подготовить acquisition/browser/OCR corpus 120 объектов с RU/GB и multilingual расширениями; верификация: явно размечены HTML/JS/recipe/text-PDF/scanned/image scopes, языки и locators.
- [ ] 2.3 Реализовать расчёт field/link precision/recall, false merge, unique-result cost, latency/resource/manual-review metrics; верификация: отчёт воспроизводится по фиксированному manifest и различает upstream claims/наши измерения.
- [ ] 2.4 Документировать критерии cutover и критические ошибки; верификация: ноль наблюдаемых cross-tenant/wrong-subject случаев на acceptance, размер выборки/неопределённость и per-country показатели не скрыты общей средней.

## 3. Entity identity и доказательства

- [ ] 3.1 Добавить tenant-scoped research entity IDs и совместимый mapping старых CompanyID; верификация: одна организация с двумя доменами и два юрлица одного сайта не схлопываются, прежние ссылки demo читаются.
- [ ] 3.2 Реализовать jurisdictional identifier types/normalization с ведущими нулями и областью реестра; верификация: RU ИНН/КПП, GB prefix/zeros, DE register/court, FR SIREN/SIRET и unknown ID проходят предметные cases.
- [ ] 3.3 Добавить evidence documents/revisions/artifact refs/locators и subject scopes; верификация: контакт можно восстановить до сохранённого фрагмента и версии источника, footer каталога не становится контактом каждой компании.
- [ ] 3.4 Добавить assertion states/validity/history/source-family и conflict policy; верификация: копии одного источника не повышают независимость, новый телефон не стирает старое наблюдение.
- [ ] 3.5 Реализовать candidate matching и reversible identity decision с revision checks; верификация: подтверждение/отмена связи восстанавливают projection и не переписывают историю отправленного действия.
- [ ] 3.6 Ввести страновую нормализацию contacts и evidence-based readiness; верификация: нечисловые labels/даты/ID не дают телефоны, допустимая национальная длина не отклоняется универсальным demo-порогом, MX не доказывает ящик.
- [ ] 3.7 Добавить persistence/contract tests и dossier evidence UI для этого среза; верификация: реальный RU/GB seed даёт один объяснимо связанный контакт, правила и новая owner model описаны рядом с реализацией.

## 4. ResearchMission, долговечность и память

- [ ] 4.1 Реализовать ResearchMission до company identity и коллекцию результатов вместо first-result artifact; верификация: миссия начинается по имени/номеру без фиктивного домена и сохраняет несколько различимых entity refs.
- [ ] 4.2 Добавить registered research-planner block и structured tool proposals; верификация: недопустимые tool/args/scope/country/budget отклоняются, prompt injection источника не меняет полномочия.
- [ ] 4.3 Реализовать intent/reservation/outbox → activity → receipt/result CAS вне открытой DB transaction; верификация: crash до/после внешнего эффекта, duplicate result и lease expiry дают различимые устойчивые исходы.
- [ ] 4.4 Реализовать budget/time/attempt limits и actual cost ledger; верификация: search/proxy/browser/OCR/LLM измеряются своими единицами, неизвестный тариф не считается нулём и hard budget не расширяется моделью.
- [ ] 4.5 Сохранять episodic/negative memory с TTL, scope и нормализованной целью; верификация: не повторяется та же бесполезная попытка, но разрешается другой язык/источник после конкретного negative result.
- [ ] 4.6 Реализовать exact/typed-graph/lexical/semantic retrieval с проверкой полномочий; верификация: похожая запись чужого tenant и revoked evidence не попадают в prompt/summary/vector response.
- [ ] 4.7 Добавить stop/pause/resume/cancel/partial причины и прогресс заявки; верификация: рестарт сохраняет счётчики/артефакты, UI показывает текущий шаг, last activity, дефицит и основание ожидания.
- [ ] 4.8 Зафиксировать workflow/prompt/model versions и описать recovery; верификация: replay старой истории не вызывает новый LLM/платный запрос без разрешённого действия, документация совпадает с наблюдаемым recovery.

## 5. Международные рынки

- [ ] 5.1 Добавить country registry/localized labels и RU default с multiselect; верификация: группы EU/GCC не подменяют ISO страны, UI language не меняет target countries.
- [ ] 5.2 Реализовать MarketStudy с явным запуском/бюджетом и sourced recommendations; верификация: кнопка показывает стоимость/исход, SERP count не выдаётся за размер рынка, рекомендации не меняют утверждённый выбор автоматически.
- [ ] 5.3 Реализовать country profiles, local synonyms и разделение registration/operating/served markets; верификация: developer в construction/software не смешивается без проверки контекста, совпавший TLD/IP не доказывает регистрацию.
- [ ] 5.4 Реализовать quotas/common-pool/min-max и ready support levels; верификация: дефицит DE не закрывается RU без новой явной версии выбора, неподдержанный рынок виден до расхода бюджета.
- [ ] 5.5 Проверить реальный discovery и registry lookup RU/GB с provider capabilities/стоимостью; верификация: опубликован per-country acceptance report, реальные ключи/доступ не заменены fixtures, доступные источники и ограничения описаны в runbook.

## 6. Собственный поиск

- [ ] 6.1 Добавить regional-search gateway с query/source/geography/cost receipts; верификация: различаются search_lang/ui_lang/SERP region и запрос через proxy без country параметра не выдаётся за региональную выдачу.
- [ ] 6.2 Испытать один external regional API и документировать покрытие/тарифы/лимиты RU/GB; верификация: воспроизводимый разрешённый trial, empty/limited/unavailable не маскируются успехом.
- [ ] 6.3 Подключить собственный SearXNG backend для разрешённых upstream engines; верификация: видны конкретный engine/лимиты, отказ upstream и fallback, public community instance не является production dependency.
- [ ] 6.4 Реализовать tenant-safe поиск в накопленном corpus через PostgreSQL exact/FTS/vector; верификация: локальный no-result не объявляет компанию несуществующей, deletion/ACL отражаются в выдаче и snippets.
- [ ] 6.5 Добавить source/frontier/recrawl manifest вертикального индекса и обновляемые проекции; верификация: контролируемый набор источников пополняется/переиндексируется, freshness/duplicates видны в UI.
- [ ] 6.6 По измеренному corpus выбрать необходимость OpenSearch либо сохранить PostgreSQL; верификация: решение опирается на latency/ranking/throughput и стоимость, documented load procedure воспроизводится, дополнительный cluster без доказанной нужды не вводится.

## 7. Собственное получение страниц и география

- [ ] 7.1 Сравнить Crawlee Python/Crawl4AI/Firecrawl на фиксированных версиях и одинаковом корпусе/лимитах; верификация: отчёт показывает correct contact links, locators, стоимость и добавочную пользу каждого движка, а не только число страниц.
- [ ] 7.2 Реализовать принятый acquisition adapter/service с HTTP/browser profiles и artifact receipt; верификация: HTML/DOM/Markdown/redirect/version capabilities подтверждены contract и real integration tests.
- [ ] 7.3 Реализовать согласование mission/frontier/runtime queue lifecycle; верификация: crash/resume и duplicate dispatch не порождают независимый бесконечный обход одной задачи.
- [ ] 7.4 Добавить source-wide throttling, retries, byte/page/time limits и cancellation; верификация: несколько workers соблюдают общий host budget, зависший browser получает наблюдаемый исход и освобождает ресурсы.
- [ ] 7.5 Реализовать geo/session profiles с requested/claimed/observed receipts; верификация: реальный разрешённый выход, wrong-country/error taxonomy, sticky flow и отсутствие тихого fallback при required country.
- [ ] 7.6 Проверить network/redirect/private-address и credentials isolation; верификация: prompt/URL/QR источника не открывает private network, tenant cookies/proxy keys не попадают в LLM/logs.
- [ ] 7.7 Описать deployment/ресурсы, licensing выбранных компонентов и versioned rollout/rollback; верификация: собственный cohort проходит real acceptance, старый Firecrawl demo остаётся рабочим до cutover.

## 8. Браузерные сценарии и document/OCR

- [ ] 8.1 Реализовать deterministic site recipes для JS contacts/reveal/pagination/public search; верификация: сохраняются действия/постусловия/locators, не выполняется коммуникационная отправка.
- [ ] 8.2 До autonomous browser mode оформить MODIFIED-дельту действующего запрета и activation policy; верификация: active spec/version однозначно разрешает ровно принятый режим, текущий demo не получает его скрыто.
- [ ] 8.3 Сравнить ограниченный Browser Use с прямыми Playwright recipes, Stagehand оставить альтернативой по нужному пробелу; верификация: trial с action/time/token caps, semantic side-effect checks и правильным unsupported outcome.
- [ ] 8.4 Добавить model capability profiles text/tool/vision и раздельный budget; верификация: несовместимая модель не получает screenshot под видом текста и не выдаётся за успешный vision extraction.
- [ ] 8.5 Реализовать DOM/JSON-LD/structured blocks и text-PDF extraction до OCR; верификация: contact/legal footer сохраняется отдельно от main-text очистки, многостолбцовый PDF не смешивает субъектов.
- [ ] 8.6 Испытать Docling/PaddleOCR/Tesseract на corpus, проверить release/model weights/licenses и способ композиции; верификация: per-language exact email/ID/phone results, coordinate fidelity, CPU/GPU/cost report и зафиксированное решение.
- [ ] 8.7 Добавить document/OCR adapter с page/bbox/crop transform/raw text/model version и limits; верификация: каждый контакт восстанавливается до оригинального artifact, большой/повреждённый файл не расходует неограниченные ресурсы.
- [ ] 8.8 Реализовать ambiguous-symbol/subject checks и review; верификация: rn/m, О/0, несколько карточек на скане не становятся тихо исправленными подтверждёнными контактами; OCR confidence отдельно от ownership.
- [ ] 8.9 Добавить UI evidence overlay и regression fixtures/runbook; верификация: оператор видит нужный crop/страницу и исходное чтение, upstream/parser/model update проходит corpus до включения.

## 9. Материалы и управляемый outreach

- [ ] 9.1 Реализовать versioned research-handoff с offer/recipient/evidence/language refs; верификация: телефон-only company не закрывает email-ready, unavailable recipient/material различаются.
- [ ] 9.2 Генерировать материал по разрешённым claims и сохранять связь персонализации с evidence; верификация: новость о расширении не становится выдуманной потребностью купить, неизвестное имя не подставляется.
- [ ] 9.3 Провести existing exact-version approval через владельцев material/recipient; верификация: изменение текста/адресата и stale decision не разрешают действие, campaign-aware action ID сохраняется.
- [ ] 9.4 Добавить pre-delivery freshness/suppression checks и unknown-send reconciliation; верификация: отписка после approval блокирует отправку, обрыв SMTP после возможного принятия не вызывает слепой дубль.
- [ ] 9.5 Реализовать sender profile и task подготовки чужой формы с реальными контактами; верификация: отсутствующее обязательное поле требует данных, отправка остаётся ручной, manual-confirmed отличается от system-verified.
- [ ] 9.6 Добавить видимый список материалов/адресатов/статусов и документацию канала; верификация: реальный разрешённый end-to-end даёт проверяемый материал, точное решение и наблюдаемый исход без подмены доставки capture-only сервисом.

## 10. Наши формы и входящая почта

- [ ] 10.1 Оформить явную ревизию прежнего исключения mail sync и развести inbound form relay/outbound form task contracts; верификация: действующие specs/catalog/model согласованы, PII relay не превращён в green archive.
- [ ] 10.2 Зарегистрировать mailbox/form bindings, inquiry/conversation roots и contracts владельцев; верификация: archlint/importlint, неподдельный tenant routing и red storage boundary.
- [ ] 10.3 Реализовать red client intake нашей формы с submission ID/attribution/policy; верификация: retry создаёт одно обращение, отсутствие компании не создаёт ложный domain-based entity, недоступный red sink виден посетителю.
- [ ] 10.4 Подключить один реальный mailbox через provider webhook/API либо поддерживаемый IMAP adapter; верификация: реальное письмо устойчиво принято и отображается, credentials/permissions/subscription/политика scope описаны.
- [ ] 10.5 Реализовать transport dedup/cursor/ack/backfill; верификация: reconnect, duplicate webhook, UIDVALIDITY reset и missing/duplicate Message-ID не теряют и не дублируют message effects.
- [ ] 10.6 Реализовать MIME/charset/attachments parsing в red storage и безопасный просмотр; верификация: raw message lineage, sanitized HTML, limits, отсутствие автозагрузки внешнего контента и повторное использование document/OCR refs.
- [ ] 10.7 Проверить реальные receiving aliases/Reply-To и correlation token mapping; верификация: ответ приходит в наш mailbox, token opaque, неправильный binding не открывает чужую conversation, неподдержанное plus-addressing не заявлено рабочим.
- [ ] 10.8 Добавить correlation по scoped headers/token/provider thread и ambiguity review; верификация: одинаковые темы, forwards, shared domain, missing refs не сливают чужих участников и допускают исправление связи.
- [ ] 10.9 Реализовать создание/дополнение inbound lead и inquiry disposition; верификация: human business message даёт кандидат/обновление, reply не дублирует лид, spam/OOO/DSN остаются различимыми.
- [ ] 10.10 Реализовать intent/type classification с цитатами/версиями и operator correction; верификация: OOO не interest, unsubscribe при положительном тоне сохраняется, подпись с номером не объявляется registry-confirmed.
- [ ] 10.11 Связать qualified reply/отказ с engagement/outreach pause/stop; верификация: human reply ставит цепочку на паузу, отписка подавляет дальнейшее действие, green event не содержит MIME/body.
- [ ] 10.12 Добавить общий inbox и conversation timeline в contact/company UI; верификация: источник/направление/связь/классификация/последнее действие/ответственный видны в tenant scope, исправление correlation имеет audit.
- [ ] 10.13 Описать mailbox/form runbook и replay/recovery/retention; верификация: documented real acceptance выполняется с подключённым тестовым mailbox и клиентским form sink, а не только event endpoint или Mailpit.

## 11. Пакетная цель, feedback и эксплуатация

- [ ] 11.1 Выполнить цель 25 различимых единиц с квотами/проверкой контактов/материалами; верификация: terminal fulfilled либо partial с объяснённым shortfall, каждый счётчик раскрывается объектами, чужие pool matches не маскируются новыми находками.
- [ ] 11.2 Добавить целевые metrics/alerts для stuck jobs, no-progress, budget, source health и inbound lag; верификация: провал источника или ingest виден до ложного статуса «готово», нет PII/secrets в telemetry.
- [ ] 11.3 Реализовать отзыв evidence и удаление по purpose/retention во всех artifacts/crops/vector/summary/conversation projections; верификация: удалённое/запрещённое не возвращается через производный retrieval или cached prompt context.
- [ ] 11.4 Проверить feedback адреса/компании/кампании и operator исправления; верификация: bounce одного адреса не удаляет компанию, оценка принадлежит заявке, исправление identity не повторяет доставку.
- [ ] 11.5 Выполнить постепенный source/country/cohort cutover и rollback rehearsal; верификация: старые ссылки/заявки совместимы, переход не теряет входящие данные и не повторяет оплаченные/исходящие действия.

## 12. Итоговая сквозная приёмка

- [ ] 12.1 Пройти human request → selected markets → linked entity/contact → memory/resume → material → exact approval → разрешённая доставка → реальный ответ → conversation/lead update; верификация: evidence и trace IDs связывают весь путь, пользователь видит результат каждого этапа.
- [ ] 12.2 Пройти negative acceptance: footer traps, mixed identity, OCR ambiguity, wrong-country, injection, cross-tenant attempts, provider crash и unknown SMTP; верификация: блокирующие ошибки не обнаружены, ограничения/непокрытые рынки явно перечислены.
- [ ] 12.3 Выполнить `make check`, `go -C tools/archlint run .`, `openspec validate intelligent-lead-research --strict` и требуемые реальные integration suites; верификация: зелёные результаты, отсутствие product-path заглушек, отчёт об объёме corpus/измерениях и обновлённая implementation matrix.

## Workflow follow-up

- Архивировать реализованные требования только после соответствующей проверки; при поставке по отдельным apply-changes не закрывать этим чужие pending tasks.
- После архивации проверить согласованность main specs, каталога, доменной модели и фактической implementation matrix.
