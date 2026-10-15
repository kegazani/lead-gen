# Аудит orchestration: поиск лидов как управляемое исследование

Baseline `bbe49f6`, 2026-10-15. Это исследование и проект реализации, без изменений runtime и без вызовов платных API. Прочитаны концепция 01/03, proposal/design/tasks, все 12 требований adaptive-lead-research и существующие workflow/blocks/search/request contracts. По каждой из 13 назначенных задач составлен blueprint в [планах задач](../task-blueprints.json), [выводах аудита](../audit-findings.json) и [источниках](../audit-sources.json); там же 12 clause_reviews, 12 findings, 17 фактов, 8 гипотез, 7 решений и 8 первичных внешних источников с временем, размером и SHA-256. Изучение документации/кода не означает runtime acceptance.

## 0. Последнее уточнение пользователя о бюджете

Пользователь явно уточнил: **«общий бюджет только для firecrawl для остального не нужно там уже proxy ты это учел?»** Это заменяет прежнее предложение универсального денежного ledger. Application monetary/credits reserve, cap, settlement и финансовая reconciliation вводятся **только для Firecrawl Search/Crawl/Extract**, совместно для всех действий/раундов человеческого intent. LLM и остальные инструменты работают через существующий proxy без второго application monetary reserve/deny и без обязательного price/usage receipt. Optional usage записывается, если proxy её возвращает. Общие action/attempt/idempotency и технические ограничения времени, глубины, concurrency, retries сохраняются. Денежный бюджет Firecrawl и TechnicalLimits — разные поля/причины остановки.

## 1. Вывод, меняющий центр концепции

Сильная часть change — проверяемость свидетельств, отдельные сущности, память и защита исполнения. Главный пробел — **как из оффера и описания желаемых клиентов системно получить поток подходящих компаний**. Документ 03 главным образом отвечает «как дообогатить уже найденную компанию». Его приоритеты P0–P5 начинают с неизвестного субъекта или недостающего контакта. Нет полноценного плана покрытия рынка, типов источников, семейств запросов и выбора между новой компанией и очередным дорогим поиском email у прежней.

При этом baseline уже содержит LLM `Plan` с несколькими целями и bounded `planMoreTargets`: `run_search.go:520,572`. Нельзя утверждать, что сейчас есть только один fixed query. Проблема — строковые цели, ephemeral attempts и отсутствие сохраняемого портфеля стратегий с измеренной обратной связью. Новая архитектура расширяет эти механизмы, а не переписывает всё ради агентного framework.

Предлагаемое ядро: **SearchBrief → портфель discovery strategies → две очереди (новые кандидаты и пробелы известных компаний) → общий scheduler/attempt journal и отдельный Firecrawl ledger → acquisition/extraction/identity → устойчивые знания → зачёт по человеческим критериям**. LLM полезна при интерпретации, локальной терминологии, выборе гипотезы и чтении сложного источника. Программный код сохраняет полномочия, Firecrawl расходы, технические пределы и дедупликацию, условия принятия и остановки.

## 2. Что существует фактически и что нельзя наследовать

| Наблюдение кода | Следствие для реализации |
|---|---|
| `agent-orchestration/internal/domain/agent_run.go:20` требует CompanyID; search block `:76` возвращает только первую ссылку | Нужна новая bulk ResearchMission без фиктивного домена; существующий company-agent полезен после identity |
| `agent-orchestration/internal/app/command/run_step.go:62,122` исполняет внешний block внутри UoW | Новый долгий research loop нельзя просто зарегистрировать старым Execute; нужны отдельные intent/receipt transactions |
| `lead-management/internal/app/run_search.go:194` отбрасывает usage построения плана | Planner action/attempt фиксируются до запуска; usage optional из proxy, отдельный денежный ledger для planner не вводится |
| `run_search.go:337` восстанавливает result refs, но `:377` создаёт новые maps visited/usedTargets | Продолжение может повторить поиск по уже пройденной стратегии; нужны durable attempts и receipts |
| `run_search.go:630` нормализует запрос лишь trim/lowercase; `:637` приводит к lowercase весь URL | Семантическое ограничение попыток нуждается в QueryFamily, а URL path/query нельзя автоматически считать case-insensitive |
| `request-fulfillment/.../activities.go:136` создаёт разные budget scopes суффиксами запроса; `fulfillment.go:292` повторяет раунды; `run_search.go:414` имеет внутренний refine | Один approved intent должен владеть одним общим Firecrawl credits budget; запрещён одновременный запуск old top-up и нового mission loop |
| Firecrawl `budget.go:294` Registry process-local; `:400` удерживает unknown до exact operation evidence | Переиспользовать семантику guard, добавить durable Firecrawl ledger у integration-platform; общий account balance не доказательство стоимости отдельного действия |
| `run_search.go:849,859` сохраняет компанию до assessment, quota `:624` по IDs | Сохранение кандидата и принятие по ICP/unit должны быть отдельны, ни один candidate не исчезает из памяти только из-за отсутствия email |

Существующий runtime сохраняется как совместимый маршрут до принятия нового. Все названные ниже новые таблицы, пути и contracts — **proposed**, не обнаруженная готовая реализация.

## 3. SearchBrief: перевод цели в проверяемый план

Brief строится из человеками заданных OfferVersion, TargetAudience и ограничений. Пример «продаём внедрение CRM генподрядчикам коммерческого строительства» нельзя искать как «внедрение CRM»: это выдаст поставщиков CRM, а не покупателей. Current `criteriaFromOffer` уже отдаёт приоритет Target.Description (`activities.go:124`), поэтому сохраняем это разделение и делаем его типизированным.

Предлагаемый SearchBrief:

- `request_intent_ref`, `criteria_version`, `human_approved_at`, tenant/purpose;
- `offer_facts`: что продаём, реальные sender capabilities; используются для понимания задачи и будущего письма;
- `target_segments`: отрасль, роль в цепочке бизнеса, продукты/услуги, география;
- `must`, `should`, `exclude`: типизированные предикаты с ожидаемым evidence и правилом unknown;
- `result_unit`: company / company-with-contact / email-ready-company, нужная роль/канал, target и country quotas;
- `research_questions`: какие признаки наблюдаемы, чего человек не уточнил, какие гипотезы предложила LLM;
- `language_profiles` и разрешённые source families; locales не расширяют countries;
- общий Firecrawl credits budget; отдельно технические deadline/attempt/depth/concurrency caps, необходимость review, workflow/policy version.

Каждое поле содержит provenance `human-explicit / derived-from-approved / proposed-inference`. Непроверенное предположение о размере компании или потребности купить не становится hard requirement или confirmed fact. LLM может предложить предпочтение; отсутствие ответа человека не создаёт новые полномочия.

Раздельные счётчики результатов корректны, только если засчитанные компании проходят критерии данной заявки. Подтверждённый email нерелевантной фирмы не закрывает цель. Юридическая идентичность — отдельная степень проверки; для unit company не всегда нужна запись в реестре, если человек не потребовал именно это. Уровень достаточности должен быть задан criteria policy, иначе международные рынки с ограниченным registry access искусственно становятся невозможными.

## 4. Discovery portfolio: откуда реально брать компании

StrategyCell — сохраняемый участок исследования `(country, target segment, source family, language, objective, policy version)`. Полное декартово произведение не исполняется: план создаёт ограниченную стартовую волну и расширяет только недопокрытые/полезные участки.

Источник не равен поисковику. Search provider помогает найти URLs, но стратегия определяется **тем, где нужные компании представлены по природе своего бизнеса**:

| Семейство | Полезный результат | Пример вопроса/запроса | Ограничение |
|---|---|---|---|
| Собственный корпус и прежние entity/OpenNeed | Уже известные фирмы, свежие признаки, прежние источники | «Что уже есть по этому ICP/стране?» | Старое соответствие другой заявке не наследуется автоматически |
| Реестр/лицензии | Юрлица и идентификаторы, иногда классификация деятельности | Поиск по разрешённому коду деятельности/району/статусу | Код деятельности может быть широким/устаревшим; это discovery, не доказанный ICP |
| Отраслевые каталоги/ассоциации | Списки участников с нужной ролью | Члены ассоциации коммерческих подрядчиков | Publisher footer не контакт всех участников |
| Выставки/награды/партнёры | Названия и контекст деятельности | Список экспонентов отраслевой выставки | Экспонент, спонсор, организатор — разные роли |
| Закупки/проекты/кейсы/новости | Компания действительно выполняла нужный тип работ | Генподрядчик конкретного коммерческого объекта | Статья — evidence о названной компании, не компания владельца сайта |
| Прямые сайты бизнеса | Описание услуг, реквизиты, контакты | Категория+услуга+город | TLD/язык/IP не доказывают юридическую страну |
| Cross-site exact lookup | Связь известной фирмы с другим источником/контактом | Точный ИНН/номер, имя+адрес, прежнее имя | Точный ID проверяется по типу и юрисдикции |

QueryFamily сохраняет `family_id`, `template_version`, семантические slots, country/language/source capability, objective и разрешённые operators. QueryVariant — лишь текст для конкретного provider. Пример:

- Q1 category/service/location: `генподрядчик коммерческое строительство Казань`; `commercial building contractor Manchester`; `Gewerbebau Generalunternehmer Hamburg`.
- Q2 list discovery: members/exhibitors/suppliers плюс категория, страна и source recipe.
- Q3 event/project evidence: названия компаний из реализаций/новостей, затем отдельная identity задача.
- Q4 registry discovery: код деятельности+регион при поддержке API, с проверкой смысла классификатора.
- Q5 identity/contact gap: exact identifier, legal name+address, contact/legal terms местного языка.

Локальные синонимы предлагает LLM и проверяет country profile. Слово `developer` должно различать строительство и программирование. Домыслы модели о якобы существующем сайте не идут сразу в доверенную карточку. Каждый найденный URL/list row становится DiscoveryHit с source/document ref, rank/locator, proposed subject и ролью (`publisher/subject/partner/unknown`). Статья или страница без email остаётся допустимым источником candidate discovery. Persist candidate не требует готового контакта.

## 5. Две очереди и конкретный цикл scheduler

Очередь D содержит discovery cells/query variants/непрочитанные списки. Очередь E — вопросы известных subjects: различить identity, проверить соответствие ICP, найти domain, найти контакт, доказать связь контакта, устранить конфликт, обновить важный факт. URL frontier и runtime crawler queue находятся ниже: они исполняют bounded acquisition action, а не создают бесконечную самостоятельную исследовательскую миссию.

Один цикл:

1. Прочитать mission revision, текущие criteria, quota debt, pending receipts, Firecrawl ledger и owner event watermark.
2. Применить уже устойчивые результаты: evidence owner receipt → canonical entity/revision → criteria/unit verdict → membership. Не вызывать LLM, чтобы просто пересчитать счётчик.
3. Если target достигнут, cancel/deadline или terminal failure — закрыть планирование и перейти к bounded finalization. Исчерпание Firecrawl cap исключает только его actions; memory/proxy/self-hosted пути продолжаются, если полезны и технически допустимы.
4. Обновить D/E из свежей памяти и событий, не стирая прежние attempts. Поздний контакт может закрыть OpenNeed без нового fetch.
5. Убрать недопустимые действия: scope, capability, stale input refs, prohibited source, exhausted limits, active same action, insufficient Firecrawl reserve (для Firecrawl actions). Сохранить причины отказа.
6. Выделить обязательные локальные проверки и affected conflicts; блокируется зависимое использование конкретного контакта, а не весь поиск при каждой неоднозначности.
7. Определить, где ограничение миссии: мало обнаруженных целевых субъектов → D; достаточный backlog с близкой готовностью → E; конкретная country quota недобрана → соответствующая cell/subjects.
8. Детерминированный recipe предлагает известный следующий шаг. В остальных случаях bounded planner получает только нужные refs/пробел/историю и предлагает ограниченное число действий с expected observation.
9. Scheduler выбирает допущенный action по versioned policy; фиксирует reason, сравниваемые альтернативы, policy/model/input revisions. LLM не сама определяет лимиты. Отдельная денежная политика для её proxy-вызова не добавляется.
10. Intent+outbox; только Firecrawl дополнительно credit reserve; внешний activity; receipt; owner persistence; только Firecrawl settlement, optional proxy usage; пересмотр D/E и membership. Повтор цикла — только после понятного события/таймера.

Для компании без email последовательность может быть: актуальная память → уже известная contact/legal страница → точный ID на другом источнике → catalog subject block → подтверждение принадлежности. Не требуется перед каждым точным lookup снова общий запрос «строительные компании». Если реестр недоступен, допустим иной путь подтверждения согласно criteria; неподтверждённая часть видна.

## 6. Utility, exploration и остановка: что известно, что гипотеза

Формула из doc03 `information_gain * task_value * success_estimate / cost` недостаточна как спецификация: self-rated model confidence и «новый факт» могут создавать ложные priorities. По уточнению пользователя monetary constraint только Firecrawl. Для proxy tools отсутствие цены не создаёт отказ. Сначала жёсткий порядок, затем измеряемая техническая результативность.

**Детерминированный порядок:** admissibility → срочность пересмотра используемого факта → country quota debt → fair opportunity entity/cell → проверенный дешёвый recipe → измеряемая utility → tie-break stable ID. Неизвестные Firecrawl credits не равны нулю; optional proxy price не является gate. Обязательное исправление контакта не откладывается только потому, что новая фирма дешевле.

Когда накоплены данные, для action `a` оценивать:

`U(a) = [w1 * E(delta accepted_units) + w2 * critical_gap_reduction + w3 * uncovered_stratum_gain] / max(epsilon, constrained_resource_cost(a)) - duplicate_or_stale_penalty(a)`.

Это **proposed heuristic**, не измеренный оптимум и не probability truth. `E(delta accepted_units)` для discovery составляется из наблюдавшихся stage transitions: distinct saved candidate → criteria pass → identity sufficient → owned target contact → accepted unit. Для enrichment — вероятность закрытия именно блокирующего gap. Source/country/strategy estimates имеют размер выборки, recency, uncertainty; при мало данных использовать общий prior/round-robin и явно считать cold start. Отрицательный результат может быть качественным evidence и завершить вопрос, но не увеличивает accepted unit.

`constrained_resource_cost` — Firecrawl credits для его операций, техническое время/число попыток для остальных инструментов. Это не второй денежный budget поверх proxy: не резервируем LLM/browser/OCR деньги, не блокируем их при отсутствии тарифов. Разнородные единицы не складываются: дискретные priority tiers и измеренный yield/latency достаточно для первой поставки. Proxy usage/cost может отображаться как optional диагностика. Online bandit допускается только после измеримого преимущества.

**Начальные числа исключительно гипотезы H02/H03:** до 6 discovery cells/волна, до 2 variants/cell, 1 активное external action/entity, максимум 3 варианта вопроса, park после 2 действий без уменьшения gap, exploration 10–20% технических dispatch opportunities после bootstrap. Это тестовые knobs, не утверждённые требования пользователя и не обещание оптимальной полноты. Нельзя задать универсальное 70/30 discovery/enrichment: сначала определить bottleneck по фактическому backlog и conversion.

Park хранит причину, evidence/input revision, next_review/condition. Стратегия с изменившимся источником/локалью/новым ID может быть вновь полезна; перестановка слов без изменения QueryFamily slots учитывается в прежнем лимите. Семантическая эквивалентность не решается одной произвольной оценкой LLM: typed family и normalized slots дают жёсткое ядро, ambiguous free query имеет ограниченный repair/review path.

Stop означает один из наблюдаемых исходов: target reached; человеческая cancel; deadline; все полезные допустимые стратегии исчерпаны; невозможность устойчиво продолжать. Firecrawl budget-exhausted становится причиной global partial только когда полезный остаток требует Firecrawl; его cap не запрещает прочие разрешённые proxy/local/self-hosted действия. Отдельный source 429/blocked не «компания не существует» и обычно не останавливает другие клетки. Global pause требует обязательного внешнего решения/условия; у pause есть owner, deadline и resume guard. Точные stop-yield thresholds принимаются сравнительным experiment, а не на веру.

## 7. Durable execution, бюджет и границы транзакций

Предлагается новый `ResearchMission` aggregate в **agent-orchestration**, без нового отдельного сервиса. `lead-management` сохраняет evidence/entity/assertions/OpenNeed; `integration-platform` исполняет acquisition/search/model capability, владеет единым Firecrawl account/grant/operation ledger и возвращает receipts; `request-fulfillment` владеет формальной заявкой. Контракты/relationships вводятся task1.2.

Proposed таблицы:

- `research_missions`: `(tenant_id, mission_id)` PK, unique approved execution intent+criteria+generation, revision, lifecycle, plan refs, opaque Firecrawl grant_ref/проекция, TechnicalLimits, source cursor/checkpoint;
- `research_strategy_cells`, `research_query_families`, `research_attempts`: typed identity, policy/input revisions, outcome/reason and next eligible condition; index `(tenant,mission,state,next_attempt_at,priority)`;
- `research_actions`: stable action_id, logical attempt fingerprint, dispatch generation/lease, provider job ref, input schema/hash; unique logical dispatch + CAS revision;
- `research_receipts`: provider/owner receipt ID, payload hash, cost/ingest outcome; duplicate ID+different payload → conflict;
- у **integration-platform**: `firecrawl_accounts`, `budget_grants`, `operation_reservations`, append-only `credit_entries`: одна operation charge lineage, атомарные account+grant ограничения; agent хранит только refs/проекции; для остальных proxy tools денежные таблицы не обязательны;
- `research_result_memberships`: unique `(tenant,mission,criteria_version,unit,canonical_entity_id)`, owner receipt, canonical/assessment version, criteria/unit state/reason;
- `research_completion_snapshots`: immutable accepted composition/revisions/watermark and Firecrawl credits-known/unknown at stop; later Firecrawl reconciliation versions отдельны; proxy usage optional.

Dispatch protocol: короткая agent Tx intent+outbox → для Firecrawl awaiting_fc_reservation → integration-platform ReserveForAction(account/grant/action/attempt) в собственной Tx → reservation receipt → agent CAS ready → worker claim fencing generation → dispatch commit → network activity → provider receipt/artifact refs → owner evidence ingest idempotently → persistence receipt → mission CAS membership/outbox. Firecrawl Settle/MarkUnknown — отдельная идемпотентная команда владельцу ledger, agent получает проекцию. Proxy usage optional. Нет общей транзакции двух contexts: crash после owner reserve до agent acknowledgement восстанавливается GetReservation(action/attempt), повтор не резервирует ещё раз. Сеть не держит открытую предметную транзакцию.

Crash после внешнего вызова до receipt — неизвестный эффект. Temporal может повторить activity; lease expiry означает потерю worker liveness, не отсутствие внешнего эффекта. Если provider имеет request id/job lookup/idempotency, делаем reconciliation. Для Firecrawl сохраняем unknown credit reservation и повторяем только в пределах общего cap. Для остальных proxy tools сохраняем technical unknown и применяем bounded retry policy без второго application monetary reserve. Hard Firecrawl budget требует обоснованной верхней оценки credits/ограничиваемой операции; неизвестность тарифа других tools не gate.

Один provider account policy принадлежит integration-platform. Approved execution intent получает grant_ref, распространяющийся на Search/Crawl/Extract и все refine действия Firecrawl. Account и grant — два ограничения одной operation reservation, не две кассы и не двойной debit. Human-button MarketStudy до формальной request имеет свой studyintent и grant linkage в той же account policy, без нового финансового budget type. Остальные источники study через proxy не получают app monetary cap. Нельзя продолжать старые `criteriaForRound` отдельные scopes вокруг новой миссии. `StartResearchMission` вызывается идемпотентно один раз; далее read/signal/wait. Legacy route и new route выбираются cohort/version один раз, не запускаются параллельно для одного intent.

При дедлайне закрывается планирование, bounded drain принимает завершённые receipts; остальное уходит в reconciliation с held unknown Firecrawl credits. Snapshot содержит owner event watermark/as-of revisions. Late evidence обновляет current dossier и OpenNeed, но не переписывает исторический состав. Позднее Firecrawl списание отображается финансовой ревизией с той же lineage. Отсутствие usage от других proxy tools не препятствует completion. Перед будущей отправкой читается актуальная readiness, исторический accepted snapshot сам по себе не разрешает письмо.

## 8. Что заимствуем из внешнего опыта

Все источники реально скачаны из официальных repositories, метаданные — JSON. Прочитаны implementation docs/code, а не только README.

- **Temporal SDK workflow/activity/internal options:** deterministic workflow, bounded activities, heartbeat/cancel, total timeout including retries, versioning `GetVersion`, `ContinueAsNew`. Заимствуем механизм durable orchestration и history tests. Не делаем внешний LLM через SideEffect; не обещаем exactly-once provider payment. В master workflow/doc.go есть противоречивая фраза «at-most-once» activities внутри SideEffect раздела; она не принимается как guarantee, retry policy/source и crash model требуют explicit idempotency.
- **OpenAI Agents function_schema.py/run.py:** strict typed tool schemas и `max_turns` as engine guard. Заимствуем схему и ограничения. Не внедряем второй runtime поверх Temporal; max_turns не заменяет общий Firecrawl ledger и semantic authorization; вторую финансовую политику для proxy не добавляем.
- **Anthropic orchestrator-workers cookbook:** dynamic decomposition уместна когда подзадачи неизвестны заранее; predictable steps проще делать явными recipes. Наша модель предлагает конкретный missing-evidence step, а известный registry/contact parser обходится без LLM manager.
- **Anthropic evaluator-optimizer cookbook:** полезно отделять генерацию и проверку при ясных критериях. Cookbook `while True` без cap и LLM PASS не production-политика; заменяем bounded loop и domain/evidence checks. Не сохраняем chain-of-thought: достаточно структурированного решения и краткого предметного основания.

Попытки получить два guessed raw URL LangGraph durable docs и Temporal sample вернули 404; это факт неправильного/перемещённого locator, не отсутствие возможностей framework. Они не используются как доказательства. Не было live trials, runtime benchmarks, утверждений о тарифах или выборе модели.

## 9. Проверка гипотез и первый результат реализации

Сравнивать четыре policies на одинаковых briefs, corpus/holdout, технических limits и отдельном Firecrawl credits cap:

1. baseline current multi-target Plan/refine;
2. typed brief+source recipes+round-robin;
3. dual frontier с deficit/fairness/gap priorities;
4. measured utility + bounded exploration.

Размечать отдельно discovery recall, candidate distinctness, ICP correctness, identity/ownership precision, accepted units, time-to-first-saved/first-accepted, Firecrawl actual+unknown credits, marginal Firecrawl credits на accepted unit, optional proxy usage, spread по странам/источникам, no-progress/false park. Holdout разделён по source family/site/template, иначе одни каталоги утекут в train/test. Dynamic live run нужен отдельно от frozen replay: нельзя выдавать результат fixture за доступность внешнего источника. На critical wrong-subject/cross-scope scenarios gate не ослабляется ради большего числа лидов; качество и confidence intervals показываются вместе с объёмом корпуса.

**Search-first vertical slice:** человек задаёт ICP → новая миссия без seed company → discovery на выбранных реальных источниках → сохраняется компания без email → другой источник с общим подтверждённым ID даёт контакт → критерии/unit дают credit → пользователь видит все сохранённые кандидаты и причины недобора. Затем restart/late-contact/cancel/unknown-cost. Письма и inbound остаются обязательными последующими задачами полного change; search-first acceptance не закрывает всю программу.

Предлагаемые новые tasks `4.16–4.18` в JSON: typed brief/strategies; persistent discovery+dual frontier; comparative policy evaluation и принятие thresholds. Существующие IDs не изменены. Их следует поставить перед full search acceptance и увязать с markets_search 5.x/6.x, acquisition7.x, identity3.x/4.13–15. DAG реализации однонаправленный; runtime research feedback loop не означает циклическую dependency tasks.

## 10. Порядок работ и диагностический UI

Порядок: 1.1/1.2/1.5 contracts → mission4.1 + entity/evidence3.x → action protocol4.3 → owner Firecrawl ledger/grants4.4 → planner4.2 и brief4.16 → source gateways6.1/acquisition7.2 → strategy frontier4.17 и memory4.5/4.6 → scheduler4.11 → membership4.9/lifecycle4.7 → versions/recovery4.8/finalization4.10 → visible progress4.12 → real search acceptance11.1 и policy eval4.18. Часть работ независима и может идти параллельно, но выключенный/bare stub adapter не закрывает acceptance.

Пользователь должен видеть: «Ищем новые компании по спискам участников отраслевой ассоциации», «Нашли 17 сохранённых кандидатов, 8 прошли критерии, у 6 подтверждён контакт», «У компании A найден номер, проверяем карточку B», «Источник C недоступен, используем D», «Контакт отсутствует в проверенных страницах; открытая потребность сохранена». Нужны ссылки от каждого count к exact list, с одинаковой snapshot revision. Heartbeat и last meaningful progress разделены; Firecrawl receipts credits known/reserved/unknown не смешиваются. Отчёт о 17 URL и UI с шестью компаниями без причин больше не допустим.

Metrics должны ловить живой worker без содержательного прогресса, provider200/empty parsed result, duplicate-heavy source, persistence lag, Firecrawl unknown-credit backlog и исчерпание конкретной country quota. Thresholds на основе source latency и policy, IDs в trace, не labels с неограниченной cardinality. Никакие emails/phones/raw queries/секреты не попадают в общий telemetry.
