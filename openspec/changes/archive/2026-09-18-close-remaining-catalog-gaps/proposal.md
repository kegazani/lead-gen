## Why

DDD-ревизия каталога выявила два межконтекстных взаимодействия, которые уже происходят в модели, но не объявлены в `architecture/domain-catalog.yaml`. Поведенческая сегментация `audience` потребляет сигналы `engagement-observation`; `audience` подчиняется `data-zone-policy` от `data-governance`, а не пересказывает правило локально. Каталог — канонический источник зависимостей, и без этих связей модель недоговаривает: у правила нет объявленного контракта, а запрет на прямой импорт чужого домена не на что опереть.

## What Changes

- **Добавляется связь `engagement-to-audience`.** Upstream `engagement-observation` → downstream `audience`, pattern `published-language`, integration `event`, contract `engagement-event`. Аудитория ведёт поведенческую сегментацию по квалифицированным событиям взаимодействия, а не по сырым сигналам.
- **Добавляется связь `governance-policy-for-audience`.** Upstream `data-governance` → downstream `audience`, pattern `published-language`, integration `policy`, contract `data-zone-policy`. Аудитория применяет политику зон данных, не воспроизводя правило локально.
- **Обновляются представления модели.** `architecture/diagrams/context-relationships.puml` получает два ребра, `docs/domain-model.md` — две записи в разделе «Межконтекстные отношения и контракты».
- **Что не меняется.** Capability, агрегаты, aggregate roots, сущности, domain events и policies не добавляются и не меняются; владение остаётся прежним. Новые контракты не вводятся: `engagement-event` объявлен `engagement-observation`, `data-zone-policy` — `data-governance`.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

Нет.

**Обоснование.** Требования capability `domain-architecture` уже требуют, чтобы каждая разрешённая зависимость была объявлена в каталоге. Изменение добавляет данные каталога и его представления, а не новое требование, поэтому помечено `skip_specs: true` в `.openspec.yaml`. Продуктовое поведение не меняется: отношение сегментации к событиям и применение политики зон уже описаны соответствующими capability.

## Impact

**Затрагиваемые bounded contexts:** `engagement-observation`, `audience`, `data-governance`, `lead-management`.

**Документы и артефакты:** `architecture/domain-catalog.yaml` (две связи), `architecture/diagrams/context-relationships.puml` (два ребра), `docs/domain-model.md` (раздел межконтекстных отношений).

**Проверка:** `go -C tools/archlint run .` и `openspec validate close-remaining-catalog-gaps --strict`.

**Риски.**

- **Новый потребитель `data-zone-policy` и `engagement-event`.** Обе связи переиспользуют существующие контракты, поэтому новой версии контракта не требуется; добавление потребителя смысл контракта не меняет.
