# tasks.md — request-delivery-fulfilment

> Статус: реализация контекстов и контрактов выполнена и проверена гейтом.
> Проводка подписки в `backend/cmd/lead-gen` и живой e2e выполнены головным
> агентом (группа 6).

## 1. Контракты (аддитивно, MINOR)

- [x] 1.1 `contracts/message-delivery/deliveryevent`: добавить необязательное поле `campaign_id` (MINOR, schema `1.1.0`), не добавляя его в обязательную проверку; тест контракта на перенос значения и обратную совместимость.
- [x] 1.2 `contracts/message-delivery/deliverycommand`: добавить необязательное поле `campaign_id` (MINOR, schema `1.2.0`); тест контракта.
- [x] 1.3 `cd contracts/message-delivery && go test ./... && go vet ./...` — зелёный.

## 2. Публикация `campaign_id` в `message-delivery`

- [x] 2.1 `internal/domain/command.go`: `DeliveryCommand.CampaignID`.
- [x] 2.2 `internal/domain/message.go`, `snapshot.go`, `events.go`: хранение `campaignID` в агрегате `message`, перенос в снимок и в `MessageDelivered`/`MessageDeliveryFailed`.
- [x] 2.3 `internal/adapters/commandsource/approvedaction`: заполнять `CampaignID` из `action.CampaignID`.
- [x] 2.4 `messaging.go` (`mapCommand`), `internal/app/command/deliver_message.go` (`reserve`): пробросить `CampaignID` до агрегата.
- [x] 2.5 `internal/adapters/persistence/postgres`: `messageRecord` (JSONB-снимок) и `codec.go` (outbox-события) переносят `campaign_id`.
- [x] 2.6 `internal/adapters/outbox/relay/relay.go` (`MapDeliveryEvent`): заполнять `CampaignID` в `delivery-event`.
- [x] 2.7 Тесты: доставка → событие несёт `campaign_id`; снимок переживает round-trip; повтор не теряет ссылку.
- [x] 2.8 `cd backend/contexts/message-delivery && go test ./... && go vet ./...` — зелёный.

## 3. Завершение заявки по доставке (`request-fulfillment`)

- [x] 3.1 Передача поднимает кампанию в граф: `HandoffOutcome` несёт `CampaignID` и `Expected` (число реально переданных писем); activity `HandoffDrafts` возвращает их. Тест activity.
- [x] 3.2 Фиксация `campaign_id` заявки: activity/вызов существующей команды `RecordCampaign`; `RecordCampaign` идемпотентна (повтор с тем же `campaign_id` — no-op). Тесты.
- [x] 3.3 Чтение заявки по кампании: метод репозитория `FindByCampaign(ctx, tenantID, campaignID)` (memory + PostgreSQL + индекс в миграции `0003_*` + contracttest). Тесты.
- [x] 3.4 Workflow: версионированное ожидание доставки (`changeDeliveryConclusion`) — пауза снимается сигналом утверждения, сигналом завершения доставки или таймером; «все доставлены» → `fulfilled`; истечение срока/недоставка → `cancelled` с причиной; дедупликация по `message_id`. Тесты графа.
- [x] 3.5 Порт сигнала завершения: `ports.WorkflowSignaler.SignalDeliveryOutcome` + адаптер Temporal (`internal/adapters/workflow/starter.go`) + фейки в тестах.
- [x] 3.6 Подписчик `delivery-event`: разбор `campaign_id`/`message_id`/`outcome`, дедупликация по `event_id` (`platform/messaging/inbox`), сопоставление по кампании+тенанту, сигнал workflow; чужое игнорируется. Тесты (успех/повтор/недоставка/недоступность).
- [x] 3.7 Порт источника событий + сборка подписки в `request-fulfillment` (без проводки в `cmd/lead-gen` — это головной агент).
- [x] 3.8 `cd backend/contexts/request-fulfillment && go test ./... && go vet ./...` — зелёный.

## 4. Домен, каталог и документация (в том же change)

- [x] 4.1 `architecture/domain-catalog.yaml`: capability `request-delivery-conclusion` у `request-fulfillment`; связь `delivery-events-for-requests` (`message-delivery` → `request-fulfillment`, `event`, `async · amqp`, `delivery-event`).
- [x] 4.2 `docs/domain-model.md`: статусная модель `request` (завершение по доставке), строка capability matrix, межконтекстное отношение, заметка о семантике `fulfilled`.
- [x] 4.3 Диаграммы `architecture/diagrams/bounded-context-map.puml`, `context-relationships.puml` (стрелка `Delivery → Requests : delivery-event`).

## 5. Гейт приёмки

- [x] 5.1 `cd backend/contexts/request-fulfillment && go test ./... && go vet ./...` — зелёный.
- [x] 5.2 `cd backend/contexts/message-delivery && go test ./... && go vet ./...` — зелёный.
- [x] 5.3 `go -C tools/importlint run .` — 0 запрещённых рёбер.
- [x] 5.4 `go -C tools/archlint run .` — код 0 (12 контекстов / 60 capability / 50 связей).
- [x] 5.5 `openspec validate request-delivery-fulfilment --strict` — valid.

## 6. Проводка в точке входа и живой прогон (головной агент)

- [x] 6.1 Подписка на `delivery-event` в `backend/cmd/lead-gen` (роль worker request-fulfillment): `Config.DeliveryEvents` — общий брокер процесса, `DeliveryEventsDeduper` — `inbox.NewMemoryDeduper()`, `DeliveryEventsQueue` — флаг `-request-delivery-queue` / ENV `LEADGEN_REQUEST_DELIVERY_QUEUE` (пусто — очередь по умолчанию `request-fulfillment.delivery-event`). Без брокера подписка не собирается, заявка ждёт прежним способом. Доказательство: тест `TestBuildRequestFulfillmentDeliverySubscription` (событие `delivery-event.1` покидает очередь в роли worker); лог контура `intake`: `worker: подписка на события доставки запущена`.
- [x] 6.2 Живой e2e: новая заявка `req-deliv-20261006a` (недобор: found 3 / target 20) → `HandoffDrafts` → кампания `handoff-req-deliv-20261006a` → утверждение черновиков в outreach → доставка (mailpit: `info@1forma.ru`, `info@norbit.ru`) → подписка `delivery-event` сигналит workflow → `processing → fulfilled`. Доказательство: `GET /requests/req-deliv-20261006a/workflow` → `phase=fulfilled, execution=Completed, target=20, shortfall=17`; `Status=fulfilled`; лог `intake`: `все письма заявки доставлены — завершение по доставке … expected 2` → `переход заявки выполнен (FulfillRequest)`.
