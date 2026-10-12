## Context

См. `proposal.md`. Текущая схема связывает ручной rerun со сбросом заявки и повторным вызовом `StartFulfillment` из HTTP. Это не атомарно: при `validated → processing` старт может быть проигнорирован, а параметры предыдущего исполнения не являются durable intent. `FailRequest` использует бесконечный retry, который не ограничивает зависание при недоступности владельца. Значение `LeadTarget` в старых исполнениях может отсутствовать; считать отсутствие нулём нельзя.

## Goals / Non-Goals

**Goals:** атомарно фиксировать команду и параметры попытки; иметь одного владельца запуска; восстанавливать инфраструктурный старт без повторения бизнес-команды; устанавливать терминальное доменное состояние после hard timeout; не смешивать поколения попыток и handoff-результаты.

**Non-Goals:** автоматический бизнес-rerun; новый широкий операторский интерфейс; подмена неизвестного состояния значением по умолчанию; завершение задачи без работающих PostgreSQL/Temporal-проверок.

## Decisions

### 1. Durable owner intent и единственный dispatcher (B2)

Команды approve/rerun и прямой workflow HTTP endpoint сходятся в одни owner admission commands и атомарно записывают intent + outbox entry в хранилище владельца: уникальный `CommandID`, owner-level `attempt_id` и generation, неизменяемый snapshot параметров и `LeadTarget` с тремя состояниями: `explicit`, `unspecified`, `unknownLegacy`. `request_id` — идентификатор бизнес-заявки; `workflow_id` — отдельный стабильный Temporal WorkflowID, производный от заявки; ни один из них не является Temporal RunID. Temporal server-assigned `run_id` существует только после успешного старта и хранится отдельно, связываясь с attempt после проверки payload/history. Один attempt допускает не более одного принятого `run_id`. Повтор с тем же `CommandID` и теми же параметрами идемпотентен; несовпадающие параметры — конфликт. HTTP подтверждает durable commit (`202`) и не вызывает Temporal напрямую. Единственный автоматический технический dispatcher/reconciler выполняет повторы только того же принятого attempt, не создавая бизнес-rerun.

Семантический payload для повтора канонизируется детерминированно и сравнивается полностью: command kind, reason, target state/value, request version, offer ID/revision и fingerprint неизменяемого offer snapshot, а также остальные lifecycle-affecting параметры. Tenant/request scope проверяются отдельно. Replay существующего `CommandID` выполняется до lifecycle gate и возвращает прежнюю admission receipt; изменение любого семантического поля конфликтует.

Admission нового бизнес-запроса с новым `CommandID` отклоняется, если предыдущая попытка/исполнение `ACTIVE` или `UNKNOWN`; intent/outbox бизнес-generation не записывается и запрос не сохраняется в очереди. Исключение — идемпотентный replay уже сохранённого `CommandID` с идентичными параметрами: он возвращает существующий результат и не создаёт intent, attempt или generation. Новое поколение после доказанного terminal требует нового `CommandID` и допустимого перехода домена. После принятого intent подготовка embeddings выполняется durable отдельно по неизменяемому versioned/fingerprinted input snapshot. Ошибка подготовки видима и повторяема для того же snapshot, не отменяя и не изменяя принятую команду.

**Альтернативы:** A — продолжить запуск синхронно после доменного перехода; проще, но сохраняет окно потери старта и конкурирующие пути. Выбрано B по голосованию 2:1. Вариант «HTTP синхронно ждёт Temporal» также отклонён: сбой ответа после успешного старта провоцирует неоднозначный повтор.

### 2. Одна стабильная WorkflowID, идентичность каждого запуска и проверяемый старт

`workflow_id` остаётся стабильным для заявки. Каждая попытка имеет owner-level `attempt_id`, монотонное generation и `command_id`; `run_id` назначает Temporal server, он не является идентификатором intent. При старте dispatcher передаёт attempt identity в payload, проверяет кандидата `AlreadyStarted` по этому payload/history и сохраняет server `run_id` только при полном совпадении. Один attempt не может быть принят с двумя разными RunID; несоответствие/нехватка доказательств — `UNKNOWN`, не успех. После завершения attempt новый запуск требует нового поколения и distinct `CommandID`. Legacy resolution не создаёт доказательство terminal.

### 3. Терминальная компенсация ограничена, recovery независим

Workflow ограниченно повторяет доменную компенсацию. До/в рамках компенсации он записывает `TerminalIntent` в Temporal history с исходной причиной и attempt identity. Отдельный worker reconciler постранично сверяет Describe/history и применяет terminal intent идемпотентно. Target resolution и runtime resolution — разные типы данных в append-only журнале. Storage может записать unverified runtime observation, но он не меняет terminal/admission state. Verified runtime resolution создаёт только доверенный application reconciler/Temporal history verifier через отдельную узкую capability после проверки identity и полной истории. Совпадение request/workflow/run ID, actor-строка или fingerprint сами по себе не являются доказательством. Target resolution никогда не служит terminal evidence. Временная ошибка оставляет reconciler pending; неизвестное состояние блокирует admission следующей попытки.

Resolution имеет монотонную revision: writer передаёт expected revision, CAS атомарно добавляет запись и обновляет детерминированный current pointer (или эквивалентную supersedes-цепочку). Устаревшая/конкурирующая запись конфликтует; выбор current не сортирует по `ResolvedAt`, одинаковые timestamps допустимы. Evidence fingerprint — lowercase SHA-256 hex по определённому безопасному evidence либо явно заданному content-hash metadata; формат одинаков для memory/PostgreSQL. Fingerprint не является credential, identity proof или авторизацией.

### 4. Fencing по attempt и неизменяемый handoff

Все owner-side команды записи, артефакты и поздние workflow activities предъявляют generation/attempt identity. Устаревшая попытка получает конфликт и не меняет текущую заявку, не публикует результат и не присоединяет артефакт к новой попытке. До handoff сохраняются manifest ожидаемых объектов, receipts по результатам и delivery projection, привязанные к исходной попытке; rerun читает неизменяемый handoff-срез и не смешивает его с новыми результатами. Неизвестный receipt остаётся неизвестным и не считается успешной доставкой.

### 5. Аддитивная схема и parity адаптеров

Ввести additive migration для intent/попыток и versioned append-only resolutions: unverified observations и verified resolutions различаются, expected revision/CAS + current pointer (либо supersedes chain) дают единственный детерминированный current resolution. DB блокирует UPDATE/DELETE и `BEFORE TRUNCATE` отклоняет TRUNCATE. Для уже выпущенных 0005–0008 исправления добавляются следующими миграциями без переписывания прежних: 0008 обеспечивает TRUNCATE triggers и составной FK `(request_id, tenant_id)` на request с `ON DELETE RESTRICT`; 0009 проверяет в каталоге PostgreSQL фактическое событие trigger (`BEFORE STATEMENT TRUNCATE`), функцию-обработчик и включённое состояние, пересоздавая trigger с неверной конфигурацией. До создания FK миграция проверяет имеющиеся строки на отсутствующего родителя и tenant mismatch и останавливается без удаления/исправления строк. Триггеры не защищают от table owner/superuser. Роли приложения и инфраструктурные grants принадлежат отдельному change `service-per-context`, не этому изменению. Memory/PostgreSQL имеют одинаковые CAS, fingerprint и conflict semantics; PostgreSQL тестируется отдельным isolated suite. Legacy snapshot decode сохраняет различие отсутствующего target от нуля. Новые workflow payload versioned/optional; старые histories replay без недетерминированных внешних чтений.

### 8. Источник времени, admission gate и одинаковая машина состояний

Lease — время-владельческая операция: PostgreSQL вычисляет `lease_until` и expiry по `clock_timestamp()`; memory использует один внедрённый clock. Переходы attempt одинаковы: `queued → claimed → started → terminal`; из `claimed`/`started` допустимы `unknown`/`terminal`. Ни один новый бизнес intent не принимается, пока предыдущая попытка/исполнение `ACTIVE` или `UNKNOWN`; поэтому следующая попытка не ожидает в очереди. Reconciler/explicit runtime resolution может снять блок только по установленному доказательству и безопасному переходу.

Memory UoW сериализует полный lifecycle clone → callback → prepare → commit одним общим эксклюзивным context-aware gate/channel semaphore для repository, journal и всех UoW. Все прямые доступы context-aware и проходят через этот gate; после него не должно быть скрытого ожидания на `sync.RWMutex`. Прямые writers и читатели подчиняются тому же gate, поэтому публикация repository/outbox атомарна относительно всех доступов. Отмена до acquire, при ожидании и после acquire/до callback возвращает cancellation без callback и commit. При завершении, ошибке или panic gate освобождается. Commit повторно проверяет context непосредственно перед публикацией; после этой проверки публикация атомарна и не может завершиться ошибкой.

Callback получает derived transaction-scoped context и передаёт его во вложенные операции того же repository. Если вложенный `WithinTransaction` того же repository получает этот `txCtx` или производный context, он promptly возвращает typed `ports.ErrNestedTransaction`: повторного захвата и reentrant lock нет. Callback, отбросивший переданный context и использующий `context.Background()` или исходный context для повторного вызова того же repository, нарушает API-контракт. Надёжно обнаружить такое generic Go goroutine nesting нельзя.

Callback выполняет только транзакционные доменные вычисления и операции tx ports; внешний I/O (включая сетевые и Temporal-вызовы) запрещён. Это ограничение проверяется spies и review call graph, но callback нельзя механически sandbox-ить. Отмена во время уже начавшегося callback не гарантирует остановку некооперативного callback; проверка перед commit не публикует отменённую транзакцию. Тесты используют установленный до запуска тестов hook `Contended`/`Acquired` либо injected fake gate на адаптерах, чтобы наблюдать настоящую конкуренцию и acquire; timeout служит только watchdog. Hook не должен вводить нежелательный production seam. PostgreSQL использует собственную транзакционную модель и тестируется независимо; memory gate не заменяет PostgreSQL проверки.

### 6. Реальная проводка без заглушек

Dispatcher и reconciler запускаются настоящими worker roles с Temporal client и владельческим repository. Ни периодический no-op, ни in-memory production queue, ни фиктивное подтверждение `AlreadyStarted` не закрывают задачу. Проверки проходят против реального PostgreSQL и Temporal test server/environment, включая replay прежних histories.

### 7. Архитектурные документы

Каноническая модель относит `execution-intent-accepted` к уже объявленному агрегату `request-execution` с root `execution-intent`; владелец агрегата и события — bounded context `request-fulfillment`. Это отдельное событие от `request-execution-failed`, которое остаётся событием агрегата `request`. Одна транзакция/UoW может сохранять переход `request`, intent и outbox атомарно, но транзакционная атомарность не меняет владельца события.

В текущем коде есть доменный тип `ExecutionIntent`, persistence для intent/attempt и application handler admission, который публикует `execution-intent-accepted`. Полной обёртки агрегата `RequestExecution` нет; не описывать её как уже реализованную. В рамках change задача 6.1 согласует каталог, `docs/domain-model.md` и диаграмму владения с вышеуказанным разделением, не вводя новый aggregate root или bounded context. Документы различают lifecycle `request`, состояние attempt и доказательность runtime resolution; отсутствующие dispatcher/verifier/handoff части остаются целевой моделью, а не заявленным фактом реализации.

Пока отсутствует доверенный verifier полной Temporal history, новый бизнес rerun для v2 не принимается. HTTP путь возвращает явный blocked/conflict и не создаёт новый CommandID или intent; v1 G1.5 поведение остаётся неизменным.

### 9. HTTP admission и чтение состояния

Approve, rerun и прямой HTTP endpoint используют общий owner command/admission путь и одну транзакцию intent + outbox. Принятый HTTP запрос отвечает `202` только после commit, с идентификаторами заявки/Workflow/attempt и текущим состоянием; это подтверждает durable admission, не старт или успех workflow. Same-`CommandID` replay возвращает существующую admission; изменение параметров с тем же ID — конфликт. NEW command при ACTIVE/UNKNOWN отклоняется до записи. Для request, которого нет или который принадлежит другому tenant, HTTP возвращает одинаковый `404`; до owner/tenant resolution ответ не раскрывает, существует ли чужой ID. Ошибки цели (например, conflict или admission rejection) различаются только после успешного разрешения owner и tenant. Cookie-authenticated mutations требуют CSRF. Query текущего состояния читает owner storage и не зависит от доступности Temporal. Автоматический технический dispatcher/reconciler один; бизнес-rerun автоматически не выполняется.

### 10. ExecutionProtocol v1/v2 и совместимый rollout

Протокол исполнения явно версионируется как `ExecutionProtocol v1` и `ExecutionProtocol v2`. Migration 0010 добавляет verified-resolution storage; G1.5 bridge исполняет v1 поведение для полного request-fulfillment migration catalog 0001–0010: approve, rerun, direct-start, прежний route/aggregate behavior, worker/activity payload и уже записанные Temporal histories. Это новый точный rollback target, не исходный G1 (0001–0004) и не промежуточный verifier на 0001–0009. Binary schema verifier и release manifest G1.5 обязаны соответствовать всем migration files 0001–0010; wildcard, generic unknown migrations и ослабление strict `Verify` запрещены. Release manifest разрешает этот exact набор только после PG17 compatibility acceptance, включая проверку additive 0010 относительно поведения v1. v2 admission/intent не является поведением по умолчанию и включается только явным rollout после deployment G1.5 и пройденного release gate.

До первой durable v2 admission/intent допустим rollback только на G1.5. После первой durable v2 записи граница rollback закрыта: v1 не умеет обрабатывать v2 intent, поэтому разрешены только G2 roll-forward/recovery; неизвестный legacy/v2 intent блокируется, а не интерпретируется как v1. Feature flag не эквивалентен bridge и сам по себе не доказывает отсутствие durable v2 writes. Тесты bridge и protocol compatibility используют disposable DB/history; пользовательская DB и production deployment не затрагиваются.

## Risks / Trade-offs

- **Сбой между запуском Temporal и записью результата старта** → dispatcher сверяет execution/history по identity; неоднозначность переводит intent в pending/unknown и запрещает конкурирующий запуск.
- **Потеря старого `LeadTarget`** → сохранять `unknownLegacy`; вести append-only target resolution с audit evidence. Она не устанавливает execution terminal; runtime resolution отдельна.
- **Повторный запрос при активной/неизвестной попытке** → admission отклоняется явно, без сохранения в очереди.
- **Hard timeout/недоступность Temporal** → ограниченные повторы с backoff, durable terminal intent и отдельный reconciler; отсутствие подтверждения остаётся pending, не success.
- **Поздние записи старой попытки** → обязательная проверка generation на каждой owner-side мутации, result и artifact attach.
- **Повтор handoff и смешение доставки** → immutable manifest и attempt-scoped receipts/projection; unknown receipt не трактуется как delivered.
- **Replay несовместимость** → Temporal version markers/optional payload decoding и replay tests на сохранённых прежних histories; нельзя закрывать без этой проверки.
- **Ложные зелёные проверки от doubles** → критические тесты реально запускают PostgreSQL и Temporal; mocks допустимы только для unit-тестов, не для критериев интеграции.

## Migration Plan

1. Добавить additive migrations и backward-compatible decode; миграции 0008/0009 применить поверх неизменённых 0005–0007, проверить сохранение данных, целостность составного tenant FK и фактическую конфигурацию triggers на старой и текущей таблицах. Предварительная проверка существующих строк должна завершать миграцию ошибкой при потерянном/несовпадающем родителе, не удаляя и не переписывая данные. Старые snapshot без доказуемых target/attempt не обогащать выведенными значениями: сохранить unknown/blocked до проверки истории.
2. Внедрить owner intent/attempt и append-only resolution storage; покрыть memory lock и отдельно PostgreSQL.
3. Перевести HTTP approve/rerun на durable admission; отклонять запросы при ACTIVE/UNKNOWN; подключить единственный dispatcher и убрать sync start path.
4. Внедрить bounded compensation, Temporal-history marker и работающий reconciliation worker.
5. Добавить fencing, immutable handoff manifest/receipts/projection; провести legacy resolution/replay проверки.
6. Обновить каталог/доменную модель/диаграммы; выполнить реальную интеграционную проверку PostgreSQL + Temporal, `make check`, archlint и strict validation.

Откат не удаляет новую схему. Старые бинарники не должны интерпретировать queued intent как завершённый запуск; при поэтапном развёртывании новый dispatcher включается после миграции и до переключения HTTP на очередь. Если безопасный совместный rollout не обеспечен, выкладка останавливается, intent сохраняются для нового кода.
