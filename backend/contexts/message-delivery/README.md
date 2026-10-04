# message-delivery

Bounded-контекст **Доставка сообщений** (`architecture/domain-catalog.yaml`):
исполнение утверждённых почтовых действий, доставляемость и репутация
отправляющих доменов. Классификация — core; capabilities —
`outreach-channel-email`, `domain-deliverability`.

Это вертикальный срез: работающий путь «команда доставки → отправка → исход →
публикация события». Отправка идёт через реального почтового провайдера (контракт
`mail`, владелец integration-platform), стоп-лист — через контракт
`suppression-policy` (владелец data-governance), состояние (сообщения, попытки,
репутация, outbox) хранится в PostgreSQL (schema-per-service, миграции сервиса).
In-memory адаптеры остаются только для тестов и явного dev-режима.

## Слои

```
contexts/message-delivery/
  service.go          сборка сервиса и роли (sender, worker, schedule)
  messaging.go        Build/Slice/Params, AMQPSink, фабрики контрактов и адаптеров
  migrations/green    SQL-миграции схемы сервиса (message-delivery)
  internal/domain     агрегаты, VO, события, политики
  internal/ports      репозитории, стоп-лист, почтовый порт, часы, outbox, транзакция
  internal/app        прикладные сценарии доставки и повторных окон
  internal/adapters   реальный mailer (contract mail), стоп-лист (suppression-policy),
                      PostgreSQL-персистентность, in-memory (тесты/dev), outbox/relay
```

Правила слоёв проверяет `tools/importlint`. Домен зависит только от стандартной
библиотеки; `app` не импортирует `adapters`; межконтекстные связи — только через
`contracts/`.

## Домен

### Агрегаты

- **`message`** (aggregate root) — одно исходящее письмо, созданное по
  утверждённому действию. Состояния: `pending → sending → delivered|failed`;
  `delivered` и `failed` терминальны, кроме `failed` после временного исхода —
  такое сообщение возвращается в `sending` повторным окном. Несёт адрес
  назначения (`delivery-address`), содержимое письма (тема, тело) и журнал
  попыток (`delivery-attempt`).
- **`sender-reputation`** (aggregate root) — репутация отправляющего домена:
  счётчики доставок и отказов, вычисляемые ограничения (`paused`, `throttled`).

### Инварианты

| Агрегат | Инвариант | Где обеспечен |
| --- | --- | --- |
| message | Сообщение создаётся только для утверждённого действия | политика `approved-action-only`, `domain.ApprovedActionOnly` |
| message | Повторная отправка с тем же idempotency key запрещена | ключ `(tenant_id, approved_action_id)`, уникальный индекс репозитория, ранний возврат в сценарии |
| message | Активный стоп-лист блокирует попытку непосредственно перед доставкой | `ports.SuppressionList` в фазе резервирования, до вызова провайдера |
| sender-reputation | Ограничения вычисляются только по нормализованным ответам провайдера | `ApplyNormalizedOutcome(domain.NormalizedOutcome)`; тип не несёт сбой транспорта/стоп-лист |
| sender-reputation | Активные ограничения приостанавливают отправки домена | `Suspended()`; политика `deliverability-policy` |

### События

- `message-delivered` — `domain.MessageDelivered`
- `message-delivery-failed` — `domain.MessageDeliveryFailed`

Оба реализуют `domain.DomainEvent`; публикуемый контракт — `delivery-event`.

### Политики

- **`approved-action-only`** — `domain.ApprovedActionOnly`: доставка возможна
  только для команды с утверждённым действием.
- **`deliverability-policy`** — `domain.DeliverabilityPolicy(cmd, reputation,
  suppressed)`: порядок — форма команды → стоп-лист (приоритет) → репутация
  домена. Чистая функция, без часов и хранилища.

## Use case

`internal/app/command.DeliverMessageHandler` — единственный сценарий изменения
состояния при первичной доставке. Порядок шагов отражает необратимость отправки:

1. **Резерв** (транзакция): проверка стоп-листа и репутации, создание `message`
   в состоянии `sending`, сохранение. Резерв коммитится **до** отправки, поэтому
   повтор команды с тем же ключом не приводит ко второй отправке.
2. **Отправка** через `ports.Mailer` (реальный провайдер за контрактом `mail`).
   Недоступность провайдера фиксируется как временный исход (`temporary`), а не
   роняет сценарий.
3. **Фиксация** (транзакция): запись попытки, терминальное состояние, обновление
   репутации по нормализованному ответу и запись события в outbox — атомарно
   (transactional outbox).

Повтор команды идемпотентен: возвращается существующее сообщение с
`Replayed = true`, второго эффекта и события нет.

`internal/app/command.RetryPendingHandler` — сценарий повторных окон. Роль
`schedule` периодически выбирает сообщения, последняя попытка которых завершилась
временным исходом, и повторяет отправку тем же содержимым; число попыток
ограничено конфигурацией. Порядок тот же: claim в состоянии `sending` до отправки,
затем фиксация исхода.

**Содержимое письма.** Тема и тело приходят в команде доставки (`delivery-command`
1.1.0, необязательные поля) и сохраняются в агрегате `message`, поэтому реальная
отправка и повтор передают провайдеру то же содержимое. Сборка тела из
`content-production` — вне объёма контекста.

## Контракты

`contracts/message-delivery/` — отдельный модуль (только стандартная библиотека).

| Контракт | Форма | Транспорт | Назначение |
| --- | --- | --- | --- |
| `delivery-command` | `Command`, `Port`, `InProc` + `inproc.Adapter` (dedup + бюджет), поля темы/тела (1.1.0) | sync | входной шлюз роли `sender` |
| `delivery-event` | `DeliveryEvent` + `Envelope` | async (amqp) | исход доставки для observation/analytics/lead |
| `deliverability-policy` | `Policy` + `Envelope` | async (amqp) | снимок правил и репутации домена |

Общий конверт контрактов — пакет `contracts/message-delivery/envelope`.
In-proc адаптер `deliverycommand/inproc` выравнивает наблюдаемое поведение
co-location с будущим сетевым клиентом (повтор по ключу применяется один раз).

## Роли

`deploy/services.yaml`, группа `delivery`, механизм `in-process`:
`message-delivery` → `[sender, worker, schedule]`. Роли `api` у контекста нет.

- **`sender`** — **единственная точка отправки**, изолированная от обработки
  API-запросов. Читает команды доставки из `ports.CommandSource` (в срезе —
  очередь в памяти) и исполняет их сценарием доставки. Роль также публикует
  контракт `delivery-command` через `NewDeliveryCommandPort` для co-location
  вызовов. Доменный отказ по одной команде не останавливает роль.
- **`worker`** — публикует `delivery-event` из доменного outbox: транспортный
  `outbox.Relay` поверх `amqp.Publisher` (publisher confirm) плюс проход
  `AMQPSink`. Обработчиков очередей в срезе нет — это логируется честно.
- **`schedule`** — тикер повторных окон доставки: выбирает сообщения с временным
  исходом и повторяет отправку; интервал и число попыток — конфигурация.

## Что осталось вне объёма

- **Приём `approved-action`** — контракт владельца outreach-orchestration не
  транслируется в `delivery-command`: `approved-action` несёт непрозрачные ссылки и
  не содержит адреса получателя, разрешение `RecipientRef`/`MaterialVersionRef`
  требует отдельного межконтекстного контракта — это отдельный change. Пока мост
  не реализован, реальная отправка не получает адрес и содержимое (отсутствие
  данных даёт отказ, а не фиктивный успех).
- **Сборка тела письма** (content-production) — вне объёма: содержимое приходит
  в команде доставки, а не собирается контекстом.
- **Сетевой транспорт `delivery-command`** — при разделении сервисов форма
  контракта даётся сетевым клиентом; в срезе вызов co-located.

## Тесты

```
go -C backend/contexts/message-delivery test -count=1 ./...
go -C contracts/message-delivery test -count=1 ./...

# PostgreSQL-адаптеры (нужна база):
LEADGEN_POSTGRES_TEST_DSN='postgres://postgres:postgres@localhost:5439/leadgen?sslmode=disable' \
  go -C backend/contexts/message-delivery test -count=1 ./internal/adapters/persistence/postgres/...
```

- домен: переходы состояний, события, инварианты репутации, value objects,
  политики; временный исход и повтор, содержимое письма;
- use cases: доставка, отказ, сбой транспорта, идемпотентный повтор,
  приостановленный домен, стоп-лист, отсутствие утверждённого действия;
- повторные окна: повтор временного исхода, предел попыток, отключение;
- адаптеры: реальный mailer (нормализация ответа и сбоя), стоп-лист
  (недоступность — ошибка), PostgreSQL-персистентность (round-trip, ключ
  идемпотентности, transactional outbox, откат), in-memory, relay, источник команд;
- срез: путь «команда → отправка → публикация события», передача содержимого и
  роли сервиса;
- контракты: round-trip, обязательные поля, закрытые наборы, ключи
  идемпотентности, in-proc адаптер, аддитивные поля содержимого (1.1.0).
