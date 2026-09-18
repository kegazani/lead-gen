## Why

DDD-ревизия каталога выявила три межконтекстных взаимодействия, которые уже происходят в модели, но не объявлены в `architecture/domain-catalog.yaml`. Поведенческая сегментация `audience` потребляет сигналы `engagement-observation`; `audience` подчиняется `data-zone-policy` от `data-governance`, а не пересказывает правило локально; карточка сделки `sales-workflow` читает профиль компании, включая размер, из `lead-management`. Каталог — канонический источник зависимостей, и без этих связей модель недоговаривает: у правила нет объявленного контракта, а запрет на прямой импорт чужого домена не на что опереть.

## What Changes

- **Добавляется связь `engagement-to-audience`.** Upstream `engagement-observation` → downstream `audience`, pattern `published-language`, integration `event`, contract `engagement-event`. Аудитория ведёт поведенческую сегментацию по квалифицированным событиям взаимодействия, а не по сырым сигналам.
- **Добавляется связь `governance-policy-for-audience`.** Upstream `data-governance` → downstream `audience`, pattern `published-language`, integration `policy`, contract `data-zone-policy`. Аудитория применяет политику зон данных, не воспроизводя правило локально.
- **Добавляется связь `lead-to-sales`.** Upstream `lead-management` → downstream `sales-workflow`, pattern `customer-supplier`, integration `query`, contract `company-profile`. Сделка читает профиль компании (в том числе размер) из `lead-management`, а не владеет его копией.
- **Обновляются представления модели.** `architecture/diagrams/context-relationships.puml` получает три ребра, `docs/domain-model.md` — три записи в разделе «Межконтекстные отношения и контракты».
- **Что не меняется.** Capability, агрегаты, aggregate roots, сущности, domain events и policies не добавляются и не меняются; владение остаётся прежним. Новые контракты не вводятся: `engagement-event` объявлен `engagement-observation`, `data-zone-policy` — `data-governance`, `company-profile` — `lead-management`.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

Нет.

**Обоснование.** Требования capability `domain-architecture` уже требуют, чтобы каждая разрешённая зависимость была объявлена в каталоге. Изменение добавляет данные каталога и его представления, а не новое требование, поэтому помечено `skip_specs: true` в `.openspec.yaml`. Продуктовое поведение не меняется: отношение сегментации к событиям, применение политики зон и чтение профиля компании уже описаны соответствующими capability.

## Impact

**Затрагиваемые bounded contexts:** `engagement-observation`, `audience`, `data-governance`, `lead-management`, `sales-workflow`.

**Документы и артефакты:** `architecture/domain-catalog.yaml` (три связи), `architecture/diagrams/context-relationships.puml` (три ребра), `docs/domain-model.md` (раздел межконтекстных отношений).

**Проверка:** `go -C tools/archlint run .` и `openspec validate close-remaining-catalog-gaps --strict`.

**Риски.**

- **Отдельное доменное событие истории размера компании не добавляется.** Связь `lead-to-sales` объявляет только чтение контракта `company-profile`; изменение размера компании не становится отдельным доменным событием и не публикуется в `sales-workflow`. Если сделке потребуется реакция на смену размера, это отдельное решение о событии и подписке.
- **Новый потребитель `data-zone-policy` и `engagement-event`.** Обе связи переиспользуют существующие контракты, поэтому новой версии контракта не требуется; добавление потребителя смысл контракта не меняет.
