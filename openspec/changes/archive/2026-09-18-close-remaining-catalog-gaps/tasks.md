## 1. Канонический каталог

- [x] 1.1 Добавить связь `engagement-to-audience`: upstream `engagement-observation`, downstream `audience`, pattern `published-language`, integration `event`, contract `engagement-event` (верификация: `go -C tools/archlint run .` завершается кодом 0, контракт объявлен upstream)
- [x] 1.2 Добавить связь `governance-policy-for-audience`: upstream `data-governance`, downstream `audience`, pattern `published-language`, integration `policy`, contract `data-zone-policy` (верификация: `go -C tools/archlint run .` завершается кодом 0, контракт объявлен upstream)

## 2. Представления модели

- [x] 2.1 Добавить два ребра в `architecture/diagrams/context-relationships.puml`: `Engagement --> Audience : PL / event\engagement-event`, `Governance --> Audience : PL / policy\ndata-zone-policy` (верификация: маркеры `CTX:<context-id>` сохранены, состав контекстов прежний)
- [x] 2.2 Дополнить `docs/domain-model.md`, раздел «Межконтекстные отношения и контракты»: `data-governance` применяется также `audience`; `engagement-observation` публикует `engagement-event` также для `audience` (верификация: все записи согласованы с каталогом)

## 3. Проверка

- [x] 3.1 Выполнить `go -C tools/archlint run .` и убедиться, что связей 27, код выхода 0 (верификация: в выводе `archlint: OK — ... связей: 27`)
- [x] 3.2 Выполнить `openspec validate close-remaining-catalog-gaps --strict` (верификация: команда завершается кодом 0)
- [x] 3.3 Сверить, что ни одно требование capability не изменилось: правки ограничены каталогом, диаграммой и `docs/domain-model.md`, `openspec/specs/**` не затронут (верификация: `skip_specs: true`, разделы Capabilities — «Нет»)
