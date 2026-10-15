# Аудит идентичности и памяти как механизма поиска лидов

Baseline: `bbe49f6`. Область: задачи 3.1–3.8, 4.5, 4.6, 4.13–4.15, 11.3, 11.4. Это проект реализации после чтения кода и первичных материалов; runtime приёмка и платные вызовы не выполнялись. Пользовательские требования примерные: модели ниже — инженерные предложения для достижения поиска реальных компаний/контактов, а не приписанные пользователю выбранные технологии.

## Актуальное уточнение о бюджете

Общий финансовый лимит и резервирование средств применяются только к Firecrawl. Для LLM, собственного crawler, OCR и остальных инструментов используется существующий proxy без второго прикладного финансового бюджета. Deadline, concurrency, retry/attempt limits и дедупликация остаются техническими ограничениями; разрешение исследования и область доступа проверяются независимо от денег. Упоминания стоимости ниже — измерения или фактические Firecrawl receipts, а не общий финансовый gate каждого инструмента.

## Вывод

Главная единица поисковой работы — конкретный пробел о различимом субъекте: «найти email продаж этой организации», «установить чей номер в блоке», «подтвердить свежесть контакта». Память должна возвращать планировщику не только текст, но текущие допустимые основания, кандидатов, неизвестности, прошлые попытки и условия полезного продолжения. Компания без email сохраняется с момента достаточного различения; наличие контакта влияет на зачёт цели конкретной заявки, а не на право компании существовать в памяти.

Рабочая цепочка: `полученный документ → блок/упоминание субъекта → кандидаты на тождество → проверенные утверждения → недостающие сведения → разрешённый следующий поиск`. Обнаруженная статья может дать название/ИНН/бренд и новый адрес поиска. Это полезное наблюдение даже когда контакт из её footer заведомо принадлежит издателю. Принятие наблюдения, различение субъекта, подтверждение контакта и зачёт заявки — разные операции.

## Дефекты и недостающие решения исходной концепции

1. **Переиспользование текущего tenant/vector пути небезопасно как основа нового контура.** У `lead_profile` ключ глобальный `id`, равный домену; `ON CONFLICT (id)` обновляет `tenant_id` (`lead_profile_repository.go:196–203`). В contacts нет tenant (`migrations/red/0001_init.sql:11`), в vector-контракте нет scope/deletion (`contracts/integration-platform/vector/vector.go:34–62`), SQL читает весь индекс (`vector/postgres/store.go:193`). Концепция §3.8 обещает существующий vector port с сохранением контекста: это требует новой версии контракта и хранилища. Наблюдение кода не доказывает эксплуатацию или итоговую конфигурацию доступа; оно запрещает считать tenant-scoping уже выполненным.
2. **Текущий crawler сохраняет лишь страницы с контактом.** `run_search.go:799` отсеивает articles/blog/news, `:843` вызывает `usableCrawlPage`, где отсутствие contacts ведёт к false (`contact_quality.go:135`). Поэтому исходный сценарий «нашли компанию, позднее нашли email» невозможно реализовать простым добавлением retrieval: необходим отдельный ingest entity/observation до gate готовности лида. Источники-статьи надо классифицировать по роли, а не выбрасывать по сегменту URL.
3. **Один enum Assertion смешивает три независимые оси.** `observed/confirmed/human_confirmed/stale/unavailable` (§2.5) теряет факт прежнего подтверждения при устаревании и скрывает ограничения доступа. Предлагаются `knowledge_state`, `freshness_state`, `use_state` и независимые immutable evaluations. Ручное подтверждение сохраняет автора/метод и не делает email достижимым.
4. **OpenNeed смешивает пробел и отсутствие активного исполнения.** `dormant` в одном ряду с `satisfied` (§3.16) не отвечает, удовлетворён ли dormant need. Хранить knowledge state отдельно от execution attachment. Завершение миссии отсоединяет исполнение, но оставляет `open+dormant`; поздний факт может дать `satisfied+dormant` без нового запуска; финансовый лимит Firecrawl при этом не меняется.
5. **Описаны assertions и relation edges, но не единственный источник истины.** Рёбра предметного графа должны быть текущей проекцией версий assertions, а не вторым независимо редактируемым хранилищем фактов. Изменение graph edge всегда указывает assertion/evaluation refs.
6. **Обратимое слияние требует уровня resolution projection, а не смены primary key.** Исторические entity IDs сохраняются; identity decision создаёт новую revision членства в группе разрешённого тождества. Отмена пересчитывает только затронутую группу и зависимые текущие решения. Домен/телефон/бренд не являются ключом такой группы. Транзитивный путь из вероятностных пар недостаточен: A≈B и B≈C могут конфликтовать по сильным IDs.
7. **Уникальность ID надо применять к принятой привязке, а не raw-наблюдениям.** Иначе ошибочное распознавание/двойной номер не удастся сохранить как конфликт. Нужны claims и отдельно canonical identifier binding. Для DE в область входит court/register type; КПП и SIRET идентифицируют иной уровень субъекта; unknown ID остаётся строкой с неизвестной семантикой. Нормализатор не проверяет существование в реестре.
8. **Dependency graph требует логики достаточности и защиты от циклического самообоснования.** Список refs недостаточен. Для каждого evaluation хранить альтернативные support sets; внутри набора все основания обязательны, между наборами достаточно одного. Явный противоречащий strong-ID — отдельный veto по policy. Нельзя подтвердить A только ссылкой на B, если B получен только из A.
9. **Negative memory не должна быть привязана только к run ID.** При таком ключе новая заявка повторит бесполезный поиск. Разделить idempotency key исполнения (с action/run) и reuse key наблюдения (subject/question/source/version/locale/purpose); исходное поручение/criteria всё равно сохранять. Ошибка провайдера или timeout — недоступность, а не доказанное отсутствие контакта.
10. **Истечение и отзыв не равны ложности факта.** Ошибочный источник, недоступный URL, запрет использования и истечение срока проверенности требуют разных событий/причин. При удалении сразу исключать из чтения и подачи в LLM, затем асинхронно удалять raw/crops/vector/cache с подтверждением. Запоздалая индексация не должна воскресить запись.

## Что реально есть в коде

- `CompanyID` описан как нормализованный домен (`domain/company.go:9`), конструктор требует Domain и Name (`:65–90`); это не готовая регистрационная идентичность.
- `Contact` принадлежит одному CompanyID (`domain/contact.go:42–48`), `Enrich` заменяет значения/provenance (`:130–145`); полной истории evidence versions нет.
- `Provenance` имеет URL/time (`domain/provenance.go:18`), `AttributeProvenance` дополнительно необязательный text Evidence и method (`:61`); нет immutable representation, checksum, locator и subject scope.
- `crawl.Page` несёт URL/Markdown/metadata (`contracts/integration-platform/crawl/crawl.go:67`); Markdown evidence можно сохранить сейчас, DOM selector нельзя выдумать.
- `persistCompany` сохраняет profile, затем contacts (`app/run_search.go:981–1009`); это не наблюдаемый единый commit entity+evidence+outbox. Новый ingestion contract должен выдавать durable receipt только после принятия своих объектов.
- Уже разведены deliverability/risk в domain (`email_verification.go:10` прямо запрещает считать это доказательством существования ящика). Сохранить семантику, добавив method/date/input revision.
- Demo phone filter требует 10–15 digits (`contact_quality.go:89`); нормализатор убирает все кроме цифр (`normalize.go:40`). Это не международная phone model, теряются extension/context.
- Текущий `NormalizeEmail` приводит весь адрес в lower case (`normalize.go:34`); новая модель должна сохранять original local part, отдельно IDNA/lower domain, policy-versioned lookup aliases. Provider-specific dot/plus folding по умолчанию не делать.

Полные path:line и пределы выводов приведены в JSON.

## Что перенимаем из первичных материалов

| Источник | Реальный механизм | Как применяем | Предел вывода |
|---|---|---|---|
| Splink blocking docs, IM-S01 | Несколько узких blocking rules; анализ числа пар до scoring | Exact registration key OR normalized name+locality OR known alias+address; caps и метрика пропущенных true pairs | Blocking сам не подтверждает тождество, неподходящие правила режут recall |
| Splink clustering source, IM-S02 | Thresholded pair graph и connected components | Только ранжирование кандидатов и офлайн сравнение; перед identity group проверять все сильные ограничения | Компонента связности не доказательство юридического тождества |
| libphonenumber FAQ, IM-S03 | Страновые планы, leading zeros; valid range не значит assigned/reachable | Версионируемый parser, E.164 когда достаточно контекста, original/extension/ambiguous отдельно | Библиотека не устанавливает владельца номера и не звонит для проверки |
| Graphiti edge model, IM-S04 | `created_at`, `valid_at`, `invalid_at`, `expired_at`, episode refs | Разделять время наблюдения/записи/предметной действительности и хранить episode lineage | Не переносить автоматическое LLM fact resolution в authority; graph partition не полная ACL |
| pgvector docs, IM-S05 | Exact recall; approximate tradeoff; post-scan filtering может недодать top-k | Сначала exact по разрешённому scope, ANN только после измерений с iterative scans/partitioning | pgvector не стоит в текущей миграции; embedding similarity не confidence принадлежности |
| PostgreSQL RLS, IM-S06 | Default deny; owner/superuser/BYPASSRLS обходят policy | Defense-in-depth scoped role/SET LOCAL, composite keys и явный фильтр в owner queries | RLS не лечит неверный глобальный ключ; не защищает внешний cache/object-store |
| python-stdnum RU/FR source, IM-S07/08 | Типизированный checksum/format; SIRET establishment; исключение La Poste | Источник golden vectors и предметных адаптеров, сохранять нормализатор version | Это library behavior, не подтверждение текущего реестрового статуса; не назначает legal truth |

Ни Graphiti, ни Splink, ни pgvector не становятся обязательной зависимостью только из-за полезного опыта. Для первого пути достаточно существующего PostgreSQL + новых owner tables; точные версии библиотек и лицензии проверяются при выборе адаптера. Исходники fetched 2026-10-15, SHA256/URLs/ограничения — в JSON; живой benchmark не выполнялся.

## Предлагаемая модель хранения

Все следующие имена — proposed; владелец предметных данных `lead-management`. Точные номера миграций назначать при apply. Для контактных/raw доказательств сохранять клиентскую/red область, green получает только допустимые opaque refs/метаданные. Не помещать тексты/PII в общую event шину.

- `research_entity(tenant_id, entity_id UUID, kind, revision, created_at, retired_at)`; composite PK, отсутствие email разрешено. Kind `unresolved_business_subject` допустим до подтверждения LegalEntity, Website/Brand/Establishment различаются.
- `legacy_entity_mapping(tenant_id, legacy_company_id, entity_id, relation_kind, decision_ref, revision)`. Старая карточка домена может отображать несколько субъектов; единственного primary назначать лишь по решению. Не импортировать существующий domain profile как registry-confirmed юрлицо.
- `identifier_claim(tenant_id, claim_id, entity_ref/mention_ref, jurisdiction, registry_authority, identifier_type, original, normalized, normalizer_version, evidence_revision, validation_result)`; индекс `(tenant,jurisdiction,authority,type,normalized)`. В таблице допускаются конфликтующие claims. `identifier_binding` — принятая версия связи с entity и действительным периодом; уникальность/EXCLUDE только для типов с известной уникальной семантикой, неизвестный период не выдумывать.
- `source_document` и `document_revision`: source key, requested/final URL, capture receipt, representation, sha256, observed_at, recorded_at, source profile/ref, policy state. Повтор одинаковых bytes может ссылаться на общий разрешённый blob, но новый capture остаётся новым наблюдением времени; hash не доказывает истинность.
- `subject_mention(tenant, mention_id, document_revision, locator, name/ID observations, publisher_flag, subject_candidates)` — отделяет местный блок карточки от издателя/footer. Locator привязан к normalization version: text byte span/Markdown; DOM locator только при сохранённом DOM; PDF page+bbox; OCR crop+transform.
- `assertion_revision(tenant, assertion_id, revision, subject, predicate, typed_object, origin_kind, observed_at, recorded_at, valid_from?,valid_to?,policy_version)`. Значения не обновлять на месте; corrections добавляют revision/supersedes. Не хранить временную свежесть как истинность.
- `verification_evaluation(tenant, evaluation_id, assertion_revision, dimension, outcome, method/version, actor?, evaluated_at, expires_at?,reason)`; dimensions source/extraction/identity/link/format/reachability/freshness/use. Отдельные outcomes остаются видимыми. Source reliability обусловлена predicate, primary/secondary ролью, историей ошибок и актуальностью; scalar universal trust не нужен.
- `support_set` + `support_member`: OR достаточных групп и AND members, каждый ref с revision; contradiction refs отдельно. Reverse index `(tenant, dependency_kind, dependency_id,dependency_revision)` и target revision watermark. Derived relations, summaries и readiness указывают support-set/evaluation refs.
- `identity_decision(tenant, decision_id, member_ids, input_revisions, rule_version, decision, actor, created_at, supersedes)`; active resolution projection с revision. Source records/observations остаются неизменными при merge/split.
- `open_need(tenant,purpose,need_id,entity_id,predicate,target_constraints_hash,knowledge_state,execution_state,revision,next_review_at,reason)` плюс origin-criteria refs и attempts refs. Unique active exact semantic key, без объединения несовместимых criteria; index `(tenant,purpose,entity_id,knowledge_state)` и due queue `(tenant,next_review_at)` только для оценки.
- `research_attempt`/`negative_observation`: durable action receipt, normalized question key, source/version/locale/channel, outcome/error category,time,reusable_until,normalizer version; для Firecrawl — фактические финансовые receipts, для остальных — доступная техническая статистика proxy без нового app budget. Частично разобранный документ и rate limit не превращаются в contact-absent.
- `inbox`, `outbox`, `projection_watermark`, `revocation_tombstone` принадлежат локальному owner; уникальный inbox `(consumer,tenant,event_id)`. Не хранить full raw в outbox.

FK в каждой предметной таблице включают tenant. Purpose — проверяемое право обработки, не просто произвольная строка из UI; scope version входит в retrieval/cache/index receipt. Разные purpose могут использовать одну entity лишь при разрешённом policy пересечении; копирование private evidence под другим purpose само по себе не разрешено.

## Команды, события и транзакции

Предлагаемые owner-команды: `RecordObservation`, `ResolveCandidates`, `DecideIdentity`, `EvaluateAssertion`, `UpsertOpenNeed`, `ReevaluateDependencies`, `RevokeEvidence`. Каждая несёт tenant/purpose, actor/service authority, idempotency key, expected input revisions и versioned typed payload; LLM может предлагать payload, но не назначать authority.

`RecordObservation`: получить/проверить готовность artifact → отдельная короткая DB transaction записывает receipt, revision, mention/assertion candidates, inbox/result и outbox. Большой upload/LLM/HTTP за пределами transaction. Raw upload в staging; DB ссылается только на ready artifact, orphan staging удаляется GC. Если red/green физически разделены, не обещать общую транзакцию: red owner принимает evidence, публикует receipt; green discovery проецирует refs идемпотентно. Зачёт миссии ждёт accepted receipt, а не успешного HTTP 200.

Событие `evidence-recorded` передаёт permitted refs и owner revision. Consumer читает текущее состояние (event timestamp не источник порядка), проверяет purpose и dependency reverse index, пересчитывает затронутые support sets; `UPDATE ... WHERE revision=expected` вместе с inbox+outbox фиксирует результат. При CAS conflict повторно читает состояние. Поздний event v3 после v5 не откатывает текущую проекцию; события разных aggregates нельзя сравнивать одним глобальным номером.

Изменение identity group пересчитывает candidate resolution, нужные contact links, OpenNeed и текущую readiness. Исторические mission completion и фактически отправленный recipient/content snapshot неизменны. При корректировке active mission владелец миссии пересчитывает membership/credit по своей criteria version, а не повторяет отправку.

## Долговечный T0–T4 сценарий

T0: A содержит подтверждённый ID и legal name, email не найден на обследованной версии. Создаются entity, source/evidence/assertion и `need=open,dormant/attached`. Negative result содержит scope конкретной попытки. Компания видна с причиной дефицита контакта.

T1: достигнут технический deadline либо исчерпан Firecrawl grant и все полезные оставшиеся действия требуют Firecrawl, результат partial. При доступных разрешённых non-Firecrawl путях поиск продолжается; компания и need не удалены, execution_state становится dormant. Запланированный `next_review_at` разрешает локальную проверку данных; внешнее acquisition без разрешённого run не стартует. Для разрешённого собственного crawler финансовый gate Firecrawl не применяется.

T2: другой разрешённый crawl B обнаруживает ID и email в карточке субъекта. Candidate lookup по jurisdictional key находит A. Проверка exact ID+subject locator без конфликтов создаёт подтверждённый contact relation; один и тот же endpoint может обслуживать несколько entities отдельными edges. Позднее появление не меняет дату первого discovery.

T3: dependency worker закрывает только подходящие needs по criteria; email-format и принадлежность подтверждены, reachability unchecked. Активная миссия может зачесть по её unit; terminal snapshot прежней миссии остаётся прежним. UI показывает «Email найден в B; связь с компанией по ИНН; ящик не проверен».

T4: B отозван. Если это единственная достаточная support set, relation pending/disputed по причине и need вновь open. Если независимое C ещё достаточно по rule, link сохраняет подтверждение с C. Копия B не считается независимым C; неизвестная source-family помечается uncertain, не выдумывается уверенное первоисточничество.

Проверки: crash в каждом шаге; повторное событие; v3 после v5; поздний worker после deletion; две компании в карточке; publisher footer; два tenants с одним domain/номерной строкой; неизвестный ID; split после отправки. Все должны работать в реальном хранилище. Fixtures проверяют invariant, но живую интеграцию/реальные источники не заменяют.

## Retrieval для следующего полезного действия

1. Scope/authority gate и перечень разрешённых entity/evidence revisions.
2. Exact typed identifiers и aliases → кандидаты, включая неподтверждённые/конфликтующие.
3. Ограниченные typed edges (например, establishment→organization→website; max depth и fanout в запросе). Наличие пути не создаёт новый факт.
4. FTS по original/transliterated/localized names и evidence; multilingual поля/конфигурации раздельные, язык не определяет страну компании.
5. Optional semantic recall только из доступного corpus. Фильтр/partition применяется в хранилище до выдачи кандидатов, после разрешения refs повторный gate. Если ANN с фильтром недодаёт результат, это проблема recall: iterative/exact fallback, не снятие ACL.
6. Context package: accepted assertions с методами/датами, current conflicts, open needs, negative attempts со scope/TTL, candidate sources и разрешённые инструментальные действия. Каждая фраза summary имеет claim refs. Кэш ключ включает tenant/purpose/ACL epoch/subject revision/policy version; response revision фиксируется в LLM action.

Метрики: exact-ID candidate hit rate, blocking recall на размеченных парах, pair false merge и cluster contradiction, correct-contact-link precision/recall, opened→satisfied need, полезных evidence на crawl/page, повторные бесполезные расходы, dependency lag, stale-context rejections. Это связывает качество памяти с реальным поиском, а не с количеством graph edges.

## Порядок реализации и невыясненное

Сначала 3.1–3.4 и минимальный Firecrawl evidence ingestion; затем candidate/link checks 3.5–3.8; затем attempts/retrieval/OpenNeed 4.5/4.6/4.13; dependency processing 4.14 и T0–T4 4.15. Domain/identity/evidence не зависят от выбора Crawlee/Crawl4AI и не требуют отдельной graph DB. Отзыв использования блокирует retrieval уже в первом срезе; полный physical cleanup всех будущих artifacts/vectors/conversation projections завершается задачей 11.3.

Эмпирические гипотезы: достаточно ли точных правил и blocking для требуемого recall; хватает ли PostgreSQL FTS/exact vector; какие TTL действительно сокращают лишние запросы; нужен ли дополнительно вероятностный linker; как точно обнаруживать source copies. Для каждой нужен независимый размеченный corpus и сравнение с простым baseline. Ни 120 объектов acquisition, ни несколько успешных контактов не доказывают статистическую точность всех рынков.

Не выбраны заранее: конкретная graph/linker библиотека, TTL актуальности сведений, глобальный score достоверности, публичное cross-tenant knowledge sharing. Предложение — отдельные проверяемые измерения и tenant-scoped знания; глобальный public corpus, если понадобится, требует отдельной модели лицензирования/доступа и не получается автоматически из совпадения URL.

В [планах задач](../task-blueprints.json), [выводах аудита](../audit-findings.json) и [источниках](../audit-sources.json) — все 15 task blueprints: dependencies, owner, proposed paths, input/output, шаги, failures, acceptance и hypotheses. Задачи не отмечались выполненными.
