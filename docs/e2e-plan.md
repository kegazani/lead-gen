# План: customer-demo request-to-emails

> **Handoff-план.** Дополняет `docs/progress-log.md` (точка подхвата) и
> `docs/core-roadmap.md` (этапы). Источники истины — `AGENTS.md`,
> `architecture/domain-catalog.yaml`, `openspec/`. Документ — производный план, не
> требование; при расхождении правы источники истины.

## Приоритет сейчас: change `customer-demo-request-to-emails`

Цель — показать через существующий интерфейс один свежий tenant: регистрация компании →
заявка → approve заявки → реальный crawl → сформированные тематические emails, видимые
оператору на существующем экране. «Мне не надо идеально». Артефакты:
`openspec/changes/customer-demo-request-to-emails/`.

## Целевой поток

1. Клиент проходит существующую регистрацию; оператор одобряет регистрацию и профиль.
2. Одобренный клиент подаёт заявку с раздельными offer/services и описанием desired leads; оператор одобряет заявку существующим экраном.
3. На новом tenant с пустым пулом запускается существующий тематический Firecrawl путь — без нового crawl mode. Перед кодовыми изменениями выполняется первый реальный ручной crawl через интерфейс, блокеры фиксируются по наблюдениям.
4. После поиска реальный LLM формирует тематические письма для найденных лидов независимо от shortfall. При нуле лидов письма не выдумываются.
5. Оператор видит фактически сформированные письма на существующем intake/operator экране; допустима минимальная интеграция существующего request drafts view.

**Не блокируют результат:** отправка/SMTP, отдельное approve/reject писем. Также исключены отдельная `editorial-review` capability, exact-version CAS, durable editorial handoff/status «Подготовлено к проверке», full approval/reject, acceptance отсутствия recipient, PG upgrade/CAS/legacy replay hardening, полный no-action-approved sweep, whole v2, recovery/hardening, forced-crawl mode и несвязанные фронты.

## Соответствие домену

| Шаг | Capability / спека | Контекст |
|---|---|---|
| Регистрация компании | `client-portal` | `platform-governance`, `request-fulfillment` |
| Заявка (оффер) | `client-request` | `request-fulfillment` |
| Валидация админом | `request-validation` | `request-fulfillment` |
| Durable-исполнение | `request-fulfillment-execution` | `request-fulfillment` |
| Поиск/crawl | `lead-search` | `lead-management` / существующий Firecrawl adapter |
| Письма под найденных лидов | `email-generation` | `lead-management` / реальный LLM |
| Просмотр drafts | существующий request/intake UI | `request-fulfillment` и существующий операторский экран |

**Вне текущей цели:** CRM/сделки (`sales-workflow`) и биллинг (`billing`) удалены из продукта; `publishing`/`community-intelligence` удалены.

## Критерии закрытия demo scope

1. Одна свежая компания проходит через UI регистрацию/profile approval, request с разделёнными полями и approve заявки.
2. Существующий путь выполняет реальный тематический crawl; проверяется первый ручной запуск через UI и фиксируются его фактические блокеры.
3. Реальный LLM формирует тематические письма по найденным лидам независимо от target shortfall.
4. Оператор видит эти письма на существующем UI.
5. Приёмка — один свежий tenant end-to-end через UI. Отправка и отдельное approve/reject писем не входят в критерии.

## Распределение работы change

### Владение работой

| # | Scope (каталоги) | Задача | Change |
|---|---|---|---|
| Роль | Зона | Задачи |
|---|---|---|
| Coder | существующие request/lead generation участки | минимально устранить блокеры сценария, сохраняя уже внесённый код |
| Интеграционный writer | UI composition и docs | видимость писем на существующем экране; согласовать документы |
| Tester | относящиеся к сценарию suites и UI | compile, relevant tests, один свежий tenant E2E; реальный crawl/LLM |

## Последовательность и гейты

Работы строго последовательны по пяти задачам `tasks.md`: сначала ручной crawl через UI,
затем минимальные исправления, отображение drafts и один сквозной UI acceptance. Объём
проверок ограничен компиляцией, релевантными тестами и одним E2E; не запускать полный
PG/legacy/recovery/security sweep. Ставить `[x]` только после критерия конкретной задачи.

## Правила параллелизма

- **Один writer на bounded context.** Каталоги контекстов не пересекаются.
- **Общие файлы** (`backend/cmd/lead-gen`, `Makefile`, `go.work`, `architecture/**`, `docs/**`, `deploy/**`, `openspec/**`) правит только головной агент.
- **Каждый поток — под OpenSpec change**; новый change создавать через `openspec new change` (не вручную).
- **Гейт этого demo-плана:** compile, относящиеся к сценарию тесты, один E2E и strict OpenSpec validation; не требовать полного PostgreSQL/hardening sweep.
- Задачи отмечать `- [x]` только при выполненном критерии «верификация».
- **Заглушки запрещены** как результат (`AGENTS.md` §3); честный `unavailable` — только как явное состояние.
- `openspec/changes/archive/**` не редактировать.

## Как подхватить в новой сессии

1. Прочитать (авто — `AGENTS.md`; затем) `docs/progress-log.md` (строка «Фокус сейчас») и этот файл.
2. Сначала выполнить ручной реальный crawl через существующий интерфейс на свежем tenant с пустым пулом и записать фактические блокеры.
3. Продолжить пять последовательных задач `customer-demo-request-to-emails`; не создавать параллельный change на этот scope и сохранять уже внесённый coder код.
4. Не запускать sender/SMTP; separate email approval не требуется.
