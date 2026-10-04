## Context

`architecture/domain-catalog.yaml` — канонический источник идентификаторов, владения и межконтекстных зависимостей; `docs/domain-model.md` и PlantUML-диаграммы — его представления. DDD-ревизия показала два отношения, которые уже существуют в модели, но не объявлены: поведенческая сегментация читает события взаимодействия, а аудитория обязана соблюдать политику зон данных.

## Goals / Non-Goals

**Goals:**

- объявить две недостающие связи в каталоге, не меняя владение и состав модели;
- сохранить согласованность каталога, документа и диаграммы в рамках одного изменения.

**Non-Goals:**

- не добавлять capability, агрегаты, aggregate roots, сущности, value objects, события и policies;
- не выбирать новые контракты: обе связи переиспользуют уже объявленные `engagement-event` и `data-zone-policy`;
- не закрывать прочие пробелы модели, не входящие в два перечисленных отношения.

## Decisions

### Решение 1. Событие взаимодействия как вход поведенческой сегментации

`audience` строит сегменты в том числе по поведению, а квалифицированные сигналы производит `engagement-observation` как контракт `engagement-event`. Связь объявляется как `engagement-to-audience` (upstream `engagement-observation` → downstream `audience`, pattern `published-language`, integration `event`, contract `engagement-event`) — по образцу существующих потребителей того же контракта `observation-to-outreach` и `observation-to-analytics`.

**Почему существующий контракт.** `engagement-event` уже публикуется `engagement-observation`; сегментация — ещё один потребитель того же класса факта, поэтому новая версия контракта не нужна.

**Рассмотренная альтернатива:** тянуть сырые сигналы напрямую (`read-signal`) в `audience` — отклонено: квалификация и дедупликация принадлежат `engagement-observation`, а сырой сигнал не равен квалифицированному событию (инвариант владельца).

### Решение 2. Политика зон данных применяется аудиторией, а не пересказывается

`audience` работает с red-данными (скоринг, сегменты, снимки) и потому подчиняется `data-zone-policy`. Связь объявляется как `governance-policy-for-audience` (upstream `data-governance` → downstream `audience`, pattern `published-language`, integration `policy`, contract `data-zone-policy`) — по образцу `governance-policy-for-leads/outreach/delivery`.

**Рассмотренная альтернатива:** воспроизвести правило зон локально в `audience` — отклонено: правило стало бы дубликатом политики владельца и разошлось бы с ней при изменении.

### Решение 3. Каталог каноничен, документ и диаграмма обновляются вместе с ним

Связи добавляются сначала в каталог, затем отражаются в `docs/domain-model.md` и `architecture/diagrams/context-relationships.puml` в том же изменении. Маркеры `CTX:<context-id>` диаграмм не меняются: состав контекстов прежний.

## Risks / Trade-offs

- **[`data-zone-policy` и `engagement-event` получают новых потребителей]** → Контракты версионируются по общим правилам; добавление потребителя смысл и обязательность полей не меняет.

## Migration Plan

1. Добавить две связи в `architecture/domain-catalog.yaml`; проверить `go -C tools/archlint run .`.
2. Отразить две связи в `docs/domain-model.md` и `architecture/diagrams/context-relationships.puml`.
3. Проверить `openspec validate close-remaining-catalog-gaps --strict`.

**Откат.** Удаление двух записей из каталога и двух рёбер из представлений; продуктовые требования не затрагивались, поэтому откат не влияет на capability.
