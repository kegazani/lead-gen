# Дорожная карта: полноценный core + фронт

**Назначение.** Единая точка входа для людей и агентов: что значит «полноценный
core», из каких изменений он состоит, в каком порядке их брать и где
подхватывать работу. Это план, а не спецификация: требования живут в
`openspec/`, канонические границы — в `architecture/domain-catalog.yaml`.

**Статус на 2026-10-02.** Реализованы шесть сервисов каталога (сборка в
`backend/cmd/lead-gen`, функция `buildService`): `outreach-orchestration`,
`lead-management`, `analytics`, `request-fulfillment`, `platform-governance`,
`integration-platform`. Остальные девять сервисов каталога поднимаются
заглушкой `stubService` в `backend/cmd/lead-gen`. Веб-интерфейс существует
частично (консоль оператора `lead-management`, публичный портал); остальной
фронт — как дизайн (`web-frontend-architecture`, `service-topology-console`).
Цель — полный core и фронт, без сокращений.

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
| `lead-management` | `lead-profile`, `contact` | `osint-enrichment`, `company-size-assessment`, `email-verification` | реализован (MVP-срез) |
| `audience` | `audience-segment`, `audience-snapshot` | `lead-segmentation` | заглушка |
| `outreach-orchestration` | `campaign` (есть), `call` | `outreach-approval` (есть), `outreach-sequences`, `call-orchestration`, `outreach-channel-form` | частично реализован |
| `message-delivery` | `message`, `sender-reputation` | `outreach-channel-email`, `domain-deliverability` | заглушка |
| `sales-workflow` | `deal`, `crm-sync` | `crm-workflow` | заглушка |
| `request-fulfillment` | `request` | `client-request`, `request-validation` | реализован |

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
| `billing` | подписка, лимиты, success fee | supporting |

Вне программы остаются только `community-intelligence` и `publishing`
(Telegram-дайджесты) — это не core и не на критическом пути.

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
                                       (обратно в outreach) --outreach-outcome-->
                                                     sales-workflow
                                                       | result-attributed
                                                       v
                                                  billing / analytics
```

Правило прочтения: стрелка — обязательная зависимость. Нельзя наполнить
`lead-management` без `integration-platform`; нельзя закрыть цикл без
`message-delivery` и `engagement-observation`; консоль без `platform-governance`
останется на заглушке доступа.

---

## 5. Инвентарь изменений OpenSpec

**Текущий факт (2026-10-02).** В коде собраны шесть сервисов: `outreach-orchestration`, `lead-management`, `analytics`, `request-fulfillment`, `platform-governance`, `integration-platform`; остальные девять — заглушка. Эпик `client-request-domain` (6 change'ов: `client-request-domain`, `temporal-execution-platform`, `request-validation-workflow`, `semantic-lead-search`, `client-portal`, `client-dashboard-metrics`) заархивирован 2026-09-30…2026-10-02 и дал сервис `request-fulfillment`; `lead-management` и `integration-platform` наполнены MVP-срезом `mvp-lead-search` и последующими change'ами. Таблица ниже — план; статусы задач уточняются в `tasks.md` соответствующих change'ей.

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
| `call-orchestration` | 0/45 | core: `outreach-orchestration` |
| `sequences-and-reporting` | 0/43 | core: `outreach-orchestration` + `analytics` |
| `web-frontend-architecture` | 0/68 | фронт: консоль оператора |
| `service-topology-console` | 0/57 | фронт: экран топологии |
| `landing-page-generator` | 0/36 | фронт: публичные лендинги |
| `full-enterprise-core-scope` | 0/78 | рамка продукта (юридический гейт) |
| `service-per-context` | 42/54 | выделение сервисов; 4.2–4.5 упираются в отсутствующие реализации |
| `migrate-platform-to-go` | 2/27 | каркас |
| `system-map-and-cpp-baseline` | 14/23 | карта системы |
| `digest-publishing` | 0/38 | вне core |
| `telegram-community-digest` | 6/44 | вне core |
| `archive-order-and-rule-deltas` | 0/17 | служебное |

### Нет — нужно создать

| Change | Контекст | Почему нужен |
|---|---|---|
| `message-delivery` | `message-delivery` | исполнение утверждённых действий; замыкает отправку |
| `sales-workflow` | `sales-workflow` | сделки и атрибуция результата |

---

## 6. Порядок этапов

```text
  ЭТАП 0   platform-governance, data-governance, integration-platform
           Без них core — заглушки за заглушками, а консоль — без настоящей auth.
  ЭТАП 1   lead-management  ->  audience
           Первый настоящий входной контур: компании/контакты -> сегмент.
  ЭТАП 2   message-delivery, sales-workflow
           + до-закрыть outreach: sequences, call, channel-form.
  ЭТАП 3   supporting на критическом пути: content-production,
           engagement-observation, analytics, billing.
  ЭТАП 4   фронт: web-frontend-architecture -> service-topology-console
           -> landing-page-generator.
```

**Частично выполнено.** ЭТАП 0 закрыт наполовину: `platform-governance` и `integration-platform` собраны, `data-governance` остаётся заглушкой. ЭТАП 1 закрыт наполовину: `lead-management` реализован (MVP-срез), `audience` — ещё заглушка. Порядок остальных этапов не меняется.

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

- Осталось по текущим changes: **≈620 задач** (из 750).
- Пять отсутствующих change'ей добавят сопоставимый объём: ориентир
  **~900–1000 задач** в сумме.
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
6. Следующий шаг программы: **артефакты планирования ЭТАПА 0** — change'и
   `platform-governance`, `data-governance`, `integration-platform` созданы
   (proposals готовы), дальше specs/design/tasks; точка подхвата и решения —
   `docs/progress-log.md`.

### Источники истины

| Артефакт | Что задаёт |
|---|---|
| `architecture/domain-catalog.yaml` | контексты, агрегаты, capability, связи |
| `docs/system-map.md` | карта исполнения |
| `docs/architecture.md` | архитектурные решения, стек, §11 контракты |
| `docs/domain-model.md` | доменная модель и владение |
| `deploy/services.yaml` | группы процессов и роли запуска |
| `openspec/specs/`, `openspec/changes/` | требования продукта |
| `README.md` | локальная разработка и `make`-цели |
