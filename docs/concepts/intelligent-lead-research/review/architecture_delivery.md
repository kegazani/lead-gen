# Аудит архитектуры, измерений и порядка реализации

Baseline: `bbe49f6`. Это исследование исходного кода, спецификаций и первичных материалов; runtime интеграции и успешный краулинг здесь не заявлены. Репозиторий не изменён.

## Главный вывод

Нужно начать с продуктивного поиска и накопления доказанных знаний: сохранить найденную компанию даже без email, записать пробел, ограниченно исследовать следующий источник, привязать поздний контакт к прежней сущности и показать основания. Firecrawl обслуживает первый реальный срез, собственный acquisition сравнивается параллельно. Приём почты и доставка остаются обязательными последующими функциями; они не должны задерживать проверку поискового механизма.

Первый срез не требует graph DB, второго workflow engine или нового полнотекстового cluster. Он требует точных scoped ключей, evidence/attempt persistence, коротких транзакций вокруг внешних действий, честного счётчика и сравнительного корпуса.



## Выводы аудита

### AD-F01 (critical)
Нельзя сделать безопасный legacy bridge простым upsert текущего domain CompanyID: PK lead_profile глобальный по id; сохранение одинакового domain от другого tenant меняет tenant_id существующей строки.

Основания: `backend/contexts/lead-management/migrations/green/0001_init.sql:13`, `backend/contexts/lead-management/migrations/green/0012_lead_profile_tenant.sql:10`, `backend/contexts/lead-management/internal/adapters/persistence/postgres/lead_profile_repository.go:195`.

В новом хранилище composite (tenant_id, entity_id); bridge хранит tenant + legacy key + version/ambiguity. Проверить отдельным реальным PG тестом два tenant/один domain. Legacy запись через projection только после scoped миграции/совместимого adapter; потерянное старым upsert владение нельзя выдуманно восстановить.

### AD-F02 (major)
Существующий vector port ошибочно назван pgvector; это double precision[] и полный перебор без tenant/purpose/revision/delete. Для evidence памяти текущего контракта недостаточно.

Основания: `contracts/integration-platform/vector/vector.go:34`, `contracts/integration-platform/vector/vector.go:52`, `backend/contexts/integration-platform/internal/adapters/vector/postgres/store.go:100`, `backend/contexts/integration-platform/internal/adapters/vector/postgres/store.go:193`, `backend/contexts/integration-platform/migrations/0002_vector_index.sql:9`.

Исправить описание baseline. Новая scoped retrieval/index revision API, фильтрация до top-k и resolve повторно, revoke/delete tombstones. Сначала exact/FTS; pgvector отдельный измеряемый adapter, embeddings не блокируют начальный поиск.

### AD-F03 (major)
План правильно обнаружил UoW проблему, но registered research block без нового execution seam всё равно вызовет внешнюю сеть внутри открытой транзакции.

Основания: `backend/contexts/agent-orchestration/internal/app/command/run_step.go:62`, `backend/contexts/agent-orchestration/internal/app/command/run_step.go:94`, `backend/contexts/agent-orchestration/internal/app/command/run_step.go:122`.

Разделить PrepareResearchAction/ExecuteResearchAction/CommitResearchAction; workflow v2 или отдельный workflow name, registered block создаёт intent/child-mission ref и сразу выходит. Не переписывать все legacy blocks. Резерв credits применяется только к Firecrawl; другие intent через proxy финансового резерва не требуют.

### AD-F04 (major)
120 объектов не обосновывают сами по себе ≥99% confirmed-link precision; документы не равны независимым контактным решениям и template/source-family корреляция уменьшает effective sample.

Основания: `docs/concepts/intelligent-lead-research/07-architecture-and-delivery-plan.md:150`, `docs/concepts/intelligent-lead-research/07-architecture-and-delivery-plan.md:152`, `AD-S04`.

120 оставить стартовым acquisition benchmark, отдельно размечать решения и наращивать blind acceptance. Для 0 ошибок из n независимых решений 95% односторонняя нижняя граница=0.05**(1/n); n=120 даёт97.5345%, минимум299 для99%. Привести interval и strata, не объявлять абсолютную гарантию.

### AD-F05 (major)
Последовательность R3 mailbox перед R4 собственным acquisition не отражает последний приоритет пользователя: продуктивный поиск лидов и краулинг. Она также заставляет ждать полной коммуникации перед исследованиями извлечения.

Основания: `openspec/changes/intelligent-lead-research/design.md:135`, `docs/concepts/intelligent-lead-research/07-architecture-and-delivery-plan.md:129`.

Новый search-first порядок: persisted entity/evidence→bounded discovery+acquisition→late enrichment+configured-goal/progress→self-hosted browser/OCR/index, затем outreach/inbound; исследование собственного crawler параллельно с начальным Firecrawl. Полный scope сохранён, inbound не prerequisite поиска.

### AD-F06 (major)
Полный gate заранее не green: archlint на bbe49f6 возвращает одну ошибку canonical capability content-production отсутствует; make check также не запускает PostgreSQL suite и не доказывает реальные provider integration.

Основания: `architecture/domain-catalog.yaml:49`, `Makefile:44`, `Makefile:52`, `openspec/specs/content-production/spec.md (отсутствует)`.

Зафиксировать baseline blocker отдельным узким reconciliation change/spec owner решением; не подавлять archlint и не объявлять документацию интеграцией. Для каждого slice добавить реальные DB/provider suites и тестовые evidence отдельно.

### AD-F07 (major)
Таблица предлагает имена будущих контрактов, но не задаёт version compatibility, transport payload artifact ownership и outcomes; v1 crawl/search/documentparse не могут вернуть DOM coordinates, subject или применённую географию.

Основания: `contracts/integration-platform/crawl/crawl.go:67`, `contracts/integration-platform/search/search.go:25`, `contracts/integration-platform/documentparse/documentparse.go:46`, `contracts/integration-platform/adaptercapability/adaptercapability.go:28`.

Версионированные contracts с capability validation до стоимости, receipts на выходе, separate declared/observed. V1 wrapper manifest ограничивает Markdown/search без geo гарантий. Owner lead-management принимает observation и возвращает persisted entity revision; integration не подтверждает корпоративный факт.

### AD-F08 (minor)
Применить все66sources нельзя трактовать как установить каждую изученную библиотеку или считать цитирование proof. 66 — baseline corpus, новые источники/сравнения тоже нужны.

Основания: `docs/concepts/intelligent-lead-research/implementation-coverage.json`, `docs/concepts/intelligent-lead-research/research-sources.json`.

Для source ID запись adopted mechanism/compared/rejected/superseded + reason + version + implementation/eval refs; каждое USE требует результата решения/измерения. Не урезать функции, но не вводить все competing frameworks.

### AD-F09 (major)
План недостаточно различает end-to-end yield и isolated extraction: LLM может выиграть один fixture, но проиграть discovery, недобрать компании либо съесть бюджет. Нет фиксированного независимого baseline и выборки zero-result missions.

Основания: `docs/concepts/intelligent-lead-research/07-architecture-and-delivery-plan.md:142`, `docs/concepts/intelligent-lead-research/09-browser-ocr-and-tool-selection.md:182`.

Три evaluation layers: frozen artifacts extraction/linking, paired live acquisition одинаковый Firecrawl cap и технические лимиты, end-to-end blinded missions. Baseline current demo/Firecrawl при одинаковых лимитах; stop/partial включены в denominator и cost. Держать минимум deterministic plan и LLM bounded planner в ablation.

### AD-F10 (major)
Rollback route недостаточен для смешанных worker/schema/workflow versions и оплаченных действий: результат позднего старого adapter должен продолжать приниматься по originating action/profile.

Основания: `backend/platform/migrate/migrate.go:43`, `backend/platform/migrate/migrate.go:90`, `backend/contexts/agent-orchestration/internal/adapters/workflow/agent_run.go:14`, `AD-S01`, `AD-S06`.

Additive migrations, read-old/write-versioned, pinned route/profile per mission; drain старого queue, принимать receipts по их версии; rollback только новых dispatch. Не down-migrate evidence и не запускать повторно unknown effects. Replay tests старой истории и N/N-1 mixed workers.

### AD-F11 (major)
Фраза о переиспользовании existing object storage не подтверждена в обследованном коде; у agent-orchestration есть только green storage, а raw SearchBrief/query/LLM context может содержать контакты. Недостаточно назвать client scope без физического пути и владельца payload.

Основания: `backend/contexts/agent-orchestration/migrations/migrations_test.go:1`, `backend/contexts/lead-management/adapters.go:56`, `compose.demo.yaml:123`.

Добавить агентский red payload repository и client research-executor role по существующему green/red DSN pattern. Evidence bytes сохраняет lead-management red artifact adapter. Для первого bounded среза допустим PostgreSQL bytea с лимитом; S3/blob adapter вводить как новую явно реализуемую функцию. Green/Temporal/outbox хранят только разрешённые metadata/opaque refs.

### AD-F12 (major)
Универсальный финансовый ledger для search/proxy/browser/OCR/LLM противоречит последнему уточнению пользователя: бюджет приложения относится только к Firecrawl, остальные обращения идут через существующий proxy.

Основания: `Последнее уточнение пользователя, передано root; CL-16 будет зарегистрировано root`.

Оставить credit admission/reservation/reconciliation только для Firecrawl Search/Crawl/Extract. Для собственного acquisition/LLM/OCR через proxy сохранять технические time/bytes/concurrency/attempt limits и наблюдение usage; не вводить дополнительный финансовый approval или денежный reserve. Возможности proxy, включая geo/sticky sessions, ещё требуют проверки.

## Конкретное хранение и исполнение

**agent-orchestration**: `backend/contexts/agent-orchestration/migrations/red/<next>_research_payload.sql` — новое предлагаемое размещение.
Схема: `research_payload(tenant_id,payload_id,revision,kind,purpose_id,body_hash,body_json,created_at,expires_at); PK(tenant_id,payload_id,revision)`.
Содержимое: сырой SearchBrief, текст QueryFamily и поисковых запросов, разрешённый контекст и prompts, сырые предложения модели до deterministic validation.

**lead-management**: `backend/contexts/lead-management/migrations/red/<next>_research_evidence.sql` — новое предлагаемое размещение.
Схема: `artifact(tenant_id,artifact_id,revision,hash,mime,bytes_or_blob_ref,byte_length,retention); document/assertion/locator tables с scoped keys`.
Ограниченные по размеру bytes можно хранить bytea в клиентской БД для первого среза; крупные PDF/image требуют явно реализованного blob adapter, не воображаемого existing storage.

Новая роль research-executor в agent-orchestration, размещённая в клиентской зоне с red binding. Activity input только refs. Executor разрешает refs, собирает context и вызывает integration transport в той же зоне. Общая platform DB и Temporal history не получают raw query/snippets/prompts.

Integration-platform владеет одним authoritative durable frontier. Red payload хранит URL/query; control row keyed(scope,action_id,target_fingerprint,profile_revision) хранит state, lease_epoch, expires_at, attempt и provider_job_ref. Agents выдаёт bounded action grant, не вторую очередь URL. Physical fetch slots — технические лимиты/concurrency, не денежный reserve. Credit reservation применяется только к Firecrawl invocation.

Один task/recipe на lease. Внутренние retries укладываются в grant и включены в receipt. Самостоятельный durable scheduler/recrawl выключен. Если native runtime queue необходима, это один bounded child job: discoveries возвращаются в authoritative frontier, а не порождают параллельную mission.

После lease expiry dispatcher сначала выясняет существующий provider job. Повтор результата дедуплицируется по task/attempt/receipt. Fencing epoch не разрешает устаревшее изменение control state, но поздние bytes/cost receipts сохраняются для reconciliation. Unknown effect не запускается на другом engine слепо. Для остальных tools через proxy учёт usage не создаёт нового app financial gate.

В green/Temporal/outbox передаются только approved scope/countries/target/budget/deadline, типизированные несекретные policy и state metadata, opaque refs+hashes+versions, логические action/membership/lease IDs.

## Минимальный путь и оценка

Первый исполнимый путь: человек задаёт цель → scoped memory lookup → bounded intent → реальный search/acquisition → durable evidence/entity/OpenNeed → адресный следующий поиск → доказанный contact link → membership → UI. Контакт, найденный позднее, обновляет прежнюю компанию. После completion её текущие знания могут меняться, но исторический итог заявки остаётся неизменным.

Долгий вызов LLM/browser/Firecrawl выполняется вне SQL транзакции. Короткий prepare фиксирует intent и резерв credits только для Firecrawl; короткий commit фиксирует receipt и revisions. Между owners используются published contracts и inbox/outbox, а не общая распределённая транзакция. Stable action ID предотвращает двойную запись, но не гарантирует отсутствие повторного provider эффекта без idempotency/lookup.

Три уровня оценки: frozen artifact extraction/linking; paired live acquisition при одинаковом Firecrawl cap и технических лимитах; end-to-end missions с учётом неудач и недобора. Сравнивать current baseline, новый deterministic planner и bounded LLM. Для выбора crawler downstream extraction общий; native capability оценивается отдельно.

120 объектов — начальный инструментальный corpus. При 120 независимых правильных link decisions и нуле ошибок точная односторонняя 95% нижняя граница precision составляет 97.5345%. Для нижней границы 99% нужен минимум 299 независимых all-correct решений. Документы одного шаблона/источника не независимы. Измерять отдельные страны/языки и показывать интервалы.

Очередность: definitions/corpus/contracts → persistent entity/evidence/OpenNeed → bounded search/actions/late enrichment/counts/UI → собственный acquisition/browser/OCR/index → международное расширение/MarketStudy и обязательные communications/inbound. Исследование собственного движка идёт параллельно первым Firecrawl срезам. Нынешний приоритет не отменяет полный scope.

## Бюджет после последнего уточнения пользователя

Общий бюджет приложения только Firecrawl; для остального пользователь указал существующий proxy.
Credit cap/admission/reservation/reconciliation по Search/Crawl/Extract и реальным receipts; неизвестный outcome credit не обнуляется.
Имеющийся proxy и технические concurrency/bytes/time/attempt limits. Usage metrics разрешены; отдельного денежного reserve/gate/approval не добавлять.
Наличие proxy не доказывает региональный поиск, страну выхода, sticky sessions, browser/OCR transport capabilities.
Intent/outbox/lease/receipt/dedup остаются для долговечности; не являются финансовым ledger.

## Blueprints всех 13 назначенных задач

### 1.2
Владелец: lead-management / agent-orchestration / integration-platform; catalogue владельцы совместно. Зависимости: 1.1.

Вход: Согласованные сущности первого среза, их scope и владельцы.
Выход: Согласованная owner matrix, зарегистрированные roots/VO/events/contracts и места хранения.

1. Регистрировать по мере ввода: lead-management владеет entity/assertion/evidence/identity decision/OpenNeed/dependencies; agent-orchestration — mission/action/grant_refs/projection/membership; integration-platform — invocation/frontier lease и authoritative Firecrawl account/grant ledger. Не запирать весь tenant graph одним агрегатом.
2. Добавить агентский red payload repository для SearchBrief, QueryFamily, raw query и prompt context. Green хранит scope, страны, budgets, версии policy и opaque refs. В lead-management red хранить evidence/содержимое источников. Добавить client research-executor role с DSN binding по образцу существующего lead green/red wiring.
3. Зарегистрировать команды и события у владельцев. Межконтекстные events содержат разрешённые refs/versions; технический receipt не подтверждает компанию. SQL транзакции ограничиваются одной owner database, межконтекстное согласование через inbox/outbox.
4. Обновить каталог, domain-model и диаграммы вместе с первым кодом; published Go contracts зависят от стандартной библиотеки. Чужие internal/domain пакеты не импортировать.
5. До итогового green gate устранить узкий baseline mismatch content-production через владельца канонической спецификации. Это не повод закрывать весь незавершённый content change.

Отказы: Импорт чужого internal/domain; Два владельца корпоративного факта; Raw query или email попал в общую green БД/Temporal history; Capability заявлена без рабочего контракта.
Приёмка: Owner matrix и storage zone согласованы для каждой таблицы/события; archlint/importlint без новых ошибок; baseline ошибка не выдана за зелёный gate.

### 1.5
Владелец: integration-platform technical contracts; lead-management subject/dossier contracts. Зависимости: 1.2.

Вход: CriteriaSnapshot, tenant/purpose, action ID, required capabilities, технические пределы; Firecrawl credit cap только для Firecrawl.
Выход: Версионированные DTO, сериализация, capability manifests и contract tests.

1. SearchRequest включает scoped payload ref на query, query language, target market, provider region, cursor и max results. SearchHit — URL/snippet/rank в клиентском payload; технический receipt содержит output ref и фактически применённый profile.
2. AcquireCommand содержит action/task ID, profile revision, input ref, required output kinds, session ref и лимиты. Outcome возвращает artifacts, redirect chain, hash, requested/claimed/observed geography, provider job ID, cursor, время и cost units; чувствительные поля остаются в red payload.
3. DocumentObservation содержит immutable artifact ref, page/bbox/text offset/crop transform, engine/model revision и исходное извлечение. Принадлежность contact/entity подтверждает отдельная команда lead-management, которая возвращает сохранённую entity/assertion revision.
4. ResearchIndex v2: scope/purpose, object+revision, upsert/delete/tombstone, exact/lexical/optional semantic query. Tenant фильтруется до top-k, ref resolve повторно проверяет полномочия. Существующий v1 vector не подходит как общая память.
5. HTTP и in-process adapters проверяются одинаковыми контрактными cases. V1 Firecrawl wrapper объявляет Markdown-only и неизвестную географию; unsupported required capability отклоняется до платного вызова. Не путать declared capabilities с observed receipt.
6. Python runtime получает job ref + lease epoch + ограниченный input и возвращает receipt; raw credentials не появляются в payload. Единственный долговечный frontier управляет retries, runtime не запускает отдельный бесконечный scheduler.
7. Firecrawl имеет credit admission/reservation. Все остальные tools используют существующий proxy без нового денежного gate; проверяются только технические quotas/capabilities и scopes.

Отказы: Неподдерживаемый schema major; Выдуманные DOM/OCR locators; Ошибка провайдера замаскирована под отсутствие результатов; Повтор receipt с другим hash; Чужой scope у artifact ref.
Приёмка: Одинаковое поведение HTTP/in-process contracts; Manifest совпадает с реально возвращаемыми видами artifacts; Receipt не подменяет предметное подтверждение.

### 2.1
Владелец: evaluation владелец программы + lead-management quality. Зависимости: 1.1.

Вход: Разрешённые снимки источников и исследовательские задачи с размеченной истиной.
Выход: Версионированный manifest, subject/contact labels, отдельный скрытый holdout.

1. Разметить subject span, identifier, contact string, belongs-to relation, temporal validity и ожидаемый статус. Различать отсутствие свидетельства, отрицательный результат и неизвестность.
2. Делить данные группами по организации, source family и шаблону сайта. Копии и варианты одной страницы не попадают по разные стороны; development fixtures отделены от скрытого holdout.
3. Спорные связи размечают независимо два человека; сохраняются disagreement и adjudication. Сильный номер юрлица не доказывает, что соседний footer email принадлежит этому юрлицу.
4. Включить статьи, каталоги с чужим footer, shared phones/domains, несколько юрлиц на сайте, leading zeros, отсутствие email, поздний контакт, отзыв/истечение evidence. Критерии заявки размечаются отдельно от существования компании.

Отказы: Утечка source family между split; Email без установленного владельца; Отсутствующая метка объявлена ложным фактом; Неполная компания выброшена из корпуса.
Приёмка: Manifest воспроизводим и права использования источников зафиксированы; Для каждого ожидаемого link есть subject evidence; T0–T4 включает неизменность исторического completion snapshot.

### 2.2
Владелец: integration-platform acquisition evaluation. Зависимости: 1.1.

Вход: 120 разрешённых уникальных объектов с modality и ожидаемыми locators/subjects.
Выход: Стартовый corpus для выбора acquisition/document инструментов.

1. Предложенная непересекающаяся primary-modality раскладка: 30 static HTML, 25 JS/render, 15 pagination/public-search recipes, 20 text PDF, 20 scanned PDF, 10 images. Footer traps, языки и временные конфликты — дополнительные пересекающиеся tags.
2. Страны выбираются для реального пилота. Пример 50 RU / 50 GB / 20 других — предложение, не выбор пользователя. Язык и регистрационная страна размечаются отдельно.
3. Отделить frozen extraction benchmark от live acquisition. Для выбора crawler downstream extraction общий; отдельная оценка native locator capabilities не смешивается с contact-link accuracy.
4. Зафиксировать hashes, время получения, изменения страниц, warm/cold cache, hardware/concurrency, бюджеты. Разработка recipes ведётся на другом наборе.

Отказы: Страница изменилась между сравнениями; BBox потерял связь с исходным изображением; Кэш или разные лимиты создают ложного победителя; Unknown price записана нулём.
Приёмка: Ровно 120 primary objects с expected evidence и scopes; Одинаковые лимиты и честные различия профилей; 120 документов не объявлены доказательством precision ≥99%.

### 2.3
Владелец: evaluation tooling / analytics projections. Зависимости: 2.1, 2.2, 1.5.

Вход: Corpus/qrels, persisted entity/action versions, raw cost receipts и результаты сравнений.
Выход: Воспроизводимый JSON и читаемый отчёт с раздельными quality/cost/latency показателями.

1. Считать field exact match (raw/normalized), precision/recall contact-to-entity links, false merge/split, abstention/review rate. OCR text average не заменяет точность email/ID/phone.
2. Для retrieval считать Recall@k/NDCG@k по qrels. Сохранять весь список intended queries, включая пустые выдачи/отказы; BEIR-подобный evaluator не должен незаметно выкинуть отсутствующие results.
3. Для миссий считать unique accepted entities/contacts, target reached/partial shortfall, time to first useful result, end-to-end p50/p95, CPU/RAM/browser seconds/bytes/tokens и ручные минуты. Не считать дубли как прирост.
4. Firecrawl отчёт отдельно хранит credit cap/reserved/used/unknown и связь с provider receipts. Для прочих инструментов через существующий proxy измерять usage/latency/resources и, если доступна, внешнюю наблюдаемую стоимость; не создавать app money reservation или дополнительный финансовый gate. Нулевой результат сохраняет технический расход в отчёте.
5. Для независимых решений показать exact binomial intervals; для clustered/paired сравнения использовать sampling/cluster bootstrap по entity/source family. Версии, seeds и hashes закреплены, strata не скрыты общим средним.

Отказы: Отказы исключены из знаменателя; Повторы увеличили yield; False merge скрыл неверный контакт; Unknown spend записан нулём; Слабая страна скрыта общей средней.
Приёмка: Повтор отчёта из сохранённых receipts без новых платных вызовов; Все знаменатели объяснены; Current demo, deterministic и LLM варианты сравниваются на одинаковых условиях.

### 2.4
Владелец: product+quality gate owner. Зависимости: 2.3.

Вход: Стратифицированные baseline измерения и интервалы неопределённости.
Выход: Версионированные критерии cutover по источникам/странам/когортам.

1. Наблюдаемый wrong-subject confirmed link, wrong recipient или cross-scope exposure блокирует соответствующий cohort. Ноль наблюдаемых ошибок ограничен проверенной выборкой.
2. Разделить функциональный пилот и статистическую приёмку. При нуле ошибок минимум 299 независимых contact decisions нужен для односторонней 95% нижней границы ≥99%; для связанных шаблонов/семейств требуется иной sampling plan.
3. До финального holdout установить Firecrawl credit cap и технические latency/resource/attempt targets по trial. Для прочих вызовов используется имеющийся proxy; дополнительный финансовый approval не требуется.
4. Принимать поддержанные профили с явными ограничениями; review сохраняет полезность неполных данных. Полная программа остаётся открытой до выполнения всех required функций.

Отказы: Порог выбран задним числом; Десять успешных примеров выданы за мировое покрытие; Тихая замена неподдержанной страны.
Приёмка: Сохранены policy version, sample sizes, failed strata и область применимости; Критические ошибки не скрыты средним результатом.

### 2.5
Владелец: program traceability owner. Зависимости: 1.2, 1.5.

Вход: CL/D provenance, baseline 66 источников, новые исследования и доказательства apply.
Выход: Связи REQ → task → code/contracts/migrations → tests/live evidence, USE → принятое решение.

1. Сохранять различие пользовательского требования, вывода из него и инженерного предложения. Последний приоритет меняет порядок, но не выбирает библиотеку автоматически.
2. Для каждого источника указать: применённый механизм, сравнённая альтернатива, отклонение или замена; причина, версия и evidence. Установка всех конкурирующих frameworks не требуется.
3. Каждая обязательная функция связывается с реальной реализацией и критериями. Недоступный provider оставляет integration task открытой; документы закрывают только исследовательскую работу.
4. Связать immutable commit/report hashes; обновлять при новых источниках. Число 66 — стартовый реестр, а не запрет дальнейшего исследования.
5. CL-16 отменяет прежнюю гипотезу универсального бюджета: Firecrawl-only cap; остальные tools через существующий proxy. Это изменение scope должно быть видно в requirements/decisions.

Отказы: README назван работающей интеграцией; Task закрыта без требуемого провайдера; Новая информация не включена из-за старого количества sources; Предложение названо решением пользователя.
Приёмка: Все исходные source IDs учтены, новые добавлены; Все 36 REQ имеют implementation evidence либо явные оставшиеся шаги.

### 11.5
Владелец: agent-orchestration + integration-platform release + lead-management migrations. Зависимости: 2.4, 3.7, 4.3, 4.8, 4.15, 7.2, 7.3, 7.4, 7.5.

Вход: Принятый source cohort, закреплённые images/contracts/models, scoped mapping и action receipts.
Выход: Canary route и доказанный откат без потери результатов/повторного эффекта.

1. Применить additive migrations без переписывания legacy IDs. Scoped bridge требует теста двух tenant с одним domain; старые записи без доказанного tenant не присваиваются автоматически.
2. Закрепить route/profile/schema/workflow revision у mission/action. Shadow выполнять по сохранённым artifacts. Два live Firecrawl вызова должны укладываться в общий credit cap; прочие сравнения через существующий proxy имеют технические лимиты без нового денежного reserve.
3. Переключать по tenant/source/country. Старые workers обслуживают свою history до drain; новый workflow name или version marker позволяет replay. Поздние receipts принимаются по исходной версии.
4. Rollback меняет новые dispatch. In-flight unknown actions reconciled; evidence/новые компании/inbound/исторические completion snapshots сохраняются. Не выполнять destructive down-migration.
5. Использовать выбранный deploy path: demo через явный compose.demo.yaml, production через существующий release manifest. Новый client executor получает red binding, а shared controller — только refs.

Отказы: Старый worker читает несовместимую схему; После route flip повторно оплачен тот же unknown action; Поздний receipt потерян; Red payload попал в green storage; Rollback удалил новые знания.
Приёмка: Реальные PG/Temporal crash и mixed-version проверки; Старые ссылки доступны, две компании одинакового domain в разных tenant независимы; Нет тихих потерь/re-send; пределы восстановления отражены в runbook.

### 12.1
Владелец: cross-context acceptance; request-fulfillment final request outcome. Зависимости: 11.1, 4.15, 5.5, 9.6, 10.12.

Вход: Человеческая заявка со странами и целью, Firecrawl credit cap, существующий proxy для прочих инструментов и разрешённый тестовый адресат.
Выход: Воспроизводимая сквозная цепочка с evidence/trace IDs.

1. Первый поисковый checkpoint: request → bounded search → сохранённая компания без контакта → OpenNeed → поздний источник → принадлежность контакта → membership → видимый UI.
2. Для окончательной программы добавить material version → exact approval → фактическая доставка → реальный ответ → conversation/lead update. Промежуточный checkpoint не закрывает всю задачу.
3. Использовать выбранный реальный seed через Firecrawl/search, затем принятый self-hosted profile. Отдельно пройти partial и unavailable исходы.
4. Сохранить action/receipt/evidence/version/trace refs и состояние после restart; публичный отчёт не включает ключи или сырые контактные данные.

Отказы: Получено, но не сохранено; Компания исчезла из-за отсутствия email; Счётчики не объясняются списками; Ответ связан с чужой компанией.
Приёмка: Каждый счётчик раскрывается сохранёнными объектами; Request terminal status устанавливает его владелец; Для заявленной communication приёмки действительно получен ответ; draft не назван доставкой.

### 12.2
Владелец: quality and respective owner teams. Зависимости: 2.4, 3.7, 4.15, 7.6, 8.9, 9.4, 10.13.

Вход: Закреплённый negative corpus и контролируемые отказы в тестовом окружении.
Выход: Таблица ожидаемого/реального исхода каждого отказа с traces.

1. Проверить search failures: footer/article/multi-entity/numeric noise, wrong country, чужой scope, private URL, unsupported OCR, stale/conflicted evidence и отсутствие email.
2. Внести persist failure, worker crash до/после эффекта, duplicate receipt, lease expiry, out-of-order revision, concurrent last slot и unknown cost.
3. В более позднем communication срезе проверить unknown SMTP, late inbound, classification timeout и correlation conflict без неразрешённой отправки.
4. После каждого отказа проверить entity/OpenNeed/membership/attempt ledger, записи двух tenant, API/UI projection и неизменность terminal snapshot.

Отказы: Проверен только happy path; Ошибка стала пустым успешным результатом; Fixture выдан за провайдера; Green controller сохранил raw prompt.
Приёмка: Ноль наблюдаемых блокирующих ошибок в выпускаемой когорте; Размер выборки, неподдержанные случаи и baseline blockers перечислены; Неисправленные дефекты блокируют соответствующую область выпуска.

### 12.3
Владелец: release acceptance owner. Зависимости: 12.1, 12.2, 12.4, 11.5, 6.6.

Вход: Точная candidate revision, pinned зависимости, тестовая БД и требуемые реальные integrations.
Выход: Сохранённые команды/коды выхода, coverage и implementation matrix.

1. Устранить canonical content-production baseline mismatch через правильного владельца. Не подавлять archlint или подменять отсутствующую спецификацию заглушкой.
2. Выполнить make check, archlint и strict validate change. Отдельно выполнить реальные PostgreSQL suites и требуемые provider integrations: make check сам не поднимает БД.
3. Проверить фактическую runtime конфигурацию и отсутствие fake/noop на поставляемом пути. Неподдержанная функция остаётся явно недоступной и открытой.
4. Связать corpus/real acceptance/rollback отчёты с точным commit. Skipped checks и недоступные окружения отражать явно, не объявлять всё зелёным.

Отказы: DB тесты пропущены незаметно; Runtime flag выбрал заглушку; Provider test skipped выдан за pass; Отчёт относится к старому commit.
Приёмка: Все требуемые gates зелёные на точной candidate revision; Реальная интеграция подтверждена отдельно от unit/fixture теста.

### 12.4
Владелец: request-fulfillment/lead-management frontend + platform-governance auth. Зависимости: 3.8, 4.12, 5.4, 9.6, 10.12.

Вход: Два tenant с одинаковым company domain, разные роли и все состояния результатов.
Выход: Проверенная UI/route/role матрица и объяснимое отображение данных.

1. Пройти request → progress → candidates/OpenNeeds/credited → dossier → draft → conversation с сохранением actor/tenant context.
2. Сохранение domain в tenant B не меняет tenant A. Direct IDs и legacy URLs не открывают чужие записи; старый неизвестный scope не присваивается автоматически.
3. Проверить разрешённые переходы без ложного 404/login redirect; role denial объясним, настоящий expired session требует входа. После reconnect цифры соответствуют одной progress revision.
4. Показать компанию без email, пробел и причину незачёта. У каждого поля/отношения видны source/method/date/conflict и отдельная неизвестная reachability.

Отказы: Tenant потерян в ссылке; Запись domain перенесена другому tenant; Разрешённый route вернул 404; Счётчики и списки разных revisions.
Приёмка: Browser/API/DB evidence для двух tenant и разрешённых ролей; Проверенность принадлежности не выдаётся за проверенную доставляемость.

### 12.5
Владелец: program/product acceptance owner. Зависимости: 2.5, 12.3.

Вход: Результаты всех REQ/USE, task evidence и открытые ограничения.
Выход: Версионированная приёмка всей программы или точный остаток работ.

1. Пройти все 36 функций: приоритет поиска/краулинга не отменяет международный MarketStudy, собственный index, browser/OCR, forms/inbound/outreach.
2. Каждый USE подтверждается применённым механизмом либо обоснованным выбором; все competing products одновременно устанавливать не требуется.
3. Для каждого результата проверить code/contract/migration/UI/test/live evidence на согласованной версии и связь с пользовательскими уточнениями.
4. Архивировать только реально принятые требования с правильным порядком зависимых changes. R1 или новый поисковый срез не считать завершением всей программы.

Отказы: Прототип назван всей программой; Отложенная required функция исчезла; Страна объявлена поддержанной без источников; Цитата подменила реализацию.
Приёмка: Все обязательные функции имеют реальные evidence и зелёные gates; Незавершённая внешняя зависимость сохраняет соответствующую задачу открытой.

## Поэлементная проверка design/proposal

- `openspec/changes/intelligent-lead-research/design.md#1. ResearchMission до известной компании` — **refine**. Миссия до известной компании нужна. Registered block должен создать intent или ссылку на дочернюю миссию и выйти из legacy UoW; длительный вызов выполняется отдельно. Задачи: 1.2, 1.5.
- `openspec/changes/intelligent-lead-research/design.md#2. Entity identity независимо от домена` — **refine**. Непрозрачная scoped identity верна. Legacy bridge обязан учитывать глобальную коллизию domain PK и сохранять неоднозначность. Задачи: 1.2, 11.5, 12.4.
- `openspec/changes/intelligent-lead-research/design.md#3. Evidence и identity decision — отдельные версии` — **retain**. Immutable evidence и обратимое identity decision сохраняются. При apply нужны конкретный owner transaction и CAS участников. Задачи: 1.2, 2.1.
- `openspec/changes/intelligent-lead-research/design.md#4. Scope и память` — **correct**. PostgreSQL подходит как начальная база. Существующий vector API не имеет нужного scope и не использует pgvector; новый scoped retrieval обязателен. Задачи: 1.5, 2.3.
- `openspec/changes/intelligent-lead-research/design.md#5. Международность в ядре` — **refine**. Страна компании, язык, регистрация, SERP и IP различаются. Supported status проверяется до расходов, пилотные страны остаются предложением. Задачи: 1.5, 2.4, 12.1.
- `openspec/changes/intelligent-lead-research/design.md#6. Собственный acquisition за capability contract` — **experiment**. Предпочтение Crawlee — гипотеза. Требуется сравнение по одинаковым лимитам/strata; дополнительный runtime оправдывает измеримый прирост. Задачи: 2.2, 2.3, 11.5.
- `openspec/changes/intelligent-lead-research/design.md#7. Три вида поиска` — **refine**. Разделение discovery, metasearch и собственного index сохраняется. Свой gateway вводится отдельно от корпуса; SearXNG остаётся кандидатом. Задачи: 1.5, 2.3.
- `openspec/changes/intelligent-lead-research/design.md#8. Browser и OCR — ограниченные инструменты` — **experiment**. Порядок DOM → render/recipe → document/OCR проверяется по добавочным полезным контактам и цене. Locator fidelity измеряется отдельно. Задачи: 2.2, 2.3, 12.2.
- `openspec/changes/intelligent-lead-research/design.md#9. Durable effects вне DB transaction` — **refine**. Intent/activity/result — правильный механизм. Для него требуется новый execution seam, workflow version и обработка unknown receipts. Reservation относится только к Firecrawl credits; для прочих эффектов через proxy сохраняются intent/lease/receipt без денежного gate. Задачи: 1.2, 11.5, 12.2.
- `openspec/changes/intelligent-lead-research/design.md#10. Исследование передаётся в существующий outreach` — **retain**. Existing exact approval и владельцы материалов сохраняются. Это следующий этап после поиска, а не prerequisite сохранения компании. Задачи: 12.1, 12.3.
- `openspec/changes/intelligent-lead-research/design.md#11. Входящие обращения и переписка` — **retain**. Реальный inbound и корректная корреляция остаются обязательными. Они не блокируют первый поисковый срез; собственный mail server не нужен заранее. Задачи: 12.1, 12.5.
- `openspec/changes/intelligent-lead-research/design.md#12. Формы и контакты отправителя` — **pending-decision**. Формы и реальные обратные контакты обязательны. Режим ручной/автоматической отправки остаётся открытым до явной activation spec. Задачи: 12.1, 12.5.
- `openspec/changes/intelligent-lead-research/design.md#13. Каталог и выпуск по этапам` — **refine**. Владельцы capability зарегистрированы как план. Roots/contracts/policies добавляются с apply; очередность меняется под последний приоритет поиска. Задачи: 1.2, 11.5.
- `openspec/changes/intelligent-lead-research/design.md#14. Зачёт по версии цели и сохранённому membership` — **retain**. Saved membership и CriteriaSnapshot сохраняются. Company/contact/email-ready — разные единицы; зачёт происходит после durable принятия. Задачи: 2.3, 12.1, 12.4.
- `openspec/changes/intelligent-lead-research/design.md#15. Детерминированные приоритеты и отдельный lifecycle` — **experiment**. Детерминированные приоритеты — измеряемый baseline. Пользу LLM, справедливость очереди и marginal yield требуется проверить. Задачи: 2.3, 2.4, 12.1.
- `openspec/changes/intelligent-lead-research/design.md#16. Pending reply и конфликт correlation` — **retain**. Pending hold отличен от квалифицированного ответа. Уже принятый/неизвестный эффект нельзя атомарно отменить задним числом. Задачи: 12.1, 12.2.
- `openspec/changes/intelligent-lead-research/design.md#17. Последовательные прототипы R1–R4` — **correct**. Порядок mailbox перед собственным acquisition устарел после уточнения пользователя. Перестроить на поиск/краулинг сначала, сохранив полный scope. Задачи: 11.5, 12.1, 12.5.
- `openspec/changes/intelligent-lead-research/design.md#18. Происхождение пользовательских уточнений` — **retain**. CL/D provenance нужен. Новые инженерные рекомендации не становятся словами пользователя; примерные требования уточняются реализацией. Задачи: 2.5.
- `openspec/changes/intelligent-lead-research/design.md#19. Полный объём и применение исследования обязательны` — **refine**. Полный scope сохраняется. Применение 66 источников означает освоенный механизм/обоснованный выбор, а не установку всех инструментов. Задачи: 2.5, 12.5.
- `openspec/changes/intelligent-lead-research/design.md#20. Межзапусковый граф знаний, потребностей и зависимостей` — **refine**. OpenNeed и временные зависимости входят в первый полезный срез. Поздний контакт дополняет прежнюю компанию; бюджет не расширяется сам, исторический snapshot сохраняется. Задачи: 1.2, 2.1, 12.1.
- `openspec/changes/intelligent-lead-research/proposal.md#Why` — **retain**. Доказательная цепочка и межзапусковая память соответствуют цели. Текущий demo-код не доказывает их наличие. Задачи: 1.2, 12.1.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Подтверждённый полный объём` — **refine**. Все 36 REQ сохраняются. Последний приоритет меняет очередность; примерные требования не фиксируют непроверенный стек. Задачи: 2.5, 12.5.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Происхождение решений` — **retain**. Crawlee, SearXNG, RU/GB и unit пилота — предложения. Режим отправки форм остаётся открытым. Задачи: 2.5.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Спроектировать интеллектуальную исследовательскую миссию` — **experiment**. LLM проверяется против deterministic baseline. Firecrawl credit cap относится только к Firecrawl; остальные tools используют имеющийся proxy с техническими ограничениями. Задачи: 2.3, 2.4.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Ввести типизированные сущности` — **refine**. Identity и legacy bridge требуют scoped composite constraints; глобальный domain PK не подходит. Задачи: 1.2, 11.5.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Сохранять компанию без email` — **retain**. Компания без email и поздний контакт — критерий начального runtime, а не последующая оптимизация. Задачи: 2.1, 12.1.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Разделить наблюдения источников` — **retain**. Каждое assertion хранит subject/time/evidence. Отрицательная попытка ограничена scope/TTL; доверие источнику не равно факту. Задачи: 1.2, 2.1.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Сделать международные рынки` — **refine**. Типы стран нужны сразу, реальное покрытие расширяется по принятым profiles. MarketStudy не блокирует поиск. Задачи: 2.4, 12.1.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Разделить географию целевой компании` — **retain**. Requested/claimed/observed geography различаются. Proxy country не заменяет регион выдачи API. Задачи: 1.5, 12.2.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Использовать Firecrawl` — **experiment**. Рабочий Firecrawl сохраняется; self-hosted инструменты сравниваются на равных лимитах. Задачи: 2.2, 2.3, 11.5.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Спроектировать собственный поиск` — **refine**. Свой gateway и поиск по собственному corpus дают разные функции; vector scope требует нового API. Задачи: 1.5, 2.3.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Учесть формы на сайтах` — **pending-decision**. Не путать inbound relay и outbound task. Реальный mailbox входит в программу, режим отправки чужих форм ещё не выбран. Задачи: 12.1, 12.5.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Связать подтверждённое исследование` — **retain**. Пакет исследования версионирован. Approval не нужен для сохранения компании без контакта; доставка — отдельный этап. Задачи: 12.1.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Оформить подробную концепцию` — **refine**. Нужны точные contracts, task DAG и evaluation. Наличие кода отличается от пройденной живой интеграции. Задачи: 1.5, 2.5.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Уточнить исполнимые правила` — **correct**. Правила зачёта/качества/остановки верны. Старую последовательность R1–R4 заменить очередностью с приоритетом поиска. Задачи: 2.3, 11.5, 12.1.
- `openspec/changes/intelligent-lead-research/proposal.md#What Changes:Эта поставка содержит требования` — **retain**. Текущий deliverable — audit/planning. Будущие срезы принимаются по реальной функции, не по числу документов. Задачи: 2.5, 12.5.
- `openspec/changes/intelligent-lead-research/proposal.md#New Capabilities` — **refine**. Семь owners логичны. Новые roots/contracts пока план; primary owner в каталоге не является integration acceptance. Задачи: 1.2, 1.5.
- `openspec/changes/intelligent-lead-research/proposal.md#Modified Capabilities` — **refine**. Планировочная дельта не активирует browser/mail режим. Перед apply нужны явные изменения старых ограничений и совместимость v1. Задачи: 1.5, 11.5.
- `openspec/changes/intelligent-lead-research/proposal.md#Impact:Затронутые будущей реализацией bounded contexts` — **retain**. Context-first сохраняется без обязательного отдельного процесса на каждую модель. Долгие внешние эффекты вынести из транзакций. Задачи: 1.2.
- `openspec/changes/intelligent-lead-research/proposal.md#Impact:Предлагаются новые сущности и контракты` — **correct**. PostgreSQL/Temporal можно использовать. Existing vector и object storage нельзя объявлять готовыми для research; canonical content gate требует исправления. Задачи: 1.2, 1.5, 12.3.
- `openspec/changes/intelligent-lead-research/proposal.md#Impact:Связанные изменения` — **retain**. Старые pending changes не закрываются автоматически. Узкие зависимости активации/миграции фиксируются для каждого среза. Задачи: 2.5, 12.5.

## Первичные источники и перенимаемые механизмы

- [AD-S01](https://raw.githubusercontent.com/temporalio/sdk-go/v1.36.0/workflow/workflow.go): Перенимаем сохранённые в history версии переходов и replay tests. Внешние запросы остаются activities. Предел: Прочитан upstream tag v1.36.0, совпадающий с backend/contexts/agent-orchestration/go.mod:8. Механизм version marker не гарантирует exactly-once внешнего провайдера. SHA256 `7de917082023a7165a2445bd9e32257f54ab0e82caf16c473c7995832dadb3e1`.
- [AD-S02](https://raw.githubusercontent.com/beir-cellar/beir/main/beir/retrieval/evaluation.py): Перенимаем qrels/results/k evaluation, отделяем retrieval от linking и сохраняем неуспешные queries в собственном знаменателе. Предел: BEIR не оценивает наши entity/contact labels; qrels ограничены разметкой. SHA256 `f346bd20f258e552895e438257e3cc91308b65e0b1ee6c09422e987084bc3bd0`.
- [AD-S03](https://raw.githubusercontent.com/pgvector/pgvector/master/README.md): Перенимаем exact baseline для recall, scope filtering/partitioning и измерение hybrid retrieval. Предел: Документация upstream не доказывает установку, версию или производительность в этом репозитории. SHA256 `19cebadeeb8d3181d01ef00399674ec41bd2f0363850cdccc7685d4260c5a1c6`.
- [AD-S04](https://raw.githubusercontent.com/scipy/scipy/main/scipy/stats/_binomtest.py): Перенимаем exact binomial intervals для precision/error rate; при n правильных независимых решениях lower = alpha**(1/n). Предел: Предполагается независимость решений; source/template clusters требуют отдельного sampling plan. SHA256 `296ae07e882ce2c821ba37c57840753e43d4b88ace2130e6f392d413051c8023`.
- [AD-S05](https://raw.githubusercontent.com/postgres/postgres/REL_17_STABLE/doc/src/sgml/ddl.sgml): Перенимаем composite PK/FK для entity/assertion/evidence/bridge. Tenant scope входит в ключ, а не только в optional filter. Предел: Документация PostgreSQL 17 не является готовой миграцией нашего продукта. SHA256 `cea060a26fddc0b9b401e1a478941854d7e4bd9c1340f1d91f4beaa8a17207ed`.
- [AD-S06](https://raw.githubusercontent.com/scrapy/scrapy/master/docs/topics/jobs.rst): Не делаем framework queue источником истины миссии. Pin версии и action/receipt ledger дополняются проверкой аварийного восстановления. Предел: Ограничение относится к Scrapy JOBDIR; Crawlee/Crawl4AI проверяются отдельно. SHA256 `ee81c4dc1454b48a8b9bd73313e56545e25525555b797bf46dd7eeee831d27e8`.

## Проверки и пределы

Архитектурный gate запускался read-only: одна baseline ошибка — `catalog: capability "content-production" отсутствует в OpenSpec`. Общий make check, DB migrations и платные/provider интеграции не запускались. Выводы о SQL получены анализом кода, не объявлены продовым инцидентом. Собственные измеренные performance/quality результаты не заявлены.

Итого: 12 findings, 13 фактов, 5 гипотез, 6 решений, все 13 assigned tasks, 41 clause reviews (20 Decisions + 21 proposal points), 6 новых первичных snapshots. Репозиторий не изменён.
