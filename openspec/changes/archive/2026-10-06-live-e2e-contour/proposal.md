## Why

Целевой поток продукта (клиент регистрирует компанию → создаёт заявку → админ
проверяет и одобряет → ИИ ищет лиды и при недоборе генерирует письма → оператор
утверждает и отправляет → клиент видит дашборд) в коде собран, портал и дашборд
клиента и пять админских консолей реализованы, но **живьём сквозная цепочка не
прогонялась**: `compose.yaml` устарел (ссылается на удалённые `billing`/`publishing`,
не поднимает половину сервисов, не имеет SMTP), `Dockerfile` не собирает актуальную
топологию, а совмещённый запуск нескольких сервисов с ролью `api` в одном процессе
конфликтует за `-addr`. Плюс объявленный сетевой транспорт синхронных контрактов
(`approved-action`, `content-version`, `delivery-target`) не реализован, поэтому в
многоконтейнерном контуре «утверждено → отправлено» рвётся. Без этого проверить
работу клиентского и админского фронта полным циклом нельзя.

## What Changes

- **Фаза 1 — исполняемый совмещённый контур.** В `cmd/lead-gen` появляются
  per-service адреса API (каждый сервис с ролью `api` слушает свой порт), поэтому
  совмещённый процесс с несколькими `api`-сервисами запускается. `compose.yaml`
  переписывается под реальную топологию: PostgreSQL (зелёная/красная), RabbitMQ,
  Temporal, локальный приёмник SMTP, совмещённый app-процесс; удаляются
  несуществующие сервисы. `Dockerfile` приводится к актуальному набору модулей и
  миграций. Заглушка шага поиска в контуре снимается (реальный `lead-search`).
- **Фаза 2 — сетевой транспорт контрактов.** Для синхронных межсервисных контрактов
  `approved-action` (`outreach-orchestration` → `message-delivery`), `content-version`
  (`content-production` → `message-delivery`) и `delivery-target`
  (`lead-management` → `message-delivery`) реализуются сетевые адаптеры (HTTP),
  чтобы топология с отдельными контейнерами ролей `api` работала без in-process
  связывания; in-process адаптеры остаются для co-location.
- **Приёмка.** Сквозной сценарий «клиент → заявка → валидация → поиск → письма →
  утверждение → отправка → дашборд» на живом контуре (`docker compose`) и его
  проверка; обновление `docs/e2e-plan.md`/`deploy/README.md`.
- **Не входит:** изменение доменной модели, каталога и поведения контрактов
  (только транспорт); внешние ключи (Firecrawl/LLM) и реальный SMTP — параметры
  запуска, без них соответствующие шаги честно недоступны.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

Нет. Изменение реализует уже объявленные требования `deployment-topology`
(«Исполняемая dev-топология», «Единый образ и группы процессов») и `delivery-bridge`;
текст требований не меняется, поведение продукта не меняется. `.openspec.yaml`
содержит `skip_specs: true`.

## Impact

**Affected bounded contexts:** `outreach-orchestration`, `message-delivery`
(транспорт `approved-action`), `content-production`/`message-delivery`
(`content-version`), `lead-management`/`message-delivery` (`delivery-target`);
`backend/cmd/lead-gen` (сборка, адреса ролей), инфраструктура (`compose.yaml`,
`Dockerfile`, `deploy/`). Доменная модель, каталог и связи не меняются.

**Код.** `backend/cmd/lead-gen` (per-service адреса, сборка сетевого транспорта),
`backend/contexts/message-delivery` (приём `approved-action` сетевым адаптером,
резолверы получателя/материала по сети), `backend/contexts/content-production` и
`backend/contexts/lead-management` (HTTP-поверхности контрактов), `compose.yaml`,
`Dockerfile`, `deploy/README.md`, `docs/e2e-plan.md`.
