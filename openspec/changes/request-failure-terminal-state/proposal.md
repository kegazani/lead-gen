## Why

Сбой Temporal сейчас может оставить заявку в `processing`: синхронные approve/rerun теряют запуск, rerun теряет исходный `LeadTarget`, а безлимитная компенсация `FailRequest` может бесконечно оставить процесс в ожидании. Нужно сохранить намерение запуска до подтверждённого старта и ограничить компенсацию, не создавая повторное бизнес-исполнение автоматически.

## What Changes

- Перейти на выбранное голосованием решение B2: durable owner execution intent с `CommandID`, неизменяемым snapshot параметров и явным состоянием `LeadTarget`: `explicit`, `unspecified` или `unknownLegacy`. HTTP принимает intent после durable commit; один технический dispatcher запускает тот же attempt после восстановления инфраструктуры. Удалить конкурирующий синхронный fast path.
- Развести идентичности: `request_id` — стабильная бизнес-заявка; `workflow_id` — стабильный Temporal WorkflowID, полученный из заявки; `attempt_id`/generation/`command_id` — owner-level попытка конкретного принятого бизнес-запуска; Temporal server-assigned `run_id` хранится и проверяется отдельно. Один attempt не может получить второй принятый Temporal RunID. Новый запуск после terminal — только новая попытка и отдельный `CommandID`; автоматического бизнес-rerun нет.
- Отклонять admission нового бизнес-запроса, пока предшествующая попытка/исполнение `ACTIVE` или `UNKNOWN`; не ставить такой запрос в очередь. Admission разрешён только после terminal и допустимого перехода домена.
- Выполнять embeddings как durable preparation после принятого intent, используя неизменяемый versioned/fingerprinted input snapshot. Сбой подготовки видим и допускает повтор подготовки того же snapshot/version, не меняя принятую бизнес-команду.
- Сохранить причину сбоя в durable `TerminalIntent`; workflow выполняет ограниченную компенсацию и сохраняет marker в Temporal history. Независимый ограниченный reconciler постранично сверяет описание/историю завершившихся workflow и применяет терминальное доменное решение даже после hard timeout.
- Ограничить legacy recovery: отсутствующий `LeadTarget` не становится `0`; target resolution и runtime execution resolution — отдельные append-only `execution_resolution` записи с типом, actor, временем и evidence. Разрешение target никогда само по себе не доказывает terminal исполнения. Неизвестный результат reconciler не выдаёт за успех и блокирует новую попытку.
- Развести наблюдение и доказательство: storage может append-only записать непроверенное runtime observation, но такая запись не делает execution terminal и не открывает admission. Только доверенный application reconciler/Temporal history verifier с отдельной узкой capability может записать verified resolution после проверки identity и полной истории. Совпадения ID/runID и fingerprint/actor-строки сами по себе доказательством не являются; target resolution никогда не делает runtime terminal.
- Версионировать resolution append-only: предпочтительная модель — монотонная expected revision с CAS и детерминированным указателем на текущую запись/цепочкой supersedes; конкурентные/устаревшие записи конфликтуют, выбор не зависит от `ResolvedAt`. Evidence fingerprint — каноническая lowercase SHA-256-строка по определённому безопасному evidence либо явно указанному content hash; fingerprint не является авторизацией. Memory/PostgreSQL обязаны иметь одинаковую семантику и формат.
- Оградить локальные мутации, результаты и артефакты попыткой-владельцем; сохранить manifest, receipts и projection доставки между handoff и rerun, чтобы результаты старой попытки не смешивались с новой.
- Добавить аддитивную миграцию, включая append-only журнал resolutions и защиту от UPDATE/DELETE/TRUNCATE (TRUNCATE trigger; привилегии ролей, где миграционная модель это поддерживает). Триггеры не защищают от владельца таблицы/superuser — этот предел зафиксировать. Обеспечить одинаковый контракт memory/PostgreSQL, реальную проводку dispatcher/reconciler в worker и совместимость replay старых Temporal histories. Для memory синхронизировать полный lifecycle clone/callback/commit и все прямые writers общим lock; callback короткий и не выполняет внешних вызовов. PostgreSQL проверяется отдельно.
- Обновить каталог домена, `docs/domain-model.md` и связанные диаграммы отдельной задачей реализации этого change; planning не меняет общие документы.
- Развести `ExecutionProtocol` v1/v2 и rollout: G1.5 compatibility bridge сохраняет v1 approve/rerun/direct-start и прежние worker/activity payload; v2 — отдельное явное включение только после deployment bridge и release gate. Исходный G1 не является rollback target после схемы `0005–0009`; rollback eligibility заканчивается до первой durable v2 admission/intent, далее допустимы только G2 roll-forward/recovery. Feature flag сам по себе bridge не заменяет.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `request-fulfillment-execution`: фиксирует durable запуск и повторы, сохранение цели, fenced ownership, ограниченную терминальную компенсацию/reconciliation и безопасный handoff между поколениями попыток.

## Impact

- **Bounded contexts:** `request-fulfillment` — владелец `request`, execution intent, попыток, терминальных причин и handoff projection; затрагиваемый `request-intent` должен сохранять наблюдаемое состояние без раскрытия успеха при неизвестном исходе. Temporal — инфраструктура исполнения, не владелец доменной модели.
- **Код:** домен/app/порты/память/PostgreSQL/HTTP и workflow-адаптеры `backend/contexts/request-fulfillment`; регистрация dispatcher и reconciler в `backend/cmd/lead-gen`; контракты и тесты Temporal workflow.
- **Данные:** новая additive migration, append-only `execution_resolution` с audit actor/time/evidence и совместимость со снапшотами без `LeadTarget`.
- **Общие документы:** каталог домена, `docs/domain-model.md`, затронутые диаграммы — отдельная реализационная задача 6.1; эти файлы не являются planning scope данного редактирования.
