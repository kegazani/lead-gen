# Контекст `sales-workflow`

Bounded-контекст **Продажи и сделки** (core) из `architecture/domain-catalog.yaml`:
жизненный цикл сделки, синхронизация с внешней CRM и подтверждаемая атрибуция
результата. Это рабочий вертикальный срез: домен, прикладной слой, порты,
адаптеры, опубликованные контракты и сборка ролей.

```
backend/contexts/sales-workflow/
  service.go, slice.go        сборка сервиса и ролей (api, ingest, worker)
  internal/domain/            deal, crm-sync, VO, события, политики
  internal/app/command/       use cases жизненного цикла, приёма crm-event, атрибуции
  internal/app/query/         чтение сделки и состояния синхронизации
  internal/ports/             граница CRM, репозиторий синхронизации, outbox, транзакция
  internal/adapters/          in-memory CRM, недоступная CRM, хранилище, outbox, HTTP, контракты
contracts/sales-workflow/     deal-state, crm-event, attributed-result (+ in-proc адаптеры)
```

## Домен

| Агрегат | Корень | VO | События | Политика |
|---|---|---|---|---|
| Сделка | `deal` | `deal-stage`, `attribution-proof` | `deal-state-changed`, `result-attributed` | `confirmed-attribution` |
| Синхронизация | `crm-sync` | `external-version` | `crm-sync-conflict` | `conflict-resolution` |

Жизненный цикл сделки: `lead → qualified → proposal → negotiation → won`, ветвь
`lost`; `won`/`lost` терминальны. Инварианты:

- переходы этапов следуют согласованному workflow (`DealStage.CanTransitionTo`);
- подтверждённый результат имеет достаточное доказательство и идемпотентную
  внешнюю ссылку (`AttributionProof`, ключ идемпотентности — `external_ref`);
- синхронизация хранит внешнюю версию и **не перезаписывает локальное состояние
  при конфликте** — конфликт фиксируется событием `crm-sync-conflict`, а
  применённая версия сохраняется.

Политики — чистые функции `domain.ConflictResolution` и
`domain.ConfirmedAttribution`; решения применяет агрегат.

**Зона и хранилище.** Сделка — зона `red`, `storage: external`: источник истины по
сделке — внешняя CRM, контекст ей не владеет. `crm-sync` — зона `green`,
relational: локальная запись состояния синхронизации.

## Use cases

Команды: `CreateDeal`, `ChangeDealStage`, `AttributeResult`, `IngestCRMEvent`.
Запросы: `GetDeal`, `CRMSyncStatus`.

`IngestCRMEvent` связывает обе политики: сначала `conflict-resolution` решает
судьбу внешней версии (применить / устарела / конфликт), затем применимая версия
обновляет этап сделки, а событие с подтверждением результата проходит
`confirmed-attribution`.

## Роли

| Роль | Что запускает |
|---|---|
| `api` | внутренний HTTP-сервер: `/deals`, `/deals/{id}`, `/deals/{id}/stage`, `/deals/{id}/attribution`, `/deals/{id}/crm-sync`, `/healthz` |
| `ingest` | публичный приём событий CRM: `POST /crm/events`, `/healthz` |
| `worker` | relay публикации событий из outbox в брокер |

## Контракты

| Контракт | Форма | Направление |
|---|---|---|
| `deal-state` | событие `DealStateEvent` + синхронный `Port` | владелец; async → analytics, query ← request-fulfillment |
| `crm-event` | событие `CRMEvent` + `Handler` | вход от CRM (ingest) |
| `attributed-result` | событие `AttributedResult` + `Publisher` | async → billing |

Общий конверт — `contracts/sales-workflow/envelope`. In-proc адаптеры:
`dealstate.InProc`, `crmevent.InProc`, `attributedresult/inproc.Bind`
(дедупликация по ключу идемпотентности). В срезе дополнительно есть адаптеры
`internal/adapters/contract`, связывающие контракты с use cases.

## Что осталось заглушкой

- **CRM-адаптер.** Реального сетевого адаптера внешней CRM нет. По умолчанию
  подключается `internal/adapters/crm/memory` — in-memory стенд-ин внешней CRM
  (сети нет, сделки в памяти процесса). Альтернатива —
  `internal/adapters/crm/unavailable`: честно отвечает `ErrCRMUnavailable`. Замена
  на настоящий адаптер не меняет порт `ports.DealRepository`.
- **Потребители очередей брокера** в роли `worker` не зарегистрированы: relay
  только публикует. Это логируется, а не скрывается.
- **Атомарность «сделка + событие».** Запись сделки идёт во внешнюю CRM и не
  входит в локальную транзакцию (локальная транзакция покрывает синхронизацию и
  outbox). Падение между записью сделки и фиксацией события оставит сделку
  изменённой без опубликованного события — для внешней системы это неизбежно и
  требует сверки; ограничение названо в doc `internal/adapters/outbox/inmemory` и
  `internal/app/command`.
- **Хранилище синхронизации** — in-memory (PostgreSQL-адаптер не реализован).

## Проверка

```sh
gofmt -l backend/contexts/sales-workflow contracts/sales-workflow
go -C backend/contexts/sales-workflow test -count=1 ./...
go -C backend/contexts/sales-workflow vet ./...
go -C contracts/sales-workflow test -count=1 ./...
```
