## Why

Несущие механики CRM-синхронизации (`sales-workflow`) и счетов/расчётного контура (`billing`) уже реализованы, но не нормированы в OpenSpec. Capability `crm-workflow` покрывает стадии, задачи и атрибуцию, но не приём `crm-event`, агрегат `crm-sync`, VO `external-version`, политику `conflict-resolution` и событие `crm-sync-conflict`. `billing-subscription` и `billing-success-fee` покрывают тарифы, лимиты и условие вознаграждения, но не выпуск/списание счёта, идемпотентность потребления по `(period_key, usage_key)`, контур `SettleDuePeriods`, порт `PaymentGateway`, контракты `usage-record`/`billing-result`/`billing-entitlement` и события `billing-result-recorded`/`success-fee-calculated`.

Все эти агрегаты, VO, события, политики и контракты **уже объявлены** в `architecture/domain-catalog.yaml`, но отсутствуют в спеках — поведение не защищено от регрессии. Change — «догоняющая документация»: фиксирует фактическое поведение как требования, не меняя код.

## What Changes

**А. Синхронизация с внешней CRM (`sales-workflow`).**

- **Приём события внешней CRM.** Контракт `crm-event` (роль `ingest`, `POST /crm/events`) с конвертом, обязательными полями и правилом «подтверждение результата требует доказательства атрибуции»; доменный отказ отличается от недоступности.
- **Состояние синхронизации сделки.** Агрегат `crm-sync` (зелёная зона, relational, одна запись на сделку), VO `external-version` (ссылка на запись CRM + ревизия) и чтение `GET /deals/{id}/crm-sync`.
- **Разрешение конфликтов.** Чистая политика `conflict-resolution` (`crm-event → crm-sync-conflict`): первая версия применяется, следующая ревизия применяется, устаревшая игнорируется, версия другой записи или с пропуском ревизий — конфликт. Конфликт **не перезаписывает** локальное состояние и фиксируется событием `crm-sync-conflict`.
- **Порядок применения.** Политика решает судьбу версии до мутации сделки; применимая версия обновляет этап, а подтверждение результата идёт через существующую политику `confirmed-attribution`.
- **Опубликованные контракты.** `deal-state` (событие и синхронный запрос), `crm-event` (вход) и `attributed-result` (async в billing, идемпотентный по `(tenant_id, external_ref)`).

**Б. Счета, списание и расчётный контур (`billing`).**

- **Выпуск счёта.** Счёт за период выставляется только в пределах активной подписки и её лимитов; повтор за тот же период идемпотентен; успех эмитит `invoice-issued`.
- **Расчётный контур.** Воркер `SettleDuePeriods` по активным подпискам выставляет недостающий счёт за текущий период и открывает следующий период при его завершении; проход идемпотентен.
- **Списание.** Команда `ChargeInvoice` через порт `PaymentGateway`: недоступность — `503`, доменный отказ — `409`, ни один исход не выдаётся за успех; оплаченный счёт повторно не списывается.
- **Идемпотентность потребления.** Запись потребления идемпотентна по `(period_key, usage_key)`; политика `subscription-entitlement` решает допустимость и признак исчерпания периода, при исчерпании счёт выставляется в той же транзакции.
- **Результат биллинга, права и контракты.** События `billing-result-recorded` и `success-fee-calculated`; контракты `usage-record`, `billing-result` и `billing-entitlement`; идемпотентность начисления вознаграждения по `(tenant, result_ref)` и версия правила расчёта.

**Код и поведение не меняются.** Change нормирует уже работающее.

## Capabilities

### New Capabilities

Нет. Все затронутые capability уже существуют: `crm-workflow` создан рамкой `full-enterprise-core-scope`, `billing-subscription` и `billing-success-fee` — тоже. Новая capability не вводится, чтобы не дробить поведение агрегата `subscription-invoice` между соседними capability и не нарушать правило «ровно один primary context на capability».

### Modified Capabilities

- `crm-workflow`: добавляются требования приёма `crm-event`, состояния синхронизации (`crm-sync`, `external-version`), разрешения конфликтов (`conflict-resolution`, `crm-sync-conflict`), порядка применения синхронизации к сделке и опубликованных контрактов `deal-state`/`crm-event`/`attributed-result`.
- `billing-subscription`: добавляются требования выпуска счёта (`invoice-issued`), расчётного контура `SettleDuePeriods`, списания через `PaymentGateway`, идемпотентности потребления по `(period_key, usage_key)`, фиксации результата биллинга (`billing-result-recorded`) и контрактов `usage-record`/`billing-result`/`billing-entitlement`.
- `billing-success-fee`: добавляются требования версии правила расчёта и события `success-fee-calculated`, идемпотентности начисления по `(tenant, result_ref)` и статусной модели вознаграждения.

## Impact

**Затрагиваемые bounded contexts:**

- `sales-workflow` — владелец приёма `crm-event`, агрегата `crm-sync`, VO `external-version`, политики `conflict-resolution` и событий `crm-sync-conflict`; публикует `deal-state`, `crm-event`, `attributed-result`. Код: `backend/contexts/sales-workflow/**` (домен, app, адаптеры HTTP/ingest/contract), контракты `contracts/sales-workflow/**`.
- `billing` — владелец агрегатов `subscription-billing` и `success-fee`, событий `invoice-issued`/`billing-result-recorded`/`success-fee-calculated`, контрактов `usage-record`/`billing-result`/`billing-entitlement` и порта `PaymentGateway`. Код: `backend/contexts/billing/**`, контракты `contracts/billing/**`.
- `integration-platform` — поставляет `crm-event` в `sales-workflow` через ACL (связь `integrations-for-sales`); контракт не меняется.

**Доменная модель:** новых агрегатов, сущностей, value objects, domain events, policies и межконтекстных контрактов нет — всё уже объявлено в `architecture/domain-catalog.yaml` (агрегаты `crm-sync`, `deal`, `subscription-billing`, `success-fee`; VO `external-version`, `deal-stage`, `attribution-proof`, `billing-period`, `money`, `tariff-limit`, `calculation-version`; события `crm-sync-conflict`, `invoice-issued`, `billing-result-recorded`, `success-fee-calculated`; политики `conflict-resolution`, `confirmed-attribution`, `subscription-entitlement`, `success-fee-eligibility`; контракты перечислены выше). Поэтому правило эволюции (обновлять каталог, модель домена и диаграммы при новых capability/агрегатах/владении/связях) **не срабатывает**: `architecture/domain-catalog.yaml`, `docs/domain-model.md` и диаграммы не меняются.

**Спецификации, которые явно не меняются:** `billing-success-fee` в части условия применимости и запрета внешних CRM сохраняется как есть (change её не отменяет, а уточняет mechanics расчёта); `client-request`/`request-fulfillment-execution` не покрывают CRM-синхронизацию и счета и не дублируются.
