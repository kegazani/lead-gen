## Why

DDD-ревизия прошлых changes выявила два межконтекстных взаимодействия, которые уже требуются спеками, но не объявлены в `architecture/domain-catalog.yaml`. `outreach-orchestration` обязывает генерировать ссылку на конференцию через провайдера, а `email-verification` замыкает цикл подавления асинхронными отказами после отправки. Каталог — канонический источник зависимостей, и без этих связей модель недоговаривает: у требования нет объявленного контракта, а правило «домен чужого контекста не импортируется напрямую» не на что опереть.

## What Changes

- **Добавляется связь `integrations-for-outreach`.** Upstream `integration-platform` → downstream `outreach-orchestration`, pattern `anticorruption-layer`, integration `command`, contract `provider-port`. Провайдер генерации ссылки на конференцию подключается как адаптер за provider port, а не как SDK в домене `outreach-orchestration`.
- **Добавляется связь `delivery-to-lead`.** Upstream `message-delivery` → downstream `lead-management`, pattern `published-language`, integration `event`, contract `delivery-event`. Асинхронный отказ доставки возвращается в aggregate `contact` и обновляет статус адреса — основа цикла подавления `email-verification`.
- **Обновляются представления модели.** `architecture/diagrams/context-relationships.puml` получает оба ребра, `docs/domain-model.md` — обе записи в разделе «Межконтекстные отношения и контракты».
- **Что не меняется.** Capability, агрегаты, aggregate roots, сущности, domain events и policies не добавляются и не меняются; владение остаётся прежним. Новые контракты не вводятся: `provider-port` объявлен `integration-platform`, `delivery-event` — `message-delivery`.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

Нет.

**Обоснование.** Требования capability `domain-architecture` уже требуют, чтобы каждая разрешённая зависимость была объявлена в каталоге. Изменение добавляет данные каталога, а не новое требование, поэтому помечено `skip_specs: true` в `.openspec.yaml`. Поведение продуктовых capability не меняется — оно уже описано в `email-verification`.

## Impact

**Затрагиваемые bounded contexts:** `integration-platform`, `outreach-orchestration`, `message-delivery`, `lead-management`.

**Документы и артефакты:** `architecture/domain-catalog.yaml` (две связи), `architecture/diagrams/context-relationships.puml` (два ребра), `docs/domain-model.md` (раздел межконтекстных отношений).

**Проверка:** `go -C tools/archlint run .` и `openspec validate close-catalog-relationship-gaps --strict`.

**Риски.**

- **Неверная граница владельца подавления.** Стоп-лист принадлежит `data-governance` (aggregate `processing-governance`), а `lead-management` лишь обновляет статус адреса. Изменение объявляет возврат отказов в `lead-management`; передача в стоп-лист остаётся за политикой владельца и в объём не входит — при необходимости это отдельное уточнение модели.
- **Совместимость контрактов.** Обе связи переиспользуют существующие контракты, поэтому новой версии контракта не требуется.
