## Why

После реальной отправки письма заявка навсегда остаётся в статусе `processing`: workflow исполнения ждёт сигнал утверждения из консоли заявок, которого в недоборном контуре не будет — оператор утверждает письма в очереди `outreach-orchestration`, письма доставляются (`message-delivery.message.status = delivered`, mailpit их захватывает), но заявка к терминальному `fulfilled` сама не переходит. Живой прогон 2026-10-06 подтвердил это на `req-handoff-e2e-20261006a/b/c`. Жизненный цикл заявки не замкнут: наблюдаемого триггера завершения нет.

## What Changes

- **Новая capability `request-delivery-conclusion` (`request-fulfillment`)** — завершение заявки по доставке: получив исход доставки писем своего недобора, заявка переходит в терминальное `fulfilled` тогда и только тогда, когда **все** переданные в `outreach-orchestration` письма доставлены; недоставка (`failed`) не завершает заявку выдуманным успехом. Завершение идемпотентно (повтор события не даёт второго эффекта) и не подменяется заглушкой при недоступности источника событий.
- **`outreach-channel-email` (`message-delivery`) — MODIFIED**: публикуемый контракт `delivery-event` (MINOR) несёт непрозрачную ссылку на кампанию-владельца (`campaign_id`), унаследованную от `approved-action` (`approved-action.CorrelationID = CampaignID`). Без этого события доставки невозможно сопоставить с заявкой.
- **`request-fulfillment-execution` (`request-fulfillment`) — MODIFIED**: человеческая пауза workflow снимается не только сигналом консоли заявок, но и наблюдаемым завершением доставки; исход «все письма доставлены» ведёт к `fulfilled`, недоставка в срок — к `cancelled` с явной причиной. Граф исполнения версионируется, прежние бегущие исполнения воспроизводятся без изменений.
- **`request-shortfall` (`request-fulfillment`) — MODIFIED**: после автоматической передачи писем в `outreach-orchestration` заявка завершается по исходу их доставки, а не остаётся в `processing`; срок ожидания доставки ограничен.
- **Новая межконтекстная связь каталога** `delivery-events-for-requests` (`message-delivery` → `request-fulfillment`, `delivery-event`, `async · amqp`).

**Не входит:** изменение контура утверждения/отправки `outreach-orchestration`, смена владельцев, автоотправка без человека, изменение контракта `approved-action`.

## Capabilities

### New Capabilities

- `request-delivery-conclusion`: завершение заявки по исходу доставки её писем недобора — идемпотентно, только при полной доставке, без ложного завершения.

### Modified Capabilities

- `outreach-channel-email`: `delivery-event` дополняется ссылкой `campaign_id` (MINOR), переносимой от `approved-action` через команду и агрегат `message`.
- `request-fulfillment-execution`: пауза утверждения снимается и наблюдаемым завершением доставки; ограниченный срок ожидания доставки; терминальный исход по доставке.
- `request-shortfall`: заявка с недобором завершается по доставке переданных писем, а не остаётся в `processing`.

## Impact

**Affected bounded contexts:** `request-fulfillment` (потребитель исхода доставки, новая capability, завершение заявки), `message-delivery` (публикация `campaign_id` в `delivery-event`, хранение ссылки в агрегате `message`). Смежные (`outreach-orchestration`, `lead-management`, `content-production`) не меняются: их контракты не затрагиваются.

**Доменная модель:** каталог `architecture/domain-catalog.yaml` дополняется связью `delivery-events-for-requests` (и capability, если она заводится отдельно); обновляются `docs/domain-model.md` и затронутые диаграммы — в том же change.

**Код и данные:** `contracts/message-delivery/deliveryevent` (MINOR `campaign_id`), `contracts/message-delivery/deliverycommand` (MINOR `campaign_id`), `backend/contexts/message-delivery` (хранение `campaign_id` в агрегате `message` и его проброс в `delivery-event`), `backend/contexts/request-fulfillment` (чтение исхода доставки, завершение заявки, сигнал завершения workflow, запись `campaign_id` заявки), `backend/cmd/lead-gen` (проводка подписки — головной агент).

**Спецификации/приёмка:** снимается наблюдаемый дефект «заявка навсегда в `processing`»; завершение идемпотентно и не подменяется заглушкой при недоступности источника событий.
