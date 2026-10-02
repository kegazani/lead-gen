# message-delivery

Bounded-контекст **Доставка сообщений** (`architecture/domain-catalog.yaml`):
исполнение утверждённых почтовых действий, доставляемость и репутация
отправляющих доменов. Классификация — core; capabilities —
`outreach-channel-email`, `domain-deliverability`.

Это вертикальный срез: работающий путь «команда доставки → отправка → исход →
публикация события» на in-memory адаптерах. Реальный SMTP (контракт
`provider-port`, владелец integration-platform) и интеграция стоп-листа
(контракт `suppression-policy`, владелец data-governance) **не подключены** — их
места честно заняты портами и адаптерами.

## Слои

```
contexts/message-delivery/
  service.go          сборка сервиса и роли (sender, worker, schedule)
  messaging.go        Build/Slice/Params, AMQPSink, фабрика контракта delivery-command
  internal/domain     агрегаты, VO, события, политики
  internal/ports      репозитории, стоп-лист, почтовый порт, часы, outbox, транзакция
  internal/app        прикладной сценарий доставки
  internal/adapters   in-memory репозитории, outbox/relay, mailer, стоп-лист, источник команд
```

Правила слоёв проверяет `tools/importlint`. Домен зависит только от стандартной
библиотеки; `app` не импортирует `adapters`; межконтекстные связи — только через
`contracts/`.

## Домен

### Агрегаты

- **`message`** (aggregate root) — одно исходящее письмо, созданное по
  утверждённому действию. Состояния: `pending → sending → delivered|failed`
  (терминальные). Несёт адрес назначения (`delivery-address`) и журнал попыток
  (`delivery-attempt`).
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
состояния. Порядок шагов отражает необратимость отправки:

1. **Резерв** (транзакция): проверка стоп-листа и репутации, создание `message`
   в состоянии `sending`, сохранение. Резерв коммитится **до** отправки, поэтому
   повтор команды с тем же ключом не приводит ко второй отправке.
2. **Отправка** через `ports.Mailer` (SMTP за портом). Недоступность провайдера
   фиксируется как временный исход (`temporary`), а не роняет сценарий.
3. **Фиксация** (транзакция): запись попытки, терминальное состояние, обновление
   репутации по нормализованному ответу и запись события в outbox — атомарно
   (transactional outbox).

Повтор команды идемпотентен: возвращается существующее сообщение с
`Replayed = true`, второго эффекта и события нет.

## Контракты

`contracts/message-delivery/` — отдельный модуль (только стандартная библиотека).

| Контракт | Форма | Транспорт | Назначение |
| --- | --- | --- | --- |
| `delivery-command` | `Command`, `Port`, `InProc` + `inproc.Adapter` (dedup + бюджет) | sync | входной шлюз роли `sender` |
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
- **`schedule`** — **заглушка**: тикер повторных окон в объёме среза не
  реализован и не притворяется работающим.

## Что осталось заглушкой

- **SMTP** — порт `ports.Mailer`; в срезе in-memory адаптер (`mailer.Memory`) и
  честный недоступный (`mailer.Unavailable`). Сеть не используется.
- **Стоп-лист** — порт `ports.SuppressionList`; в срезе in-memory адаптер.
  Интеграция с data-governance (`suppression-policy`) не подключена.
- **Роль `schedule`** — каркас без тикера повторных попыток.
- **Приём `approved-action`** — контракт владельца outreach-orchestration не
  транслируется в `delivery-command`; в срезе команда подаётся напрямую.
  Соответствие `delivery-command` публикуемому контракту — интерпретация среза.
- **Разбор тела письма** (content-production) — вне объёма: провайдеру
  передаются только идентификаторы и адрес.

## Тесты

```
go -C backend/contexts/message-delivery test -count=1 ./...
go -C contracts/message-delivery test -count=1 ./...
```

- домен: переходы состояний, события, инварианты репутации, value objects,
  политики;
- use cases: доставка, отказ, сбой транспорта, идемпотентный повтор,
  приостановленный домен, стоп-лист, отсутствие утверждённого действия;
- адаптеры: репозитории (в т.ч. уникальность ключа и изоляция клона), relay
  (отображение событий, подтверждение, ошибка публикации), источник команд;
- срез: путь «команда → отправка → публикация события» и роли сервиса;
- контракты: round-trip, обязательные поля, закрытые наборы, ключи
  идемпотентности, in-proc адаптер.
