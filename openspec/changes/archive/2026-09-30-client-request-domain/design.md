## Context

Платформа построена context-first на Go: канонические границы в `architecture/domain-catalog.yaml`, их объяснение в `docs/domain-model.md`. Один сервис развёртывания на контекст, роли запуска (`api`, `worker` и др.). Сейчас есть контексты `lead-management` (поиск и лиды), `audience` (снимки аудитории), `outreach-orchestration` (кампании и утверждение), `sales-workflow` (сделки и CRM), `billing`, `platform-governance` (тенанты). Сущности «заявка клиента» нет: задания поиска заводятся оператором вручную. Мотивация — в `proposal.md`.

Добавление контекста/агрегата обязывает обновить каталог, `docs/domain-model.md` и затронутые диаграммы в рамках этого же change (правило `domain-architecture`).

## Goals / Non-Goals

**Goals:**

- Определить owning context и тактическую модель заявки (aggregate `request`, entity `offer`).
- Задать статусную машину, инварианты и доменные события заявки.
- Задать межконтекстные контракты (ссылки на `audience-snapshot`, `campaign`, `deal` по идентификаторам).

**Non-Goals:**

- Агентный конвейер исполнения (Temporal) — change `temporal-execution-platform` (№2).
- Валидационный workflow админа (звонок, правки) — change `request-validation-workflow` (№3).
- Векторный поиск и обогащение — change `semantic-lead-search` (№4).
- Клиентский портал и форма оффера — change `client-portal` (№5).

## Decisions

### Решение 1: Новый core bounded context `request-fulfillment`

Заявка — core-концепт (это вход в ценностную цепочку продукта) и не укладывается ни в один существующий контекст: она порождает поиск (`lead-management`), кампанию (`outreach-orchestration`) и сделку (`sales-workflow`), а сама не является ни одним из них.

- **Альтернатива A (отвергнута): агрегат в `outreach-orchestration`.** Заявка — вход *до* кампании; перенос туда перегрузил бы контекст чужим языком («оффер», «валидация заявки») и смешал бы intake с исполнением исходящих действий.
- **Альтернатива B (отвергнута): в `sales-workflow`.** Сделка — результат, а не запрос; заявка не является коммерческой возможностью сама по себе.
- **Альтернатива C (отвергнута): не выделять сущность вовсе.** Тогда workflow (Temporal) негде хранить состояние, а клиенту нечего показать; отпадает при первом же шаге дорожной карты.

Владелец модели — контекст `request-fulfillment`; workflow исполнения — application service этого контекста, а не отдельный контекст (различие «контекст vs процесс» сохраняется).

### Решение 2: Тактическая модель — `request` (AR), `offer` (E)

- **`request`** (aggregate root): принимает все команды. Хранит `tenant_id`, текущий `request-status`, ссылки на артефакты (`audience_snapshot_id`, `campaign_id`, `deal_id`).
- **`offer`** (entity внутри агрегата): версионируется (`revision`); VO `offer-content` (текст: что предлагаем, кому), `request-target` (кому и сколько нужно — уточняется в change №4).
- **VO:** `request-status`, `request-target`, `offer-content`.
- **Инварианты:** оффер обязателен и непуст; тенант неизменяем; `processing` только из `validated`; после `validated` оффер замораживается (новая версия при изменении); ссылки — только идентификаторы.
- **Доменные события:** `request-submitted`, `request-validated`, `request-processing-started`, `request-fulfilled`, `request-rejected`, `request-cancelled`.
- **Политики:** — (инварианты держит сам агрегат; аудит — `audit-recorded` на границе API `platform-governance`).

### Решение 3: Статусная машина

```
                 ┌───────────── cancel (из любого активного) ─────────────┐
                 │                                                         v
submitted ──► validating ──► validated ──► processing ──► fulfilled
                  │               │              │
                  v               v              v
               rejected       cancelled      cancelled
```

Разрешённые переходы: `submitted → validating`; `validating → validated | rejected`; `validated → processing | cancelled`; `processing → fulfilled | cancelled`; `submitted → cancelled`. `fulfilled`, `rejected`, `cancelled` — терминальные.

### Решение 4: Межконтекстные ссылки по идентификаторам

Заявка не владеет чужими агрегатами. Связь — по `id` + снимку опубликованного контракта (published-language), как требует тактическая модель:

- чтение статусов чужих агрегатов — синхронные `query`-контракты (`sync`/`http-json`);
- события жизненного цикла заявки — асинхронные `event` (`async`/`amqp`), с `event_id`, `occurred_at`, `tenant_id`, `correlation_id`, `causation_id`.

### Решение 5: Подготовка к Temporal, но не сам Temporal

Этот change задаёт только агрегат и его переходы. Движение по статусной машине (workflow) — change №2 (`temporal-execution-platform`). Агрегат уже спроектирован так, чтобы workflow мог вызывать его переходы как команды application service; сам workflow в этот change не входит.

### Решение 6: Зона данных

`offer` и атрибуты заявки — данные о бизнес-описании клиента, а не персональные данные лидов: зелёная зона. Заявка хранит только идентификаторы чужих агрегатов, поэтому красные данные не поднимаются в этот контекст.

## Risks / Trade-offs

- **[Новый контекст дорог]** → Оправдано core-статусом; каталог, модель домена и диаграммы обновляются в этом же change, `go -C tools/archlint run .` подтверждает границы.
- **[Имя контекста звучит как процесс]** → В `docs/domain-model.md` явно фиксируется: владелец модели — контекст, workflow — его application service, не отдельная граница.
- **[Пересечение с `campaign`]** → Заявка ссылается на кампанию по `id`, не переносит владение; границы и связи остальных контекстов не меняются.
- **[Структура `request-target` не финализирована]** → Поля ICP/количества уточняются в change №4, не влияя на статусную машину этого change.

## Migration Plan

Миграции данных нет (новая сущность). Порядок: обновить каталог и модель → поднять сервис `request-fulfillment` (роли `api`, `worker`) → добавить хранилище заявок → прогнать `archlint`. Откат: новый сервис изолирован, существующие потоки (поиск вручную) не затрагиваются.

## Open Questions

- Точная структура `request-target` (явные поля ICP, целевое число лидов, ниши) финализируется в change №4 `semantic-lead-search`; до этого заявка хранит их как свободный текст. Это не меняет спек и задач текущего change.
