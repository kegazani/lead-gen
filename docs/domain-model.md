# Доменная модель платформы

## Назначение и статус

Этот документ задаёт целевую DDD-модель платформы и правила её реализации на Go. Машиночитаемым источником истины для состава bounded contexts, владения данными, межконтекстных контрактов и обязательных диаграмм служит [`architecture/domain-catalog.yaml`](../architecture/domain-catalog.yaml). Документ раскрывает смысл каталога, но не вводит параллельную таксономию.

Платформа строится по принципу **context first**: сначала определяется bounded context и его язык, затем агрегаты и use cases, и только после этого transport, persistence и адаптеры. Go-пакеты отражают эту границу. Общие технические библиотеки допустимы; общая «универсальная» доменная модель — нет.

## Ubiquitous language

| Термин | Точное значение | Не означает |
|---|---|---|
| Тенант | Изолированная область клиента, в которой действуют пользователи, роли, данные и настройки | Юридическое лицо вне платформы |
| Пользователь и роль | Назначение пользователя тенанта с набором ролей, определяющих его доступ | Учётная запись вне тенанта или контакт из базы лидов |
| Запись аудита | Неизменяемая запись о значимом действии с актором, временем и основанием | Журнал отладки приложения или операционная телеметрия |
| Зона данных | Классификация допустимого размещения и обработки данных: `green`, `red`, `mixed`, `analytics` | Уровень секретности или роль пользователя |
| Основание обработки | Зафиксированное полномочие обрабатывать определённые данные для определённой цели | Общая настройка согласия «на всё» |
| Стоп-лист | Авторитетный набор адресатов, которым действие запрещено | Ошибка доставки или временная пауза |
| Лид | Представление потенциального клиента в контексте управления лидами | Участник кампании или сделка |
| Компания | Организация с нормализованными атрибутами и происхождением данных | Тенант платформы |
| Профиль размера компании | Набор метрик размера и цифрового присутствия компании с источником и признаком достоверности | Подтверждённый факт о размере компании или её финансовые реквизиты |
| Контакт | Идентифицируемое лицо или адресат, связанный с компанией | Пользователь платформы |
| Происхождение атрибута | Источник, время получения и доказательство для конкретного значения | Источник всей записи целиком |
| Верификация адреса | Результат проверки достижимости адреса с ограниченным сроком актуальности | Разрешение на отправку |
| Скоринг лида | Версионированная оценка соответствия критериям | Решение об отправке сообщения |
| Сегмент | Правило отбора лидов | Сохранённый список участников |
| Снимок аудитории | Неизменяемый состав аудитории кампании на момент фиксации | Живой результат запроса сегмента |
| Контент-пакет | Контейнер версий материала и связанных наполнений и документов | Одна версия текста или готовый отрендеренный артефакт |
| Версия контента | Неизменяемая редакция материала с языком, шаблоном и переменными | Доставленное сообщение |
| Задание рендеринга | Воспроизводимое задание, привязанное к точной версии контента и целевому формату | Версия контента или выполненная публикация |
| Наполнение лендинга | Структура блоков, тексты и форма заявки для страницы лендинга | Размещённая и хостируемая платформой страница |
| Документ | Файл материала (PDF, изображение) в составе контент-пакета | Договор, счёт или иной документ сделки |
| Кампания | Намерение провести согласованную серию исходящих действий | Отдельная отправка или сделка |
| Сценарий цепочки | Версионированный граф шагов и переходов | Конкретное выполнение цепочки |
| Выполнение цепочки | Состояние прохождения участником сценария | Фоновая задача или cron job |
| Черновик действия | Подготовленное, но ещё не разрешённое исходящее действие | Команда поставщику |
| Решение оператора | Аудируемое одобрение или отклонение черновика человеком | Автоматический скоринг |
| Звонок | Каноническая запись конференц-звонка, сведённая из источников, с подтверждением слота и утверждением плана | Отправленное письмо или запись разговора |
| Утверждённое действие | Неизменяемый межконтекстный факт-разрешение на конкретное действие | Бессрочное разрешение на будущие действия |
| Сообщение | Попытка исполнения утверждённого почтового действия | Версия контента или переписка целиком |
| Репутация отправителя | Показатели доставляемости и ограничения отправляющего домена | Оценка отдельного сообщения или репутация компании-клиента |
| Событие взаимодействия | Квалифицированный сигнал доставки, перехода или ответа | Сырой webhook поставщика |
| Сделка | Коммерческая возможность в sales workflow | Лид или кампания |
| Синхронизация с CRM | Сверка локальной записи с внешней CRM с хранением внешней версии | Сама сделка или её этап |
| Подтверждённый результат | Атрибутированный бизнес-результат с доказательствами | Любой ответ адресата |
| Выжимка сообщества | Обезличенный результат разрешённого чтения сообщества | Архив сообщений сообщества |
| Публикация | Попытка разместить утверждённый материал на площадке | Сам материал |
| Снимок отчёта | Воспроизводимая аналитическая read-модель на момент времени | Авторитетное состояние исходного домена |
| Подписка | Право тенанта на набор возможностей и лимитов | Факт оплаты отдельного счёта |
| Вознаграждение за результат | Начисление, рассчитанное из подтверждённого результата по версии правила | Подписка, счёт за тариф или любой ответ адресата |
| Provider port | Стабильный интерфейс потребности домена во внешней системе | SDK конкретного поставщика |
| Регистрация адаптера | Запись адаптера с объявленными возможностями и версией manifest | Код адаптера или учётные данные поставщика |
| Вызов интеграции | Единичный вызов внешней зависимости через provider port с нормализованным результатом | Конфигурация адаптера или сырой ответ SDK поставщика |

В коде, API и событиях используются термины контекста-владельца. При пересечении границы получатель переводит published language в собственную модель и не импортирует внутренние Go-типы поставщика.

## Стратегическая модель

### Bounded contexts

| Context | Класс | Назначение | Авторитетное состояние |
|---|---|---|---|
| `platform-governance` | generic | Тенанты, пользователи, роли, аудит и правила эволюции | Тенант, Пользователь и роль, Запись аудита |
| `data-governance` | generic | Зоны, полномочия обработки и подавление контактов | Основание обработки, Запись стоп-листа |
| `lead-management` | core | Нормализация, обогащение и проверка компаний и контактов | Компания, Контакт, Происхождение атрибута, Верификация адреса, Профиль размера компании |
| `audience` | core | Скоринг, сегментация и фиксация аудитории | Скоринг лида, Сегмент, Снимок аудитории |
| `content-production` | supporting | Версионирование и рендеринг материалов | Контент-пакет, Версия контента, Наполнение лендинга, Документ, Задание рендеринга |
| `outreach-orchestration` | core | Кампании, цепочки, human approval и планирование | Кампания, Сценарий цепочки, Выполнение цепочки, Черновик действия, Решение оператора, Звонок |
| `message-delivery` | core | Исполнение утверждённых email-действий и deliverability | Сообщение, Репутация отправителя |
| `engagement-observation` | supporting | Квалификация сигналов доставки, переходов и ответов | Событие взаимодействия |
| `sales-workflow` | core | Сделки, CRM-синхронизация и атрибуция результата | Сделка, Синхронизация с CRM |
| `community-intelligence` | supporting | Разрешённое чтение сообществ и обезличенные выжимки | Выжимка сообщества |
| `publishing` | supporting | Публикация утверждённых материалов | Публикация |
| `analytics` | supporting | Обезличенные read-модели и отчётность | Снимок отчёта |
| `billing` | supporting | Подписки, лимиты, счета и success fee | Подписка и счёт, Вознаграждение за результат |
| `integration-platform` | generic | Vendor-neutral порты, ACL и manifest возможностей | Регистрация адаптера, Вызов интеграции |

Core-контексты выражают конкурентное преимущество продукта. Supporting-контексты реализуются ровно настолько глубоко, насколько требуется core-потокам. Generic-контексты не должны поглощать бизнес-решения core-контекстов.

### Capability matrix

| Capability | Владелец | Исполняющие процессы | Основной результат |
|---|---|---|---|
| `domain-architecture` | `platform-governance` | api, worker | Правила границ и аудируемая эволюция |
| `data-handling` | `data-governance` | api, worker, sender, chains | `data-zone-policy` |
| `lead-pool-client-side` | `data-governance` | api, worker, sender, chains | `processing-authority`, `suppression-policy` |
| `osint-enrichment` | `lead-management` | api, worker | Обогащённый `lead-profile` |
| `company-size-assessment` | `lead-management` | api, worker | `company-profile` |
| `email-verification` | `lead-management` | api, worker | `lead-verified` |
| `lead-segmentation` | `audience` | api, worker, schedule | `audience-snapshot` |
| `linguistics` | `content-production` | api, worker | `content-version` |
| `presentation-generator` | `content-production` | api, worker | `rendered-artifact` |
| `landing-page-generator` | `content-production` | api, worker | `rendered-artifact` |
| `outreach-approval` | `outreach-orchestration` | api, worker, chains, schedule | `approved-action` |
| `outreach-sequences` | `outreach-orchestration` | api, worker, chains, schedule | `campaign-command`, `outreach-outcome` |
| `call-orchestration` | `outreach-orchestration` | api, worker, chains, schedule | `call-scheduled` |
| `outreach-channel-form` | `outreach-orchestration` | api, worker, chains, schedule | Утверждённое form-действие |
| `outreach-channel-email` | `message-delivery` | sender, worker, schedule | `delivery-event` |
| `domain-deliverability` | `message-delivery` | sender, worker, schedule | `deliverability-policy` |
| `read-tracking` | `engagement-observation` | ingest, worker | `engagement-event`, `read-signal` |
| `crm-workflow` | `sales-workflow` | api, ingest, worker | `deal-state`, `attributed-result` |
| `telegram-community-digest` | `community-intelligence` | worker, schedule | `community-digest` |
| `digest-publishing` | `publishing` | api, worker, schedule | `publication-result` |
| `campaign-reporting` | `analytics` | api, worker, schedule | `campaign-report` |
| `billing-subscription` | `billing` | api, worker | `billing-entitlement` |
| `billing-success-fee` | `billing` | api, worker | `billing-result` |
| `provider-agnostic-integrations` | `integration-platform` | api, ingest, sender, worker, chains, schedule | `provider-port`, `adapter-capability`, `integration-error` |

## Тактическая модель

Ниже **AR** означает aggregate root, **E** — entity, **VO** — value object. Только aggregate root принимает команды извне агрегата. Ссылки на другие агрегаты и контексты хранятся как идентификаторы или снимки опубликованного контракта, а не как указатели на чужие объекты. Раздел является представлением [`architecture/domain-catalog.yaml`](../architecture/domain-catalog.yaml): состав агрегатов, сущностей, value objects, инвариантов, событий и политик приводится строго по каталогу.

### `platform-governance`

- **`tenant-access`** (AR: `tenant`): E: `tenant`, `user-role`, `audit-record`; VO: `tenant-id`, `role-set`.
  - Инварианты: роль принадлежит ровно одному тенанту; отключённый тенант не выдаёт новых решений доступа.
  - События: `tenant-access-changed`, `audit-recorded`.
  - Политики: `tenant-isolation`, `least-privilege`.

### `data-governance`

- **`processing-governance`** (AR: `processing-basis`): E: `processing-basis`, `suppression-entry`; VO: `processing-purpose`, `retention-period`.
  - Инварианты: полномочие действительно только для указанной цели, категории, тенанта и периода; активная запись стоп-листа имеет приоритет над кампанией, решением оператора и поставщиком.
  - События: `processing-authority-changed`, `contact-suppressed`.
  - Политики: `data-zone-policy`, `suppression-policy`.

### `lead-management`

- **`lead-profile`** (AR: `company`): E: `company`, `company-size-profile`; VO: `company-identity`, `confidence-score`.
  - Инварианты: нормализованный идентификатор компании не меняется молча; профиль размера компании согласован с актуальными атрибутами компании.
  - События: `lead-profile-enriched`.
  - Политики: —.
- **`contact`** (AR: `contact`): E: `contact`, `attribute-provenance`, `email-verification`; VO: `contact-address`.
  - Инварианты: каждое внешнее значение атрибута контакта имеет происхождение; результат проверки адреса имеет время и срок актуальности.
  - События: `lead-verified`.
  - Политики: `attribute-provenance-required`, `verification-freshness`.

### `audience`

- **`audience-segment`** (AR: `segment`): E: `segment`, `lead-score`; VO: `segment-criteria`, `score-value`.
  - Инварианты: скоринг воспроизводим по версии модели.
  - События: `segment-membership-changed`.
  - Политики: `segment-eligibility`.
- **`audience-snapshot`** (AR: `audience-snapshot`): E: `audience-snapshot`; VO: `snapshot-criteria`.
  - Инварианты: после фиксации снимок неизменяем; запущенная кампания ссылается на снимок, а не на живой сегмент.
  - События: `audience-snapshot-created`.
  - Политики: —.

### `content-production`

- **`content-package`** (AR: `content-asset`): E: `content-asset`, `content-version`, `landing-content`, `document`; VO: `content-locale`.
  - Инварианты: опубликованная версия не редактируется; новый текст создаёт новую версию, а не изменяет существующую.
  - События: `content-version-created`.
  - Политики: `immutable-content-version`.
- **`render-job`** (AR: `render-job`): E: `render-job`; VO: `render-format`.
  - Инварианты: рендер указывает точную версию контента и шаблон.
  - События: `content-rendered`.
  - Политики: —.

### `outreach-orchestration`

- **`campaign`** (AR: `campaign`): E: `campaign`, `sequence-definition`, `sequence-execution`, `action-draft`, `operator-decision`; VO: `campaign-window`, `sequence-step`, `action-channel`.
  - Инварианты: действие нельзя исполнить до положительного решения оператора; решение относится к точной версии черновика; изменённый черновик требует нового решения; завершённое выполнение цепочки не возобновляется.
  - События: `action-approved`, `campaign-state-changed`, `outreach-outcome-recorded`.
  - Политики: `operator-approval-required`, `outreach-scheduling`.
- **`call`** (AR: `call`): E: `call`; VO: `call-window`.
  - Инварианты: звонок планируется только внутри разрешённого временного окна.
  - События: `call-scheduled`.
  - Политики: —.

### `message-delivery`

- **`message-delivery`** (AR: `message`): E: `message`; VO: `delivery-address`, `delivery-attempt`.
  - Инварианты: сообщение создаётся только для утверждённого действия; повторная отправка с тем же idempotency key запрещена; активный стоп-лист блокирует попытку непосредственно перед доставкой.
  - События: `message-delivered`, `message-delivery-failed`.
  - Политики: `deliverability-policy`, `approved-action-only`.
- **`sender-reputation`** (AR: `sender-reputation`): E: `sender-reputation`; VO: —.
  - Инварианты: ограничения репутации домена вычисляются только по нормализованным ответам провайдера; активные ограничения приостанавливают новые отправки домена.
  - События: —.
  - Политики: —.

### `engagement-observation`

- **`engagement`** (AR: `engagement-event`): E: `engagement-event`; VO: `engagement-kind`, `signal-confidence`.
  - Инварианты: входящий webhook сначала дедуплицируется; сырой сигнал не равен квалифицированному событию.
  - События: `engagement-observed`.
  - Политики: `pixel-free-observation`.

### `sales-workflow`

- **`deal`** (AR: `deal`): E: `deal`; VO: `deal-stage`, `attribution-proof`.
  - Инварианты: переходы этапов следуют согласованному workflow; подтверждённый результат имеет достаточное доказательство и идемпотентную внешнюю ссылку.
  - События: `deal-state-changed`, `result-attributed`.
  - Политики: `confirmed-attribution`.
- **`crm-sync`** (AR: `crm-sync`): E: `crm-sync`; VO: `external-version`.
  - Инварианты: синхронизация хранит внешнюю версию и не перезаписывает локальное состояние при конфликте.
  - События: `crm-sync-conflict`.
  - Политики: `conflict-resolution`.

### `community-intelligence`

- **`community-digest`** (AR: `community-digest`): E: `community-digest`; VO: `community-source`, `digest-period`.
  - Инварианты: читаются только разрешённые сообщества; выжимка не содержит сырой архив и прямые идентификаторы участников.
  - События: `community-digest-created`.
  - Политики: `authorized-community-read`, `digest-anonymization`.

### `publishing`

- **`publication`** (AR: `publication`): E: `publication`; VO: `publication-target`, `publication-status`.
  - Инварианты: публикуется только утверждённая версия; повтор с тем же idempotency key не создаёт вторую публикацию.
  - События: `material-published`.
  - Политики: `approved-publication-only`.

### `analytics`

- **`campaign-report`** (AR: `report-snapshot`): E: `report-snapshot`; VO: `report-period`, `funnel-metric`.
  - Инварианты: аналитика не является владельцем операционного состояния; красные данные не копируются в read-модель без агрегации или обезличивания; снимок воспроизводим по watermark и версии формул.
  - События: `report-snapshot-created`.
  - Политики: `anonymized-reporting`.

### `billing`

- **`subscription-billing`** (AR: `subscription-invoice`): E: `subscription-invoice`; VO: `billing-period`, `money`, `tariff-limit`.
  - Инварианты: запись об использовании идемпотентна по ключу периода; счёт выставляется в пределах активной подписки и её лимитов.
  - События: `invoice-issued`, `billing-result-recorded`.
  - Политики: `subscription-entitlement`.
- **`success-fee`** (AR: `success-fee`): E: `success-fee`; VO: `calculation-version`.
  - Инварианты: вознаграждение создаётся только из подтверждённого результата; расчёт хранит версию применённого правила.
  - События: `success-fee-calculated`.
  - Политики: `success-fee-eligibility`.

### `integration-platform`

- **`adapter-registration`** (AR: `adapter-registration`): E: `adapter-registration`; VO: `manifest-version`.
  - Инварианты: возможности адаптера проверяются до вызова; секреты не входят в доменные события и ошибки.
  - События: `adapter-registered`.
  - Политики: `adapter-selection`.
- **`integration-invocation`** (AR: `integration-invocation`): E: `integration-invocation`; VO: `correlation-ref`.
  - Инварианты: вызов идёт через provider port, а не через SDK поставщика; ответы и ошибки поставщика нормализуются до доменных значений.
  - События: `integration-failed`, `integration-recovered`.
  - Политики: `error-normalization`.

## Межконтекстные отношения и контракты

`published-language` означает стабильный версионированный контракт владельца. `customer-supplier` означает, что upstream согласует контракт с потребностью downstream. `anticorruption-layer` означает обязательный перевод модели внешней системы в язык потребляющего контекста.

- `data-governance` публикует `data-zone-policy`, `processing-authority` и `suppression-policy`; lead, outreach и delivery применяют решения, но не воспроизводят правила локально.
- `lead-management` предоставляет `lead-profile` для `audience`; аудитория хранит ссылку/снимок необходимых атрибутов, а не редактирует лид.
- `audience` предоставляет `audience-snapshot`, а `content-production` — `content-version`; оба являются входами кампании.
- `outreach-orchestration` публикует `approved-action` как command intent для delivery и `outreach-outcome` для sales.
- `message-delivery` публикует `delivery-event`; observation квалифицирует его, analytics проецирует его.
- `engagement-observation` возвращает `engagement-event` в orchestration и analytics.
- `sales-workflow` публикует `attributed-result` для billing и `deal-state` для analytics.
- `community-intelligence` передаёт `community-digest` в publishing по customer-supplier контракту.
- `platform-governance` публикует `access-decision`; потребители не реализуют собственную RBAC-семантику.
- `integration-platform` предоставляет `provider-port` через ACL для lead, content, delivery, sales, community и publishing. Он не владеет их workflow.

Контракты имеют semantic version. Добавление опционального поля совместимо; изменение смысла, обязательности или типа требует новой major-версии и периода параллельного чтения. Command принимается идемпотентно; event неизменяем и содержит `event_id`, `occurred_at`, `tenant_id`, `correlation_id`, `causation_id`, имя и версию схемы.

## Владение и зоны данных

1. У каждой сущности ровно один context-владелец, указанный в каталоге. Только он изменяет её авторитетное состояние.
2. Другой контекст хранит лишь идентификатор, контрактный снимок или собственную проекцию. Проекция помечается источником, версией и временем; она не даёт права обратной записи.
3. `red` хранится в client-controlled relational storage; вынос в shared storage, логи, события или аналитику запрещён без отдельного правила и минимизации.
4. `mixed` разделяется по атрибутам: green-часть допустима в platform storage, red-часть подчиняется client-side правилам.
5. `analytics` содержит обезличенные/агрегированные производные и не используется для восстановления operational state.
6. `external` означает, что внешняя система остаётся system of record; локально хранится ссылка и состояние синхронизации.
7. `green` не отменяет tenant isolation, retention и audit.
8. Стоп-лист проверяется в `data-governance`; delivery выполняет preflight-проверку непосредственно перед необратимым действием.

## Политики сквозных процессов

- **Human-in-the-loop:** ни письмо, ни form submit, ни публикация не исполняются на основании одного результата генерации. Решение оператора связано с точной версией действия.
- **Privacy by boundary:** red-данные не появляются в публичных событиях. Межконтекстный контракт передаёт минимальный идентификатор или разрешённый снимок.
- **Idempotency:** команды внешнего эффекта имеют стабильный idempotency key; обработчики событий дедуплицируют `event_id`.
- **Transactional outbox:** изменение агрегата и запись его события атомарны. Публикация события выполняется после commit с повторной доставкой.
- **Eventual consistency:** проекции и downstream-процессы могут отставать; интерфейсы показывают freshness/watermark там, где это влияет на решение.
- **No distributed aggregate:** транзакция не охватывает два bounded contexts. Длинные процессы моделируются saga/process manager в контексте, который владеет целью процесса.
- **Auditability:** операторские решения, смена основания обработки, suppression и финансовые расчёты сохраняют actor, time, reason и evidence.

## Реализация на Go

Целевая структура — отдельное дерево на контекст, например:

```text
internal/contexts/<context-id>/
  domain/     # агрегаты, VO, domain events, чистые политики
  app/        # commands, queries, use cases, process managers
  ports/      # потребляемые и предоставляемые интерфейсы контекста
  adapters/   # repositories, outbox, provider adapters, HTTP/gRPC/event handlers и DTO
internal/platform/      # общие технические примитивы без доменных правил
internal/composition/   # сборка контекстов в приложение и процессы
cmd/<process>/          # точка входа процесса из `deployed_by`
```

`<context-id>` — точный kebab-case идентификатор контекста из каталога; соответствие пакета контексту фиксируется в README пакета. Внутренние типы одного контекста не импортируются другим. Интеграция идёт через contract DTO, сгенерированные типы схем или application ports. Каталог описывает доменное владение, а `deployed_by` — процессы размещения; один процесс может собрать несколько контекстов, не объединяя их модели.

Aggregate root инкапсулирует поля, проверяет инварианты в конструкторах и методах и возвращает domain events. Value object неизменяем и валидируется при создании. Repository существует для aggregate root, а не для каждой таблицы. Application service задаёт транзакцию и авторизацию, но не содержит доменных правил. `adapters` и `ports` не импортируются из `domain`.

## Правила эволюции

1. Любой change, затрагивающий домен, называет owner context, capability, aggregate root, инварианты, данные/зону и затронутые контракты.
2. Новый context допускается только если различаются язык, модель, темп изменений или ownership. «Чтобы разложить код» недостаточно.
3. Новая сущность сначала получает owner context, aggregate root и zone; только затем проектируются таблицы/API.
4. Новый межконтекстный вызов добавляется в catalog relationship с направлением, pattern, integration и contract.
5. Изменение published language содержит план совместимости, миграции потребителей и удаления старой версии.
6. Перенос ownership выполняется фазами: новый контракт, backfill, dual-read/controlled dual-write при необходимости, переключение system of record, удаление старого пути. Не допускается скрытая совместная запись.
7. Изменение red/mixed/analytics зоны требует отдельного privacy review и плана удаления уже созданных копий.
8. Новая capability имеет ровно одного owner context; `deployed_by` не делает процесс владельцем capability.
9. Диаграммы являются проверяемыми представлениями каталога и обновляются в том же change.
10. Исключение из правил фиксируется ADR со сроком, владельцем долга и критерием удаления.

## Критерии ревью доменного change

- Язык change совпадает с ubiquitous language или явно расширяет его.
- Команда попадает в aggregate root и не обходит инварианты прямой записью в хранилище.
- Синхронная связь действительно нужна; для уведомления или проекции выбран event.
- Контракт не раскрывает внутреннюю таблицу, provider SDK или red-атрибуты.
- Определены идемпотентность, порядок, повторы, дедупликация и наблюдаемость.
- Указаны system of record, zone, retention и поведение при удалении.
- Go-пакеты соблюдают направление зависимостей `adapters -> app -> domain`.
- Catalog, схемы контрактов, тесты и обязательные диаграммы изменены атомарно.
