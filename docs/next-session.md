# Следующая сессия агента: список работ

> **Назначение.** Durable-хендофф: что закрыто, что осталось, кому и в каком
> порядке делать, чем проверять. Производный документ; при расхождении правы
> источники истины (`AGENTS.md`, `architecture/domain-catalog.yaml`, `openspec/`).
> Первым читать `AGENTS.md` §6 (чек-лист старта) и `docs/progress-log.md`
> («Фокус сейчас»).

## 0. Состояние на конец прошлой сессии

- **HEAD:** `e7c9158` (предыдущий — `4bf0b61`; оба backend-волн). Рабочее дерево
  чистое; ветка `main`.
- **Гейт зелёный:** `make check` (OpenSpec **50 passed**, build/test/vet/importlint/
  archlint), `make test-postgres` (11 suite `ok`), `go -C tools/archlint run .`
  (`53 capability / 30 связей`).
- **Волны 1–3 сделаны:** backend-контур (RAG `vector-recall`, `request-shortfall`,
  мост `delivery-bridge`), консоль (срезы 3–5), генерация лендингов (`linguistics`),
  in-process E2E `approve → relay → мост → SMTP` (`backend/cmd/lead-gen/e2e_approved_to_sent_test.go`).
- **Архив:** `real-vector-recall`, `shortfall-to-letters`, `outreach-delivery-bridge`
  заархивированы; спеки в `openspec/specs/`.

**Текущие активные changes (остаток задач, `openspec list`):**

| Change | Осталось | Суть |
|---|---:|---|
| `web-frontend-architecture` | 14/41 | срезы 3.4, 5.3, 6.x, 7.x, 8.2 |
| `service-topology-console` | 57/58 | экран топологии (нужна инфраструктура) |
| `landing-page-generator` | 9/36 | 1.2, 2.1, 6.2–6.4, 8.1, 10.1–10.3 |
| `sequences-and-reporting` | 17/43 | сверить остаток цепочек/отчётности и архивировать |
| `platform-governance` | 32/35 | access-срез сделан — сверить и архивировать |
| `email-verification` | 32/36 | реальный провайдер верификации |
| `lead-segmentation` | 31/36 | `audience` |
| `company-size-assessment` | 50/51 | источники размера |
| `outreach-channel-form` | 38/39 | форма-канал |
| `data-governance` | 20/34 | foundation |
| `service-per-context` | 10/54 | выделение сервисов |
| `migrate-platform-to-go` | 15/26 | каркас |
| `system-map-and-cpp-baseline` | 9/23 | карта |
| `archive-order-and-rule-deltas` | 14/17 | служебное |
| `full-enterprise-core-scope` | 67/67 | рамка + юргейт (вопрос вне кода) |
| `self-hosted-crawler` | 35/35 | вытеснен Firecrawl — решить судьбу |

## 1. Приоритет 0 — замкнуть продуктовый контур

### 1.1 Новый change `material-ref-in-approved-action`
**Проблема (найдена E2E волны 3):** в реальном пути `approve → outbox → relay`
(`outreach-orchestration`, `relay.MapActionApproved`) поле
`approvedaction.ApprovedAction.MaterialVersionRef` не заполняется, поэтому материал
письма не резолвится — письмо уходит с пустыми темой/телом. Материал резолвится
только когда ссылка задана вручную (проверено веткой `bridge.Sink()`).
**Сделать:** при утверждении черновика заполнять `MaterialVersionRef` из материала
черновика (связь с `content-production`/версией контента) и, если нужно, поля
черновика, чтобы `content-version` вернул тему/тело. Обновить
`e2e_approved_to_sent_test.go` (основной путь → непустые subject/body).
**Scope:** `backend/contexts/outreach-orchestration/**` (+ каталог/контракт при
необходимости), при нужде — `contracts/outreach-orchestration/**`.
**Верификация:** E2E-тест основного пути даёт непустой материал; `make check`/`make test-postgres` зелёные.
**Порядок:** создать change (`openspec new change material-ref-in-approved-action`),
proposal→specs→design→tasks, применить.

### 1.2 Проводка и live-контур
- `content-production` `SiteCollector` (Firecrawl) — впрыск из `cmd` (сейчас nil).
- Live-приёмка: `docker compose up --build` (PG + Temporal + SMTP + брокер), прогнать
  сквозной сценарий `docs/e2e-plan.md` (клиент → заявка → валидация → поиск/RAG →
  письма → утверждение → отправка → дашборд). Требует реальных Temporal/SMTP.
  **Scope общих файлов — только головной агент:** `backend/cmd/lead-gen/**`,
  `Makefile`, `go.work`, `deploy/**`, `docker-compose`/`compose.yaml`.

## 2. Приоритет 1 — до-закрыть `web-frontend-architecture` (14 задач)

| Задача | Что | Scope | Верификация |
|---|---|---|---|
| 3.4 | Экспорт отчётов как длительная операция через очередь | `analytics` + примитив `backend/platform/jobs` | экспорт возвращает id задания и ссылку на результат |
| 5.3 | Раздел интеграций и состояния адаптеров | `platform-governance` + новый контракт `adapter-status` владельца `integration-platform` | секреты не утекают; недоступность владельца — честный исход |
| 6.1–6.4 | Длительные операции и DLQ (`команда→id→прогресс→результат`) | `backend/platform/jobs` + `cmd` + экраны контекстов | прогресс, повтор, DLQ-видимость |
| 7.1–7.5 | Локализация, словарь верификации адресов, клавиатурная доступность, контраст/не-только-цвет, `no-store`/rate-limit | `frontend/components/**` + контексты | проверки a11y/контраста/заголовков |
| 8.2 | importlint-правило для HTTP-адаптеров консоли | `tools/importlint/**` | отрицательная фикстура отклоняется |
| 8.3 | `openspec validate`/`status` | — | проходит |

**Сериализация:** `frontend/components/**` — **один writer**. Задачи 7.x и
navigation-раздел «Рассылки» (сейчас у `outreach` локальный обход; нужно добавить
`ConsoleSectionOutreach` в общий слой и обновить golden-тесты всех консолей)
выполнять **последовательно**, не параллельно с контекстными агентами.

## 3. Приоритет 2 — `landing-page-generator` (9 задач)

- **1.2** разбор документов через `Firecrawl parse`.
- **2.1** реальный сбор с сайта: впрыск `crawl.Port` (Firecrawl) из `cmd` в
  `content-production` (`Config.SiteCollector`).
- **6.2–6.4** приёмник заявок: транзитный приёмник и поведение при недоступности —
  `frontend/public` (edge) + контур клиента; персональные данные не сохранять.
- **8.1** сравнение со сторонними источниками — внешний сервис (честный `unavailable`).
- **10.1–10.3** сквозной сценарий, аудит ПД, подтверждение переиспользования
  (`linguistics` + движок сбора).

## 4. Приоритет 3 — сверка и архивация прочих changes

Много задач в активных changes уже реализованы кодом (прошлые сессии), но не
отмечены. Рекомендация: по каждому change — сверить `tasks.md` с кодом и, где
критерий верификации выполнен, отметить `[x]`, затем `openspec verify`/`archive`.
Начать с `sequences-and-reporting`, `platform-governance`, `data-governance`,
`service-per-context`, `migrate-platform-to-go`, `system-map-and-cpp-baseline`.
Отдельно решить: `self-hosted-crawler` (вытеснен), `full-enterprise-core-scope`
(юргейт), `archive-order-and-rule-deltas`.

## 5. Приоритет 4 — внешние провайдеры (честный `unavailable` до подключения)

- `email-verification` — провайдер верификации адресов (порт заведён).
- `company-size-assessment` — источники трафика/приложений/продуктов.
- `linguistics` — уже подключён LLM-порт; проверить на реальном ключе.

## 6. Правила работы (обязательно)

- **OpenSpec-процесс** (`AGENTS.md` §2): любое изменение — через change
  (`proposal→specs→design→tasks→apply→verify→archive`); каталоги под
  `openspec/changes/` — только через `openspec new change`.
- **Один writer на bounded context**; **общие файлы** (`backend/cmd/lead-gen/**`,
  `Makefile`, `go.work`, `architecture/**`, `docs/**`, `deploy/**`, `openspec/**`,
  `frontend/components/**`) правит только головной агент, по одному за раз.
- **Заглушки запрещены** (`AGENTS.md` §3): нет внешней системы — задача открыта;
  честный `unavailable` — явное состояние, держит change открытым.
- Статус задачи — `[x]` только при выполненном критерии «верификация».
- Не коммитить без явной просьбы; не переписывать чужие правки.

## 7. Гейт (перед отметкой/архивацией)

```sh
make check            # build/test/vet/importlint/archlint/openspec
make test-postgres    # 11 suite на одноразовом PostgreSQL (требует Docker)
go -C tools/archlint run .
openspec validate <change> --strict
```

## 8. Рекомендуемый параллелизм волнами

- **Волна A (параллельно):** `outreach-orchestration` (1.1 material-ref),
  `analytics` (3.4), `platform-governance`+`integration-platform` (5.3 adapter-status),
  `content-production` (1.2/2.1), `frontend/public` (6.2–6.4).
- **Волна B (последовательно, после A):** `frontend/components` (7.x + nav-раздел),
  `tools/importlint` (8.2), примитив jobs/DLQ (6.x) — один writer.
- **Волна C:** live-приёмка на `docker compose` (головной агент), сверка/архивация changes (§4).

## 9. Точка подхвата

1. `AGENTS.md` §6 → `docs/progress-log.md` («Фокус сейчас») → этот файл.
2. `openspec list` — активные changes и остаток задач.
3. `docs/implementation-matrix.md` — факт по слоям.
4. Начать с §1.1 (замкнуть материал письма), затем §8 волны A.
