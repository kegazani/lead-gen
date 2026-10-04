# Дорожная карта: полноценный core + фронт

**Назначение.** Единая точка входа для людей и агентов: что значит «полноценный
core», из каких изменений он состоит, в каком порядке их брать и где
подхватывать работу. Это план, а не спецификация: требования живут в
`openspec/`, канонические границы — в `architecture/domain-catalog.yaml`.

**Статус на 2026-10-03.** Собраны **все одиннадцать сервисов каталога** (сборка в
`backend/cmd/lead-gen`, `buildService`; `stubService` остался только как честный
отказ для неизвестного идентификатора). Зрелость контекстов различается — точная
фактура по слоям, персистентности и внешним адаптерам в
[`docs/implementation-matrix.md`](implementation-matrix.md):

- **уровень A — рабочий контур** (домен + app + PostgreSQL + реальная внешняя
  зависимость): `lead-management`, `request-fulfillment`, `integration-platform`;
- **уровень B** (домен + app + СУБД, внешние связи — заглушки/в процессе):
  `outreach-orchestration` (PostgreSQL), `platform-governance`, `analytics`;
- **уровень C — вертикальный срез в памяти** (домен + app + порты + in-memory,
  внешние порты — честные заглушки): `data-governance`, `audience`,
  `content-production`, `message-delivery`, `engagement-observation`.

Реально ходят в сеть Firecrawl, LLM, SMTP (через `integration-platform`), Temporal
и headless Chromium (`content-production`); PostgreSQL-адаптер и миграции есть
у большинства контекстов (изменение `context-persistence`); реальная отправка
(`message-delivery`, контракт `mail`) подключена. Веб-интерфейс существует частично (консоль
оператора `lead-management`, консоль заявок `request-fulfillment`, публичный
портал); остальной фронт — как дизайн (`web-frontend-architecture`,
`service-topology-console`). Цель — полный core и фронт, без сокращений.

---

## 1. Что значит «полноценный core»

Эталон — уже реализованный `outreach-orchestration`, прошедший
`outreach-approval-slice`. Полноценный контекст — тот же набор слоёв и проверок:

```text
  domain    агрегаты, value objects, domain events, policies, инварианты
            (строго по каталогу, без самодельных сущностей)
  app       use cases: команды (command) и запросы (query)
  ports     репозиторий, outbox/unit-of-work, clock, access-decision
  adapters  in-memory + PostgreSQL (schema-per-service) + HTTP API
  contracts published-language владельца + in-process и сетевые адаптеры
  service   сборка в backend/contexts/<context-id>, раннеры ролей, миграции
  tests     unit + contract suite + e2e + `make race`
```

Контекст считается полноценным, когда `make check` (сборка, тесты, vet,
importlint, archlint, OpenSpec) зелёный, а контрактные suite и сквозной сценарий
проходят против in-memory и PostgreSQL.

---

## 2. Core-контексты (classification: core)

| Контекст | Агрегаты (каталог) | Capability | Статус |
|---|---|---|---|
| `lead-management` | `lead-profile`, `contact` | `osint-enrichment`, `company-size-assessment`, `email-verification`, … | **A** — рабочий контур (PG, Firecrawl/LLM/SMTP) |
| `audience` | `audience-segment`, `audience-snapshot` | `lead-segmentation` | **C** — срез в памяти |
| `outreach-orchestration` | `campaign` (есть), `call` | `outreach-approval` (есть), `outreach-sequences`, `outreach-channel-form` | **B** — домен + app + PG, внешние связи in-proc |
| `message-delivery` | `message`, `sender-reputation` | `outreach-channel-email`, `domain-deliverability` | **C** — срез в памяти |
| `request-fulfillment` | `request` | `client-request`, `request-validation` | **A** — рабочий контур (PG, Temporal) |

Уровни зрелости (A/B/C) и фактура по слоям — [`docs/implementation-matrix.md`](implementation-matrix.md). Статус задач — `tasks.md` соответствующих change'ей.

## 3. Контексты, без которых core не работает

Core не существует в вакууме. Полный цикл требует ещё шесть контекстов:

| Контекст | Роль в программе | Classification |
|---|---|---|
| `platform-governance` | тенанты, пользователи, роли, аудит, `access-decision`, `tenant-context` | prerequisite |
| `data-governance` | зоны данных, основание обработки, `suppression-policy`, `data-zone-policy` | prerequisite |
| `integration-platform` | порты провайдеров, `provider-port`, нормализация ошибок | prerequisite |
| `content-production` | версии контента и рендеринг для действий outreach | supporting |
| `engagement-observation` | события доставки/переходов, `read-signal` без пикселей | supporting |
| `analytics` | обезличенные read-модели и отчётность | supporting |

---

## 4. Цепочка зависимостей (критический путь)

```text
  integration-platform --provider-port--> lead-management
  data-governance -----data-zone-policy-> lead-management / outreach
  platform-governance --access-decision-> ВСЕ контексты И консоль
                                                       |
        lead-profile                                   |
  lead-management ---> audience ---audience-snapshot---> outreach-orchestration
                                                       |         |
                                          content-version/       | approved-action
                                          rendered-artifact      v
                            content-production ----> message-delivery
                                                             |
                                                   delivery-event
                                                             v
                                                engagement-observation
                                                  | read-signal
                                                  v
                                             (обратно в outreach)
                                                  | analytics-event
                                                  v
                                              analytics
```

Правило прочтения: стрелка — обязательная зависимость. Нельзя наполнить
`lead-management` без `integration-platform`; нельзя закрыть цикл без
`message-delivery` и `engagement-observation`; консоль без `platform-governance`
останется на заглушке доступа.

---

## 5. Инвентарь изменений OpenSpec

**Текущий факт (2026-10-03).** Собраны все одиннадцать сервисов каталога; заглушек сервисов нет (`stubService` — только честный отказ для неизвестного id). Различие — в глубине: `lead-management`, `request-fulfillment`, `integration-platform` — рабочий контур; `outreach-orchestration`, `platform-governance`, `analytics` — домен + app + СУБД; остальные пять — вертикальные срезы в памяти с заглушками внешних зависимостей (коммит `bab59e6`). Точная фактура — [`docs/implementation-matrix.md`](implementation-matrix.md). Таблица ниже — план; статусы задач уточняются в `tasks.md` соответствующих change'ей.

### Есть (в работе)

| Change | Задачи | Отношение к программе |
|---|---|---|
| `outreach-approval-slice` | 30/30 ✅ | базовый core, реализован |
| `outreach-approval-batch` | 27/27 ✅ | базовый core, реализован |
| `platform-governance` | 1/35 | ЭТАП 0: foundation (создан 2026-09-22, планирование завершено) |
| `data-governance` | 1/34 | ЭТАП 0: foundation (создан 2026-09-22, планирование завершено) |
| `integration-platform` | 0/27 | ЭТАП 0: foundation (создан 2026-09-22, планирование завершено) |
| `company-size-assessment` | 0/52 | core: `lead-management` |
| `email-verification` | 3/36 | core: `lead-management` |
| `lead-segmentation` | 3/36 | core: `audience` |
| `outreach-channel-form` | 0/39 | core: `outreach-orchestration` |
| `sequences-and-reporting` | 0/43 | core: `outreach-orchestration` + `analytics` |
| `web-frontend-architecture` | 0/68 | фронт: консоль оператора |
| `service-topology-console` | 0/57 | фронт: экран топологии |
| `landing-page-generator` | 0/36 | фронт: публичные лендинги |
| `full-enterprise-core-scope` | 0/78 | рамка продукта (юридический гейт) |
| `service-per-context` | 42/54 | выделение сервисов; 4.2–4.5 упираются в отсутствующие реализации |
| `migrate-platform-to-go` | 2/27 | каркас |
| `system-map-and-cpp-baseline` | 14/23 | карта системы |
| `archive-order-and-rule-deltas` | 0/17 | служебное |

### Нет — нужно создать

| Change | Контекст | Почему нужен |
|---|---|---|
| `message-delivery` | `message-delivery` | исполнение утверждённых действий; замыкает отправку |

---

## 6. Порядок этапов

```text
  ЭТАП 0   platform-governance, data-governance, integration-platform
           Без них core — заглушки за заглушками, а консоль — без настоящей auth.
  ЭТАП 1   lead-management  ->  audience
           Первый настоящий входной контур: компании/контакты -> сегмент.
  ЭТАП 2   message-delivery
           + до-закрыть outreach: sequences, channel-form.
  ЭТАП 3   supporting на критическом пути: content-production,
           engagement-observation, analytics.
  ЭТАП 4   фронт: web-frontend-architecture -> service-topology-console
           -> landing-page-generator.
```

**Состояние по этапам.** Все контексты присутствуют в коде, но полнота различается. ЭТАП 0: `platform-governance` и `integration-platform` собраны (уровень A/B), `data-governance` — вертикальный срез в памяти (уровень C). ЭТАП 1: `lead-management` — рабочий контур (уровень A), `audience` — срез в памяти (уровень C). ЭТАПАМ 2–3 соответствуют срезы `message-delivery`, `content-production`, `engagement-observation`, `analytics` — без реальных внешних интеграций. Порядок этапов не меняется: ближайший содержательный разрыв — не «построить контекст», а «довести уже построенные до рабочего контура» (персистентность, внешние адаптеры, сквозные сценарии).

Обоснование порядка: foundation-first выбран сознательно — компромисс «демо
раньше фундамента» отклонён, чтобы не оставлять заглушку аутентификации и не
переделывать слой доступа. После каждого контекста — запись в
`deploy/services.yaml` (перенос сервиса в собственную группу) и прогон
`make check` + `go -C tools/archlint run .`.

---

## 7. Риски и ограничения

- **Юридический гейт.** `full-enterprise-core-scope` задача 1.1 — заключение
  юриста до реализации. Там же прямо сказано: заключение может опровергнуть
  рамку, на которой построены 12 capability. Подтвердить до ЭТАПА 1.
- **Отсутствующие контракты.** Часть межконтекстных связей каталога не имеет
  объявленных контрактов (зафиксировано в `service-per-context`, задачи 3.19 /
  6.4: покрыто 4 связи из 28). Расширение контрактов — часть этапов, а не
  отдельная задача «потом».
- **schema-per-service не реализована.** Разделение схем и прав СУБД описано
  только в дизайне (`service-per-context` 3.10–3.11). Первый контекст,
  которому нужна вторая схема, обязан закрыть это.
- **Смена владельца границы.** Новые применения домена требуют обновления
  `architecture/domain-catalog.yaml`, `docs/domain-model.md` и диаграмм в том же
  изменении, иначе `archlint` упадёт.

---

## 8. Масштаб (честно)

- Собраны все одиннадцать сервисов, но большинство — срезы в памяти: без
  PostgreSQL и без реальных внешних интеграций (см.
  [`docs/implementation-matrix.md`](implementation-matrix.md)). Это не «нулевая
  база», а «есть что доводить»: персистентность, внешние порты, сквозные сценарии.
- Осталось по текущим changes: **≈680 задач**. Один отсутствующий change
  добавит сопоставимый объём: ориентир **~900–1000 задач** в сумме.
- Это программа на месяцы командной работы, а не один заход. Работать нужно
  изменениями, по одному контексту за раз.

---

## 9. Где подхватывать (для агентов)

1. Прочитай этот файл целиком.
2. Определи текущий этап из §6 и найди соответствующий change (§5).
3. Проверь его состояние:

   ```sh
   openspec list --json
   openspec status --change "<name>" --json
   openspec instructions "<artifact-id>" --change "<name>" --json
   ```

4. Собери отсутствующие изменения через `openspec new change "<name>"` — вручную
   каталог под `openspec/changes/` не создавать (нужна метаданные `.openspec.yaml`).
5. Перед завершением любого изменения — `make check` и
   `go -C tools/archlint run .`; нарушение архитектурной границы не отмечать
   выполненным.
6. Следующий шаг программы: **доведение уже собранных срезов до рабочего контура** —
   персистентность закрыта (`context-persistence`), подключены реальная отправка
   (`message-delivery`) и рендер (`content-rendering`). Фактура по контекстам —
   `docs/implementation-matrix.md`, точка подхвата — `docs/progress-log.md`.

### Источники истины

| Артефакт | Что задаёт |
|---|---|
| `architecture/domain-catalog.yaml` | контексты, агрегаты, capability, связи |
| `docs/system-map.md` | карта исполнения |
| `docs/architecture.md` | архитектурные решения, стек, §11 контракты |
| `docs/domain-model.md` | доменная модель и владение |
| `docs/implementation-matrix.md` | факт по слоям: что реализовано, где заглушка |
| `deploy/services.yaml` | группы процессов и роли запуска |
| `openspec/specs/`, `openspec/changes/` | требования продукта |
| `README.md` | локальная разработка и `make`-цели |
