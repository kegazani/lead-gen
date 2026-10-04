# Матрица реализации: контекст → слои

> **Статус документа: производный рабочий снимок.** Составлен по коду, а не по
> декларациям (`docs/core-roadmap.md`, `docs/progress-log.md`, `deploy/services.yaml`).
> Отвечает на вопрос «что реально собрано в каждом контексте и какие внешние
> зависимости подключены, а какие — честные заглушки». Требований не вводит;
> при расхождении правы источники истины (`openspec/`, `architecture/domain-catalog.yaml`).
> Назначение — опора для приведения `docs/core-roadmap.md` и `docs/system-map.md`
> к фактическому состоянию.
>
> **Снимок кода:** HEAD `78e35ae` (2026-10-03).

## 0. Заглушки к незамедлительному исправлению (P0)

> Правило запрета заглушек — `AGENTS.md` §3: заглушка не считается реализацией и
> держит задачу/change/контур незакрытыми. Ниже — полный перечень мест, где
> продуктовая функция сейчас опирается на заглушку. Всё в этом разделе — **P0**.
> Работы ведутся через OpenSpec change (создаётся отдельно на каждый пункт или
> группу): регистр — очередь приоритета, а не замена change'у.
>
> **Статус (2026-10-04).** Закрыты/устранены **A1** (`message-delivery`: реальная отправка `mail`/SMTP), **A2** (`content-rendering`: реальный рендер Chromium), **A9** (`sequences-and-reporting`: движок последовательностей) и **B** (`context-persistence`: PostgreSQL для 7 контекстов).
>
> **Волна 1 (2026-10-04).** Добавлены capability `vector-recall` (реальный эмбеддер + персистентный векторный индекс `integration-platform`, миграция `0002`; in-memory `vector/memory` — только тест/dev-двойник), `request-shortfall` (недобор → авто-генерация писем) и `delivery-bridge` (мост `approved-action → delivery-command`; контракты `delivery-target` у `lead-management` и `content-version` у `content-production` — расширен темой `Subject`, связи `leads-for-delivery`/`content-to-delivery`; адаптер-источник и обёртка `ApprovedActionBridge` в `message-delivery`; `ApprovedActionSink` в `outreach-orchestration`). Отчётность `campaign-reporting` групп 9–12 реализована в `analytics`. Проводка выполнена в `cmd/lead-gen`: вектор в `request-fulfillment` и `lead-management`; мост собран (резолвер адреса → резолвер материала → источник команд → приёмник утверждённых действий в outbox-релизе). **Все три change заархивированы** (specs `vector-recall`/`request-shortfall`/`delivery-bridge` в `openspec/specs/`). Осталось: остальные срезы консоли (волна 2) и live-E2E (волна 3).

### A. Внешние интеграции (заменить на реальную систему)

| # | Заглушка (файл) | Контекст | Заменить на | Capability | Блокирует |
|---|---|---|---|---|---|
| ~~A1~~ | `message-delivery/internal/adapters/mailer/unavailable.go` | `message-delivery` | реальный провайдер отправки | `outreach-channel-email` | ✅ закрыто change `message-delivery` (адаптер за контрактом `mail`/SMTP; мост `approved-action → delivery-command` — отдельный change) |
| ~~A2~~ | `content-production/internal/adapters/renderer/deterministic/renderer.go` | `content-production` | headless Chromium по CDP | `presentation-generator`, `landing-page-generator`, `linguistics` | ✅ change `content-rendering` (живой рендер PDF/PNG подтверждён) |
| ~~A9~~ | `outreach-orchestration` — планировщик цепочек (`runChains`) | `outreach-orchestration` | движок последовательностей | `outreach-sequences` | ✅ change `sequences-and-reporting` |

### B. Персистентность (in-memory вместо PostgreSQL)

Состояние в памяти процесса: не переживает рестарт, не разделяется между ролями.
Не заменена и `schema-per-service` (схема и роль СУБД на сервис — только в дизайне,
`service-per-context` 3.10–3.11).

| Контексты без PostgreSQL | Заменить на |
|---|---|
| ~~`analytics`, `audience`, `content-production`, `data-governance`, `engagement-observation`, `integration-platform`, `platform-governance`~~ ✅ | PostgreSQL зелёной зоны, адаптер + миграции на сервис. Закрыто change `context-persistence` (7 контекстов); `message-delivery` — в своём change. Образец — `lead-management`, `outreach-orchestration`, `request-fulfillment`. |

**Допустимые исключения** (не являются заглушкой как результатом, `AGENTS.md` §3):
тестовые двойники (`fake`/`deterministic` в тестах и dev), честный `unavailable`
как явное состояние нереализованного контура (он и перечислен выше — означает
нереализованность и держит change открытым).

## 1. Как читать

**Уровни зрелости контекста.**

| Уровень | Что означает |
|---|---|
| **A — рабочий контур** | домен + app + PostgreSQL (миграции) + реальная внешняя зависимость, подключённая в `cmd/lead-gen`; сквозные тесты |
| **B — вертикальный срез с СУБД** | домен + app + PostgreSQL-адаптер, но внешние зависимости — заглушки или отсутствуют |
| **C — вертикальный срез в памяти** | домен + app + порты + in-memory адаптеры; внешние зависимости — честные заглушки; состояние не переживает рестарт |

**Легенда внешних портов.**

| Метка | Значение |
|---|---|
| **R** | реальный адаптер, ходит в сеть (Firecrawl, LLM, SMTP) |
| **D** | детерминированная заглушка (`deterministic`) — воспроизводимый ответ для тестов |
| **U** | честный недоступный (`unavailable`) — порт отвечает отказом, а не выдумывает исход |
| **M** | in-memory реализация порта (данные в процессе) |

**Колонки.** «Домен» — агрегаты/сущности/VO/события/policies под `internal/domain`.
«App» — use cases под `internal/app`. «СУБД» — наличие PostgreSQL-адаптера и
миграций. «API» — HTTP-адаптер роли `api`. «Внешнее» — внешние порты. «Контракты» —
опубликованные контракты в `contracts/<owner>`.

## 2. Сводная матрица

| Контекст (сервис) | Роли | Домен | App | Порты | СУБД | API | Внешнее (реальное) | Внешнее (заглушки) | Контракты | Уровень |
|---|---|---|---|---|---|---|---|---|---|---|
| `lead-management` | api, worker | да | да | да | **PostgreSQL** (green+red, 3 мигр.) | да | Firecrawl R, LLM R, SMTP R, forum R | — | `leadsearch`, `emailgeneration` | **A** |
| `request-fulfillment` | api, worker | да | да | да | **PostgreSQL** (2 мигр.) | да | Temporal R, leadsearch R, emailgeneration R | call U | `requestintent` | **A** |
| `integration-platform` | api, ingest, worker, chains, schedule | да | да | да | — | нет | Firecrawl R, OpenAI R, SMTP R, vector R (персистентный индекс) | provider fake, repository M | `providerport`, `crawl`, `search`, `llm`, `mail`, `forum`, `vector`, `budget`, … | **A** |
| `outreach-orchestration` | api, worker, chains, schedule | да | да | да | **PostgreSQL** (1 мигр.) | да | in-proc (access, transport) | — | `approvedaction`, `callintent`, `formsubmission` | **B** |
| `platform-governance` | api, worker | да (`tenant`) | да | да | — | да | — | — | — | **B** |
| `analytics` | api, worker, schedule | нет (read-model) | да | да | — | да | consumer очереди | — | — | **B** |
| `data-governance` | api, worker, chains | да | да | да | — | да | access (in-proc) | — | `datazonepolicy`, `processingauthority`, `suppressionpolicy` | **C** |
| `audience` | api, worker, schedule | да | да | да | — | да | — | — | `audiencesnapshot`, `segmentmembership` | **C** |
| `content-production` | api, worker | да | да | да | — | да | — | renderer D | `contentrequest`, `contentversion`, `renderedartifact` | **C** |
| `message-delivery` | sender, worker, schedule | да | да (command) | да | — | нет | — | mailer U, suppression, commandsource | `deliverycommand`, `deliveryevent`, `deliverabilitypolicy` | **C** |
| `engagement-observation` | ingest, worker | да | да | да | — | да (ingest) | consumer очереди | — | `engagementevent`, `readsignal` | **C** |

Итог: **3 контекста уровня A**, **3 уровня B**, **5 уровня C**.

**Обновление (2026-10-04).** После изменений `context-persistence` и
`message-delivery` семь контекстов получили PostgreSQL (по СУБД — уровень
B), а `message-delivery` — реальную отправку (A1). Колонка «СУБД» и итоговые
уровни выше — снимок на 2026-10-03.

## 3. Внешние зависимости платформы (кто реально ходит в сеть)

| Интеграция | Адаптер | Природа | Подключён в `cmd/lead-gen` |
|---|---|---|---|
| Краулинг/поиск | `integration-platform/.../firecrawl` | HTTP-клиент, реальный | да (`buildLeadManagement`) |
| LLM (план поиска, письма) | `integration-platform/.../openai` | HTTP-клиент, реальный | да, при заданном ключе |
| Отправка почты | `integration-platform/.../smtp` | `net/smtp`, реальный | да |
| Форумный источник | `integration-platform/.../forum` | обёртка над поиском | да |
| Векторный recall | `integration-platform/.../vector/memory` | in-memory | через сервис |
| Провайдеры-адаптеры | `integration-platform/.../provider/fake` | fake для тестов | нет |
| Долгие процессы | Temporal (через контракты `workflow`) | реальный, self-hosted | да (при DSN) |
| Отправка (контур доставки) | `message-delivery/.../mailer/provider` (контракт `mail`/SMTP) | реальный | да (при SMTP) |
| Рендер (PDF/лендинг) | `content-production/.../renderer/chromium` (CDP) | реальный | да |

**Принципиальное различие.** Все внешние заглушки — не «немые пустышки»:
`unavailable` отвечает нормализованным отказом (`ErrUnavailable`-класс), а
`deterministic` даёт воспроизводимый ответ для тестов. Подмена реальной
интеграции заглушкой видна из типа ошибки, а не скрыта.

## 4. Персистентность

| Хранилище | Контексты | Миграции |
|---|---|---|
| PostgreSQL зелёной/красной зоны | `lead-management` (green+red), `request-fulfillment` (green), `outreach-orchestration` (green) | 3 / 2 / 1 |
| PostgreSQL (добавлено `context-persistence`, 2026-10-04) | `analytics`, `audience`, `content-production`, `data-governance`, `engagement-observation`, `integration-platform`, `platform-governance` | по 1 на контекст |
| PostgreSQL (добавлено `message-delivery`, 2026-10-04) | `message-delivery` | 1 (green) |
| Только in-memory (только тесты/dev без DSN) | in-memory адаптеры сохранены для тестов и dev-режима | — |

`schema-per-service` (схема и роль СУБД на сервис) в коде **не реализована**:
сервисы используют общий пул по DSN, разделение схем — только в дизайне
(`service-per-context`, задачи 3.10–3.11).

## 5. Объём и тесты (факт по дереву)

| Контекст | .go файлов | LOC | тест-файлов | тест LOC |
|---|---|---|---|---|
| `lead-management` | 127 | 29 787 | 46 | 12 987 |
| `outreach-orchestration` | 110 | 16 908 | 43 | 8 247 |
| `request-fulfillment` | 103 | 13 907 | 34 | 6 059 |
| `integration-platform` | 68 | 11 175 | 26 | 4 589 |
| `data-governance` | 50 | 4 830 | 11 | 1 478 |
| `audience` | 49 | 4 442 | 6 | 892 |
| `content-production` | 45 | 5 407 | 6 | 905 |
| `engagement-observation` | 36 | 3 710 | 6 | 964 |
| `message-delivery` | 50 | 4 318 | 10 | 1 368 |
| `analytics` | 19 | 2 384 | 6 | 864 |
| `platform-governance` | 25 | 2 050 | 3 | 497 |

Тестовая глубина коррелирует с уровнем: A/B несут контрактные suite и сквозные
тесты (`e2e_test.go`, `contract_test.go`), C — в основном unit.

## 6. Расхождения с декларациями

| Декларация | Факт |
|---|---|
| `docs/core-roadmap.md`, `docs/progress-log.md`: «реализованы 6 сервисов, остальные 9 — заглушка `stubService`» | Собраны **все 11** (`deploy/services.yaml`; `buildService` в `cmd/lead-gen/runners.go`). `stubService` остался только как честный отказ для неизвестного id |
| `deploy/services.yaml`: «Заглушек не осталось» | Верно для **сервисов**, но **внешние зависимости** у 5 контекстов — заглушки (раздел 3). Это разные утверждения |
| `docs/core-roadmap.md` §2: таблица статусов (заглушка/частично) | Устарела на два коммита (`bab59e6`, `d556ab7`) |
| `openspec/changes/data-governance` (1/33), `platform-governance` (1/34) | Код существует; `tasks.md` не синхронизирован с деревом |
| `docs/crawl4ai-playbook.md` | Помечен устаревшим; change `self-hosted-crawler` (35 задач) активен, но вытеснен Firecrawl |
| `services.yaml`: роль `sender` снята у `integration-platform` | Согласовано с кодом (`RoleRunner` без `RoleSender`) — здесь расхождения нет |

## 7. Что из матрицы следует для «полноценного core»

По эталону `outreach-orchestration` (домен + app + порты + Postgres + контракты +
тесты) контекст считается полноценным. Матрица показывает, где остался разрыв:

1. **5 контекстов уровня C** — есть домен и app, нет PostgreSQL и реальных внешних
   зависимостей. Это сознательные вертикальные срезы (`bab59e6`), но не «полный core».
2. **`schema-per-service` не реализована** — первый контекст, которому нужна
   вторая схема, обязан закрыть (`docs/core-roadmap.md` §7).
3. **`data-governance`** — единственный foundation-контекст без отдельной СУБД;
   код есть, но покрытие задач не отмечено.

## 8. Как проверялось

Сведения получены из дерева (не из документов) командами вида:

```sh
ls backend/contexts/*/internal/{domain,app,ports,adapters}
ls backend/contexts/*/internal/adapters/persistence
ls backend/contexts/*/migrations
grep -rn 'unavailable|deterministic' backend/contexts/*/internal/adapters
sed -n '125,525p' backend/cmd/lead-gen/runners.go   # что реально впрыскивается
```
