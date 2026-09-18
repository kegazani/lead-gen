## 1. Канонический каталог

- [x] 1.1 Добавить связь `integrations-for-outreach`: upstream `integration-platform`, downstream `outreach-orchestration`, pattern `anticorruption-layer`, integration `command`, contract `provider-port` (верификация: `go -C tools/archlint run .` завершается кодом 0, контракт объявлен upstream)
- [x] 1.2 Добавить связь `delivery-to-lead`: upstream `message-delivery`, downstream `lead-management`, pattern `published-language`, integration `event`, contract `delivery-event` (верификация: `go -C tools/archlint run .` завершается кодом 0, дубликатов связи нет)

## 2. Представления модели

- [x] 2.1 Добавить два ребра в `architecture/diagrams/context-relationships.puml`: `Integration ..> Outreach : ACL / command\nprovider-port` и `Delivery --> Leads : PL / event\ndelivery-event` (верификация: маркеры `CTX:<context-id>` сохранены, состав контекстов прежний)
- [x] 2.2 Дополнить `docs/domain-model.md`, раздел «Межконтекстные отношения и контракты»: `integration-platform` предоставляет `provider-port` также `outreach-orchestration`; `message-delivery` публикует `delivery-event` также для `lead-management` (верификация: обе записи согласованы с каталогом)

## 3. Проверка

- [x] 3.1 Выполнить `go -C tools/archlint run .` и `openspec validate close-catalog-relationship-gaps --strict` (верификация: обе команды завершаются кодом 0)
- [x] 3.2 Сверить, что ни одно требование capability не изменилось: `openspec list --specs` и diff не содержат правок `openspec/specs/**` (верификация: изменены только каталог, диаграмма и `docs/domain-model.md`)
