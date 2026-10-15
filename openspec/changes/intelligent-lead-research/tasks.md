# Tasks

Это **план будущей реализации**, не отчёт об исполнении. Все задачи открыты. Приоритетный порядок S0–S6: discovery и краулинг первыми; R1–R4 — проверочные сценарии, карта S0–S6, владельцы и критерии — [документ 7](../../../docs/concepts/intelligent-lead-research/07-architecture-and-delivery-plan.md). Для каждого apply-среза перед отметкой выполненного обязательны проектные гейты из `AGENTS.md`; проверки в пунктах ниже дополняют их. Для параллельных продуктовых приоритетов допустим отдельный согласованный apply-change с сохранением контрактов/зависимостей.

**Полный объём обязателен:** [REQ-01…REQ-36 и USE-01…USE-14](../../../docs/intelligent-lead-platform-roadmap.md). Начальный прототип не заменяет остальные требования. Перенос на другой этап/apply-change сохраняет связь и открытое состояние до реальной приёмки.

Для всех **94 открытых задач** составлены [подробные планы](../../../docs/concepts/intelligent-lead-research/task-blueprints.json): owner, вход/выход, зависимости, точки изменения, шаги, отказы и приёмка. Аудит исходных 91 задачи/66 требований сохранён [отдельно](../../../docs/concepts/intelligent-lead-research/12-change-audit.md). **Денежный бюджет приложения только Firecrawl**; proxy-инструменты имеют технические ограничения без второго monetary gate.

## 1. Зависимости требований и владение

- [ ] 1.1 Зафиксировать выбранные человеком единицу результата и criteria version: company / company-with-contact / email-ready-company; company-with-contact — предложенный ассистентом пилот; верификация: объём N задаётся заявкой без общей числовой цели, зачёт определяется выбранной единицей и критериями, а недобор равен max(0, N − зачёт).
- [ ] 1.2 При вводе первой entity/evidence/mission дополнить уже зарегистрированные назначения capability агрегатами, VO, событиями, policies и relationships из design в `architecture/domain-catalog.yaml`, `docs/domain-model.md` и диаграммах; верификация: archlint/importlint и согласованная owner matrix без прямых чужих domain imports.
- [ ] 1.3 Оформить явное согласование existing `osint-enrichment` и pending `self-hosted-crawler` по effective source/robots policy; верификация: отсутствуют несовместимые требования/defaults на активируемом маршруте, legacy demo default не изменён скрыто.
- [ ] 1.4 Оформить ADR единого model gateway/extractor против pending LLMExtractionStrategy в Crawl4AI; верификация: одно извлечение имеет один action lineage, без скрытого дублирования LLM вызовов; proxy не требует app monetary budget.
- [ ] 1.5 Разделить технические понятия internet search, local index, acquisition, document/OCR и subject verification в versioned contracts; верификация: adapter manifest не обещает неподдержанные DOM/географию/search.

## 2. Независимый корпус и измерения

- [ ] 2.1 Подготовить разрешённый размеченный корпус identity/contact/evidence с footer-trap, articles, multi-entity pages, shared domains/phones и временными конфликтами; верификация: manifest фиксирует ожидаемые субъект/значение/связь и separate holdout без source-family leakage.
- [ ] 2.2 Подготовить acquisition/browser/OCR corpus 120 объектов как инженерный smoke и отдельный независимый holdout принятых стран (RU/GB предложены); верификация: размечены HTML/JS/recipe/text-PDF/scanned/image scopes, языки/locators; 120 страниц не объявлены доказательством precision ≥99%.
- [ ] 2.3 Реализовать расчёт field/link precision/recall, false merge, unique-result cost, latency/resource/manual-review metrics; верификация: отчёт воспроизводится по фиксированному manifest и различает upstream claims/наши измерения.
- [ ] 2.4 Документировать критерии cutover и критические ошибки; верификация: ноль наблюдаемых cross-tenant/wrong-subject случаев на acceptance, размер выборки/неопределённость и per-country показатели не скрыты общей средней.
- [ ] 2.5 Применить USE-01…USE-14, исходные 66 источников и 69 receipts аудита с ограничениями; поддерживать implementation-coverage.json и audit/task blueprints; верификация: применение/отказ от механизма обоснованы, pinned versions и real evidence связаны с задачами, рекомендации не выданы за пожелания пользователя.

## 3. Entity identity и доказательства

- [ ] 3.1 Добавить tenant-scoped research entity IDs, composite keys и scoped bridge старых CompanyID; верификация: два tenant с одним доменом не перезаписывают владельца, несколько доменов/юрлиц не схлопываются, прежние ссылки demo читаются.
- [ ] 3.2 Реализовать jurisdictional identifier types/normalization с ведущими нулями и областью реестра; верификация: RU ИНН/КПП, GB prefix/zeros, DE register/court, FR SIREN/SIRET и unknown ID проходят предметные cases.
- [ ] 3.3 Добавить evidence documents/revisions/artifact refs/locators и subject scopes; верификация: контакт можно восстановить до сохранённого фрагмента и версии источника, footer каталога не становится контактом каждой компании.
- [ ] 3.4 Добавить раздельные epistemic/freshness/access состояния assertion, validity/history/source-family и conflict policy; верификация: stale не стирает прежнее подтверждение, копии источника не повышают независимость, новый телефон не стирает наблюдение.
- [ ] 3.5 Реализовать candidate matching и reversible identity decision с revision checks; верификация: подтверждение/отмена связи восстанавливают projection и не переписывают историю отправленного действия.
- [ ] 3.6 Ввести страновую нормализацию contacts и независимые измерения identity/link/format/reachability/freshness/action eligibility; верификация: формат не доказывает принадлежность/достижимость, даты/ID не дают телефон, допустимая национальная длина не отклоняется demo-порогом, неполная legal identity не становится registry-confirmed.
- [ ] 3.7 Добавить persistence/contract tests и dossier evidence UI для этого среза; верификация: реальный seed принятых пилотных стран (RU/GB предложены ассистентом) даёт один объяснимо связанный контакт, правила и новая owner model описаны рядом с реализацией.

- [ ] 3.8 Реализовать объяснимые статусы поля/отношения: источник и его пригодность, извлечение, подтверждение с методом/версией/автором/датой, независимые identity/link/format/reachability/freshness; верификация: карточка объясняет «принадлежность подтверждена, достижимость не проверена», конфликт и ручная отметка не превращаются во всеобщее подтверждение.

## 4. ResearchMission, долговечность и память

- [ ] 4.1 Реализовать ResearchMission до company identity и коллекцию результатов вместо first-result artifact; верификация: миссия начинается из описания аудитории либо имени/номера без фиктивного домена и сохраняет несколько entity refs.
- [ ] 4.2 Добавить registered research-planner block и structured tool proposals; верификация: недопустимые tool/args/scope/country/budget отклоняются, prompt injection источника не меняет полномочия.
- [ ] 4.3 Реализовать intent/outbox → activity → receipt/result CAS вне DB transaction; Firecrawl-only reservation через идемпотентный ReserveForAction у integration-platform до dispatch; верификация: crash до/после эффекта, duplicate receipt, lease epoch и восстановление reservation различимы без cross-context transaction.
- [ ] 4.4 Реализовать единый Firecrawl credit ledger у integration-platform с account cap и mission/study grant, одним резервом/списанием на action; агент хранит grant_ref и проекцию. Остальные инструменты через существующий proxy без app monetary budget; верификация: nested top-up/retry не обнуляет FC grant, unknown FC charge согласуется, FC exhaustion не запрещает остальные допустимые пути, time/attempt/concurrency caps независимы.
- [ ] 4.5 Сохранять episodic/negative memory с TTL, scope и нормализованной целью; верификация: не повторяется та же бесполезная попытка, но разрешается другой язык/источник после конкретного negative result.
- [ ] 4.6 Реализовать scoped exact/typed-graph/FTS retrieval и измеренное semantic расширение; верификация: текущий array/full-scan vector adapter не объявлен готовым pgvector index, tenant/purpose/revision/revoke фильтры действуют до выдачи в prompt/summary.
- [ ] 4.7 Добавить отдельные execution state/phase/waiting reason/completion outcome и разрешённые stop/resume/cancel; верификация: рестарт сохраняет артефакты, heartbeat отличается от meaningful progress, resume не повышает бюджет, миссия сама не присваивает fulfilled всей заявке.
- [ ] 4.8 Зафиксировать workflow/prompt/model versions и описать recovery; верификация: replay старой истории не вызывает новый LLM/платный запрос без разрешённого действия, документация совпадает с наблюдаемым recovery.

- [ ] 4.9 Реализовать receipt-backed ResultMembership и canonical credit по mission/criteria/unit; верификация: persist failure, duplicate receipt, конкурентный последний слот, два домена одного субъекта и merge/revoke до завершения дают правильный зачёт/дефицит без потери evidence/cost.
- [ ] 4.10 Реализовать completion snapshot и ограниченное согласование in-flight действий; верификация: late receipt имеет происхождение без изменения терминального итога, неизвестный Firecrawl charge не считается нулём и резерв согласуется; proxy receipt не ожидает денежного settlement.
- [ ] 4.11 Реализовать policy приоритетов/нормализованных попыток и справедливую очередь субъектов; верификация: перестановка слов не обнуляет лимит, no-progress seed уступает другим с reason, разные языки/источники допускаются по обоснованному действию.
- [ ] 4.12 Реализовать revisioned progress snapshot и фильтры списка сохранённых кандидатов/credit; верификация: reconnect/duplicate/out-of-order events не меняют зачёт, 17 кандидатов и 6 принятых видны со своими причинами, ETA не выдумывается.

- [ ] 4.13 Реализовать устойчивые OpenNeed и сохранение неполной entity между run с tenant/purpose, criteria/attempt refs, причинами и условиями пересмотра; верификация: компания без email остаётся видимой после restart/completion, scoped negative result не становится глобальным отсутствием контакта и потребность не запускает платный поиск сама.
- [ ] 4.14 Реализовать версионируемый индекс зависимостей и идемпотентный адресный пересмотр по evidence/assertion/merge/split/revoke/expiry; верификация: поздний контакт обновляет прежнее досье без дубля, единственное отозванное основание снимает зависимое подтверждение/открывает пробел, другое достаточное основание учитывается, out-of-order событие не откатывает проекцию.
- [ ] 4.15 Принять межзапусковый сценарий T0–T4 с реальным хранилищем, версиями, UI и документацией; верификация: поздний источник с подтверждённым ID дополняет прежнюю компанию и закрывает подходящий пробел, чужой scope не виден, активная миссия действует в пределах, терминальный snapshot и история отправки неизменны, новый бюджет/заявка самовольно не создаются.

- [ ] 4.16 SearchBrief и SearchStrategyPlan от оффера/ICP до источников/поисковых семейств; верификация: Один и тот же offer с разными ICP даёт разные целевые сегменты; source keywords не превращаются в asserted факты клиента; даты/имена/регион не выдумываются.
- [ ] 4.17 Discovery portfolio и двойная frontier новых кандидатов/пробелов; верификация: Старт без seed компании; article/name/registry/listing даёт сохраняемый кандидат даже без email; застрявший источник уступает новым; merge не размножает yield.
- [ ] 4.18 Сравнить measured-yield policy с rule-based поиском и принять числа caps/exploration; верификация: Quality/unique target outcomes, Firecrawl credits и техническая latency/attempts по странам/сегментам, uncertainty; proxy cost optional. Bandit без преимущества не принимается.

## 5. Международные рынки

- [ ] 5.1 Добавить country registry/localized labels и RU default с multiselect; верификация: группы EU/GCC не подменяют ISO страны, UI language не меняет target countries.
- [ ] 5.2 Реализовать MarketStudy по кнопке как human study intent с technical caps и ссылкой на общий Firecrawl account/grant при его использовании; верификация: coverage/evidence и FC credits видны, остальные proxy инструменты без app financial budget, SERP count не выдан за размер рынка и выбор стран не меняется автоматически.
- [ ] 5.3 Реализовать country profiles, local synonyms и разделение registration/operating/served markets; верификация: developer в construction/software не смешивается без проверки контекста, совпавший TLD/IP не доказывает регистрацию.
- [ ] 5.4 Реализовать quotas/common-pool/min-max и ready support levels; верификация: дефицит DE не закрывается RU без новой явной версии выбора, неподдержанный рынок виден до расхода бюджета.
- [ ] 5.5 Проверить реальный discovery и registry lookup принятых пилотных стран (предложены RU/GB) с provider capabilities/стоимостью; верификация: опубликован per-country acceptance report, реальные ключи/доступ не заменены fixtures, доступные источники и ограничения описаны в runbook.

## 6. Собственный поиск

- [ ] 6.1 Добавить regional-search gateway с query/source/geography/cost receipts; верификация: различаются search_lang/ui_lang/SERP region и запрос через proxy без country параметра не выдаётся за региональную выдачу.
- [ ] 6.2 Испытать один external regional API и документировать покрытие/тарифы/лимиты принятых пилотных стран (предложены RU/GB); верификация: воспроизводимый разрешённый trial, empty/limited/unavailable не маскируются успехом.
- [ ] 6.3 Выбрать и подключить принятый backend собственного поискового gateway, исследовав self-hosted metasearch (кандидат SearXNG); верификация: выбор/замена обоснованы, источники/лимиты/ошибки видны, принятая поисковая функция реально работает и не подменяется public community instance.
- [ ] 6.4 Реализовать tenant-safe поиск в накопленном corpus через PostgreSQL exact/FTS/vector; верификация: локальный no-result не объявляет компанию несуществующей, deletion/ACL отражаются в выдаче и snippets.
- [ ] 6.5 Добавить source/frontier/recrawl manifest вертикального индекса и обновляемые проекции; верификация: контролируемый набор источников пополняется/переиндексируется, freshness/duplicates видны в UI.
- [ ] 6.6 По измеренному corpus выбрать необходимость OpenSearch либо сохранить PostgreSQL; верификация: решение опирается на latency/ranking/throughput и стоимость, documented load procedure воспроизводится, дополнительный cluster без доказанной нужды не вводится.

## 7. Собственное получение страниц и география

- [ ] 7.1 Сравнить Crawlee Python/Crawl4AI/Firecrawl на фиксированных версиях и одинаковом корпусе/лимитах; верификация: отчёт показывает correct contact links, locators, стоимость и добавочную пользу каждого движка, а не только число страниц.
- [ ] 7.2 Реализовать принятый acquisition adapter/service с HTTP/browser profiles и artifact receipt; верификация: HTML/DOM/Markdown/redirect/version capabilities подтверждены contract и real integration tests.
- [ ] 7.3 Реализовать authoritative technical frontier/lease ledger у integration-platform в клиентском размещении; Python runtime выполняет bounded task и возвращает discovered links; верификация: crash/resume/lease expiry/duplicate dispatch не порождают второй durable scheduler или обход без оставшихся caps.
- [ ] 7.4 Добавить source-wide throttling, retries, byte/page/time limits и cancellation; верификация: несколько workers соблюдают общий host concurrency/rate limit с учётом redirects и session rotations, зависший browser освобождает ресурсы; это технические ограничения, не денежный бюджет.
- [ ] 7.5 Реализовать geo/session profiles с requested/claimed/observed receipts; верификация: реальный разрешённый выход, wrong-country/error taxonomy, sticky flow и отсутствие тихого fallback при required country.
- [ ] 7.6 Проверить network enforcement для фактических соединений, redirects, subresources/service workers и credentials isolation; верификация: URL/QR страницы не открывает private network, route precheck не выдан за полную egress защиту, tenant cookies/proxy keys изолированы.
- [ ] 7.7 Описать deployment/ресурсы, licensing выбранных компонентов и versioned rollout/rollback; верификация: собственный cohort проходит real acceptance, старый Firecrawl demo остаётся рабочим до cutover.

## 8. Браузерные сценарии и document/OCR

- [ ] 8.1 Реализовать deterministic site recipes для JS contacts/reveal/pagination/public search; верификация: сохраняются действия/постусловия/locators, не выполняется коммуникационная отправка.
- [ ] 8.2 До autonomous browser mode оформить MODIFIED-дельту действующего запрета и activation policy; верификация: active spec/version однозначно разрешает ровно принятый режим, текущий demo не получает его скрыто.
- [ ] 8.3 Сравнить ограниченный Browser Use с прямыми Playwright recipes, Stagehand оставить альтернативой по нужному пробелу; верификация: trial с action/time/token caps, semantic side-effect checks и правильным unsupported outcome.
- [ ] 8.4 Добавить model capability profiles text/tool/vision и технические token/time/size caps; верификация: несовместимая модель не получает screenshot под видом текста, proxy tools не требуют финансового резерва приложения.
- [ ] 8.5 Реализовать DOM/JSON-LD/structured blocks и text-PDF extraction до OCR; верификация: contact/legal footer сохраняется отдельно от main-text очистки, многостолбцовый PDF не смешивает субъектов.
- [ ] 8.6 Испытать Docling/PaddleOCR/Tesseract на corpus, закрепить release/model weights/licenses и способ композиции; верификация: RapidOCR Paddle backend не выдан за native PaddleOCR pipeline, per-language exact fields/coordinates и resource report подтверждают выбор.
- [ ] 8.7 Добавить document/OCR adapter с page/bbox/crop transform/raw text/model version и limits; верификация: каждый контакт восстанавливается до оригинального artifact, большой/повреждённый файл не расходует неограниченные ресурсы.
- [ ] 8.8 Реализовать ambiguous-symbol/subject checks и review; верификация: rn/m, О/0, несколько карточек на скане не становятся тихо исправленными подтверждёнными контактами; OCR confidence отдельно от ownership.
- [ ] 8.9 Добавить UI evidence overlay и regression fixtures/runbook; верификация: оператор видит нужный crop/страницу и исходное чтение, upstream/parser/model update проходит corpus до включения.

## 9. Материалы и управляемый outreach

- [ ] 9.1 Реализовать versioned research-handoff с offer/recipient/evidence/language refs; верификация: телефон-only company не закрывает email-ready, unavailable recipient/material различаются.
- [ ] 9.2 Генерировать материал по разрешённым claims и сохранять связь персонализации с evidence; верификация: новость о расширении не становится выдуманной потребностью купить, неизвестное имя не подставляется.
- [ ] 9.3 Провести exact-version approval через владельцев material и immutable recipient snapshot/epoch; верификация: legacy resolver текущего первого email заменён в новом пути, смена текста/адресата требует нового решения, campaign-aware action ID сохранён.
- [ ] 9.4 Добавить pre-delivery freshness/suppression/pending-reply checks и unknown-send reconciliation; верификация: ответ/отписка блокирует не начавшийся follow-up, provider-accepted и unknown эффекты не объявляются отменёнными и не вызывают слепой дубль.
- [ ] 9.5 Реализовать sender profile и подготовку чужой формы с реальными контактами; верификация: обязательные отсутствующие данные не выдуманы, manual-confirmed отличается от system-verified, режим исполнения указан явно и не приписан пользователю из старого pending change.
- [ ] 9.6 Добавить видимый список материалов/адресатов/статусов и документацию канала; верификация: реальный разрешённый end-to-end даёт проверяемый материал, точное решение и наблюдаемый исход без подмены доставки capture-only сервисом.
- [ ] 9.7 Зафиксировать продуктовый режим исполнения чужих форм и согласовать прежний pending план с новой программой; верификация: подготовка/обратный канал сохранены, выбранный ручной либо автоматизированный эффект имеет явные требования/утверждение/evidence, автоматизация не запускается из исследовательского browser режима и отсутствие выбора не выдано за manual-only пожелание пользователя.

## 10. Наши формы и входящая почта

- [ ] 10.1 Оформить явную ревизию прежнего исключения mail sync и развести inbound form relay/outbound form task contracts; верификация: действующие specs/catalog/model согласованы, PII relay не превращён в green archive.
- [ ] 10.2 Зарегистрировать mailbox/form bindings, inquiry/conversation roots и contracts владельцев; верификация: archlint/importlint, неподдельный tenant routing и red storage boundary.
- [ ] 10.3 Реализовать red client intake нашей формы с submission ID/attribution/policy; верификация: retry создаёт одно обращение, отсутствие компании не создаёт ложный domain-based entity, недоступный red sink виден посетителю.
- [ ] 10.4 Подключить один реальный mailbox через provider webhook/API либо IMAP; верификация: сообщение durable принято с transport identity до MIME/OCR/LLM, реальные credentials/permissions/subscription и scope описаны, UI показывает письмо.
- [ ] 10.5 Реализовать transport dedup/cursor/ack/backfill; верификация: reconnect, duplicate webhook, UIDVALIDITY reset и missing/duplicate Message-ID не теряют и не дублируют message effects.
- [ ] 10.6 Реализовать MIME/charset/attachments parsing в red storage и безопасный просмотр; верификация: raw message lineage, sanitized HTML, limits, отсутствие автозагрузки внешнего контента и повторное использование document/OCR refs.
- [ ] 10.7 Проверить receiving aliases/Reply-To и opaque correlation token; сохранять точные wire Message-ID/In-Reply-To/References без lossy sanitization; верификация: реальный ответ коррелирует в нужном tenant, unsupported plus-addressing не заявлен рабочим.
- [ ] 10.8 Добавить correlation по scoped headers/token/provider thread и ambiguity review; верификация: conflicting reply refs/References, одинаковые темы, forwards, общий endpoint двух компаний и missing refs не сливают участников и допускают аудируемое исправление.
- [ ] 10.9 Реализовать создание/дополнение inbound lead и inquiry disposition; верификация: human business message даёт кандидат/обновление, reply не дублирует лид, spam/OOO/DSN остаются различимыми.
- [ ] 10.10 Реализовать intent/type classification с new-body quotes/версиями и operator correction; верификация: OOO не interest, новая просьба не писать не теряется в старой положительной цитате, подпись с номером не объявляется registry-confirmed.
- [ ] 10.11 Связать qualified reply/отказ с engagement/outreach pause/stop; верификация: human reply ставит цепочку на паузу, отписка подавляет дальнейшее действие, green event не содержит MIME/body.
- [ ] 10.12 Добавить общий inbox и conversation timeline в contact/company UI; верификация: источник/направление/связь/классификация/последнее действие/ответственный видны в tenant scope, исправление correlation имеет audit.
- [ ] 10.13 Описать mailbox/form runbook и replay/recovery/retention; верификация: documented real acceptance выполняется с подключённым тестовым mailbox и клиентским form sink, а не только event endpoint или Mailpit.

- [ ] 10.14 Подключить scope-safe conversation-signal от владельца переписки для pending-reply hold до глубокой квалификации, зарегистрировать relationship/contract при apply и переходы по outcome; верификация: classification timeout сохраняет видимую паузу, pending hold не выдаётся за qualified human reply, конфликт correlation требует review, human/OOO/bounce/отписка имеют разные исходы и origin/число уникальных лидов не подменяются числом сообщений.

## 11. Пакетная цель, feedback и эксплуатация

- [ ] 11.1 Выполнить заданный человеком объём различимых целевых единиц с квотами/контактами/материалами; верификация: mission completed с target-reached либо partial и объяснённым shortfall, terminal статус заявки устанавливает её владелец, каждый счётчик раскрывается объектами и источник найденных данных не маскируется.
- [ ] 11.2 Добавить metrics/alerts для stuck jobs, no-progress, Firecrawl credits, technical resource limits, source health и inbound lag; верификация: провал источника/ingest виден до ложного «готово», proxy price не становится gate и нет PII/secrets в telemetry.
- [ ] 11.3 Реализовать отзыв evidence и удаление по purpose/retention во всех artifacts/crops/vector/summary/conversation projections; верификация: удалённое/запрещённое не возвращается через производный retrieval или cached prompt context.
- [ ] 11.4 Проверить feedback адреса/компании/кампании и operator исправления; верификация: bounce одного адреса не удаляет компанию, оценка принадлежит заявке, исправление identity не повторяет доставку.
- [ ] 11.5 Выполнить постепенный source/country/cohort cutover и rollback rehearsal; верификация: старые ссылки/заявки совместимы, переход не теряет входящие данные и не повторяет оплаченные/исходящие действия.

## 12. Итоговая сквозная приёмка

- [ ] 12.1 Пройти human request → selected markets → linked entity/contact → memory/resume → material → exact approval → разрешённая доставка → реальный ответ → conversation/lead update; верификация: evidence и trace IDs связывают весь путь, пользователь видит результат каждого этапа.
- [ ] 12.2 Пройти negative acceptance: footer traps, mixed identity, OCR ambiguity, wrong-country, injection, cross-tenant attempts, provider crash и unknown SMTP; верификация: блокирующие ошибки не обнаружены, ограничения/непокрытые рынки явно перечислены.
- [ ] 12.3 Выполнить `make check`, `go -C tools/archlint run .`, `openspec validate intelligent-lead-research --strict` и требуемые реальные integration suites; верификация: зелёные результаты, отсутствие product-path заглушек, отчёт об объёме corpus/измерениях и обновлённая implementation matrix.

- [ ] 12.4 Пройти tenant/role UI acceptance: заявка → кандидаты/лиды → email → conversation, а также требуемые переходы консоли в своей области; верификация: корректный tenant видит свои записи и разрешённые экраны, нет ложных 404/login redirect, чужие данные не открываются, ограничения ролей объяснимы.
- [ ] 12.5 Принять всю программу по REQ-01…REQ-36 и USE-01…USE-14, включая международный MarketStudy, свой поиск/index, self-hosted acquisition, browser/OCR и inbound; верификация: по каждому required пункту есть связанная реализация/evidence и зелёные требуемые гейты, завершение R1–R4 или перенос задач не выдан за полноту программы.

## Workflow follow-up


- Архивировать реализованные требования только после соответствующей проверки; при поставке по отдельным apply-changes не закрывать этим чужие pending tasks.
- После архивации проверить согласованность main specs, каталога, доменной модели и фактической implementation matrix.
