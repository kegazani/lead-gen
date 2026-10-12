# Деплой: служебный образ, публичный edge и группы сервисов

Единица деплоя — сервис, и сервис равен bounded context: одиннадцать контекстов
дают одиннадцать сервисов, по одному на контекст (`architecture/domain-catalog.yaml`).
Физически внутренние сервисы запускает **служебный образ с единой точкой входа `backend/cmd/lead-gen`**
(корневой `Dockerfile`); назначение — какие сервисы и в каких ролях подняты — задаёт
`deploy/services.yaml`. Публичный edge — **отдельный минимальный образ** модулей
`frontend/public` и `frontend/components` на своём домене (`docs/architecture.md` §8, §11); он не
объявляется сервисом каталога и здесь не описывается.

Роли (`api`, `ingest`, `sender`, `worker`, `chains`, `schedule`) — режимы запуска
внутри сервиса, а не единицы размещения. Группа процессов (`process`) — способ
запуска: один процесс образа поднимает один или несколько сервисов, каждый со своим
подмножеством ролей.

## Формат `deploy/services.yaml`

Файл читают две стороны, обе ожидают одну и ту же форму:

- `backend/platform/deployment` — строгий минимальный парсер на стандартной
  библиотеке: нераспознанная строка даёт ошибку с номером, а не молчаливый
  пропуск. Разбор живёт рядом с загрузчиком графа сервисов и инфраструктуры,
  а не в точке входа;
- `tools/archlint` (правило «деплой не спорит с моделью», design.md Решение 7).

Ключи фиксированы: `processes` → группа (`name`, `mechanism`) → `services` →
сервис (`id`, `roles`). Комментарии начинаются с `#` и парсером отбрасываются,
поэтому шапка файла может быть любой длины.

## Два состояния конфигурации

Одна и та же конфигурация описывает два **именованных состояния** деплоя.

| Состояние | Групп | Сервисов в группе | Смысл |
|---|---|---|---|
| **Совмещённый первый деплой** (текущее содержимое файла) | 6 | 2–3 | не эксплуатировать 15 процессов на старте |
| **Целевое** | 15 | ровно 1 | по группе на сервис; независимая граница отказа, релиза и масштабирования |

Совмещение — только способ запуска. Оно **не создаёт второго владельца модели**:
каталог в обоих состояниях объявляет один сервис на контекст, а группа — лишь
размещение. Поэтому переход между состояниями — правка конфигурации, а не кода и
не модели (design.md, Решения 5 и 11).

### Текущее (совмещённое) состояние — 6 групп

| Группа | Сервисы | Обоснование группировки |
|---|---|---|
| `intake` | `request-fulfillment` | вход ценностной цепочки: заявка клиента |
| `core` | `outreach-orchestration`, `analytics` | горячий путь кампании: оркестрация → аналитика |
| `delivery` | `message-delivery` | изолированный контур отправки: роль `sender` не делит процесс с `api` |
| `demand` | `lead-management`, `audience`, `content-production` | цепочка подготовки аудитории: лиды → сегменты → контент |
| `public` | `engagement-observation` | публичный контур: приём внешних событий наблюдения |
| `platform` | `integration-platform`, `platform-governance`, `data-governance` | общий фундамент: интеграционные порты и правила доступа/данных |

Группа `delivery` выделена при переходе к модульному монолиту: раньше `message-delivery`
со всей ролью `sender` и `integration-platform` с ролями `api` и `sender` стояли в
`core` и `platform`, то есть опасная роль `sender` делила процесс с точкой входа `api`.
Отправка — необратимое внешнее действие, и её границы отказа и масштабирования отделены
от обработки API-запросов (изменение `modular-monolith-and-infrastructure`, задача 2.2).
Роль `sender` у `integration-platform` снята как нереализованная: сервис не регистрирует
её в `RoleRunner`, поэтому объявление в каталоге убрано; она вернётся при реализации
контура доставки.

### Целевое состояние — 11 групп (пример)

```yaml
processes:
  - name: outreach-orchestration
    mechanism: in-process
    services:
      - id: outreach-orchestration
        roles: [api, worker, chains, schedule]
  - name: message-delivery
    mechanism: in-process
    services:
      - id: message-delivery
        roles: [sender, worker, schedule]
  - name: analytics
    mechanism: in-process
    services:
      - id: analytics
        roles: [api, worker]
  - name: lead-management
    mechanism: in-process
    services:
      - id: lead-management
        roles: [api, worker]
  - name: audience
    mechanism: in-process
    services:
      - id: audience
        roles: [api, worker, schedule]
  - name: content-production
    mechanism: in-process
    services:
      - id: content-production
        roles: [api, worker]
  - name: engagement-observation
    mechanism: in-process
    services:
      - id: engagement-observation
        roles: [ingest, worker]
  - name: integration-platform
    mechanism: in-process
    services:
      - id: integration-platform
        roles: [api, ingest, worker, chains, schedule]
  - name: platform-governance
    mechanism: in-process
    services:
      - id: platform-governance
        roles: [api, worker]
  - name: data-governance
    mechanism: in-process
    services:
      - id: data-governance
        roles: [api, worker, chains]
  - name: request-fulfillment
    mechanism: in-process
    services:
      - id: request-fulfillment
        roles: [api, worker]
```

Обе группировки принимает `archlint`: правило проверяет не число групп, а
согласованность — каждый сервис каталога встречается ровно в одной группе, группа
не объявляет чужой сервис, роли группы ⊆ ролей сервиса, `mechanism` группы
совпадает с режимом роли. Совмещённая форма проверена прогоном на текущем файле,
целевая — временной подменой файла на одиннадцать групп (обе дают код 0; см.
«Проверка»).

Отдельно: юнит-тесты точки входа ожидания групп ещё не обновили — они фиксируют
прежнюю совмещённую форму (пять групп, группа `core`), тогда как текущий
`deploy/services.yaml` содержит семь групп с выделенной `delivery`. Поэтому перевод
реального файла в целевое состояние потребует согласованной правки их ожиданий. Это
тесты конфигурации, не модели владельца.

## Задача 4.1 — группировка под целевой деплой

Зафиксировано: текущий файл — совмещённый **первый** деплой, а целевое состояние —
**по группе на сервис**. Выделение сервиса — это перенос записи сервиса из общей
группы в собственную; правки касаются только `deploy/services.yaml`, домен и схема
данных не затрагиваются. Обе группировки обязан принимать `archlint` (проверено
прогоном `go -C tools/archlint run .`, см. раздел «Проверка»).

## Задачи 4.2–4.5 — порядок и критерий выделения (не выполнены)

**Статус: не выполнены.** Реализованы шесть сервисов каталога — `analytics`,
`outreach-orchestration`, `lead-management`, `request-fulfillment`,
`platform-governance` и `integration-platform`; остальные девять поднимаются
заглушкой `stubService` в `backend/cmd/lead-gen`. Поэтому остальные сервисы
существуют как **объявление в каталоге и в конфигурации**, но не как код; говорить
об их «выделении» преждевременно.

Критерий порядка (design.md, Решение 11) — не удобство кода, а совпадение трёх
факторов:

- (а) готовность контекста к самостоятельному запуску;
- (б) потребность в отдельной границе отказа и масштабирования;
- (в) цена сетевой связи (сколько связей станет сетевыми при выделении).

Порядок выделения:

| Очередь | Задача | Сервис | Почему в этом порядке | Текущий статус |
|---|---|---|---|---|
| 1 | 4.2 | `outreach-orchestration` | первый реализованный контекст: шаблон выделения проверяется на живом коде | **заблокировано**: код есть, но шаг (собственная группа + зелёные контрактные и smoke-тесты) не выполнен |
| 2 | 4.3 | `message-delivery` | изолированный контур отправки; нужна отдельная граница отказа и масштабирования; роли `sender`, `worker`, `schedule` | **заблокировано**: реализация контекста отсутствует (заглушка) |
| 3 | 4.4 | `engagement-observation` | публичный приём на отдельном домене, роль `ingest`, отличный профиль нагрузки | **заблокировано**: реализация контекста отсутствует (заглушка) |
| 4 | 4.5 | `platform-governance`, `data-governance`, `integration-platform` | общие для всех контексты: раннее выделение создало бы сетевую зависимость сразу для всех | **заблокировано**: `data-governance` — заглушка; `platform-governance` и `integration-platform` реализованы, но шаг (собственная группа + контрактные и smoke-тесты) не выполнен |

Почему 4.3–4.4 и `data-governance` сейчас заблокированы, а не просто «отложены»:
у этих контекстов нет ни домена, ни прикладного слоя, ни адаптеров, поэтому
предъявить нечего — ни собственной группы с работающим процессом, ни
контрактных/smoke-тестов, которые требует приёмка задач. `platform-governance` и
`integration-platform` уже реализованы, но их приёмка (4.5) требует выделения в
собственную группу с зелёными тестами — как в 4.2. Выделение сервиса без
реализации дало бы пустую группу, запускающую заглушку, то есть видимость
прогресса без прогресса.

Для 4.2 отдельно: контекст реализован, и это делает его первым кандидатом, но
задача требует не только «сервис в собственной группе», но и зелёных контрактных
тестов и smoke-теста. Эта часть зависит от транспорта и проверок (раздел 3) и в
настоящий момент не подтверждена, поэтому 4.2 остаётся открытой.

**Что здесь не утверждается.** Сервисы 4.2–4.5 не «выделены»: они лишь объявлены
в каталоге и в совмещённой конфигурации. Из них реализованы `outreach-orchestration`
(4.2), а также `platform-governance` и `integration-platform` (4.5), но шаг
выделения в собственную группу с тестами не выполнен.

**Что подтверждено прогоном для 4.1.** `archlint` принимает обе группировки без
изменения вердикта по владению моделью.

## Задача 4.6 — обратимость выделения

Выделение обратимо, и обратность обеспечена тремя свойствами:

1. **Перенос сервиса между группами — только конфигурация.** Изменение касается
   `deploy/services.yaml`; домен, прикладной слой и схема данных не меняются,
   потому что граница модели всегда проходила по контексту, а не по группе.
2. **Schema-per-service уже совпадает с границей сервиса.** Схема БД закреплена
   за сервисом заранее (`docs/architecture.md` §8), поэтому объединение или
   разъединение сервисов в конфигурации не требует миграции между владельцами.
3. **Параллельное чтение версий контрактов.** Маршрутизация по `<contract>.<major>`
   и период параллельного чтения позволяют откатить потребителя отдельно от
   владельца, не останавливая владельца.

Критерий приёмки задачи («diff выделения и объединения затрагивает только
конфигурацию, не домен и не схему данных») следует из этих свойств и подтверждается
тем, что `archlint` принимает **обе** группировки — целевую и совмещённую, — не
меняя вердикта по владению моделью (обе проверены прогоном, см. «Проверка»).
Отдельного рантайм-теста обратимости в объёме этой правки нет; свойство
зафиксировано на уровне конфигурации и проверок.

## Задача 4.7 — единственный владелец модели

После любого выделения контекст сохраняет **одного владельца модели**, а роль не
получает собственных агрегатов: роль владеет только запуском и адаптерами, не
доменом. Custom-проверки `archlint` подтверждают это для всех сущностей:

- сущность имеет единственный `owner_context`;
- сущность принадлежит ровно одному aggregate, `root_entity` aggregate — владелец
  того же контекста;
- capability имеет ровно один primary context.

Группировка деплоя на эти инварианты не влияет: `archlint` отдельно проверяет
пару «модель ↔ деплой» (каждый сервис ровно в одной группе, роли группы ⊆ ролей
сервиса), но владение сущностью от неё не зависит.

## Инфраструктура как код (`deploy/tofu`)

Инфраструктура описана декларативно на **OpenTofu** (MPL-2.0) и лежит в `deploy/tofu`:
один набор модулей, окружения различаются только конфигурацией.

```
deploy/tofu/
  modules/
    network/        минимальная сеть: docker-сеть; DNS — встроенный резолвер Docker
    data/           PostgreSQL, Valkey, RabbitMQ, Temporal, MinIO (состав — из YAML)
    app/            контейнеры служебного образа по группам процессов (из YAML)
    observability/  Prometheus (+ пометка про OpenTelemetry)
    secrets/        контракт секретов: значения не в состоянии и не в репозитории
  envs/
    local/          docker host — unix-сокет, состояние локальное
    bare-metal/     docker host — по SSH, состояние во внешнем backend (GitLab HTTP)
```

**Единый источник истины.** Состав сервисов и узлов не дублируется в `.tf`: env-слой
читает `deploy/services.yaml` (группы процессов) и `deploy/infrastructure.yaml` (узлы)
через `yamldecode(file(...))` и передаёт модулям переменными. Добавить группу процессов
или узел — значит изменить только YAML. Модули не знают про файлы и не содержат списков
сервисов и узлов.

**Окружения** различаются конфигурацией, а не копией описания:

| Окружение | Docker host | Состояние | Образ |
|---|---|---|---|
| `local` | локальный сокет (`unix:///var/run/docker.sock`) | локальный файл `terraform.tfstate` | собирается из корневого `Dockerfile` |
| `bare-metal` | по SSH (`var.docker_host`) | внешний backend (GitLab HTTP), вне репозитория | берётся готовым из реестра |

Секреты не хранятся ни в репозитории, ни в состоянии: контейнеры получают путь к файлу,
подготовленному `SOPS + age` в `deploy/tofu/.secrets/` (gitignored). Конфигурация
Prometheus рендерится в `deploy/tofu/.generated/` (gitignored).

**Явно вне объёма.** DNS отдельным сервисом не вводится — в контейнерной сети работает
встроенный резолвер Docker, имена узлов — сетевые псевдонимы. **TLS не настраивается:**
сертификаты, терминация и ACME — отдельная задача. Подробности, команды `tofu` и правила
секретов — `deploy/tofu/README.md`.

## Проверка

```sh
make arch                                        # правило «деплой не спорит с моделью» + владение (tools/archlint)
make build                                        # сборка всех модулей, включая backend/cmd/lead-gen
cd backend/platform/deployment && go test ./...   # парсер deploy покрыт тестами пакета
```

Корневого `go.mod` больше нет — модули связывает `go.work`, поэтому команды идут
по модулям (`Makefile`, цель `MODULES`).

Сводка на текущем (совмещённом) файле:

```
archlint: OK — контекстов: 11, агрегатов: 20, сущностей: 35, capability: 54, связей: 32, инфраструктурных узлов: 8
```

По ней и видно, что единственный владелец модели сохранён для всех 35 сущностей
при 11 контекстах. Согласованность `deploy/infrastructure.yaml` (виды узлов, зоны,
существование узлов-сервисов) проверяет тот же `go -C tools/archlint run .`
(проверка `validateInfrastructure`, `tools/archlint/README.md`). Целевая
группировка проверена так: `deploy/services.yaml` временно заменялся на пример из
одиннадцати групп, `go -C tools/archlint run .` давал тот же `OK` и код 0; затем
исходный файл возвращался. Постоянной фикстуры целевой конфигурации в
`tools/archlint` нет — проверка была разовой, в зоне `deploy/**`.

## Рантайм исполнения: Temporal (Temporal Cloud)

Контур durable-исполнения вводит change `temporal-execution-platform`. Рантайм —
**platform-primitive**, а не bounded context: он не владеет доменной моделью и не
объявляется в `architecture/domain-catalog.yaml`. Узел `temporal` (kind
`workflow`) объявлен в `deploy/infrastructure.yaml` с зоной `infra`.

Подключение задаётся точкой входа `backend/cmd/lead-gen` (флаги), секреты — вне
репозитория (env/секрет-хранилище):

| Флаг | Смысл | Значение по умолчанию |
|---|---|---|
| `-temporal-address` | адрес фронтенда Temporal, `host:port`; пусто — рантайм отключён | пусто |
| `-temporal-namespace` | пространство имён | `default` |
| `-temporal-api-key` | ключ доступа Temporal Cloud; пусто — без TLS и ключа | пусто |
| `-temporal-task-queue` | очередь задач воркера workflow | `request-fulfillment` |

**Temporal Cloud.** Адрес — `<namespace>.tmprl.cloud:7233`, ключ доступа передаётся
как `-temporal-api-key` и применяется поверх TLS. Пространство имён секционируется
по тенанту/окружению; workflow-метаданные — только идентификаторы, поэтому уход в
облако не конфликтует с зонами данных (`data-governance`).

**Self-hosted (локальный контур).** Сервер, web-интерфейс и отдельная БД подняты
в `compose.yaml` (`postgres-temporal`, `temporal`, `temporal-ui`). Динамическая
dev-конфигурация — `deploy/temporal/dynamicconfig/development.yaml` (ослабляет
проверки версии и длин идентификаторов, включает обновление search-атрибутов —
только для разработки). Адрес клиента — `temporal:7233`, порт gRPC на хост —
`${TEMPORAL_GRPC_PORT:-7233}`, интерфейс — http://localhost:${TEMPORAL_UI_PORT:-8233}.

Локальный запуск сработки workflow (`request-fulfillment`):

```sh
cp .env.example .env   # заполнить FIRECRAWL_API_KEY (для среза поиска лидов)
docker compose up --build
# workflow-заявки: http://localhost:8081/  (воркер подключён к Temporal)
# UI Temporal:       http://localhost:8233/
```

Воркер workflow поднимается только при подключённом рантайме: при недоступности
Temporal сервис не падает, заявка остаётся в `validated`, ручные переходы по API
продолжают работать.

**Поведение при недоступности.** Подключение выполняется один раз на процесс.
Если Temporal недоступен, процесс не падает: рантайм помечается выключенным,
воркер workflow не поднимается, а заявка остаётся в статусе `validated` —
существующие ручные переходы через API продолжают работать.

Проверка: `backend/platform/workflow` (клиент, host воркера, реестр регистрации)
покрыт тестами, а сборка сервиса `request-fulfillment` поднимает воркер только при
подключённом рантайме.

## Пробы живости и готовности

Процесс `lead-gen` различает **живость** (liveness) и **готовность** (readiness):

| | `healthcheck` (liveness) | `/readyz` (readiness) |
|---|---|---|
| Что проверяет | процесс жив и отвечает | готовность обслуживать работу |
| Кто проверяет | оркестратор (Docker/OpenTofu) | балансировщик/вызывающий |
| Учитывает роли | нет | да (доступность рантайма durable-исполнения) |
| Таймаут | есть; зависший процесс = unhealthy | собственный дедлайн |

**Liveness уровня контейнера.** Так как образ выполнения — `gcr.io/distroless/static-debian12:nonroot`
без оболочки и `curl`/`wget`, проба выполняется тем же статическим бинарником в
режиме пробы: `lead-gen -healthcheck -healthcheck-url http://127.0.0.1:8091/livez`.
Процесс поднимает отдельный liveness-слушатель (`-liveness-addr`, по умолчанию
`127.0.0.1:8091`), независимый от ролей и доступный всем группам — включая те, у которых нет
роли `api` (например, `delivery`). Проба проверяет только «процесс жив и отвечает»,
а не здоровье отдельных ролей, и не использует `/readyz`.

Параметры пробы (`compose.yaml` якорь `x-app`, `deploy/tofu/modules/app`):
`interval`/`timeout`/`retries`/`start_period`. `start_period` допускает медленный
старт (подключение к БД/рантайму, миграции), чтобы старт не был ошибочно принят за
зависание.

**Поведение при зависании.** Зависший процесс, не ответивший пробе в пределах
таймаута, помечается оркестратором как `unhealthy`. Сама пометка контейнер **не**
перезапускает: `restart: unless-stopped` реагирует только на выход процесса, а не
на статус `unhealthy`. Поэтому автовосстановление достигается **самозавершением**
процесса (Вариант C): процессный сторож точки входа периодически сам проверяет
собственную liveness-пробу и при устойчивой потере — N подряд неудач — инициирует
graceful-останов (отмена контекста ролей и ожидание их завершения не дольше
`-shutdown-timeout`) и завершается ненулевым кодом, а свежий контейнер поднимает
`restart: unless-stopped`. Сторож
настраивается флагами `-self-heal-interval` (интервал, по умолчанию 15s),
`-self-heal-threshold` (порог, 3; значение 1 допустимо, но не рекомендуется — оно
снимает защиту от единичного ложного сбоя), `-self-heal-timeout` (таймаут пробы,
5s; неположительное значение клампится к дефолту, а не отключает сторожа) и
`-self-heal-start-period` (стартовый период без проверок, 30s); значение 0 у
интервала или порога отключает сторожа.

**Остаточный зазор.** Полная заморозка runtime — когда и процессный сторож, и
liveness-слушатель не выполняются — не покрывается: контейнер будет помечен
`unhealthy` и детектирован, но не перезапущен автоматически, потому что процесс не
может самозавершиться. Это осознанный нерешённый зазор, а не решённая задача; его
полное покрытие требует внешнего актора (autoheal/оркестратор) с доступом к
`docker.sock`. Зависшую отдельную роль внутри живого процесса liveness тоже не
поймает — это известное ограничение, а не решённая задача.

**Ограниченный бюджет перезапуска роли.** Супервизор роли (`cmd/lead-gen`)
перезапускает упавшую роль с экспоненциальным backoff, но в пределах бюджета —
числа попыток (`-role-restart-max-attempts`) и накопленного времени
(`-role-restart-budget`). По исчерпании бюджета супервизор возвращает ошибку, и
процесс завершается неуспешно: свежий контейнер поднимает Docker
(`restart: unless-stopped`), а не вечный backoff внутри.

## Локальный контур полного цикла (`compose.yaml`)

`compose.yaml` поднимает совмещённый первый деплой: PostgreSQL (зелёная/красная),
RabbitMQ, self-hosted Temporal (+UI), локальный приёмник почты **mailpit** и один
app-контейнер со всем продуктом (`-api-port-base` даёт каждому api-сервису свой
порт; in-process мост `approved-action → delivery-command` замкнут).

Порты api-сервисов назначаются по порядку групп `deploy/services.yaml`: 8080
`outreach-orchestration`, 8081 `analytics`, 8082 `lead-management`, 8083 `audience`,
8084 `content-production`, 8085 `request-fulfillment`, 8086 `integration-platform`,
8087 `platform-governance`, 8088 `data-governance`, ingest 8090.

**Единый вход админских консолей (`admin-proxy`).** Все админские консоли живут
под одним origin — обратный прокси **`admin-proxy`** (nginx) публикует один порт
`${ADMIN_HTTP_PORT:-8000}` и маршрутизирует по префиксам путей
(`deploy/nginx/admin-proxy.conf`):

| Префикс | Сервис | Консоль |
|---|---|---|
| `/admin/` | `platform-governance` | Тенант, Пользователи, Регистрации, Интеграции, Аудит, Топология, Операции |
| `/lead-console/` | `lead-management` | Лиды |
| `/intake/` | `request-fulfillment` | Заявки, Исполнение |
| `/reporting/` | `analytics` | Отчёты |
| `/mailing/` | `outreach-orchestration` | Рассылки |

Префикс не снимается на nginx: сервис получает полный путь и сам снимает его
общим middleware `components.PrefixMiddleware`, который переписывает ссылки,
формы, HTMX-атрибуты и редиректы обратно под префикс. Поэтому навигация
оператора остаётся в пределах одного origin и не уводит на порты `:8080`/`:8082`/
`:8085`. Вход оператора: `http://localhost:8000/` → `/admin/console`. Клиентский
портал (`/login`, `/portal`, `/profile`) не является админским фронтом и остаётся
на `platform:8087` без проксирования.

Пароль операторских консолей не передаётся в argv или ENV как значение. Compose
запускает одноразовые staging services для `core`, `demand`, `intake` и `platform`;
каждый получает только host source directory read-only и собственный named volume.
Stager работает как root без сети, socket и privileged mode, с минимальными
`CHOWN`/`FOWNER`/`DAC_OVERRIDE`; app остаётся `65532:65532`, получает только свой
volume read-only. Внутри generation-файлы имеют owner `65532:65532`, mode `0400`.
`CONSOLE_PASSWORD_FILE` указывает на файл внутри поколения; descriptor reader
проверяет generation marker и строгие owner/mode/type условия. Прямой bind (A) не
используется: на Darwin Docker Desktop host `chown` не обеспечивает требуемого
owner в bind mount.

Compose volumes именуются по project, environment (`DEPLOY_ENVIRONMENT`), deployment
ID (`DEPLOYMENT_ID`), bundle и `SECRET_GENERATION`. Задайте новые environment/deployment
IDs для отдельного контура и новое generation при каждой ротации; stage локального
Compose остаётся частью его service graph. Никогда не запускайте runtime против
generation, которое не было успешно staged. Предыдущее поколение сохраняйте весь
rollback window; автоматической очистки нет, retirement выполняется отдельно.
Значения секретов не должны попадать в документацию, argv, ENV или логи.

Прочее поведение — DNS-адреса (localhost) для синхронных контрактов внутри
контейнера, **mailpit** (SMTP 1025, веб 8025) для отправки. Обязательны
`POSTGRES_PASSWORD` и `FIRECRAWL_API_KEY` (без ключа `lead-management` не собран);
`LLM_API_KEY` нужен для писем и планирования.

**Эмбеддинги (семантический recall/RAG).** Провайдер эмбеддингов отделён от
чат-LLM (change `embedding-config-separation`). По умолчанию контур поднимает
self-hosted сервис **`embeddings`** (Ollama, OpenAI-совместимый
`POST /v1/embeddings`) с многоязычной моделью **`bge-m3`** — внешний ключ не
нужен. Внутри сети адрес — `http://embeddings:11434`; на хост публикуется
`http://localhost:11435/v1/embeddings` (порт `EMBEDDINGS_PORT`, по умолчанию
11435 — чтобы не конфликтовать с нативным Ollama на 11434). Модель
подтягивается при первом старте (том `ollama-models` переживает рестарт).
`EMBEDDING_BASE_URL` задаётся **без** суффикса `/v1` (клиент сам склеивает путь
`/v1/embeddings`); `EMBEDDING_KEY=local` — фиктивные креды сервера без
авторизации, не заглушка функции. Внешний OpenAI-совместимый провайдер — опция:
переопределите `EMBEDDING_BASE_URL`/`EMBEDDING_MODEL`/`EMBEDDING_KEY` в `.env`.
Пустой `EMBEDDING_KEY` оставляет recall честно недоступным. Выбор движка/модели —
change `local-embedding-provider`.

Полная топология по одному контейнеру на роль api — задача 2.4 change
`live-e2e-contour` (сетевой транспорт трёх контрактов).

**Доступ к базам с хоста.** Три PostgreSQL контура публикуются на хост с
привязкой к `127.0.0.1` (в сеть не выходят) — для внешних инструментов
(DBeaver, `psql`). Внутренний порт всех — `5432`, host-порты разведены и
переопределяются переменными `.env`:

| Сервис | Переменная | Порт по умолчанию | DSN для внешнего клиента |
| --- | --- | --- | --- |
| `postgres-green` (зелёная зона) | `POSTGRES_GREEN_PORT` | 5433 | `postgres://lead_gen:${POSTGRES_PASSWORD}@localhost:5433/lead_gen?sslmode=disable` |
| `postgres-red` (красная зона) | `POSTGRES_RED_PORT` | 5434 | `postgres://lead_gen:${POSTGRES_PASSWORD}@localhost:5434/lead_gen?sslmode=disable` |
| `postgres-temporal` (БД рантайма) | `POSTGRES_TEMPORAL_PORT` | 5435 | `postgres://temporal:temporal@localhost:5435/temporal?sslmode=disable` |

Пароль зелёной и красной зон общий (`POSTGRES_PASSWORD` из `.env`). Порт `5433`
намеренно отличается от порта одноразовой тестовой базы `make test-postgres`
(тоже публикует `5433`): во время прогона `make test-postgres` контейнер
`postgres-green` может не получить порт — переназначьте `POSTGRES_GREEN_PORT` при
одновременной работе.

## Изолированный DB lifecycle intake

В `compose.yaml` группа `intake` работает только с тремя отдельными runtime DSN
файлами: request-fulfillment, consoleauth и vector-only integration-platform.
Старые green/red/support DSN остаются только в legacy runtime-группах; bootstrap,
migration DSN и пароли ролей в `intake` не монтируются. В фактической миграции
`vector_document.id` имеет тип `text PRIMARY KEY`, sequence для этой таблицы нет.
`integration_invocation_seq_seq` не является зависимостью vector-only intake.

Для локального Compose оператор предоставляет секреты внешним защищённым путём;
их содержимое не передаётся helper через argv/ENV и не записывается в логи. Форматы:

- `*-bootstrap-dsn`, `*-migration-dsn`, `*-runtime-dsn`: по одному DSN на файл;
- `*-migrator-password`, `*-runtime-password` и `intake-vector-runtime-password`:
  только пароль соответствующей роли, без DSN.

Файлы должны быть regular, owner-owned относительно source directory, с mode
`0400` или `0600`, без group/world permissions и без symlink. Bootstrap/migration
файлы нужны только одноразовым jobs. Они не монтируются в runtime контейнеры.
Staging helper изолирован, получает только source directory read-only и один target
volume, не имеет сети, Docker socket или privileged mode, а target bundle и имена
файлов ограничены встроенным allowlist-ом.
Обычный `docker compose up` профиль DB jobs не включает. Профиль `db-admin` и
его прямые `run db-provision-*`/`db-migrate-*` команды оставлены только как
явно fail-closed legacy entrypoints: после обязательного signed candidate manifest
они завершаются ошибкой до подключения к PostgreSQL. Они не являются способом
ручного provisioning и не обходят Release runner. DB release выполняется только
через `deploy/scripts/release-db-jobs.sh` с проверенным release plan, pinned
Docker daemon, активным remote ledger lock, trusted signing/trust key и candidate
bundle. При отсутствии этих входов provisioning недоступен; Compose не запускайте
для DB jobs.

G1.5 для `request-fulfillment` пока также заблокирован: точные checksums 0001–0009
закреплены, но release не допускается, пока isolated PostgreSQL 17 acceptance не
докажет текущие v1 approve/rerun/direct-start, worker/activity payload и cold-start
на схеме 0009. G1-original не объявляется совместимым или целью rollback. Затем
подготавливайте три runtime DSN и запускайте `intake` только с активным подписанным
manifest; отсутствие manifest блокирует runtime, legacy/static role fallback для
`runtime-only` не используется. Provision повторяем на существующем PostgreSQL
volume; он не сбрасывает данные. При ротации задайте новое значение
`SECRET_GENERATION`: Compose выберет новые версии именованных volumes, а stager
создаст поколение в отдельном каталоге без перезаписи старого. Runtime ожидает
именно выбранный generation marker и не переключается на старый secret при ошибке.
Оставьте прежние volumes на rollback window; удаляйте их вручную только после
успешного acceptance. Не запускайте `docker compose down -v`, если нужно сохранить
поколения. Это операторская процедура, а не утверждение о live rollout.

OpenTofu app module принимает `process_env` и `process_secret_files`,
индексированные именем группы из `deploy/services.yaml`; произвольные runtime CLI
аргументы не принимаются. Tofu graph содержит только runtime containers и named
volumes: root stage и DB-job containers из него удалены. Phase 1 создаёт candidate
generation volumes и выводит несекретный `release_plan`; active generation и runtime
не меняются, успешность staging не заявляется.

Release prepare запускается через `deploy/scripts/release-db-jobs.sh` с точным
`DOCKER_HOST` и только path references. В `release_plan` закрепляется точный
Docker Engine `/info.ID` (`docker_daemon_id`), фактический network ID/name/driver/
labels и candidate volume IDs/driver/labels. Runner сверяет их через Docker inspect
до создания state volume или begin ledger. Source root сравнивается с plan, allowlisted
файлы проверяются по метаданным открытых descriptors без чтения secret bytes, а все
helper/DB/signing containers проходят предварительный create+inspect; запущенный
container ID затем повторно сверяется с inspect. `LEADGEN_RELEASE_SIGNING_KEY_FILE` указывает
на owner-only regular file с Ed25519 private key в base64 (64 raw bytes), mode
`0600`; `LEADGEN_RELEASE_KEY_ID` — несекретный идентификатор ключа. Значение ключа
не передаётся в argv/ENV и не логируется: signing container получает только
read-only mount файла, а canonical payload идёт через stdin. Допустимы операции
`--operation prepare` (default), `inspect`, `recover` и `activate`. После prepare
runner создаёт удалённый state volume с именем, включающим environment, deployment
и SHA-256 fingerprint Docker daemon ID/root/name. Nonroot lock-holder в том же
lead-gen image запускается с `--network none` и единственным writable mount этого
volume; он держит Linux `flock`, публикует owner/heartbeat и остаётся работать после
выхода prepare. Он же единственный писатель ledger: CLI передаёт mutation через
Unix socket lock-holder с owner attempt, release nonce и expected revision. Прямой
`release-ledger-write` отключён; токен/owner marker без активного holder lock не даёт
права записи. Переходы сериализуются под flock и фиксируются atomic
write/fsync/rename; конфликт revision отклоняется без потери предыдущего обновления.
Подписанная Ed25519 attestation содержит только точную release metadata и имена
файлов; secret bytes и secret hashes не сохраняются. DB provision и migrate
выполняются последовательно с проверкой фактического exit code; их результаты
попадают в remote ledger.

Чтобы осмотреть незавершённый release, используйте `--operation inspect` с
`--environment` и `--deployment-id`. Stale holder не снимается по heartbeat/expiry.
`recover` требует исходный plan, явное подтверждение остановки старого container и
сверку живого target container: его inspected ID, `LEADGEN_SECRET_GENERATION`, active
G1 volume/mount и network ID должны совпасть с remote ledger и pinned plan. До этой
сверки recovery не меняет ledger и не удаляет stopped holder. При безопасном recovery
candidate помечается failed через IPC; generation/nonce остаются использованными,
active G1 и прежние credentials/volume не переключаются.
`activate` пока намеренно fail-closed: следующий coding slice подключит Tofu
signature/ledger gate, runtime verification и ledger commit. До него verified
holder сохраняется; прямой Tofu apply не объявляется защищённым подписью или lock.
Rotation/revocation trust policy и независимая acceptance остаются открытыми.
Предыдущие volumes автоматически не удаляются.

Bare-metal и local инструкции: source files обязаны существовать на том Docker host,
который указан в `DOCKER_HOST` и используется теми же Docker CLI вызовами runner-а.
Путь с машины CI сам по себе не является remote host path. Локальный сокет также
должен задаваться явно (`DOCKER_HOST=unix:///var/run/docker.sock`). Без проверенного
host runner прекращает работу до контейнерных команд. Для DB ролей запускайте
runner с `--db-target request-fulfillment|consoleauth|integration-platform
--attestation-dir <path>`; он создаёт отдельные подписанные attestations для
provision/migrate bundles и держит remote lock. Migrate не запускается после
неуспешного provision.
Bootstrap/migration credentials нельзя добавлять в runtime maps. Валидаторы
отвергают неизвестные группы, а app module — повторяющиеся имена групп.

OpenTofu `db_jobs` — только allowlisted release-plan metadata (operation, target,
source paths, candidate volume); они не создают контейнеров. Один вызов
`release-db-jobs.sh --operation prepare --db-target <context> --attestation-dir <path>` сериализует
этапы: provision bundle stage/verify → DB provision с фактическим exit check →
migration bundle stage/verify → DB migrate → подпись проверенных manifests → durable
ledger transition в `verified`. Ошибка любого шага помечает candidate failed (если
это возможно) и оставляет lock-holder для явного recovery. Секретные
значения остаются только в host files и named volumes; argv содержит только пути,
targets и несекретные generation identifiers.
