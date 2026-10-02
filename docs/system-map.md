# Карта системы: верхнеуровневые сущности и взаимодействия

> **Статус документа: карта исполнения.** Отвечает на вопросы «где исполняется» и «как связаны сервисы».
> Документ производный: требований и доменных границ не вводит. Поведение определяют спеки (`openspec/changes/*/specs/`), bounded contexts и владение — `architecture/domain-catalog.yaml` и `docs/domain-model.md`, принципы и механизмы — `docs/architecture.md`, функциональный состав — `docs/business-requirements.md`.
> **При расхождении:** рамка → спеки → архитектура (§1–10) → карта. Карта правится, а не спорит. Обратное верно тоже: изменение, добавляющее capability, движок или канал, обязано обновить карту в рамках того же изменения (чек-лист — раздел 7).

## 1. Назначение и место документа

**Что фиксирует карта:**

- сервисы и их роли запуска — ответственность и границы («что делает» и «чего не делает»);
- взаимодействия — кто кого вызывает, каким способом и что передаёт;
- проекцию owning context каждой сущности на сервис и роли, где выполняются его use cases;
- кросс-таблицу: capability ↔ движок BRD ↔ роли запуска ↔ зона данных.

**Чем карта не является:** она не описывает поведение (это спеки), не задаёт схему данных, форматы сообщений и API-эндпоинты (они меняются быстрее карты и живут в коде и спеках), не дублирует источники — ссылается на них.

**Реализации без карты нет:** каждая задача разработки начинается с поиска своего компонента здесь. Если компонента нет — сначала карта, потом код.

## 2. Контексты и компоненты платформы

Четыре уровня, отвечающие на разные вопросы:

| Уровень | Что это | Отвечает на вопрос | Раздел |
|---|---|---|---|
| Bounded contexts | единицы модели, языка и владения агрегатами | кто владеет правилами и состоянием | 2.0 |
| Сервисы | единицы деплоя, отказа и масштабирования: один сервис на контекст; роли — режимы запуска | где исполняется | 2.1 |
| Policies и platform primitives | правила с явным owning context и технические примитивы без бизнес-семантики | откуда берётся повторно используемое поведение | 2.2 |
| Инфраструктура | готовые сервисы, свой код не пишется | чем всё это работает | 2.3 |
| Группы процессов | способ запуска: какие сервисы и роли поднимает один процесс | как запускается и что изолировано | 2.4 |

### 2.0 Проекция bounded context → service

Единица сервиса и развёртывания — bounded context: у каждого контекста ровно один сервис, идентификатор сервиса равен идентификатору контекста. Сервис задаёт границу отказа, релиза и масштабирования, но доменной границей остаётся контекст: сервис собирает application services контекста в composition root и не становится владельцем сущностей.

Шесть значений `api`, `ingest`, `sender`, `worker`, `chains`, `schedule` — не единицы размещения, а **роли** (режимы запуска) внутри сервиса. Роль — способность сервиса работать в определённом режиме, а не второй владелец модели: у роли нет собственных агрегатов. Набор ролей сервиса объявляет каталог (поле `roles`).

Целевой деплой разворачивает по группе процессов на сервис; первый деплой MAY совмещать несколько сервисов в одном образе и процессе (группы в `deploy/services.yaml`, `docs/architecture.md` §8). Совмещение — только способ запуска: выделение сервиса сводится к переносу его в собственную группу без изменения модели. Группы, изоляция ролей и связывание контрактов при co-location — §2.4.

Единица кода совпадает с единицей сервиса: у каждого сервиса ровно один Go-модуль (`contexts/<ctx>`), контракты — отдельные модули `contracts/<owner>`, связывание — workspace `go.work`; раскладка и правила — `docs/architecture.md` §11.

| Сервис (= bounded context) | Роли запуска |
|---|---|
| `platform-governance` | `api`, `worker` |
| `data-governance` | `api`, `worker`, `chains` |
| `lead-management` | `api`, `worker` |
| `audience` | `api`, `worker`, `schedule` |
| `content-production` | `api`, `worker` |
| `outreach-orchestration` | `api`, `worker`, `chains`, `schedule` |
| `message-delivery` | `sender`, `worker`, `schedule` |
| `engagement-observation` | `ingest`, `worker` |
| `sales-workflow` | `api`, `ingest`, `worker` |
| `community-intelligence` | `worker`, `schedule` |
| `publishing` | `api`, `worker`, `schedule` |
| `analytics` | `api`, `worker`, `schedule` |
| `billing` | `api`, `worker` |
| `request-fulfillment` | `api`, `worker` |
| `integration-platform` | `api`, `ingest`, `sender`, `worker`, `chains`, `schedule` |

### 2.1 Роли запуска

Шесть ролей. Служебный образ с единой точкой входа `cmd/lead-gen`, назначение задаётся конфигурацией (`deploy/services.yaml`, `docs/architecture.md` §8); публичный edge — отдельный образ (§2.4). Роль — режим работы сервиса, а не единица размещения.

**`api` — внутренний контур: интерфейс и API**

- **Назначение.** Всё, что видит и делает человек внутри платформы.
- **Делает:** аутентификация и RBAC (3.16); проверка тенанта; CRUD кампаний, шаблонов, сегментов, цепочек, бренд-комплектов; постановка заданий в очередь; очередь утверждений — приём черновиков, решения оператора, журнал решений; отчёты и дашборды (чтение агрегатов); журнал аудита; серверный интерфейс оператора.
- **Не делает:** долгих операций в рамках запроса — они уходят в очередь; не отправляет письма; не обходит утверждение (прямой вызов API без решения оператора не исполняет действие).
- **Входы:** HTTPS от браузера (карточки, кампании, очередь утверждений, отчёты).
- **Выходы:** команды в очередь; записи в зелёную зону; страницы интерфейса.
- **Зона доступа:** зелёная — постоянный доступ; красная — только в рамках поручения (карточки лидов, решения оператора), без копирования на сторону платформы.
- **Реализует:** `outreach-approval`, `billing-subscription`, `billing-success-fee`, доступ и аудит (3.16), чтение отчётов.

**`ingest` — публичный контур приёма**

- **Назначение.** Принять обращение извне, зафиксировать факт и поставить задание в очередь.
- **Делает:** переходы по отслеживаемым ссылкам (3.9); приём вебхуков провайдера отправки (доставка, отказ, жалоба, ответ) и вебхуков CRM; публичные страницы — выбор слота конференц-звонка и просмотр презентации по ссылке; распознавание автоматических переходов (сканеры, шлюзы, дата-центровые диапазоны).
- **Не делает:** не хранит идентифицирующие данные получателя; не выполняет работу — только ставит задание; не имеет доступа к красной зоне.
- **Входы:** HTTP из публичного интернета (отдельный домен), вебхуки внешних сервисов.
- **Выходы:** события в аналитическое хранилище; задания в очередь.
- **Зона доступа:** аналитическое хранилище (обезличенные события); красная зона — **нет** (требование `read-tracking`).
- **Реализует:** `read-tracking`, приём событий для `email-verification` и `crm-workflow`, публичные страницы `call-orchestration` и `presentation-generator`.

**`sender` — контур отправки**

- **Назначение.** Единственный исполняющий орган для исходящих писем.
- **Делает:** исполнение утверждённых черновиков (3.8); прогрев домена и соблюдение графика (5.x); ограничение скорости по домену и получателю; проверки перед отправкой — согласие, стоп-лист, лимиты, готовность домена; обработка отказов и жалоб; фиксация факта отправки.
- **Не делает:** не формирует контент; не утверждает; не исполняет черновик без записи решения оператора; исполняется в сервисе `message-delivery` с отдельной границей масштабирования (§8).
- **Входы:** команды из очереди (утверждённые черновики), события провайдера.
- **Выходы:** вызовы провайдера отправки; факты отправки и доставки; задания для `email-verification` при отказах.
- **Зона доступа:** красная — в рамках поручения, адрес получателя на момент исполнения; зелёная — лимиты, прогрев, отчёты.
- **Реализует:** `domain-deliverability`, `outreach-channel-email` (в части исполнения отправки).

**`worker` — пулы движков**

- **Назначение.** Обработка по контракту движка. Один образ, движок выбирается конфигурацией; пулы масштабируются по длине своей очереди.
- **Делает:** сбор данных (3.1); импорт и нормализация (3.2); обогащение (3.3, 3.17); дедупликация и скоринг (3.4); сегментация (3.5); генерация контента (3.6); рендеринг документов (3.7); верификация адресов (3.18); CRM-синхронизация (3.14); отчётность (3.15); чтение сообществ Telegram (3.12); публикация выжимки (3.20); подготовка обращений через формы (3.19); генерация презентаций и наполнения лендингов.
- **Не делает:** не запускает другие движки напрямую — только через очередь; не отправляет письма; не принимает решения за оператора.
- **Входы:** команды из очереди; данные пула в контуре клиента; внешние источники.
- **Выходы:** записи в зоны и объектное хранилище; черновики в очередь утверждений; события; агрегаты.
- **Зона доступа:** по движку. Красная зона — у импорта, обогащения (включая оценку размера и привязку к лиду), дедупликации, сегментации, верификации, CRM, чтения Telegram (авторы и состав) — в рамках поручения. Без доступа к красной зоне: генерация презентаций и наполнения лендингов (рамка, Решение 7), отчётность (только агрегаты), публикация выжимки (только зелёная зона).
- **Реализует:** `osint-enrichment`, `lead-pool-client-side`, `crm-workflow`, `presentation-generator`, `linguistics`, `telegram-community-digest`, `company-size-assessment`, `email-verification`, `lead-segmentation`, `outreach-channel-form`, `campaign-reporting`, `digest-publishing`, `landing-page-generator`.

**`chains` — долгие состояния и таймеры**

- **Назначение.** Вести процессы, которые живут днями и ждут человека или внешнее событие.
- **Делает:** выполнение сценариев цепочек (3.10) — шаг, задержка, ветвление; порождение черновиков каждого шага; сроки утверждения и эскалация при отсутствии решения; напоминания, переносы и отмены конференц-звонков; контроль сроков; приостановка цепочки при ответе и остановка по исключающим событиям.
- **Не делает:** не отправляет — порождает черновики и переводит состояния; не утверждает по истечении срока (автоутверждение запрещено).
- **Входы:** команды запуска; события реакций из `ingest`; решения оператора из `api`.
- **Выходы:** черновики в очередь утверждений; уведомления; команды таймеров.
- **Зона доступа:** зелёная — состояние выполнения; красная — только идентификаторы и статусы лида в рамках поручения, без копирования содержимого.
- **Реализует:** `outreach-sequences`, исполнение `call-orchestration`, сроки `outreach-approval`.

**`schedule` — расписания**

- **Назначение.** Запускать регулярные работы идемпотентными окнами.
- **Делает:** пересчёт динамических сегментов; повторную верификацию адресов; построение отчётов; чтение сообществ Telegram по расписанию; подготовку материалов публикации (публикация без утверждения не выполняется); контроль сроков хранения.
- **Не делает:** не выполняет работу сам — только ставит задания; пропущенное окно не приводит к двойной обработке; при недоступности очереди задания не теряются.
- **Входы:** собственное расписание (настройка).
- **Выходы:** команды в очередь.
- **Зона доступа:** не имеет собственного доступа к данным — только право постановки заданий.
- **Реализует:** инициацию `lead-segmentation`, `email-verification`, `campaign-reporting`, `telegram-community-digest`, `company-size-assessment`, `digest-publishing`.

### 2.2 Policies и platform primitives

Общей библиотеки бизнес-правил нет. Каждая policy принадлежит bounded context и распространяется опубликованным контрактом; технический примитив не содержит бизнес-терминов. Это позволяет переиспользовать механизм, не создавая `shared domain`.

| Правило или примитив | Владелец | Потребители |
|---|---|---|
| Утверждение действия | `outreach-orchestration` | роли `api`, `chains`, `worker`, `sender` через `approved-action` |
| Зоны, поручение и подавление | `data-governance` | все затрагивающие данные контексты через policies |
| Идемпотентность | `platform` (технический примитив) | роли `ingest`, `worker`, `sender`, `api` |
| Лимиты и частоты | owning context задаёт policy; `platform` даёт атомарный счётчик | `message-delivery`, `lead-management`, `integration-platform` |
| Аудит и доступ | `platform-governance` | все контексты через `access-decision` и `audit-recorded` |
| Наблюдаемость | `platform` (технический примитив) | все сервисы |
| Консоль топологии сервисов | `platform-governance` (эксплуатационная проекция роли `api`) | администратор платформы; читает каталог, `deploy/services.yaml` и метрики Prometheus |
| Порты и адаптеры | `integration-platform` | контексты-потребители |
| Лингвистика | `content-production` | контексты через `content-request` / `content-version` |
| Квалификация взаимодействия | `engagement-observation` | `outreach-orchestration`, `analytics` |
| Верификация адресов | `lead-management`; suppression policy — `data-governance` | `message-delivery`, `audience` |

### 2.3 Инфраструктура

Свой код здесь не пишется — только конфигурация и интеграция.

- **Очередь** (RabbitMQ): очереди по движкам, подтверждения, повторные попытки, DLQ. Потеря задания при перезапуске воркера не допускается.
- **Хранилище зелёной зоны** (PostgreSQL): агрегаты, юрлица из открытых реестров, состояние платформы, аудит.
- **Хранилище красной зоны** (PostgreSQL в контуре клиента): пул лидов, персональные данные физлиц, состояние сделки. Физически отдельно; перекрёстные запросы невозможны (рамка, Решение 1).
- **Аналитическое хранилище:** события трекинга и доставки, read-модели отчётности. События отделены от сущностей: объём на порядки выше.
- **Объектное хранилище** (S3-совместимое): PDF, изображения, архивы лендингов, вложения.
- **Кэш и лимиты** (Valkey): счётчики частоты, блокировки по ключу дедупликации.
- **Долгие процессы** (Temporal): состояние цепочек, таймеры, сигналы утверждения.
- **Метрики** (Prometheus): источник состояния узлов и интенсивности рёбер эксплуатационной консоли топологии сервисов. Консоль — часть роли `api` сервиса `platform-governance` (capability `service-topology-console`): она показывает граф сервисов и инфраструктуры, собранный из объявлений каталога и `deploy/services.yaml`, а метрики читает через серверный прокси (браузер к Prometheus напрямую не ходит). Консоль — **не контекст и не владелец модели**: её узлы и рёбра — эксплуатационная проекция, а не новые сущности или связи.
- **Внешние сервисы:** Firecrawl (сбор и разбор), LLM-провайдер (обезличенный контент), провайдер отправки (SES), сервис верификации адресов, сервис оценки трафика, Twenty CRM (собственная инсталляция), Telegram (MTProto), официальные API площадок публикации, провайдер ссылок конференций.

### 2.4 Группы процессов, изоляция ролей и связывание контрактов

Группа процессов (`process`) — способ запуска: один процесс служебного образа поднимает один или несколько сервисов, каждый со своим подмножеством ролей. Роль — единица размещения: она встречается ровно один раз по всем группам, поэтому разнести её по процессам нельзя. Группа не вправе объявить роль или сервис, которых нет в каталоге.

Текущее (совмещённое) состояние — семь групп, объявленных в `deploy/services.yaml`; они описывают форму деплоя, а не готовность кода:

| Группа | Сервисы и роли | Контур |
|---|---|---|
| `intake` | `request-fulfillment` (`api`, `worker`) | вход ценностной цепочки: заявка клиента |
| `core` | `outreach-orchestration` (`api`, `worker`, `chains`, `schedule`); `analytics` (`api`, `worker`, `schedule`) | горячий путь кампании: оркестрация → аналитика |
| `delivery` | `message-delivery` (`sender`, `worker`, `schedule`); `integration-platform` (`sender`) | изолированный контур отправки |
| `demand` | `lead-management` (`api`, `worker`); `audience` (`api`, `worker`, `schedule`); `content-production` (`api`, `worker`) | подготовка аудитории: лиды → сегменты → контент |
| `public` | `engagement-observation` (`ingest`, `worker`); `community-intelligence` (`worker`, `schedule`); `publishing` (`api`, `worker`, `schedule`) | публичный контур: приём событий, чтение сообществ, публикация |
| `platform` | `integration-platform` (`api`, `ingest`, `worker`, `chains`, `schedule`); `platform-governance` (`api`, `worker`); `data-governance` (`api`, `worker`, `chains`) | общий фундамент: интеграционные порты и правила доступа/данных |
| `revenue` | `sales-workflow` (`api`, `ingest`, `worker`); `billing` (`api`, `worker`) | результат и деньги: сделки → биллинг |

Группы — форма первого деплоя, а не второй владелец модели: целевое состояние — группа на сервис, а переход между состояниями — правка `deploy/services.yaml` без изменения кода и домена.

**Изоляция опасных ролей.** Роль `sender` — единственный исполнитель исходящих писем — не делит процесс с `api` оператора, поэтому группа `delivery` выделена отдельно, а роль `sender` сервиса `integration-platform` убрана из группы `platform`. Публичный контур также отделён от служебного API: приём внешних событий (`ingest`) идёт в группе `public`, а публичный edge — самостоятельный модуль и отдельный образ на своём домене, сервисом каталога он не объявляется (`docs/architecture.md` §8, §11). Отправка — необратимое внешнее действие, и её граница отказа и масштабирования не может совпадать с обработкой API-запросов.

**Связывание контрактов при co-location.** Синхронная межконтекстная связь выражается контрактом-интерфейсом модуля `contracts/<owner>`. Когда сервисы исполняются в одном процессе (совмещённый деплой, `mechanism: in-process`), вызов MAY идти in-process адаптером; когда сервисы разделены — сетевым. Выбор задаёт топология, а не код потребителя: потребитель зависит от контракта и не импортирует `internal` владельца. In-process адаптер сохраняет семантику контракта — дедлайн, отмену и ключ идемпотентности, — поэтому переход к сетевому адаптеру не меняет наблюдаемое поведение.

## 3. Взаимодействия

### 3.1 Правила связей

1. **Прямых вызовов «движок → движок» нет.** Связь идёт через очередь, хранилище или публичный приём (`docs/architecture.md` §1).
2. **Синхронно — только то, что ждёт человек на экране.** Всё остальное асинхронно.
3. **Транспорт объявлен каталогом.** `command` и `query` идут синхронно (`sync`/`http-json`, HTTP/JSON поверх `net/http`, DTO владельца); `event` и `policy` — асинхронно (`async`/`amqp`, RabbitMQ). У каждой связи объявлены `transport` и `mechanism`; расхождение с видом интеграции запрещено.
4. **Утверждение — единственный вход в отправку.** Ни один поток не ведёт в `sender` иначе, как через утверждённый черновик.
5. **Приём событий не выполняет работу** — `ingest` фиксирует факт и ставит задание.
6. **Все точки приёма извне идемпотентны** по ключам `docs/architecture.md` §6.1.
7. **Данные красной зоны не покидают контур клиента.** Наружу уходят агрегаты и обезличенные материалы.
8. **Отказ внешнего сервиса не останавливает конвейер:** своя очередь у каждого движка, повторные попытки, DLQ.

### 3.2 Схема потоков

```
  БРАУЗЕР ──HTTPS──> api ──команды──> ОЧЕРЕДЬ ──> worker (пулы движков)
                      │                              │
                      │ чтение/запись                ├─> зелёная зона
                      v                              ├─> КОНТУР КЛИЕНТА (красная зона)
              ЗЕЛЁНАЯ ЗОНА                           ├─> объектное хранилище
                      ^                              └─> внешние сервисы
                      │
  ИНТЕРНЕТ ──> ingest ──события──> аналитическое хранилище
                  │      └──задания──> ОЧЕРЕДЬ
                  └──вебхуки (провайдер отправки, CRM)

  ОЧЕРЕДЬ ──утверждённые черновики──> sender ──> провайдер отправки ──> вебхуки ──> ingest
  chains ──черновики и таймеры──> ОЧЕРЕДЬ          (решения оператора приходят из api)
  schedule ──команды по расписанию──> ОЧЕРЕДЬ
```

Обозначения `api`, `ingest`, `sender`, `worker`, `chains`, `schedule` — роли запуска внутри сервисов (§2.1), а не отдельные процессы размещения.

### 3.3 Матрица связей

**Межконтекстные связи (38, по каталогу).** Транспорт каждой связи объявлен в каталоге и не выводится из контекста рассуждения: `command`/`query` — `sync`/`http-json`, `event`/`policy` — `async`/`amqp`. `policy` доставляется как подписка на изменение правила, а не синхронным вызовом: потребитель хранит контрактный снимок и применяет его локально.

| Upstream (сервис) | Downstream (сервис) | Тип | Транспорт | Механизм | Контракт |
|---|---|---|---|---|---|
| `outreach-orchestration` | `message-delivery` | command | sync | http-json | `approved-action` |
| `community-intelligence` | `publishing` | command | sync | http-json | `community-digest` |
| `integration-platform` | `lead-management` | command | sync | http-json | `provider-port` |
| `integration-platform` | `content-production` | command | sync | http-json | `provider-port` |
| `integration-platform` | `message-delivery` | command | sync | http-json | `provider-port` |
| `integration-platform` | `sales-workflow` | command | sync | http-json | `provider-port` |
| `integration-platform` | `community-intelligence` | command | sync | http-json | `provider-port` |
| `integration-platform` | `publishing` | command | sync | http-json | `provider-port` |
| `integration-platform` | `outreach-orchestration` | command | sync | http-json | `provider-port` |
| `integration-platform` | `lead-management` | command | sync | http-json | `vector` |
| `integration-platform` | `request-fulfillment` | command | sync | http-json | `vector` |
| `outreach-orchestration` | `request-fulfillment` | command | sync | http-json | `call-intent` |
| `request-fulfillment` | `platform-governance` | command | sync | http-json | `request-intent` |
| `lead-management` | `audience` | query | sync | http-json | `lead-profile` |
| `lead-management` | `sales-workflow` | query | sync | http-json | `company-profile` |
| `audience` | `outreach-orchestration` | query | sync | http-json | `audience-snapshot` |
| `content-production` | `outreach-orchestration` | query | sync | http-json | `content-version` |
| `message-delivery` | `engagement-observation` | event | async | amqp | `delivery-event` |
| `message-delivery` | `lead-management` | event | async | amqp | `delivery-event` |
| `message-delivery` | `analytics` | event | async | amqp | `delivery-event` |
| `engagement-observation` | `outreach-orchestration` | event | async | amqp | `engagement-event` |
| `engagement-observation` | `audience` | event | async | amqp | `engagement-event` |
| `engagement-observation` | `analytics` | event | async | amqp | `engagement-event` |
| `outreach-orchestration` | `sales-workflow` | event | async | amqp | `outreach-outcome` |
| `outreach-orchestration` | `analytics` | event | async | amqp | `analytics-event` |
| `sales-workflow` | `billing` | event | async | amqp | `attributed-result` |
| `sales-workflow` | `analytics` | event | async | amqp | `deal-state` |
| `data-governance` | `lead-management` | policy | async | amqp | `data-zone-policy` |
| `data-governance` | `outreach-orchestration` | policy | async | amqp | `processing-authority` |
| `data-governance` | `message-delivery` | policy | async | amqp | `suppression-policy` |
| `data-governance` | `audience` | policy | async | amqp | `data-zone-policy` |
| `platform-governance` | `outreach-orchestration` | policy | async | amqp | `access-decision` |
| `platform-governance` | `request-fulfillment` | policy | async | amqp | `tenant-context` |
| `lead-management` | `request-fulfillment` | query | sync | http-json | `lead-profile` |
| `audience` | `request-fulfillment` | query | sync | http-json | `audience-snapshot` |
| `outreach-orchestration` | `request-fulfillment` | query | sync | http-json | `campaign-state` |
| `sales-workflow` | `request-fulfillment` | query | sync | http-json | `deal-state` |
| `billing` | `request-fulfillment` | policy | async | amqp | `billing-entitlement` |

Итого: 21 синхронная связь (13 `command`, 8 `query`) и 17 асинхронных (10 `event`, 7 `policy`).

**Операционные взаимодействия.** Строки ниже описывают потоки внутри платформы и к внешним системам; инициатор и адресат — сервисы и их роли.

| Инициатор | Адресат | Способ | Предмет | Зона | Ключ идемпотентности |
|---|---|---|---|---|---|
| Браузер | `api` | HTTPS | изменяющая команда оператора | по команде | тенант + клиентский идентификатор команды |
| `api` | ОЧЕРЕДЬ | команда | постановка задания любого движка | зелёная | идентификатор команды |
| `api` | зелёная зона | синхронно | CRUD, карточки, отчёты, аудит | зелёная | идентификатор команды для изменяющих операций |
| `api` | контур клиента | по поручению | карточки лидов, решения оператора | красная | тенант + идентификатор команды |
| `ingest` | ОЧЕРЕДЬ | задание | переход по отслеживаемой ссылке | без ПД | идентификатор сообщения + тип события + временное окно |
| `ingest` | ОЧЕРЕДЬ | задание | вебхук провайдера отправки | без ПД | идентификатор события провайдера |
| `ingest` | ОЧЕРЕДЬ | задание | вебхук CRM | без ПД | идентификатор записи + версия изменения |
| `ingest` | ОЧЕРЕДЬ | задание | выбор слота на публичной странице | без ПД | источник + внешний идентификатор события |
| `ingest` | аналитическое хранилище | запись события | трекинг переходов | обезличенно | идентификатор сообщения + тип события + временное окно |
| `ingest` | контур клиента | нет доступа | — | — | — |
| `worker` | внешние сервисы | синхронно, с лимитами | сбор, LLM, верификация, оценка трафика | зелёная или обезличенно | идентификатор задания + тип запроса |
| `worker` | ОЧЕРЕДЬ | результат | обновлённые статусы, следующий шаг, черновик | по движку | идентификатор задания + версия результата |
| `worker` | контур клиента | по поручению | нормализация, обогащение, дедупликация, скоринг, сегменты, оценка размера, верификация, сделки | красная | идентификатор задания или хеш файла импорта |
| `worker` | объектное хранилище | запись | PDF, изображения, архивы лендингов | зелёная | идентификатор артефакта + версия |
| `worker` (CRM) | Twenty CRM | GraphQL и вебхуки | сделки, стадии, история коммуникаций | красная | идентификатор записи + версия изменения |
| `worker` (Telegram) | Telegram (MTProto) | MTProto | чтение сообществ: выжимка и авторы | обе | идентификатор сообщества + идентификатор сообщения |
| `worker` (публикация) | площадки | официальные API | публикация утверждённого материала | зелёная | идентификатор публикации + версия |
| `worker` (отчётность) | аналитика и контур клиента | чтение | агрегаты с минимальным порогом группы | обезличенно | идентификатор окна отчёта |
| `chains` | ОЧЕРЕДЬ | команда | следующий шаг цепочки, таймер, напоминание | зелёная | идентификатор выполнения + шаг |
| `chains` | `api` | уведомление | требуется решение оператора, срок истёк | зелёная | идентификатор объекта + тип уведомления |
| `sender` | провайдер отправки | синхронно | отправка утверждённого черновика | зелёная | идентификатор утверждённого черновика |
| `sender` | ОЧЕРЕДЬ | событие | факт отправки, отказ, жалоба | зелёная | идентификатор события провайдера |
| `schedule` | ОЧЕРЕДЬ | команда | пересчёты, повторная верификация, отчёты, чтение Telegram, подготовка публикаций | зелёная | идентификатор окна расписания |
| Провайдер отправки | `ingest` | вебхук | доставка, отказ, ответ | без ПД | идентификатор события провайдера |
| CRM | `ingest` | вебхук | событие CRM | без ПД | идентификатор записи + версия изменения |

**Что проверяется по этой матрице:** ни одна строка не ведёт в `sender` минуя утверждённый черновик; `ingest` не имеет доступа к красной зоне; ни один сервис не вызывает другой движок напрямую; транспорт каждой межконтекстной связи совпадает с видом интеграции (таблица выше).

## 4. Доменные сущности и владельцы

**Правило:** у сущности ровно один владелец — bounded context, в котором живут агрегат и его инварианты. Сервисы и их роли только исполняют use cases этого контекста. Остальные контексты меняют состояние через опубликованный контракт владельца. Для красной зоны хранение остаётся в контуре клиента; owning context описывает семантику, а не физическое размещение.

| Сущность | Владелец | Зона | Хранилище | Ключевые состояния |
|---|---|---|---|---|
| Тенант | `platform-governance` | зелёная | реляционное | активен / приостановлен |
| Пользователь, роль | `platform-governance` | зелёная | реляционное | активен / заблокирован |
| Компания | `lead-management` | зелёная либо контур клиента | реляционное | профиль собран / уточнён |
| Контакт (лид) | `lead-management` | контур клиента | реляционное в контуре клиента | нормализован → проверен |
| Происхождение атрибута | `lead-management` | контур клиента | реляционное в контуре клиента | источник и дата по каждому полю |
| Основание обработки | `data-governance` | контур клиента | реляционное в контуре клиента | есть / нет / отозвано |
| Стоп-лист | `data-governance` | контур клиента | реляционное в контуре клиента | отписка, жалоба, жёсткий отказ, `abuse` |
| Верификация адреса | `lead-management` | контур клиента | реляционное в контуре клиента | `deliverable` / `risky` / `unknown` / `invalid` / `disposable` / `role` / `abuse` |
| Скоринг | `audience` | контур клиента | реляционное в контуре клиента | оценка по ICP |
| Сегмент | `audience` | контур клиента | реляционное в контуре клиента | статический / динамический, состав зафиксирован |
| Снимок аудитории | `audience` | контур клиента | реляционное в контуре клиента | состав зафиксирован на момент запуска кампании; при изменении сегмента не пересчитывается |
| Кампания | `outreach-orchestration` | зелёная | реляционное | черновик → активна → остановлена → завершена |
| Цепочка (сценарий) | `outreach-orchestration` | зелёная | реляционное | черновик / активна / остановлена |
| Выполнение цепочки | `outreach-orchestration` | зелёная | workflow | шаг ожидает → черновик → утверждён → исполнен; пауза при ответе |
| Черновик действия | `outreach-orchestration` | зелёная | реляционное | предложено → ожидает утверждения → утверждено / отклонено → исполнено / ошибка |
| Решение оператора | `outreach-orchestration` | зелёная | реляционное | подтверждено / с правками / отклонено / отложено; пакетное — с признаком |
| Сообщение | `message-delivery` | зелёная | реляционное | отправлено / доставлено / отказ / жалоба |
| Репутация отправителя | `message-delivery` | зелёная | реляционное | прогрев не завершён → завершён; отправка приостановлена / ограничена |
| Событие | `engagement-observation` | аналитическое | аналитическое | доставка, переход, ответ, отказ; открытий нет |
| Сделка | `sales-workflow` | контур клиента | внешняя система | стадии; переход утверждается человеком |
| Синхронизация с CRM | `sales-workflow` | зелёная | реляционное | — |
| Звонок | `outreach-orchestration` | контур клиента | реляционное в контуре клиента | назначен → подтверждён → план утверждён → ссылка выдана → проведён / перенесён / отменён |
| Выжимка сообщества | `community-intelligence` | зелёная | реляционное | построена; авторы и состав — красная зона, выгрузка запрещена |
| Публикация | `publishing` | зелёная | реляционное | подготовлена → утверждена → опубликована / отклонена площадкой |
| Снимок отчёта | `analytics` | аналитическое | аналитическое | актуален; при недоступности контура — последний рассчитанный с указанием времени |
| Профиль размера компании | `lead-management` | зелёная | реляционное | `измерено` / `оценка` / `недоступно` |
| Контент-пакет | `content-production` | зелёная | реляционное | — |
| Версия контента | `content-production` | зелёная | реляционное | версия сохранена с историей изменений; откат к предыдущей |
| Наполнение лендинга | `content-production` | зелёная | объектное | версия наполнения; публикация вне платформы |
| Документ (PDF, изображение) | `content-production` | зелёная | объектное | версия; детерминированный рендеринг |
| Задание рендеринга | `content-production` | зелёная | реляционное | — |
| Подписка и счёт | `billing` | зелёная | реляционное | активна / приостановлена / лимиты тарифа |
| Вознаграждение за результат | `billing` | зелёная | реляционное | кандидат → начислено / исключено; спор приостанавливает списание |
| Регистрация адаптера | `integration-platform` | зелёная | реляционное | зарегистрирован; прошёл контрактные тесты / не допущен к поставке |
| Вызов интеграции | `integration-platform` | зелёная | реляционное | выполнен / нормализованная ошибка; тайм-аут; отменён |
| Запись аудита | `platform-governance` | зелёная | реляционное | неизменяема; корректировка только новой записью |
| Заявка | `request-fulfillment` | зелёная | реляционное | `submitted` → `validating` → `validated` → `processing` → `fulfilled`; терминальные `rejected`, `cancelled` |
| Оффер | `request-fulfillment` | зелёная | реляционное | версия оффера (`revision`); прежняя версия остаётся неизменной |

**Открытых владельцев нет.** Если при разборе новой capability окажется, что сущности не назначен владелец, это пробел требований: он выносится отдельным изменением, а не додумывается здесь.

## 5. Кросс-таблица: capability ↔ context ↔ движок BRD ↔ роли запуска ↔ зона ↔ спека

| Capability | Primary context | Движок BRD | Роли запуска | Зона | Спека |
|---|---|---|---|---|---|
| `domain-architecture` | `platform-governance` | — | — | — | `openspec/specs/domain-architecture/spec.md` |
| `platform-access` | `platform-governance` | 3.16 | `api`, `worker` | зелёная | `platform-governance/specs/platform-access/spec.md` |
| `service-topology-console` | `platform-governance` | — | `api` | зелёная | `service-topology-console/specs/service-topology-console/spec.md` |
| `outreach-approval` | `outreach-orchestration` | — (ядро) | `api`, `chains` | зелёная | `full-enterprise-core-scope/specs/outreach-approval/spec.md` |
| `read-tracking` | `engagement-observation` | 3.9 | `ingest`, `worker` | обезличенно | `full-enterprise-core-scope/specs/read-tracking/spec.md` |
| `domain-deliverability` | `message-delivery` | 3.8 | `sender`, `schedule` | зелёная | `full-enterprise-core-scope/specs/domain-deliverability/spec.md` |
| `data-handling` | `data-governance` | — (ядро) | все роли | обе | `full-enterprise-core-scope/specs/data-handling/spec.md` |
| `contact-suppression` | `data-governance` | — (ядро) | `api`, `worker`, `chains` | контур клиента | `data-governance/specs/contact-suppression/spec.md` |
| `osint-enrichment` | `lead-management` | 3.1, 3.3 | `worker` | обе (карточка — зелёная, контакты — красная) | `mvp-lead-search/specs/osint-enrichment/spec.md` |
| `outreach-channel-email` | `message-delivery` | 3.8 | `sender` | зелёная + контур клиента | `full-enterprise-core-scope/specs/outreach-channel-email/spec.md` |
| `lead-pool-client-side` | `data-governance` | 3.2, 3.4 | `worker` | контур клиента | `full-enterprise-core-scope/specs/lead-pool-client-side/spec.md` |
| `crm-workflow` | `sales-workflow` | 3.14 | `worker`, `ingest` | контур клиента | `full-enterprise-core-scope/specs/crm-workflow/spec.md` |
| `billing-subscription` | `billing` | — | `api` | зелёная | `full-enterprise-core-scope/specs/billing-subscription/spec.md` |
| `billing-success-fee` | `billing` | — | `api`, `worker` | зелёная | `full-enterprise-core-scope/specs/billing-success-fee/spec.md` |
| `presentation-generator` | `content-production` | 3.6, 3.7 | `worker` | зелёная | `full-enterprise-core-scope/specs/presentation-generator/spec.md` |
| `linguistics` | `content-production` | 3.6 | `worker` | по запросу | `full-enterprise-core-scope/specs/linguistics/spec.md` |
| `telegram-community-digest` | `community-intelligence` | 3.12 | `worker`, `schedule` | обе | `telegram-community-digest/specs/telegram-community-digest/spec.md` |
| `company-size-assessment` | `lead-management` | 3.17 | `worker` | обе | `company-size-assessment/specs/company-size-assessment/spec.md` |
| `email-verification` | `lead-management` | 3.18 | `worker` | контур клиента | `email-verification/specs/email-verification/spec.md` |
| `lead-segmentation` | `audience` | 3.5 | `worker`, `schedule` | контур клиента | `lead-segmentation/specs/lead-segmentation/spec.md` |
| `outreach-channel-form` | `outreach-orchestration` | 3.19 | `worker` | зелёная | `outreach-channel-form/specs/outreach-channel-form/spec.md` |
| `outreach-sequences` | `outreach-orchestration` | 3.10 | `chains` | зелёная + контур клиента | `sequences-and-reporting/specs/outreach-sequences/spec.md` |
| `campaign-reporting` | `analytics` | 3.15 | `worker`, `schedule` | обезличенно | `sequences-and-reporting/specs/campaign-reporting/spec.md` |
| `digest-publishing` | `publishing` | 3.20 | `worker`, `schedule` | зелёная | `digest-publishing/specs/digest-publishing/spec.md` |
| `landing-page-generator` | `content-production` | — | `worker` | зелёная | `landing-page-generator/specs/landing-page-generator/spec.md` |
| `call-orchestration` | `outreach-orchestration` | — | `chains`, `worker`, `ingest` | обе | `call-orchestration/specs/call-orchestration/spec.md` |
| `provider-agnostic-integrations` | `integration-platform` | — | все роли | по порту | `migrate-platform-to-go/specs/provider-agnostic-integrations/spec.md` |
| `client-request` | `request-fulfillment` | — (ядро) | `api`, `worker` | зелёная | `openspec/specs/client-request/spec.md` |
| `request-validation` | `request-fulfillment` | — (ядро) | `api`, `worker` | зелёная | `openspec/specs/request-validation/spec.md` |
| `service-topology` | `platform-governance` | — | — | — | `service-per-context/specs/service-topology/spec.md` |
| `deployment-topology` | `platform-governance` | — | `api`, `worker` | — | `openspec/specs/deployment-topology/spec.md` |
| `observability` | `platform-governance` | — | — | — | `openspec/specs/observability/spec.md` |
| `client-portal` | `platform-governance` | — | `api`, `worker` | зелёная | `openspec/specs/client-portal/spec.md` |
| `lead-workflow` | `lead-management` | — | `api`, `worker` | обе | `openspec/specs/lead-workflow/spec.md` |
| `lead-email` | `lead-management` | 3.8 | `api`, `worker` | зелёная + контур клиента | `openspec/specs/lead-email/spec.md` |
| `email-template-library` | `lead-management` | — | `api`, `worker` | зелёная | `openspec/specs/email-template-library/spec.md` |
| `search-strategy` | `lead-management` | — | `api`, `worker` | зелёная | `openspec/specs/search-strategy/spec.md` |
| `company-quota-search` | `lead-management` | 3.1 | `api`, `worker` | обе | `openspec/specs/company-quota-search/spec.md` |
| `forum-lead-signals` | `lead-management` | — | `api`, `worker` | обе | `ai-lead-discovery/specs/forum-lead-signals/spec.md` |
| `lead-relevance` | `lead-management` | — | `api`, `worker` | обе | `openspec/specs/lead-relevance/spec.md` |
| `search-job-control` | `lead-management` | — | `api`, `worker` | зелёная | `openspec/specs/search-job-control/spec.md` |
| `search-job-lifecycle` | `lead-management` | — | `api`, `worker` | зелёная | `openspec/specs/search-job-lifecycle/spec.md` |
| `search-job-progress` | `lead-management` | — | `api`, `worker` | зелёная | `openspec/specs/search-job-progress/spec.md` |
| `search-job-error-codes` | `lead-management` | — | `api`, `worker` | зелёная | `openspec/specs/search-job-error-codes/spec.md` |
| `search-job-events` | `lead-management` | — | `api`, `worker` | зелёная | `openspec/specs/search-job-events/spec.md` |
| `job-journal-console` | `lead-management` | — | `api` | зелёная | `openspec/specs/job-journal-console/spec.md` |
| `lead-cards-console` | `lead-management` | — | `api` | обе | `openspec/specs/lead-cards-console/spec.md` |
| `console-appearance` | `lead-management` | — | `api` | зелёная | `openspec/specs/console-appearance/spec.md` |
| `semantic-lead-search` | `lead-management` | — | `api`, `worker` | обе | `openspec/specs/semantic-lead-search/spec.md` |
| `client-dashboard` | `analytics` | 3.15 | `api`, `worker`, `schedule` | обезличенно | `openspec/specs/client-dashboard/spec.md` |
| `firecrawl-credit-budget` | `integration-platform` | 3.1 | `worker` | зелёная | `openspec/specs/firecrawl-credit-budget/spec.md` |
| `crawler-proxy-pool` | `integration-platform` | 3.1 | `api`, `ingest`, `sender`, `worker`, `chains`, `schedule` | зелёная | `self-hosted-crawler/specs/crawler-proxy-pool/spec.md` |

**Движки BRD вне таблицы — исключены из объёма, возврату не подлежат без отдельного решения:** **3.11** (синхронизация почты менеджера) и **3.13** (LinkedIn) — рамка, Решения 1 и 9.

**Движки без одноимённой capability — это норма, а не пробел:** capability описывает возможность, а не раздел BRD. Движок **3.16** (доступ, роли, аудит) реализуется как модуль ядра в `api`; движки **3.1** (сбор) и **3.2** (импорт и нормализация) покрыты capability `osint-enrichment` и `lead-pool-client-side` соответственно.

**Проверка полноты:** таблица должна совпадать с discovery `archlint` по `openspec/specs/**/spec.md` и `openspec/changes/*/specs/**/spec.md`; ручное число не считается источником истины.

## 6. Что где исполняется

| Роль | Контур | Доступ к зонам | Масштабирование | Что при отказе |
|---|---|---|---|---|
| `api` | внутренний | зелёная; красная — по поручению | горизонтальное, по числу операторов | интерфейс недоступен, конвейер продолжает работу |
| `ingest` | публичный (отдельный домен) | аналитика; красной зоны нет | отдельное, по объёму переходов | переходы теряются только за время отказа; задания не теряются |
| `sender` | отправки (изолирован) | красная — по поручению, только адрес на момент исполнения | отдельное, по графику прогрева | отправка приостанавливается, черновики остаются утверждёнными |
| `worker` | внутренний | по движку | пулы по длине своей очереди | очередь копится; DLQ видна администратору |
| `chains` | внутренний | зелёная; красная — по поручению для сущности звонка и идентификаторов лидов | по числу активных состояний | состояние сохраняется, шаг продолжается после восстановления |
| `schedule` | внутренний | нет собственного доступа | одна реплика на контур (иначе двойные окна) | пропущенное окно перезапускается, двойной обработки нет |

**Контур клиента как место исполнения не зафиксирован.** Столбец «Контур» выше описывает размещение на стороне платформы. Пул роли `worker`, работающий с красной зоной (§2.1, §3.3, §5), исполняется там же, где лежит пул; его размещение следует за формой пула в режиме A, а форма не выбрана. Рамка держит вопрос открытым (п. 1 «Открытых вопросов», `full-enterprise-core-scope/design.md`: мультитенантный SaaS с раздельными контурами / шифрование ключом клиента / on-prem) и отмечает, что решение требуется до реализации режима A. Поэтому карта фиксирует основание доступа — поручение, — а не физическую топологию, и не утверждает, что сервисы платформы и красная зона разнесены физически.

**Изоляция контуров** задана `docs/architecture.md` §8 и не пересматривается: контур отправки масштабируется независимо от API; публичный приём — на отдельном домене; контур клиента физически отделён (рамка, Решение 1).

**Локальный контур разработки.** Для первой версии допускается поднять все сервисы и инфраструктуру одним compose-контуром на одной машине: это топология деплоя, а не декомпозиция кода. Состав минимального продового набора сервисов решается при внедрении.

## 7. Изменение карты

Карта обновляется **в том же изменении**, которое добавляет движок, канал, сущность или компонент. Чек-лист:

1. **Спека** — создана или изменена capability; карта ссылается на неё, а не пересказывает.
2. **Сервис и роль** — определён сервис-владелец и его роль; если ни одна из шести ролей не подходит — это архитектурное решение, а не деталь реализации.
3. **Связи** — в матрицу добавлены строки: инициатор, адресат, способ, предмет, зона.
4. **Данные** — у новых сущностей указан владелец, зона и хранилище; для красной зоны — основание доступа.
5. **Кросс-таблица** — строка capability добавлена; проверено, что список совпадает с каталогом `openspec/changes/*/specs/*/`.
6. **Наблюдаемость и отказы** — для нового сервиса, роли или движка определено, что видно в метриках и что происходит при отказе.
7. **Приоритет не нарушен** — карта не вводит требований и не противоречит спеке; найденное расхождение правит карту, а пробел требований выносится отдельным изменением.

**Обновление карты обязательно и для правок, которые кажутся косметическими:** устаревшая карта хуже её отсутствия, потому что ей доверяют.
