## Context

`architecture/domain-catalog.yaml` — канонический источник идентификаторов, владения и межконтекстных зависимостей; `docs/domain-model.md` и PlantUML-диаграммы — его представления. DDD-ревизия прошлых changes показала два требования, не подкреплённых объявленной связью: генерация ссылки на конференцию в `call-orchestration` и обратный поток асинхронных отказов для цикла подавления в `email-verification`.

## Goals / Non-Goals

**Goals:**

- объявить обе недостающие связи в каталоге, не меняя владение и состав модели;
- сохранить согласованность каталога, документа и диаграммы в рамках одного изменения.

**Non-Goals:**

- не добавлять capability, агрегаты, сущности, события и policies;
- не выбирать конкретного провайдера конференций и не описывать протоколы;
- не закрывать прочие выявленные пробелы (связи `audience`, метрики размера компании) — они остаются отдельным решением.

## Decisions

### Решение 1. Провайдер конференций — через ACL и provider-port

`call-orchestration` генерирует ссылку при исполнении и не передаёт провайдеру данные участников (Решение 8 изменения). Это в точности профиль `integration-platform`: адаптер за provider port, нормализация ошибок, отсутствие SDK в домене. Связь объявляется как `integrations-for-outreach` (upstream `integration-platform` → downstream `outreach-orchestration`, pattern `anticorruption-layer`, integration `command`, contract `provider-port`) — по образцу существующих `integrations-for-leads/content/delivery/sales/community/publishing`.

**Рассмотренная альтернатива:** вызывать провайдера напрямую из `outreach-orchestration` — отклонено: SDK поставщика попал бы в доменный или прикладной слой и стал бы транзитным контрактом, что запрещено.

### Решение 2. Отказ доставки возвращается в lead-management событием

Цикл подавления обновляет статус адреса по факту отказа получателя. Отказ производится агрегатом `message-delivery` как событие `message-delivery-failed`; потребитель — aggregate `contact` в `lead-management`, который переводит адрес в `invalid` и инициирует подавление. Связь объявляется как `delivery-to-lead` (upstream `message-delivery` → downstream `lead-management`, pattern `published-language`, integration `event`, contract `delivery-event`).

**Почему существующий контракт.** `delivery-event` уже публикуется `message-delivery` и потребляется `engagement-observation` и `analytics`; отказ — тот же класс факта доставки, поэтому новая версия контракта не нужна.

**Рассмотренная альтернатива:** синхронный вызов из `message-delivery` в `lead-management` — отклонено: отказ приходит асинхронно и не должен блокировать отправку.

### Решение 3. Каталог каноничен, документ и диаграмма обновляются вместе с ним

Связи добавляются сначала в каталог, затем отражаются в `docs/domain-model.md` и `architecture/diagrams/context-relationships.puml` в том же изменении. Маркеры `CTX:<context-id>` диаграмм не меняются: состав контекстов прежний.

## Risks / Trade-offs

- **[Владелец подавления — data-governance, а не lead-management]** → Изменение объявляет лишь возврат отказа в `lead-management`; запись в стоп-лист `data-governance` остаётся за политикой владельца. Полная цепочка подавления при необходимости уточняется отдельно.
- **[`delivery-event` получает нового потребителя]** → Контракт версионируется по общим правилам; добавление потребителя опционального поля смысл не меняет.

## Migration Plan

1. Добавить обе связи в `architecture/domain-catalog.yaml`; проверить `go -C tools/archlint run .`.
2. Отразить обе связи в `docs/domain-model.md` и `architecture/diagrams/context-relationships.puml`.
3. Проверить `openspec validate close-catalog-relationship-gaps --strict`.

**Откат.** Удаление двух записей из каталога и двух рёбер из представлений; продуктовые требования не затрагивались, поэтому откат не влияет на capability.
