# Следующая сессия агента: список работ

**Приоритет следующей сессии (2026-10-12):** change
[`customer-demo-request-to-emails`](../openspec/changes/customer-demo-request-to-emails/)
— limited demo path от существующих registration/profile approval до реального
thematic crawl, писем всем найденным, durable handoff («Подготовлено к проверке») и
отдельного exact-version editorial approve/reject без доставки. Сначала читать
[`docs/e2e-plan.md`](e2e-plan.md), затем change `tasks.md`. Не расширять до whole v2,
recovery/hardening, SMTP или forced-crawl mode. Это подготовленный план, не заявление
о готовности; live acceptance без реальных Firecrawl/LLM остаётся открытой.

> **Назначение.** Durable-хендофф: что закрыто, что осталось, кому и в каком
> порядке делать, чем проверять. Производный документ; при расхождении правы
> источники истины (`AGENTS.md`, `architecture/domain-catalog.yaml`, `openspec/`).
> Первым читать `AGENTS.md` §6 (чек-лист старта) и `docs/progress-log.md`
> («Фокус сейчас»).

## 0. Состояние на конец прошлой сессии

- **HEAD:** `2f518ad`; в рабочем дереве — незакоммиченная интеграция контура
  агента/аналитики; ветка `main`.
- **Гейт зелёный (по журналу):** `make check` (OpenSpec **58 passed**, build/test/vet/
  importlint/archlint), `make test-postgres` (12 suite `ok`), `go -C tools/archlint run .`
  (`12 контекстов / 59 capability / 49 связей`).
- **Ранее сделано:** backend-контур (RAG `vector-recall`, `request-shortfall`, мост
  `delivery-bridge`), консоль (срезы 3–5), генерация лендингов (`linguistics`),
  материал письма в основном пути `approve → outbox → relay`; срезы длительной
  выгрузки, раздела интеграций (`adapter-status`) и приёмника заявок.
- **Контур агента/аналитики (change `intelligent-lead-operations`, 43/44):** новый
  контекст `agent-orchestration` (домен `agent-pipeline`/`agent-run`, app, порты,
  PostgreSQL, Temporal), read-модель `lead-quality-insights` (`analytics`), экран
  `operations-cockpit` (`platform-governance`), проводка в `cmd/lead-gen` (7.3).
  Открыт живой сквозной прогон (8.3): блоки, чьи контракты владельцев точка входа
  не читает, честно недоступны.
- **Обратная связь поиска (change `search-quality-feedback`, ✅ Complete):**
  `search-recommendation` (`lead-management`) — применение рекомендации анализа как
  новый запуск поиска, идемпотентно; read-контракт `lead-relevance`; связи
  `search-feedback-from-analytics` и `lead-relevance-for-agents`.
- **Архив:** `real-vector-recall`, `shortfall-to-letters`, `outreach-delivery-bridge`,
  `material-ref-in-approved-action`, `operator-cross-tenant-access`,
  `console-shell-navigation`, `lead-selection-diagnostics`,
  `request-to-outreach-handoff`; спеки в `openspec/specs/`.
- **Открыто у других change:** `live-e2e-contour` 3.1/3.2/4.3 (заглушка риска
  `NoUpstreamRiskChecks`), `embedding-config-separation` 2.2 (нужен ключ эмбеддингов).

**Текущие активные changes (остаток задач, `openspec list`):**

| Change | Осталось | Суть |
|---|---:|---|
| `intelligent-lead-operations` | 43/44 | контур агента/аналитики: `agent-orchestration` + `lead-quality-insights` + `operations-cockpit`; открыт живой прогон 8.3 |
| `search-quality-feedback` | ✅ Complete | обратная связь анализа в контур поиска: `search-recommendation` (`lead-management`) + read-контракт `lead-relevance` |
| `embedding-config-separation` | 5/6 | разделение чат-LLM и эмбеддингов; открыт 2.2 (нужен ключ) |
| `live-e2e-contour` | 10/13 | сетевой live-контур; открыты 3.1/3.2/4.3 |
| `data-governance` | 16/34 | модель стоп-листа ↔ спека; outbox/UnitOfWork; envelope контрактов |
| `risk-screening` | 1/12 | реальные признаки риска (стоп-лист/верификация) на пути пакетного утверждения |
| `platform-governance` | 27/35 | access-срез сделан; остаток — 3.4/3.5/3.6 (tenant-context, audit-recorded, порты) |
| `service-per-context` | 44/54 | выделение сервисов: schema-per-service, роль СУБД, smoke-контура |
| `service-topology-console` | 35/57 | граф топологии; остаток — метрики/раскладка-в-БД/JS-остров/доступ-UI |
| `company-size-assessment` | 1/51 | источники размера (внешний сервис) |
| `email-verification` | 4/36 | реальный провайдер верификации |
| `archive-order-and-rule-deltas` | 5/17 | служебный — срабатывает по мере архивации |
| `system-map-and-cpp-baseline` | 15/23 | §2 (C++) закрыть «заменено»; 3.4 — владелец |
| `sequences-and-reporting` | 39/43 | 1.2 (правка BRD) + досверить 5.3/11.1/13.3 |
| `landing-page-generator` | 35/36 | 8.1 (внешний источник) |
| `full-enterprise-core-scope` | 0/67 | рамка + юргейт (вопрос вне кода) |
| `migrate-platform-to-go` | 11/26 | каркас |
| `lead-segmentation` | 5/36 | `audience` |
| `outreach-channel-form` | 1/39 | форма-канал |
| `self-hosted-crawler` | 0/35 | вытеснен Firecrawl — решить судьбу |

## 1. Приоритет 0 — замкнуть продуктовый контур

### 1.1 ✅ Новый change `material-ref-in-approved-action` (закрыт 2026-10-04)
**Проблема (найдена E2E волны 3):** в реальном пути `approve → outbox → relay`
(`outreach-orchestration`, `relay.MapActionApproved`) поле
`approvedaction.ApprovedAction.MaterialVersionRef` не заполнялось, поэтому материал
письма не резолвился — письмо уходило с пустыми темой/телом.
**Сделано:** поле `Material` в `domain.ActionApproved` (заполняется из `draft.Spec().Material`),
перенос в контракт в `MapActionApproved`, аддитивное поле в проводимой форме outbox,
E2E основного пути ждёт непустые subject/body. Контракт `approved-action` не менялся.
Заархивирован (`openspec/changes/archive/2026-10-04-material-ref-in-approved-action`),
спека `openspec/specs/outreach-approval`. Гейт зелёный.

### 1.2 Проводка и live-контур
- `content-production` `SiteCollector`/`DocumentParser` (Firecrawl) — впрыск из `cmd` сделан (один клиент на оба порта; без ключа — nil).
- **Live-контур (change `live-e2e-contour`).** Фаза 1 и **Фаза 2** сделаны и подняты живьём: сетевой транспорт `approved-action`/`content-version`/`delivery-target` и раздельная топология (шесть контейнеров групп одного образа, `LEADGEN_PROCESS`/`LEADGEN_*`). Проверено: `core` — `network:true`, `delivery` принимает `POST /approved-action` и ходит в `demand` на `POST /delivery-target`. **Осталось:** приёмка 3.1/3.2 (нужны реальные ключи Firecrawl/LLM) и 4.3 (заглушка риска `NoUpstreamRiskChecks` на пути утверждения). **Находка:** изоляция тенантов скрывает заявку клиента от демо-админа — следующий change (веха M2).
  **Scope общих файлов — только головной агент:** `backend/cmd/lead-gen/**`, `Makefile`, `go.work`, `deploy/**`, `compose.yaml`, `Dockerfile`.

## 2. Приоритет 1 — до-закрыть `web-frontend-architecture`

| Задача | Что | Scope | Верификация |
|---|---|---|---|
| 3.4 ✅ | Экспорт отчётов как длительная операция через очередь | `analytics` + примитив `backend/platform/jobs` | экспорт возвращает id задания и ссылку на результат |
| 5.3 ✅ | Раздел интеграций и состояния адаптеров | `platform-governance` + новый контракт `adapter-status` владельца `integration-platform` | секреты не утекают; недоступность владельца — честный исход |
| 6.1–6.4 | Длительные операции и DLQ (`команда→id→прогресс→результат`) | `backend/platform/jobs` + `cmd` + экраны контекстов | прогресс, повтор, DLQ-видимость |
| 7.1–7.5 | Локализация, словарь верификации адресов, клавиатурная доступность, контраст/не-только-цвет, `no-store`/rate-limit | общий слой готов (`frontend/components`); нужна проводка по контекстам | проверки a11y/контраста/заголовков |
| 8.2 ✅ | importlint-правило для HTTP-адаптеров консоли | `tools/importlint/**` | отрицательная фикстура отклоняется |
| 8.3 | `openspec validate`/`status` | — | проходит |

**Сериализация:** `frontend/components/**` — **один writer**. Задачи 7.x и
navigation-раздел «Рассылки» (сейчас у `outreach` локальный обход; нужно добавить
`ConsoleSectionOutreach` в общий слой и обновить golden-тесты всех консолей)
выполнять **последовательно**, не параллельно с контекстными агентами.

## 3. Приоритет 2 — `landing-page-generator` (осталось 3 задачи)

- **1.2 ✅** разбор документов через `Firecrawl parse` (контракт `documentparse` + `content-production`).
- **2.1 ✅** реальный сбор с сайта: впрыск `crawl.Port` (Firecrawl) из `cmd` в `content-production` (`Config.SiteCollector`), границы зафиксированы.
- **6.2–6.4 ✅** приёмник заявок: реальная пересылка в `frontend/public` (`-submission-receivers`/`-submission-transit`), транзитный приёмник, отказ без сохранения ПД.
- **8.1** сравнение со сторонними источниками — внешний сервис (честный `unavailable`).
- **10.1–10.3 ✅** сквозной сценарий, аудит ПД, подтверждение переиспользования
  (`linguistics` + движок сбора) — тесты в `content-production`.

## 4. Приоритет 3 — сверка и архивация прочих changes

**Аудит 2026-10-04 (read-only) — вывод: архивировать сейчас нечего.**
Отметки по сверке с кодом уже внесены. Блокеры и решения:

- `sequences-and-reporting` — **ближайший кандидат** (39/43): осталось 1.2 (правка BRD — перечень «Движки без capability» уже удалён, формулировку надо переписать, а не закрывать) и досверить 5.3 (классификация через `linguistics`), 11.1/13.3 (аудит границы).
- `platform-governance` — **не архивировать**: нет модуля `contracts/platform-governance` (`accessdecision`/`tenantcontext`/`auditrecorded`); задача 5.3 не закрыта — в `outreach-orchestration/internal/adapters/access` всё ещё «ЗАГЛУШКА СРЕЗА»; публикация outbox в брокер не подключена.
- `data-governance` — **не архивировать**: модель `SuppressionEntry` расходится со спекой (нет канала и вида записи), нет портов `Outbox`/`UnitOfWork`, контракты — только `doc.go`.
- `service-per-context` (44/54) — **не архивировать**: остаток — реальные внешние зависимости (schema-per-service, роль СУБД, smoke-контура); отдельно поправить устаревшую запись «Отложено» про `message-delivery`.
- `migrate-platform-to-go` (11/26) — **не архивировать**: нет реестра фабрик адаптеров, портов объектного хранилища/CRM/трафика, старт-отказа на неизвестный адаптер.
- `system-map-and-cpp-baseline` (15/23) — §2 (C++/userver/CMake) устарел: закрыть явной пометкой «заменено `migrate-platform-to-go`»; 3.4 — решение владельца.
- `archive-order-and-rule-deltas` (5/17) — служебный, срабатывает по мере архивации других.
- `email-verification` (4/36), `company-size-assessment` (1/51), `lead-segmentation` (5/36), `outreach-channel-form` (1/39) — реально не завершены (домен/контракт есть, контур сбора/хранения/пакетного режима — нет; модель сегментации и формы-канала не совпадает со спекой).
- `self-hosted-crawler` (вытеснен Firecrawl) и `full-enterprise-core-scope` (юргейт) — судьба вне кода, решается отдельно.

**Порядок:** сначала закрыть блокеры `platform-governance`/`data-governance` (или упростить спеки), затем `sequences-and-reporting` → verify/archive.

## 5. Приоритет 4 — `service-topology-console` (фундамент сделан)

Фундамент графа готов: `backend/platform/topology` (типы, загрузчики каталога/`services.yaml`/
`infrastructure.yaml`, вывод рёбер «сервис → инфраструктура», детерминированная раскладка,
fail-closed, кэш) + проверка `deploy/infrastructure.yaml` в `tools/archlint`. Осталось:
метрики (3.x), хранение раскладки (5.x), HTTP/UI (6.x), доступ (7.x), документация (8.x).
Число связей каталога в спеке больше не хардкодится (сейчас 49).

## 6. Приоритет 5 — внешние провайдеры (честный `unavailable` до подключения)

> **M5 (2026-10-04).** Роль `api` `integration-platform` доведена до реальной
> HTTP-поверхности (change `integration-platform-api-surface`): состояние
> адаптеров по контракту `adapter-status` и реестр, без внешних ключей. Роль
> `ingest` — честное поведение без имитации приёма. Ниже — что по-прежнему
> требует решения владельца (не закрывается заглушкой).

- `email-verification` — провайдер верификации адресов **не выбран** (BRD §8,
  вопрос 9a). Порт и реальные адаптеры (`internal/adapters/verification/{remote,dns}`)
  есть, но провайдер задаётся адресом API + ключом, поэтому контур честно
  `unavailable`; задачи 2.x–10.x открыты. **Нужно:** выбор сервиса (≥ 20 000 адресов/ч,
  покрытие catch-all) + endpoint/ключ; затем проводка в `cmd/lead-gen` и 10.x.
- `company-size-assessment` — **не выбран** сервис оценки трафика (BRD §8,
  вопрос 15). Метрика трафика честно `недоступно` (ноль не подставляется); домен
  и тесты есть. **Нужно:** выбор сервиса (тариф/покрытие) + endpoint/ключ; ключ
  Firecrawl уже требуется основным контуром.
- `integration-platform` `provider-port`/`vector` — сетевые поверхности не
  выставляются, пока не сконфигурирован адаптер поставщика/эмбеддера (нет
  провайдера — нет функции). Это `deploy`-топология, не заглушка.
- `linguistics` — LLM-порт подключён (`content-production` `cfg.LLM`); на реальном
  ключе генерация работает, без ключа — честно недоступна. Заглушек на
  продуктовом пути нет.

## 7. Правила работы (обязательно)

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

## 8. Гейт (перед отметкой/архивацией)

```sh
make check            # build/test/vet/importlint/archlint/openspec
make test-postgres    # 12 suite на одноразовом PostgreSQL (требует Docker)
go -C tools/archlint run .
openspec validate <change> --strict
```

## 9. Рекомендуемый параллелизм волнами

- **Волна A (параллельно):** `outreach-orchestration` (1.1 material-ref),
  `analytics` (3.4), `platform-governance`+`integration-platform` (5.3 adapter-status),
  `content-production` (1.2/2.1), `frontend/public` (6.2–6.4).
- **Волна B (последовательно, после A):** `frontend/components` (7.x + nav-раздел),
  `tools/importlint` (8.2), примитив jobs/DLQ (6.x) — один writer.
- **Волна C:** live-приёмка на `docker compose` (головной агент), сверка/архивация changes (§4).

## 10. Точка подхвата

1. `AGENTS.md` §6 → `docs/progress-log.md` («Фокус сейчас») → этот файл.
2. `openspec list` — активные changes и остаток задач.
3. `docs/implementation-matrix.md` — факт по слоям.
4. Начать с §1.1 (замкнуть материал письма), затем §8 волны A.
