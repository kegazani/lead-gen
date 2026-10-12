# outreach-orchestration

Bounded-контекст `outreach-orchestration` (core): кампании, последовательности
касаний, черновики действий, решения оператора и публикация утверждённых действий
(`architecture/domain-catalog.yaml`, capabilities `outreach-approval`,
`outreach-sequences`, `outreach-channel-form`).

Это вертикальный срез: домен, прикладные use cases, порты, in-memory и
PostgreSQL-адаптеры, опубликованные контракты и работающая сборка ролей. Это
единственное место сборки своего контекста; бизнес-правил здесь нет.

## Домен (`internal/domain`)

Агрегат **`campaign`** (root `campaign`):

- сущности: `action-draft`, `operator-decision`, `sequence-definition`,
  `sequence-execution`;
- value objects: `campaign-window`, `sequence-step`, `action-channel`;
- события: `action-approved`, `campaign-state-changed`,
  `outreach-outcome-recorded`;
- политики: `operator-approval-required`, `outreach-scheduling`;
- инварианты: действие не исполняется без положительного решения оператора;
  решение относится к точной версии черновика (правка требует нового решения);
  завершённое участие не возобновляется.

Пакетный путь утверждения (`approval_batch.go`) строит состав пакетов из
ожидающих черновиков: однородность, минимальный размер и исключение рисковых —
правила домена, а не сборки.

Снимки состояния (`snapshot.go`) и конструкторы `Rehydrate*` восстанавливают
агрегат с проверкой инвариантов и не эмитят события.

Агрегат `call` в срезе **не реализован**.

## Приложение (`internal/app`)

- `command`: `CreateCampaign`, `DraftAction`, `ApproveDraft`, `RejectDraft`,
  `ReviseDraft`, `ExpireApproval`, `BuildBatch`, `ApproveBatch`,
  `DefineSequence`, `EnrollLead`, `RemoveLead`, `RecordOutcome`,
  `ResumeExecution`;
- `query`: `ApprovalQueue`, `DraftDecision`, `BatchQueue`, `SequenceHistory`,
  `SequenceState`;
- `scheduling`: движок последовательностей (`Engine.RunOnce`) — формирует черновики
  шагов и продвигает цепочки, не отправляя ничего без утверждения; роль `chains`;
- `publication`: прикладной сервис владельца контракта `approved-action`.

Все команды и проходы движка идут через `ports.UnitOfWork`: агрегат и его события
пишутся в одной транзакции (transactional outbox).

## Порты и адаптеры

- `ports`: `CampaignRepository`, `CampaignLister` (перечисление кампаний для
  роль `chains`), `DraftWindowRepository` (окно черновиков), `UnitOfWork`,
  `Outbox`, `Clock`, `AccessDecision`; потребляемые контракты — в `ports/contracts`,
  общий contract suite — в `ports/contracttest`.
- `adapters/persistence/inmemory` — репозиторий кампаний (тесты и dev-режим);
- `adapters/persistence/postgres` — PostgreSQL-репозиторий (`Save`, `FindByID`,
  `LoadDraftWindow`, `ListCampaignIDs`), unit-of-work с атомарной записью outbox
  и журнал outbox (`relay.Source`) с кодеком доменных событий; схема
  `outreach-orchestration`, миграции в `migrations/`;
- `adapters/outbox/inmemory` — unit-of-work и журнал outbox в памяти;
- `adapters/outbox/relay` — публикация подтверждённых событий (relay поверх
  `Source`/`Sink`);
- `adapters/access`, `adapters/idempotency`, `adapters/transport` — доступ,
  идемпотентность команд и выбор транспорта;
- `adapters/http` — HTTP-адаптер роли `api`.

## Персистентность

Хранилище подключается через `Params.Persistence` / `Config.Persistence`:

- нулевое значение — in-memory адаптеры (прежнее поведение тестов и dev);
- заполненное — PostgreSQL-адаптеры из публичной фабрики
  `NewPostgresPersistence(pool)` (репозиторий кампаний, unit-of-work, журнал
  outbox).

Сборка принимает **порты** (`CampaignStore`, `ports.UnitOfWork`, `relay.Source`),
поэтому точка входа выбирает хранилище без правки среза. Частично заданная
конфигурация отвергается: половина среза на in-memory, половина на PostgreSQL —
недопустимое состояние, а не молчаливый выбор.

Движок последовательностей сохраняет агрегат и события **атомарно через порт**:
на PostgreSQL `UnitOfWork.WithinTransaction` пишет снимок кампании и записи outbox
в одной транзакции БД, поэтому «изменение сохранено, но событие не ушло» и
«событие ушло, но изменение откатилось» невозможны. Журнал outbox переносит
подтверждённые записи через рестарт процесса.

Схема: `migrations/0001_campaign.sql` (кампания, черновики, решения),
`0002_sequence.sql` (сценарии и участия в снимке кампании),
`0003_outbox.sql` (журнал outbox).

### Проводка в `backend/cmd/lead-gen`

```go
if dsn := strings.TrimSpace(opts.supportGreenDSN); dsn != "" {
    pool, err := supportPool(dsn, outreachorchestration.ServiceID,
        "backend/contexts/outreach-orchestration/migrations")
    if err != nil {
        return nil, err
    }
    cfg.Persistence = outreachorchestration.NewPostgresPersistence(pool)
}
```

Пул принадлежит composition root'у (как у `audience`/`billing`), поэтому
`Service.Close()` не требуется: закрытие пула остаётся за точкой входа.

## Контракты

Опубликованные определения — модуль `lead-gen/contracts/outreach-orchestration`:
`approvedaction` (синхронный контракт с `message-delivery`), `callintent`,
`formsubmission`. In-proc адаптеры владельца — `approved_action.go`
(`approvedaction/inproc`).

## Роли и сборка (`service.go`, `outreach.go`)

- `api` — HTTP-сервер: `POST /campaigns`, `GET /campaigns`,
  `POST /campaigns/{id}/drafts`, `.../drafts/{draftID}/{approve|reject|revise}`,
  `GET .../decisions`, `GET .../batches`, `GET .../batches/composition`,
  `POST .../batches/approve`, `POST .../sequences`, `GET .../sequences`,
  `GET .../sequences/history`, `POST .../enrollments`,
  `POST .../enrollments/{executionID}/{remove|outcomes|resume}`;
- `worker` — публикация `approved-action` из outbox через relay; без заданного
  `Publisher` честно сообщает, что публикация не собрана;
- `chains` — движок последовательностей;
- `schedule` — каркас роли: тикер идемпотентных окон не реализован и об этом
  сообщается, а не маскируется.

## Осталось заглушкой

- Сетевой транспорт контракта `address-verification` — точка расширения: риск-скрининг
  пакетного пути собирается in-proc поверх контрактов владельцев
  (`delivery-target`, `suppression-policy`, `address-verification`). При отсутствии
  владельца сборщик честно отказывает: пакетное утверждение недоступно, а не идёт
  «без проверки».
- Роль `schedule`: тикер идемпотентных окон не реализован.
- Агрегат `call`: не реализован.
- Потребление `engagement-event` (реакции лида) не подключено; реакции приходят
  командой `RecordOutcome`.
- Сетевой транспорт синхронного контракта `approved-action` — точка расширения;
  в срезе связан только in-process.
- Продуктовая сборка получает PostgreSQL-адаптеры, когда точка входа задаёт
  `Config.Persistence`; без DSN `Build`/`New` собирают in-memory адаптеры (только
  dev) и предупреждают об этом.

## Проверка

```bash
gofmt -l backend/contexts/outreach-orchestration
go -C backend/contexts/outreach-orchestration build ./...
go -C backend/contexts/outreach-orchestration vet ./...
go -C backend/contexts/outreach-orchestration test -count=1 ./...
go -C tools/importlint run .
go -C tools/archlint run .

# PG-suite адаптеров (нужен Docker; строка подключения — LEADGEN_POSTGRES_TEST_DSN):
LEADGEN_POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5455/leadgen?sslmode=disable' \
  go -C backend/contexts/outreach-orchestration test \
    ./internal/adapters/persistence/postgres/...
```
