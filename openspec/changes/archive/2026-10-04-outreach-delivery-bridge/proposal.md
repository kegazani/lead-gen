## Why

Утверждённое оператором действие (`approved-action`, владелец
`outreach-orchestration`) не доходит до отправки: `message-delivery` объявляет
роль `sender`, но не потребляет `approved-action`, а фабрика
`NewDeliveryCommandPort` нигде не вызывается. Контур «утверждено → отправлено»
не замкнут: `approved-action` несёт только непрозрачные ссылки
(`RecipientRef`, `MaterialVersionRef`), и разрешение их в адрес и материал требует
межконтекстного контракта. Без этого моста реальная отправка (уже реализованная в
`message-delivery`) недостижима на продуктовом пути — то есть функция стоит.

## What Changes

- **Новая capability `delivery-bridge`** (`message-delivery`): приём утверждённого
  действия и превращение его в команду доставки (`delivery-command`).
- **Новый контракт `delivery-target`** (владелец `lead-management`): разрешение
  `RecipientRef` в проверенный адрес назначения по тенанту. Материал письма
  (`MaterialVersionRef`, если задан) разрешается контрактом владельца версии
  материала (`content-production`, `content-version`). Персональные данные не
  поднимаются в `approved-action` — их разрешает владелец соответствующей модели
  в момент доставки.
- **Источник команд** в `message-delivery`, потребляющий `approved-action` и
  выдающий `domain.DeliveryCommand` роли `sender`; идемпотентность по
  (тенант, утверждённое действие).
- **Честная недоступность.** Недоступность резолвера или неразрешимая ссылка —
  явный отказ, а не выдуманный адрес или материал; не-email канал отклоняется.
- **Не входит:** реальный сетевой транспорт контракта (in-process адаптер; сетевой
  — точка расширения), изменение состава `approved-action`.

## Capabilities

### New Capabilities

- `delivery-bridge`: потребление `approved-action` в `message-delivery` и формирование `delivery-command` через разрешение ссылок получателя и материала; идемпотентность и честная недоступность.

### Modified Capabilities

Нет. Требования `outreach-channel-email` не меняются: мост лишь делает существующий путь отправки достижимым.

## Impact

**Affected bounded contexts:** `message-delivery` (владелец capability), `lead-management` (владелец нового контракта `delivery-target`), `outreach-orchestration` (источник `approved-action`, контракт не меняется).

**Доменная модель.** Capability `delivery-bridge` добавляется контексту `message-delivery`. Новый контракт `delivery-target` объявляется у `lead-management`; добавляется связь `leads-for-delivery` (`lead-management → message-delivery`, `customer-supplier/query`, `sync/http-json`). Новых агрегатов и сущностей не появляется. `architecture/domain-catalog.yaml` и `docs/domain-model.md` обновляются в этом же change.

**Код.** `contracts/lead-management/deliverytarget` (контракт), `backend/contexts/message-delivery` (адаптер-источник `approved-action`, резолвер-порт, маппинг в `delivery-command`), `backend/contexts/lead-management` (реализация резолвера по контактам/черновикам писем), `backend/cmd/lead-gen` (сборка и связывание с `approved-action`).
