## Why

Межконтекстные контракты `lead-search` и `email-generation` (владелец `lead-management`) уже реализованы и эксплуатируются сквозным потоком заявки: `request-fulfillment` ищет лидов под заявку и генерирует черновики писем через эти контракты. Однако их поведение нигде не нормировано: в `openspec/specs/` нет capability, описывающих эти контракты, а в `architecture/domain-catalog.yaml` они не объявлены у владельца. Из-за этого работающая граница доверия (межсервисные `POST /lead-search` и `POST /email-generation` доступны без сессии консоли и CSRF) и правила матчинга критерия остаются неявными и не защищены от случайной регрессии.

Change — «догоняющая документация»: он описывает только уже реализованное поведение, ничего не меняет в коде.

## What Changes

- Добавляется capability `lead-search` — опубликованный контракт поиска лидов под заявку: сначала существующий пул, при недоборе — ограниченный краулинг; происхождение каждого лида, лимит, дедлайн, токен-матчинг критерия, дедуп по домену, wire-форма и коды ошибок, отсутствие PII.
- Добавляется capability `email-generation` — опубликованный контракт генерации персонализированного письма под лида и оффер через LLM; в промпт идут только обезличенные данные, доменный отказ вместо выдуманного письма, wire-форма и коды ошибок.
- Фиксируется политика транспорта обоих контрактов как явная граница доверия: эндпоинты обслуживают межсервисный вызов и не требуют сессии консоли и CSRF (`consoleauth.Policy.Public`), тогда как остальная консоль остаётся за сессией.
- Обновляется `architecture/domain-catalog.yaml`: оба контракта добавляются владельцу `lead-management` (в `capabilities` и `contracts`).

## Capabilities

### New Capabilities

- `lead-search`: поиск лидов под критерий заявки — пул, затем дозабор краулингом; происхождение, лимит/дедлайн, матчинг, дедуп, wire-форма, коды ошибок, отсутствие PII.
- `email-generation`: генерация персонализированного письма под лида и оффер через LLM с обезличенным промптом, разбором ответа и доменным отказом вместо выдуманного письма.

### Modified Capabilities

Нет. Существующие capability не меняются: `semantic-lead-search` описывает векторный recall, `company-quota-search` — поведение задания поиска, `lead-email` — отправку писем из консоли; ни одна из них не покрывает межконтекстные контракты `lead-search` и `email-generation`.

## Impact

- **Affected bounded contexts:** `lead-management` — владелец обоих контрактов (use case-ы `SearchLeadsForRequest` и `DraftEmailForLead`, HTTP-транспорт, публичные фабрики контрактов); `request-fulfillment` — потребитель (workflow-шаги поиска и генерации черновиков); `integration-platform` — порты `search`/`crawl`/`llm`, которыми владелец пользуется через `provider-port`.
- **Доменная модель не меняется:** новые capability не вводят агрегатов, сущностей, value objects, domain events, policies и relationships; это прикладной слой и межконтекстные контракты поверх существующих агрегатов `lead-profile` и `contact`. Каталог обновляется только составом `capabilities` и `contracts` владельца, диаграммы и `docs/domain-model.md` по границам не затрагиваются.
- **Код:** не меняется. Изменение носит документационный характер; нормируются уже существующие `backend/contexts/lead-management/contract.go`, `internal/app/search_leads.go`, `internal/app/draft_email.go`, `internal/adapters/http/leadsearch.go`, `internal/adapters/http/emailgeneration.go`, `contracts/lead-management/leadsearch/**`, `contracts/lead-management/emailgeneration/**`.
- **Проверка:** сверка дельт с реализацией, `openspec validate --all --strict`, `go -C tools/archlint run .`.
