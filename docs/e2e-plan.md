# План: сквозной E2E-продукт + клиентский и админский фронты

> **Handoff-план.** Дополняет `docs/progress-log.md` (точка подхвата) и
> `docs/core-roadmap.md` (этапы). Источники истины — `AGENTS.md`,
> `architecture/domain-catalog.yaml`, `openspec/`. Документ — производный план, не
> требование; при расхождении правы источники истины.

## Цель

Рабочий сквозной контур лидогенерации и полные клиентский и админский фронты поверх
реального backend.

## Целевой поток

1. Клиент **регистрирует компанию** (имя + описание).
2. Создаёт **заявку**: услуги, которые компания предоставляет, + описание искомых лидов.
3. **Админ** видит заявку; если она неполная/плохая — связывается с клиентом и правит текст, затем одобряет (или отклоняет с причиной).
4. **ИИ запускает поиск**: сначала по существующему пулу, при недоборе — ограниченный дозабор; семантика/RAG по профилю и офферу.
5. При **недостатке лидов** генерируются **персонализированные письма** под каждую компанию.
6. **Оператор** смотрит письмо, при необходимости правит и **утверждает**; происходит **отправка** (человек-в-контуре, без автоотправки).
7. **Клиент** видит **дашборд** (агрегаты: найдено → прокоммуницировано → ответили → прогретые).

## Соответствие домену (уже описано и в основном реализовано — эпик `client-request-domain`)

| Шаг | Capability / спека | Контекст |
|---|---|---|
| Регистрация компании | `client-portal` | `platform-governance`, `request-fulfillment` |
| Заявка (оффер) | `client-request` | `request-fulfillment` |
| Валидация админом | `request-validation` | `request-fulfillment` |
| Durable-исполнение | `request-fulfillment-execution` | `request-fulfillment` |
| Поиск: пул → дозабор | `lead-search` | `lead-management` |
| Семантика/RAG | `semantic-lead-search` | `lead-management` (+ `integration-platform` vector) |
| Релевантность | `lead-relevance` | `lead-management` |
| Цель по числу компаний | `company-quota-search` | `lead-management` |
| Диалог плана | `search-strategy` | `lead-management` |
| Письма под лид | `email-generation`, `lead-email`, `email-template-library` | `lead-management` |
| Утверждение и отправка | `outreach-approval`, `outreach-channel-email`, `domain-deliverability` | `outreach-orchestration` → `message-delivery` |
| Согласие/стоп-лист | `suppression-policy` | `data-governance` |
| Дашборд клиента | `client-dashboard` | `analytics` |

**Вне текущей цели:** CRM/сделки (`sales-workflow`) и биллинг (`billing`) удалены из продукта; `publishing`/`community-intelligence` удалены.

## Пробелы к закрытию

1. **Векторный стор для RAG** — сейчас in-memory (`integration-platform`/`vector`), нужен реальный (pgvector или stor).
2. **Авто-триггер «недобор лидов → генерация писем»** — явной capability нет.
3. **Отчётность/дашборды** — `sequences-and-reporting` группы 9–12.
4. **Фронты** — `web-frontend-architecture` (14/41), `service-topology-console` (0/57), `landing-page-generator` (1/36).
5. **Мост `approved-action → delivery-command`** — замыкание «утверждено → отправлено».

## Волны (параллельные задачи)

### Волна 1 — backend (параллельно, один writer на контекст)

| # | Scope (каталоги) | Задача | Change |
|---|---|---|---|
| 1 | `backend/contexts/integration-platform/**`, `contracts/integration-platform/**` | реальный векторный стор/RAG за портом `vector` (+ миграции) | создать `real-vector-recall` |
| 2 | `backend/contexts/analytics/**` | отчётность/дашборды (метрики, фильтры, выгрузки) | `sequences-and-reporting` (9–12) |
| 3 | `backend/contexts/request-fulfillment/**`, `contracts/request-fulfillment/**` | валидация + авто-триггер «недобор → письма» | создать `shortfall-to-letters` |
| 4 | `backend/contexts/lead-management/**` | использование RAG-поиска, квота, письма | `company-size-assessment`/`email-verification`/`lead-segmentation` + чек RAG |
| 5 | `backend/contexts/platform-governance/**` | регистрация/тенант/портал-backend | `platform-governance` |
| 6 | `backend/contexts/content-production/**` | генерация лендингов | `landing-page-generator` |
| 7 | `backend/contexts/message-delivery/**`, `contracts/message-delivery/**` | мост `approved-action → delivery-command` | создать `outreach-delivery-bridge` |

### Волна 2 — фронты (сериализовано: общий `frontend/components` — один writer)

- Админская консоль — `web-frontend-architecture`.
- Клиентский портал + дашборд UI — `client-portal`/`client-dashboard`.
- Экран топологии — `service-topology-console`.
- Публичные лендинги — `frontend/public` + `landing-page-generator`.

### Волна 3 — сквозная приёмка

E2E на реальном backend: клиент → заявка → валидация → поиск/RAG → письма → утверждение → отправка (SMTP) → дашборд; PG + движок цепочек.

## Правила параллелизма

- **Один writer на bounded context.** Каталоги контекстов не пересекаются.
- **Общие файлы** (`backend/cmd/lead-gen`, `Makefile`, `go.work`, `architecture/**`, `docs/**`, `deploy/**`, `openspec/**`) правит только головной агент.
- **Каждый поток — под OpenSpec change**; новый change создавать через `openspec new change` (не вручную).
- **Гейт:** `make check` и `make test-postgres` зелёные; `go -C tools/archlint run .` код 0.
- Задачи отмечать `- [x]` только при выполненном критерии «верификация».
- **Заглушки запрещены** как результат (`AGENTS.md` §3); честный `unavailable` — только как явное состояние.
- `openspec/changes/archive/**` не редактировать.

## Как запустить в новой сессии

1. Прочитать (авто — `AGENTS.md`; затем) `docs/progress-log.md` (строка «Фокус сейчас») и этот файл.
2. Создать недостающие changes: `real-vector-recall`, `shortfall-to-letters`, `outreach-delivery-bridge` (proposal → specs → design → tasks).
3. Раздать **волну 1** (по одному агенту на контекст из таблицы) с точными scope-ограничениями.
4. Головной агент: интеграция общих файлов, создание changes, прогон гейта, обновление `docs/implementation-matrix.md`/`docs/progress-log.md`.
5. После волны 1 — **волна 2** (фронты), затем **волна 3** (E2E).
