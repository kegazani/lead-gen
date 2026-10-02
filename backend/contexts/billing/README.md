# billing

Bounded-контекст `billing` (supporting): подписки, тарифные лимиты, счета и
вознаграждение за подтверждённый результат. Идентификатор сервиса — `billing`,
роли запуска — `api` и `worker` (architecture/domain-catalog.yaml).

Вертикальный срез: домен, прикладной слой, порты и адаптеры (in-memory и HTTP),
опубликованные контракты и сборка сервиса с `/healthz`.

## Домен (`internal/domain`)

Два агрегата по каталогу:

- **`subscription-billing`** (AR `subscription-invoice`) — подписка тенанта:
  статус (`active`/`suspended`), тариф, текущий период, потребление, счета и
  зафиксированные результаты биллинга.
  - VO: `billing-period`, `money`, `tariff-limit` (+ `tariff`, `feature`,
    `subscription-status`, `billing-result-kind`).
  - Инварианты: запись потребления идемпотентна по ключу периода
    `(period_key, usage_key)`; счёт выставляется только в пределах активной
    подписки и её лимитов; результат биллинга идемпотентен по ссылке-источнику.
  - События: `invoice-issued`, `billing-result-recorded`.
- **`success-fee`** (AR `success-fee`) — вознаграждение за результат.
  - VO: `calculation-version`.
  - Инварианты: начисление создаётся только из подтверждённого результата; расчёт
    хранит версию применённого правила.
  - Событие: `success-fee-calculated`.

Чистые политики (`policy.go`):

- `subscription-entitlement` (usage-record → invoice-issued) — допустимость
  записи потребления по тарифу и лимиту и признак исчерпания периода.
- `success-fee-eligibility` (billing-result → billing-result-recorded) —
  применимость вознаграждения: сделка закрыта в CRM платформы и связана с
  зафиксированной коммуникацией; ставка берётся по версии правила.

## Прикладной слой (`internal/app`)

Команды: `ActivateSubscription`, `SuspendSubscription`, `ResumeSubscription`,
`RecordUsage`, `IssueInvoice`, `ChargeInvoice`, `CalculateSuccessFee`,
`DisputeSuccessFee`, `SettleDuePeriods` (расчётный контур worker).

Запросы: `GetSubscription`, `GetEntitlement`, `GetSuccessFee`, `ListSuccessFees`.

Сценарии задают транзакцию и порядок вызовов; инварианты и решения о
допустимости обеспечивают агрегаты и политики домена.

## Порты и адаптеры (`internal/ports`, `internal/adapters`)

- Порты: `SubscriptionRepository`, `SuccessFeeRepository`, `UnitOfWork` +
  `Transaction` + `Outbox`, `Clock`, `IDGenerator`, `PaymentGateway`.
- Адаптеры:
  - `persistence/inmemory` — репозитории по снимкам с оптимистичной блокировкой;
  - `outbox/inmemory` — transactional outbox и unit-of-work, кодирование событий
    в записи журнала платформы;
  - `idgen` — генератор `event_id`;
  - `payment/unavailable` — честный отказ, если платёжная система не подключена;
  - `payment/deterministic` — детерминированный адаптер **без сети** для
    локального запуска и тестов (успех, идемпотентность по ключу, отклоняемые
    счета);
  - `http` — HTTP-адаптер роли `api` с `/healthz`.

## Контракты (`contracts/billing/*`)

Зависимости контрактов — только стандартная библиотека.

- `usage-record` — запись потребления (consumer → owner), идемпотентна по
  `(period_key, usage_key)`; `inproc.Bind` добавляет дедупликацию и бюджет.
- `billing-entitlement` — права и лимиты подписки (policy billing →
  request-fulfillment); `inproc.Bind` для co-location.
- `billing-result` — результат биллинга: начисление или вознаграждение; `inproc`
  адаптер доставляет результат получателю.

Фабрики владельца (`contract.go`): `NewUsageRecordPort`,
`NewBillingEntitlement`.

## Сборка (`service.go`)

`Build` соединяет адаптеры, порты и use cases; `New` публикует `composition.Service`.
Роль `api` поднимает внутренний HTTP-сервер (`/healthz`), роль `worker` —
расчётный контур периодов и, если подключён брокер, relay публикации событий.

## Что осталось заглушкой

- Внешняя платёжная система за портом `PaymentGateway`: реальной интеграции и
  сети нет — доступны `Unavailable` и детерминированный адаптер.
- Потребитель контракта `attributed-result` (sales-workflow) не подключён: вход
  расчёта вознаграждения моделируется входом политики `AttributedResult`, без
  импорта чужого контракта (его модуль — вне зоны этого среза).
- Асинхронные потребители очередей брокера не зарегистрированы; публикация идёт
  через outbox/relay при подключённом издателе, о чём worker логирует
  предупреждение.
- Персистентность — только in-memory; схемы PostgreSQL и миграций нет.
- Сервис не подключён в `backend/cmd/lead-gen` (точка входа вне зоны среза).

## Проверка

```bash
gofmt -l backend/contexts/billing contracts/billing
go -C backend/contexts/billing test -count=1 ./...
go -C backend/contexts/billing vet ./...
go -C contracts/billing test -count=1 ./...
go -C contracts/billing vet ./...
```
