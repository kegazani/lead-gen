# lead-gen

Платформа B2B-лидогенерации и исходящих коммуникаций: доказуемость законности
коммуникации как продукт. Реализация на Go, контекст-первый DDD.

## Полная программа интеллектуальной лидогенерации

**Весь согласованный объём обязателен к реализации:** [36 требований и применение исследования](docs/intelligent-lead-platform-roadmap.md). Там собраны LLM-поиск и память, международные рынки, собственные поиск/краулер, browser/OCR, письма, формы, входящие лиды и ответы. Начальные этапы R1–R4 не сокращают итоговый объём.

Программа ведётся в ветке `concept/intelligent-lead-research`: [концепция](docs/concepts/intelligent-lead-research/README.md), [OpenSpec и задачи](openspec/changes/intelligent-lead-research/tasks.md). Документы описывают план; фактическое выполнение подтверждается задачами и evidence.

[Уточнения пользователя из переписки](docs/intelligent-lead-platform-roadmap.md#ваши-уточнения-из-переписки) сохранены с цитатами; предложения по инструментам и стартовым настройкам отмечены отдельно.

## Карта репозитория

Корневого `go.mod` нет: модули связывает workspace `go.work` — `backend/cmd/lead-gen`,
`backend/contexts/<ctx>`, `backend/platform`, `contracts/<owner>`,
`frontend/{components,public}`, `tools/{archlint,importlint}`.
Единица сборки совпадает с единицей сервиса. Верхний уровень разделён по контурам:
служебный Go-код — в `backend/`, фронтенд — в `frontend/`, развёртывание — в `deploy/`,
машинная архитектура — в `architecture/`, проза — в `docs/`.

| Путь | Что это | Источник истины |
|---|---|---|
| `docs/` | Бизнес-требования, архитектура, доменная модель, карта системы | `docs/system-map.md` — карта исполнения |
| `architecture/` | Машинно-читаемый каталог bounded contexts и схема | `architecture/domain-catalog.yaml` |
| `openspec/` | Спеки и изменения (OpenSpec): требования продукта | `openspec/specs/`, `openspec/changes/` |
| `backend/cmd/lead-gen/` | Точка входа служебного образа: группы процессов и роли берутся из `deploy/services.yaml` | `docs/system-map.md` §2 |
| `backend/contexts/<ctx>/` | Модуль сервиса = модуль bounded context: приватные слои под `backend/contexts/<ctx>/internal/{domain,app,ports,adapters}`, публичная поверхность — корневой пакет сборки сервиса | `architecture/domain-catalog.yaml` |
| `backend/platform/` | Общие технические примитивы без доменных правил (собственный модуль): `composition`, `contracts`, `messaging`, `observability`, `reliability`, `transport`, `deployment`, `migrate` | — |
| `contracts/<owner>/` | Published language владельца: контракт-интерфейс и адаптеры, включая in-process (общий для backend и frontend) | `docs/architecture.md` §11 |
| `frontend/components/` | Общий слой серверных UI-компонентов на `html/template` (layout, фрагменты, поля формы) для `public` и консоли | `docs/architecture.md` §11 |
| `frontend/public/` | Публичный edge: отдельный модуль `lead-gen/frontend/public` и отдельный образ на своём домене; связь только через `contracts/*` | `docs/architecture.md` §8 |
| `deploy/` | Группы процессов и узлы: `deploy/services.yaml`, `deploy/infrastructure.yaml` | `deploy/README.md` |
| `deploy/tofu/` | Инфраструктура как код (OpenTofu): модули и окружения | `deploy/tofu/README.md` |
| `tools/archlint/` | Проверка доменного каталога, спек, диаграмм и границ развёртывания | `openspec/specs/domain-architecture/spec.md` |
| `tools/importlint/` | Проверка границ импортов между модулями, слоями и контекстами | `openspec/changes/migrate-platform-to-go/tasks.md` §2.3 |
| `.agents/` | Навыки и правила работы агентов (`.agents/skills/openspec-*`) | — |
| `AGENTS.md` (корень) | Правила агента: процесс через OpenSpec, запрет заглушек, чек-лист старта сессии — читается автоматически в каждом новом чате | этот файл |

**Дорожная карта полноценного core и фронта — [`docs/core-roadmap.md`](docs/core-roadmap.md):** что значит «полноценный core», инвентарь изменений, порядок этапов и точка подхвата для агентов.

## Контексты, сервисы и группы процессов

**Bounded context — не процесс.** Контекст — граница модели, языка и владения
агрегатами. Сервис — единица развёртывания, и сервис равен контексту: пятнадцать
контекстов дают пятнадцать сервисов. Сервис задаёт границу отказа, релиза и
масштабирования, но владельцем модели остаётся контекст (`docs/domain-model.md`).

**Роль — режим запуска, а не единица размещения.** Сервис несёт подмножество ролей
(`roles` в каталоге); роль встречается ровно один раз по всем группам процессов.

| Роль | Контур |
|---|---|
| `api` | Внутренний интерфейс и API |
| `ingest` | Публичный приём обращений и вебхуков |
| `sender` | Единственный исполнитель исходящих писем |
| `worker` | Пулы движков обработки |
| `chains` | Долгие процессы и таймеры |
| `schedule` | Регулярные запуски по расписанию |

**Группа процессов** (`process` в `deploy/services.yaml`) — способ запуска: один
процесс поднимает один или несколько сервисов, каждый со своим подмножеством ролей.
Текущее (совмещённое) состояние — семь групп: `intake`, `core`, `delivery`,
`demand`, `public`, `platform`, `revenue`. Целевое — группа на сервис; переход между
состояниями — правка только `deploy/services.yaml`, без изменения кода и домена
(`docs/system-map.md` §2.4).

**Образов два.** Служебный образ (корневой `Dockerfile`, точка входа
`backend/cmd/lead-gen`) несёт все сервисы и роли, назначение задаёт конфигурация. Публичный
edge — отдельный минимальный образ модулей `frontend/public` и `frontend/components` на
своём домене: без доступа к хранилищам контекстов, данные — только через published-контракты
(`docs/architecture.md` §8).

Роль `sender` изолирована в группе `delivery` и не делит процесс с `api`; публичный
контур отделён от служебного API. Отдельные контуры масштабируются независимо.

**Связывание контрактов.** Синхронная межконтекстная связь выражается контрактом
модуля `contracts/<owner>`. При co-location сервисов в одном процессе вызов может
идти in-process адаптером, при разделении — сетевым; выбор задаёт топология, а не код
потребителя, а in-process адаптер сохраняет дедлайн, отмену и ключ идемпотентности
(`docs/architecture.md` §11).

## Локальная разработка

Для разработки без Docker требуется Go 1.27+.

**Локальное демо через Docker**, без установки Go. Используйте существующий
`.env` с `POSTGRES_PASSWORD`, `FIRECRAWL_API_KEY`, `LLM_API_KEY` и настройками LLM:

```sh
test -f .env || cp .env.example .env  # заполнить ключи, если .env новый
docker compose -p lead-gen-demo -f compose.demo.yaml up -d --build --wait --wait-timeout 240
```

Клиентский портал — <http://localhost:18000/login>, оператор —
<http://localhost:18000/admin/console>. Логин/пароль оператора берутся из `.env`
(`CONSOLE_USER`/`CONSOLE_PASSWORD`, по умолчанию `admin`/`admin`).
Демо поднимает собственные PostgreSQL, RabbitMQ и Temporal; данные сохраняются
в отдельных томах. Инструкции — [`deploy/demo/README.md`](deploy/demo/README.md).

Штатный `compose.yaml` использует отдельную процедуру DB release и подготовки
секретов: простого `.env` для его первого запуска недостаточно. Этот путь описан
в [`deploy/README.md`](deploy/README.md).

```sh
make check     # сборка, тесты, vet, importlint, archlint, OpenSpec
make build     # сборка всех модулей workspace
make test      # go test ./... по модулям
make race      # go test -race ./... по модулям
make lint      # gofmt, vet, importlint
make arch      # archlint: каталог, спеки, границы развёртывания
make openspec  # openspec validate --all --strict
```

Цели обходят модули по списку (`MODULES` в `Makefile`), потому что `go build ./...`
из корня модули workspace не покрывает. Отдельные команды см. в `Makefile`.
OpenSpec: `openspec validate <change> --strict`.
