# request-fulfillment-execution Specification (delta)

## MODIFIED Requirements

### Requirement: Идемпотентный запуск исполнения заявки

Повтор технического старта уже принятой попытки SHALL возвращать/подтверждать именно тот же execution, если identity совпадает; он SHALL NOT сообщать о недоступности рантайма как о результате повторного запуска и SHALL NOT создавать второй execution. Обычный повтор клиентского бизнес-запроса не считается подтверждением исполнения. Система SHALL сохранять `request_id` как стабильную идентичность заявки. Пока предшествующая попытка/исполнение `ACTIVE` или `UNKNOWN`, новый бизнес-запрос SHALL быть явно отклонён без постановки в очередь. Новый запуск допускается только после terminal и безопасного доменного перехода, с новым generation и distinct `CommandID`.

Replay `CommandID` SHALL сравнивать полный канонический семантический payload approve/rerun/execute: kind, reason, target state/value, request version, offer ID/revision и snapshot reference/fingerprint, а также все прочие поля, влияющие на lifecycle. Tenant и request остаются scope ключами. Идентичный replay возвращает ту же receipt до lifecycle check; любое семантическое отличие — conflict. HTML form SHALL создать ID один раз и сохранить тот же hidden `CommandID` (ключ идемпотентности) на validation/error rerender после неоднозначного результата; скрытая генерация нового ID при повторе запрещена.

#### Scenario: Новый бизнес-запрос при ACTIVE или UNKNOWN

- **WHEN** приходит новый бизнес-запрос с новым `CommandID`, пока предыдущая попытка/исполнение имеет состояние `ACTIVE` или `UNKNOWN`
- **THEN** command adapter отклоняет admission до записи в repository; новая intent и queued бизнес-generation не создаются

#### Scenario: Семантический replay команды

- **WHEN** approve, rerun или execute повторяется с тем же `CommandID`, но изменена причина, цель, request version, offer revision/snapshot или вид команды
- **THEN** система возвращает conflict и не изменяет сохранённую intent; только полностью идентичный canonical payload возвращает прежнюю receipt

#### Scenario: Неизменный CommandID при ошибочном перерендере формы

- **WHEN** HTML mutation перерендеривается после validation error или неоднозначного исхода commit и пользователь повторяет submit
- **THEN** hidden `CommandID` (idempotency key) остаётся исходным; форма не генерирует новый бизнес-запрос незаметно

#### Scenario: Rerun до наличия trusted verifier

- **WHEN** v2 HTTP rerun приходит, пока для предшественника нет verified runtime terminal resolution
- **THEN** endpoint возвращает явное не-принятое blocked состояние и не создаёт intent/outbox/generation; v1 G1.5 путь не меняется

#### Scenario: Replay того же принятого CommandID

- **WHEN** повторно поступает команда с тем же уже принятым `CommandID` и идентичными параметрами при ACTIVE/UNKNOWN
- **THEN** система возвращает идемпотентный результат существующего intent; это не создаёт новый бизнес-запрос, generation, attempt или queued admission

#### Scenario: Повтор технического старта уже принятой попытки

- **WHEN** dispatcher повторяет старт уже принятой попытки с той же проверенной identity
- **THEN** система подтверждает тот же execution, не создаёт второй execution и не отвечает ошибкой недоступности как результатом дубликата

#### Scenario: Повторный старт уже бегущего исполнения

- **WHEN** execution той же принятой попытки уже запущен, а dispatcher повторяет технический старт
- **THEN** система подтверждает ссылку на то же execution или исход «уже запущено», но не сообщает о недоступности рантайма

#### Scenario: Недоступность рантайма отличается от повторного старта

- **WHEN** Temporal runtime действительно недоступен до подтверждения старта
- **THEN** durable intent остаётся pending/queued для dispatcher, а система не сообщает, что execution запущен

## ADDED Requirements

### Requirement: Версионированный execution protocol и граница rollback

Исполнение SHALL различать `ExecutionProtocol v1` и `ExecutionProtocol v2`. G1.5 compatibility bridge SHALL сохранять v1 approve/rerun/direct-start и прежнее route/aggregate behavior, worker/activity payload и старые Temporal histories на точном migration catalog 0001–0010, включая additive verified-resolution storage из 0010. Исходный G1 (0001–0004) SHALL NOT объявляться совместимым со schema0010 или использоваться как rollback target; G1.5 — новый exact rollback target. Его binary schema verifier и release manifest SHALL совпадать со всеми migration files 0001–0010; wildcard, generic unknown migrations и ослабление strict exact-set `Verify` запрещены. Release manifest SHALL разрешать G1.5 0001–0010 только после PG17 compatibility acceptance, проверяющей в том числе v1 route/aggregate behavior с migration 0010. v2 admission SHALL включаться только отдельным явным rollout после deployment G1.5 и прохождения release gate. Неизвестный intent/protocol SHALL блокироваться, не трактоваться как v1.

Rollback на v1/G1.5 SHALL быть допустим только до первой durable v2 admission/intent. После неё разрешены только G2 roll-forward/recovery, поскольку v1 не обрабатывает v2 intent. Feature flag сам по себе не является bridge и не доказывает отсутствие durable v2 writes. Compatibility preparation SHALL NOT применять migration к пользовательской DB или считаться production deployment.

#### Scenario: v2 не допускается до bridge release gate
- **WHEN** v2 admission пытаются включить до deployment G1.5 и прохождения release gate
- **THEN** v2 admission остаётся выключенным, protocol v1 сохраняет текущее поведение

#### Scenario: G1.5 verifier и release manifest точно покрывают 0001–0010
- **WHEN** PG17 acceptance проверяет migration catalog G1.5
- **THEN** binary schema verifier и release manifest принимают ровно имеющиеся файлы 0001–0010, а пропущенная, лишняя или неизвестная migration блокирует acceptance; `Verify` остаётся strict exact-set

#### Scenario: additive migration 0010 сохраняет v1 поведение G1.5
- **WHEN** G1.5 выполняет v1 approve/rerun/direct-start и прежние route/aggregate сценарии на schema0010
- **THEN** verified-resolution storage доступно по назначению, а прежнее поведение маршрута/агрегата, worker/activity payload и replay старой Temporal history сохраняются

#### Scenario: manifest не разрешает G1.5 до PG17 acceptance
- **WHEN** release manifest формируется до успешного PG17 compatibility acceptance G1.5
- **THEN** он не разрешает G1.5 catalog 0001–0010 для release

#### Scenario: Неизвестный v2 intent блокирует v1
- **WHEN** v1 worker получает неизвестный или v2 intent
- **THEN** intent блокируется и не интерпретируется как v1 execution

#### Scenario: После durable v2 записи возможен только G2 recovery
- **WHEN** в системе уже есть первая durable v2 admission/intent
- **THEN** rollback на v1/G1.5 запрещён; допускаются только G2 roll-forward/recovery

### Requirement: Durable намерение исполнения

Владелец SHALL атомарно сохранять owner intent до ответа HTTP о принятии. Intent SHALL включать `CommandID`, owner-level `attempt_id`/generation, неизменяемый snapshot параметров и `LeadTarget` (`explicit`, `unspecified`, `unknownLegacy`). Ответ HTTP означает durable admission, не исполнение. Единственный dispatcher выполняет только технический старт/retry этого intent; автоматический бизнес-rerun запрещён.

Команды approve, rerun и прямой HTTP endpoint SHALL проходить через общий owner admission command и сохранять intent и outbox entry одной транзакцией. При принятии HTTP SHALL отвечать `202` только после commit и возвращать `request_id`, отдельный стабильный `workflow_id`, `attempt_id`, generation и текущее состояние. `request_id` и `workflow_id` SHALL NOT совпадать по смыслу: первый идентифицирует заявку, второй — Temporal WorkflowID. Temporal server SHALL назначать RunID после старта; он не генерируется при admission. Изменение параметров при повторе `CommandID` SHALL давать conflict. Query текущего состояния SHALL читать durable owner storage и оставаться доступным при outage Temporal. Tenant authorization SHALL применяться к query и commands. Неизвестный request ID и ID заявки, принадлежащей другому tenant, SHALL давать одинаковый HTTP `404`, чтобы не раскрывать существование заявки. Различимые ошибки целевого request (включая конфликт `CommandID` и admission rejection) SHALL формироваться только после разрешения владельца и tenant. Cookie-authenticated mutation SHALL требовать CSRF.

#### Scenario: Intent сохранена до старта Temporal

- **WHEN** intent закоммичена, но Temporal недоступен
- **THEN** HTTP возвращает queued/pending после commit, а dispatcher позже запускает тот же attempt с тем же snapshot

#### Scenario: Embedding preparation завершилась ошибкой

- **WHEN** подготовка embeddings после durable admission временно завершается ошибкой
- **THEN** preparation видимо retryable для того же неизменяемого versioned/fingerprinted snapshot, без повторного admission или создания нового attempt

#### Scenario: Durable HTTP admission и outage Temporal

- **WHEN** HTTP command принят, owner transaction закоммичена, но Temporal недоступен
- **THEN** HTTP отвечает `202` с `request_id`, `workflow_id`, `attempt_id`, generation и durable текущим состоянием; ответ не утверждает, что workflow стартовал или завершился

#### Scenario: Query состояния при недоступном runtime

- **WHEN** Temporal runtime недоступен, а клиент запрашивает текущее состояние заявки в разрешённом tenant scope
- **THEN** query возвращает состояние из durable owner storage без обращения к Temporal как обязательному источнику ответа

#### Scenario: Неизвестная и cross-tenant заявка неразличимы

- **WHEN** HTTP query или command обращается к неизвестному request ID либо к существующей заявке другого tenant
- **THEN** оба случая возвращают одинаковый `404` и не раскрывают существование чужой заявки

#### Scenario: Ошибка цели после проверки владельца и tenant

- **WHEN** request и его tenant успешно разрешены, после чего обнаружены конфликт `CommandID` или admission rejection
- **THEN** система возвращает соответствующую ошибку цели, не смешивая её с `404` для неизвестного или cross-tenant request

#### Scenario: CSRF на cookie-authenticated mutation

- **WHEN** cookie-authenticated mutation не содержит валидный CSRF token
- **THEN** доступ отклоняется и новая intent/outbox entry не создаётся

### Requirement: Идентичность и единственный владелец старта

`request_id` SHALL стабильно идентифицировать бизнес-заявку. `workflow_id` SHALL быть стабильным Temporal WorkflowID, полученным из заявки; это не Temporal RunID. Каждая принятая попытка SHALL иметь owner-level `attempt_id`, generation и `command_id`. Temporal server-assigned `run_id` SHALL храниться отдельно и связываться с попыткой только после проверки request/attempt/generation/command/параметров в payload. `AlreadyStarted` SHALL быть подтверждением только если candidate identity совпадает с сохранённым attempt payload; один attempt SHALL NOT получить второй принятый `run_id`. Неоднозначность SHALL оставаться `UNKNOWN`, не успехом. Новый бизнес-запрос при ACTIVE/UNKNOWN отклоняется admission, не ставится в очередь. HTTP SHALL NOT иметь синхронный fast path.

#### Scenario: Повтор технического старта той же попытки

- **WHEN** dispatcher повторяет старт после неоднозначного ответа Temporal
- **THEN** он подтверждает `AlreadyStarted` только при совпадении execution identity и не создаёт вторую попытку

#### Scenario: AlreadyStarted с чужой identity

- **WHEN** Temporal возвращает AlreadyStarted, но candidate payload не соответствует принятому attempt_id/generation/command_id
- **THEN** запуск не связывается с attempt, состояние остаётся UNKNOWN/blocked, и этот attempt не запускается с другим run_id

#### Scenario: Новый запрос при предыдущем ACTIVE/UNKNOWN

- **WHEN** владелец получает бизнес-запрос, пока предыдущая попытка/исполнение ACTIVE или UNKNOWN
- **THEN** admission отклоняется до создания следующего attempt; queued бизнес-запроса нет

#### Scenario: Новый attempt после terminal

- **WHEN** предыдущая попытка terminal и доменный переход допустим
- **THEN** новый attempt получает новые generation/attempt_id и distinct CommandID, а Temporal назначает для его старта отдельный server run_id

### Requirement: Сохранение параметров цели и безопасное legacy resolution

Владелец SHALL сохранять `LeadTarget` вместе с intent каждой попытки с различием `explicit`, `unspecified` и `unknownLegacy`; отсутствующее legacy поле SHALL NOT преобразовываться в `0`. Target resolution и runtime resolution SHALL быть раздельными append-only типами записей. Storage MAY записывать unverified runtime observation, но оно SHALL NOT устанавливать terminal state или открывать admission gate. Verified runtime resolution SHALL создаваться только trusted application reconciler/Temporal history verifier через отдельную узкую capability после проверки identity и полной history. Совпадение ID/runID, actor-строка или fingerprint сами по себе SHALL NOT считаться доказательством. Target resolution SHALL уточнять только состояние цели и SHALL NOT доказывать terminal execution. Ни одна запись сама по себе не инициирует бизнес-rerun.

Каждая resolution SHALL иметь монотонную revision с expected-revision CAS и детерминированным current pointer либо эквивалентной `supersedes`-цепочкой. Устаревшая и конкурентная запись SHALL конфликтовать; выбор current SHALL NOT зависеть от `ResolvedAt`, и равные timestamps не создают неоднозначность. Evidence fingerprint SHALL иметь один канонический формат: lowercase SHA-256 hex от определённого безопасного evidence либо явно указанного content-hash metadata. Он SHALL NOT служить аутентификацией или proof. Memory и PostgreSQL SHALL соблюдать одинаковые format/CAS/read/conflict semantics.

База данных SHALL запрещать UPDATE/DELETE resolution rows и отклонять TRUNCATE через BEFORE TRUNCATE trigger; неполные actor/evidence/timestamp запрещены. Trigger не является защитой от владельца таблицы или superuser; если модель миграций поддерживает runtime DB role, ей SHALL быть запрещены TRUNCATE и DDL. Добавление target resolution или unverified runtime observation SHALL NOT снимать ACTIVE/UNKNOWN runtime admission gate; только verified runtime evidence от уполномоченного verifier-а и безопасный доменный переход могут разрешить следующую команду. SQL migration SHALL NOT выводить target или attempt identity из старого snapshot, если данных для этого нет. Для legacy request без достаточной проверенной workflow history admission SHALL оставаться blocked.

#### Scenario: Цель изначально не задана явно

- **WHEN** новая команда не содержит `LeadTarget`
- **THEN** хранилище сохраняет `unspecified`, отличимое от заданного нуля и неизвестного legacy

#### Scenario: Legacy target resolution не доказывает terminal

- **WHEN** оператор добавляет target resolution для legacy payload, не содержащего цель
- **THEN** append-only запись содержит actor/time/evidence, `LeadTarget` больше не считается неизвестным согласно resolution, но runtime execution остаётся UNKNOWN до независимого доказательства

#### Scenario: Непроверенное runtime observation не делает execution terminal

- **WHEN** обычный caller записывает runtime observation, даже с syntactically valid fingerprint или совпадающими request/workflow/run IDs
- **THEN** запись остаётся unverified, не меняет terminal state и не открывает admission

#### Scenario: Только verifier создаёт verified runtime resolution

- **WHEN** trusted reconciler/history verifier проверил identity и полную Temporal history и записывает resolution своей узкой capability
- **THEN** создаётся отдельная verified append-only запись; простое совпадение IDs либо actor/fingerprint string недостаточно

#### Scenario: Runtime resolution имеет отдельную запись

- **WHEN** runtime execution resolution устанавливает подтверждённый исход на основании достаточного evidence
- **THEN** создаётся отдельная append-only запись с actor/time/evidence; прежние target resolution не изменяются и не считаются доказательством исхода

#### Scenario: Revision CAS и deterministic current resolution

- **WHEN** concurrent writers предлагают resolution с одной expected revision или чтение встречает одинаковые timestamps
- **THEN** не более одного CAS succeeds, stale/conflicting writer получает conflict, а current resolution определяется revision/pointer/supersedes, не timestamp

#### Scenario: Канонический fingerprint и parity хранилищ

- **WHEN** memory и PostgreSQL сохраняют fingerprint одной и той же evidence content
- **THEN** обе реализации сохраняют lowercase SHA-256 в одном canonical format; fingerprint не принимается как authorization proof

#### Scenario: Append-only audit constraints

- **WHEN** выполняется UPDATE/DELETE/TRUNCATE resolution rows либо вставка без actor, timestamp или evidence
- **THEN** база данных отклоняет операцию; новая resolution добавляется только отдельной записью

#### Scenario: Target resolution не открывает runtime admission

- **WHEN** для заявки с ACTIVE/UNKNOWN runtime добавлена target resolution, но нет независимого terminal evidence
- **THEN** runtime остаётся ACTIVE/UNKNOWN и новая бизнес-команда отклоняется до записи intent

#### Scenario: Legacy request без проверенной истории

- **WHEN** у существующей заявки отсутствуют target/attempt в snapshot и проверенная legacy workflow history не даёт достаточного evidence
- **THEN** migration не синтезирует target/attempt из snapshot, заявка остаётся unknown/blocked и admission запрещён

### Requirement: Терминальный исход после ограниченной компенсации и timeout

Необратимый сбой SHALL приводить к durable terminal intent с исходной причиной и идентичностью попытки. Workflow SHALL выполнять компенсацию с ограниченными числом попыток и временем; невозможность применить решение SHALL оставлять durable pending-состояние, а не бесконечную activity или ложный успех. После hard timeout независимый reconciler SHALL постранично сверять Temporal Describe/history и идемпотентно применять подтверждённый terminal intent. Неизвестный исход, отсутствие достаточной legacy-истории или неподтверждённая идентичность SHALL сохраняться как blocked/unknown до явного разрешения.

#### Scenario: Workflow превышает предел компенсации

- **WHEN** владелец недоступен дольше ограниченного бюджета компенсации
- **THEN** workflow прекращает бесконечные повторы, durable terminal intent остаётся доступной reconciler, а заявка не объявляется успешно завершённой

#### Scenario: Workflow убит hard timeout

- **WHEN** Temporal завершает workflow hard timeout до применения terminal intent к агрегату
- **THEN** независимый reconciler обнаруживает marker в истории и применяет доменное терминальное решение с первоначальной причиной

#### Scenario: История не позволяет подтвердить результат

- **WHEN** reconciler обнаруживает отсутствующую или недостаточную legacy history
- **THEN** исход остаётся явно unknown/blocked, новый запуск запрещён, и система не подменяет исход на success или `LeadTarget=0`

### Requirement: Fencing мутаций попыткой-владельцем

Каждая локальная мутация заявки, результат исполнения и прикрепление артефакта SHALL предъявлять generation/attempt identity. Владелец SHALL отклонять устаревшую попытку без изменения текущего состояния и без публикации её результата как результата актуальной попытки.

#### Scenario: Поздняя activity старого поколения

- **WHEN** activity старого attempt пытается сохранить результат после создания нового поколения
- **THEN** запись отклоняется как устаревшая, новая заявка и её артефакты не изменяются

### Requirement: Неизменяемая проекция handoff и receipts

Перед передачей результатов SHALL сохраняться attempt-scoped manifest ожидаемых объектов, receipts и projection доставки. Повтор исполнения SHALL NOT смешивать receipts/результаты старого поколения с новым. Неизвестный receipt SHALL оставаться неизвестным и SHALL NOT считаться доставленным.

#### Scenario: Rerun после handoff

- **WHEN** повторный attempt начат после передачи manifest предыдущей попытки
- **THEN** исходный manifest и receipts остаются привязанными к прежнему поколению, а новая проекция не объединяет их с новыми результатами

#### Scenario: Результат доставки не подтверждён

- **WHEN** receipt отсутствует или результат доставки неизвестен
- **THEN** проекция сохраняет unknown/pending и не показывает доставку как успешную

### Requirement: Совместимость хранилищ и replay

Новая схема SHALL добавляться миграцией без потери существующих данных, включая append-only execution_resolution с audit actor/time/evidence. Memory и PostgreSQL SHALL соблюдать один функциональный контракт intent/attempt/fencing/receipts, но PostgreSQL проверяется отдельным isolated suite. Изменение workflow SHALL сохранять replay-совместимость с уже записанными историями Temporal. Live user compose использовать запрещено; отдельный изолированный PostgreSQL test допустим. Replay/history Temporal в изолированной среде остаётся обязательной проверкой до выполнения соответствующей задачи; claims о её выполнении без результата запрещены.

#### Scenario: Применение additive migration

- **WHEN** миграция запускается на предыдущей поддерживаемой схеме с существующими заявками
- **THEN** прежние записи сохраняются, а новые поля читаются с явным legacy/unknown состоянием

#### Scenario: Миграция 0008 обеспечивает защиту старой и новой таблиц resolutions

- **WHEN** migration 0008 применяется поверх неизменённых миграций 0005–0007 и существующих заявок/resolution rows
- **THEN** UPDATE, DELETE и TRUNCATE отклоняются на legacy `execution_resolution` (если таблица сохранена) и `execution_resolution_revision`; revision ссылается на request по `(request_id, tenant_id)` с `ON DELETE RESTRICT`, поэтому удаление родительской заявки запрещено, а существующие данные не удаляются и не переписываются

#### Scenario: Существующая resolution не проходит проверку tenant FK

- **WHEN** до создания составного FK обнаружена revision без родительской заявки или с несовпадающим tenant
- **THEN** migration завершается ошибкой до добавления FK, не отключает ограничение и не удаляет/изменяет исходные строки

#### Scenario: Повтор старой Temporal history

- **WHEN** прежняя история workflow воспроизводится новой версией worker
- **THEN** replay завершается без nondeterminism и прежние payload без новых полей не трактуются как новые значения по умолчанию

#### Scenario: Общий контракт memory и отдельный PostgreSQL suite

- **WHEN** общий contract suite выполняется против memory, а отдельный isolated suite — против PostgreSQL
- **THEN** обе реализации дают одинаковые результаты для идемпотентности, fencing, resolutions и receipts; пользовательский compose не затрагивается

#### Scenario: Конкурентная memory UoW publication

- **WHEN** детерминированный unit test через start/attempt barriers конкурентно запускает UoW и всех прямых writers/readers memory repository/journal
- **THEN** общий repository gate сериализует полный lifecycle clone/callback/commit, все UoW и writers; читатель наблюдает только состояние до либо после commit. Проверка не опирается на фиксированную задержку или scheduling assertion.

#### Scenario: Отмена context перед callback

- **WHEN** context отменён после прохождения queue gate, но до начала callback
- **THEN** UoW не вызывает callback и не публикует состояние; ожидание gate по возможности прекращается при отмене context

#### Scenario: Вложенный UoW того же repository

- **WHEN** callback вызывает `WithinTransaction` повторно для того же repository
- **THEN** вложенный вызов promptly возвращает явную typed error и не приводит к deadlock или потере claim

#### Scenario: Callback не выполняет внешние I/O

- **WHEN** выполняется UoW callback
- **THEN** callback ограничен транзакционной работой с repository и не вызывает внешние системы или сетевые/Temporal I/O

### Requirement: Паритет lease и переходов attempt

PostgreSQL SHALL вычислять lease expiry и проверять её по `clock_timestamp()` базы данных; caller-provided clock SHALL NOT определять claim, renew или разрешение мутации. Memory SHALL применять единый внедрённый clock. Обе реализации SHALL соблюдать одну машину переходов attempt: `queued → claimed → started → terminal`, из `claimed`/`started` разрешены `unknown` и `terminal`; `unknown` и `terminal` не могут быть повторно запущены или переведены в другое состояние этим портом.

#### Scenario: Clock клиента расходится с часами БД

- **WHEN** dispatcher передаёт часы, отличающиеся от часов владельца PostgreSQL
- **THEN** срок и проверка lease определяются только `clock_timestamp()` БД, и ответ claim содержит фактический lease expiry

#### Scenario: Одинаковые переходы состояния в хранилищах

- **WHEN** suite пытается перевести terminal в unknown или unknown обратно в started
- **THEN** memory и PostgreSQL одинаково отклоняют переход без изменения состояния
