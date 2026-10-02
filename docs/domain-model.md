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
| Статус лида | Текущая стадия воронки лида и срок отложенности (snooze), после которого лид возвращается в активную воронку | Пользователь платформы или история переходов между стадиями |
| Исходящее сообщение | Неизменяемый журнал прямой отправки письма: получатель, шаблон, тело, исход доставки и причина отказа | Сообщение delivery-конвейера или переписка целиком |
| Заявка | Верхнеуровневый запрос клиента на лидогенерацию с оффером, статусом обработки и ссылками на артефакты фулфилмента | Задание поиска, кампания или сделка |
| Оффер | Описание того, что клиент предлагает и кому: исходный текст, из которого выводятся ICP и содержание обращений | Утверждённое действие, версия контента или сгенерированное письмо |

В коде, API и событиях используются термины контекста-владельца. При пересечении границы получатель переводит published language в собственную модель и не импортирует внутренние Go-типы поставщика.

## Стратегическая модель

### Bounded contexts

| Context | Класс | Назначение | Авторитетное состояние |
|---|---|---|---|
| `platform-governance` | generic | Тенанты, пользователи, роли, аудит и правила эволюции | Тенант, Пользователь и роль, Запись аудита |
| `data-governance` | generic | Зоны, полномочия обработки и подавление контактов | Основание обработки, Запись стоп-листа |
| `lead-management` | core | Нормализация, обогащение и проверка компаний и контактов | Компания, Контакт, Происхождение атрибута, Верификация адреса, Профиль размера компании, Исходящее сообщение |
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
| `request-fulfillment` | core | Жизненный цикл заявки клиента: оффер, статусная модель и связь с фулфилментом | Заявка, Оффер |
| `integration-platform` | generic | Vendor-neutral порты, ACL и manifest возможностей | Регистрация адаптера, Вызов интеграции |

Core-контексты выражают конкурентное преимущество продукта. Supporting-контексты реализуются ровно настолько глубоко, насколько требуется core-потокам. Generic-контексты не должны поглощать бизнес-решения core-контекстов.

### Bounded context, сервис развёртывания и BRD-движок

Эти понятия не смешиваются:

| Понятие | Что это | Не означает |
|---|---|---|
| Bounded context | граница модели и владения: язык, инварианты, сущности, контракты | единицу развёртывания |
| Сервис развёртывания | эксплуатационная единица: граница отказа, релиза и масштабирования; ровно один сервис на контекст, `service` = `id` контекста | владельца сущностей или модель |
| Роль запуска | режим работы сервиса: `api`, `ingest`, `sender`, `worker`, `chains`, `schedule` | единицу размещения или второго владельца модели |
| BRD-движок | функциональная возможность из BRD | доменную границу или единицу развёртывания |

Владение сущностью всегда у bounded context — не у сервиса и не у роли. Сервис собирает application services своего контекста; совмещённый первый деплой может собрать в одной группе несколько сервисов, но модель и владелец остаются за контекстами (`docs/system-map.md` §2.0, `docs/architecture.md` §8).

### Capability matrix

| Capability | Владелец | Сервис | Роли | Основной результат |
|---|---|---|---|---|
| `domain-architecture` | `platform-governance` | `platform-governance` | api, worker | Правила границ и аудируемая эволюция |
| `platform-access` | `platform-governance` | `platform-governance` | api, worker | `access-decision`, `audit-recorded`, `tenant-context` |
| `service-topology` | `platform-governance` | `platform-governance` | api, worker | Сервисная топология: один сервис на контекст, допустимый транспорт контрактов и надёжная синхронная граница |
| `service-topology-console` | `platform-governance` | `platform-governance` | api | Эксплуатационная проекция топологии: граф сервисов и инфраструктуры, состояние узлов и метрики |
| `deployment-topology` | `platform-governance` | `platform-governance` | api, worker | Модульный монолит, группы процессов и декларативная инфраструктура |
| `observability` | `platform-governance` | `platform-governance` | api, worker | Наблюдаемость платформы: структурированные логи с корреляцией, RED-метрики и спаны на границах запроса и задания (технический примитив без доменных терминов) |
| `client-portal` | `platform-governance` | `platform-governance` | api, worker | Самообслуживание клиента: регистрация тенанта с описанием компании, подача заявки с оффером и единое окно портала |
| `data-handling` | `data-governance` | `data-governance` | api, worker, chains | `data-zone-policy` |
| `contact-suppression` | `data-governance` | `data-governance` | api, worker, chains | `suppression-policy` |
| `lead-pool-client-side` | `data-governance` | `data-governance` | api, worker, chains | `processing-authority` |
| `osint-enrichment` | `lead-management` | `lead-management` | api, worker | Обогащённый `lead-profile` |
| `company-size-assessment` | `lead-management` | `lead-management` | api, worker | `company-profile` |
| `email-verification` | `lead-management` | `lead-management` | api, worker | `lead-verified` |
| `lead-workflow` | `lead-management` | `lead-management` | api, worker | Статусная воронка лида (`not warmed`/`email sent`/`warming`/`cold`), snooze и перенос между стадиями |
| `lead-email` | `lead-management` | `lead-management` | api, worker | Прямая отправка писем из карточки и сегмента, шаблоны с merge-полями, лог доставки и история переписки |
| `email-template-library` | `lead-management` | `lead-management` | api, worker | Библиотека переиспользуемых писем: создание, редактирование, поиск и выбор шаблона, каталог объявленных переменных, запрет необъявленного плейсхолдера, подстановка значений с fallback и предпросмотр собранного письма |
| `search-strategy` | `lead-management` | `lead-management` | api, worker | Диалоговый подбор стратегии поиска с помощью LLM: уточняющие вопросы агента, согласованный план обхода (цели и лимиты) и запуск только по явному подтверждению оператора |
| `company-quota-search` | `lead-management` | `lead-management` | api, worker | Поиск до заданного оператором целевого числа уникальных компаний с добором целей обхода и предсказуемыми условиями остановки |
| `forum-lead-signals` | `lead-management` | `lead-management` | api, worker | Обезличенные сигналы с форумов (темы, боли, обсуждения) как источник выбора ниш |
| `lead-relevance` | `lead-management` | `lead-management` | api, worker | Оценка компании относительно конкретного задания: вердикт («Подходит»/«Гипотеза»/«Не подходит»), тип сигнала и свидетельства со ссылками на источники |
| `search-job-control` | `lead-management` | `lead-management` | api, worker | Отмена задания поиска из консоли (в очереди и во время выполнения) с сохранением уже обойдённых результатов |
| `search-job-lifecycle` | `lead-management` | `lead-management` | api, worker | Гарантии жизненного цикла задания поиска: легальность и неизменяемость переходов статуса, единственность взятия из очереди, согласование осиротевших заданий |
| `search-job-progress` | `lead-management` | `lead-management` | api, worker | Наблюдаемый прогресс задания поиска: фаза, адрес, обойдённые страницы, найденные компании и кредиты |
| `search-job-error-codes` | `lead-management` | `lead-management` | api, worker | Стабильные машинные коды отказа задания поиска с бизнес-формулировкой для оператора |
| `search-job-events` | `lead-management` | `lead-management` | api, worker | Персистентный журнал событий жизненного цикла задания поиска |
| `job-journal-console` | `lead-management` | `lead-management` | api | Эксплуатационная проекция журнала заданий поиска: компактные строки, раскрытие со сводкой и экран подробностей с ходом выполнения |
| `lead-cards-console` | `lead-management` | `lead-management` | api | Эксплуатационная проекция витрины карточек: плитки и строки, краткое описание, email и подсветка компании без email |
| `console-appearance` | `lead-management` | `lead-management` | api | Оформление и тема консоли: выбор светлой/тёмной/системной темы с сохранением и переключателем, осмысленная палитра статусов |
| `semantic-lead-search` | `lead-management` | `lead-management` | api, worker | Векторный recall и rich-описание: эмбеддинг компании и оффера заявки, поиск кандидатов по близости (без вердикта релевантности), rich-описание из составляющих с источниками |
| `lead-segmentation` | `audience` | `audience` | api, worker, schedule | `audience-snapshot` |
| `linguistics` | `content-production` | `content-production` | api, worker | `content-version` |
| `presentation-generator` | `content-production` | `content-production` | api, worker | `rendered-artifact` |
| `landing-page-generator` | `content-production` | `content-production` | api, worker | `rendered-artifact` |
| `outreach-approval` | `outreach-orchestration` | `outreach-orchestration` | api, worker, chains, schedule | `approved-action` |
| `outreach-sequences` | `outreach-orchestration` | `outreach-orchestration` | api, worker, chains, schedule | `campaign-command`, `outreach-outcome` |
| `call-orchestration` | `outreach-orchestration` | `outreach-orchestration` | api, worker, chains, schedule | `call-scheduled` |
| `outreach-channel-form` | `outreach-orchestration` | `outreach-orchestration` | api, worker, chains, schedule | Утверждённое form-действие |
| `outreach-channel-email` | `message-delivery` | `message-delivery` | sender, worker, schedule | `delivery-event` |
| `domain-deliverability` | `message-delivery` | `message-delivery` | sender, worker, schedule | `deliverability-policy` |
| `read-tracking` | `engagement-observation` | `engagement-observation` | ingest, worker | `engagement-event`, `read-signal` |
| `crm-workflow` | `sales-workflow` | `sales-workflow` | api, ingest, worker | `deal-state`, `attributed-result` |
| `telegram-community-digest` | `community-intelligence` | `community-intelligence` | worker, schedule | `community-digest` |
| `digest-publishing` | `publishing` | `publishing` | api, worker, schedule | `publication-result` |
| `campaign-reporting` | `analytics` | `analytics` | api, worker, schedule | `campaign-report` |
| `client-dashboard` | `analytics` | `analytics` | api, worker, schedule | Дашборд метрик клиента: real-time проекция счётчиков прогресса и операций |
| `billing-subscription` | `billing` | `billing` | api, worker | `billing-entitlement` |
| `billing-success-fee` | `billing` | `billing` | api, worker | `billing-result` |
| `client-request` | `request-fulfillment` | `request-fulfillment` | api, worker | `request-status`: жизненный цикл заявки, оффер и ссылки на артефакты фулфилмента |
| `request-validation` | `request-fulfillment` | `request-fulfillment` | api, worker | Валидация оффера админом: взятие в работу, звонок-уточнение через `call-intent`, правки оффера до `validated`, одобрение или отклонение с причиной; решение живёт в агрегате `request` |
| `provider-agnostic-integrations` | `integration-platform` | `integration-platform` | api, ingest, sender, worker, chains, schedule | `provider-port`, `adapter-capability`, `integration-error` |
| `firecrawl-credit-budget` | `integration-platform` | `integration-platform` | api, ingest, sender, worker, chains, schedule | Бюджет кредитов Firecrawl на задачу: атомарное резервирование, подтверждённый остаток аккаунта, лимиты частоты/параллелизма/размера обхода |
| `crawler-proxy-pool` | `integration-platform` | `integration-platform` | api, ingest, sender, worker, chains, schedule | Пул прокси для обхода: источники (статический список, провайдер), уровни стоимости, ротация и привязка к домену, здоровье и карантин, эскалация на премиум при блокировке |

**`service-topology-console` не добавляет модель.** Консоль — эксплуатационная проекция роли `api` сервиса `platform-governance`: она строит граф из объявлений каталога и `deploy/services.yaml` и читает метрики Prometheus через серверный прокси. Консоль **не владеет сущностями и не является bounded context**: она не вводит агрегатов, value objects, domain events, policies и межконтекстных контрактов, а её состояние узлов (`up`/`degraded`/`down`/`unknown`) и раскладка — результат наблюдения и состояние интерфейса, а не агрегат или событие. Узлы-сервисы — те же 15 bounded contexts, рёбра — те же 38 связей каталога; состав контекстов и связей не меняется, capability лишь назначена существующему владельцу как уточнение покрытия. Владение моделью остаётся у контекстов: владелец capability не получает ни новых сущностей, ни нового языка.

**`client-portal` не добавляет модель.** Capability принадлежит `platform-governance` и описывает самообслуживание клиента — эксплуатационную проекцию роли `api` по образцу `service-topology-console`. Она **не владеет сущностями и не является bounded context**: не вводит агрегатов, value objects, domain events, policies и межконтекстных контрактов. Регистрация использует модель `platform-access` (агрегат `tenant-access`), подача заявки идёт опубликованным контрактом `request-intent` владельца `request-fulfillment`, а маскирование выполняется на сервере: клиент получает только агрегаты и состояние заявки (идентификатор, статус), без сырых лидов, карточек компаний, контактов и процесса краулинга. Владение моделью остаётся у контекстов.

**`job-journal-console` и `lead-cards-console` не добавляют модель.** Эти capability принадлежат `lead-management` и описывают эксплуатационные проекции роли `api` — консольные экраны журнала заданий и витрины карточек. Они **не владеют сущностями и не являются bounded context**: не вводят агрегатов, value objects, domain events, policies и межконтекстных контрактов. Журнал показывает уже существующую операционную запись задания (`search_job`, `search_job_event`), витрина — уже существующие агрегаты `lead-profile` и `contact`; источники данных и их владельцы не меняются. Capability лишь назначена существующему контексту как уточнение покрытия, владение моделью остаётся у контекстов.

**`console-appearance` не добавляет модель.** Capability принадлежит `lead-management` и описывает оформление и тему консоли роли `api` (светлая/тёмная/системная тема, переключатель, палитра статусов). Она **не владеет сущностями и не является bounded context**: не вводит агрегатов, value objects, domain events, policies и межконтекстных контрактов; цвет и тема — проекция представления над уже существующими данными, владение моделью остаётся у контекстов.

**`search-job-control`, `search-job-lifecycle`, `search-job-progress`, `search-job-error-codes` и `search-job-events` не добавляют модель.** Эти capability принадлежат `lead-management` и описывают управление уже существующей операционной записью — заданием поиска (таблицы `search_job` и `search_job_event`), а не доменным агрегатом. Задание не является сущностью каталога: отмена, переходы состояний, прогресс, коды ошибок и журнал событий не вводят агрегатов, value objects, domain events, policies и межконтекстных контрактов. Роли `api` и `worker` одного сервиса `lead-management` лишь разделяют обязанности: `api` фиксирует запрос отмены и показывает прогресс, `worker` исполняет задание, ведёт переходы состояния и публикует прогресс; владельцем capability остаётся контекст, а не роль.

**`company-quota-search` не добавляет модель.** Capability принадлежит `lead-management` и описывает поведение задания поиска, а не доменный агрегат: целевое число компаний, добор целей обхода и причины остановки — свойства операционной записи задания (`search_job`, `search_job_event`). Capability не вводит агрегатов, value objects, domain events, policies и межконтекстных контрактов; владение моделью по-прежнему у контекстов `lead-management` и `integration-platform`, а само задание остаётся операционным примитивом, а не сущностью каталога.

**`crawler-proxy-pool` не добавляет модель.** Capability принадлежит `integration-platform` и описывает адаптерный механизм обхода, а не доменный агрегат: пул прокси, уровни стоимости, ротация, привязка к домену, здоровье и карантин — политика адаптера за портом `provider-port`. Capability не вводит агрегатов, value objects, domain events, policies и межконтекстных контрактов; владение моделью остаётся у `integration-platform`.

**`client-dashboard` не добавляет модель.** Capability принадлежит `analytics` и описывает real-time read-модель (проекцию) дашборда клиента — эксплуатационную витрину роли `api` по образцу `job-journal-console`. Она **не владеет сущностями и не является агрегатом**: не вводит агрегатов, value objects, domain events, policies и межконтекстных контрактов, а лишь проецирует нормализованные доменные события (`lead-workflow`, `engagement-observed`, `message-sent`, `delivery-event`) в счётчики в памяти процесса. `campaign-reporting` (снимки) не меняется: это второй режим того же контекста, снимки остаются источником исторической отчётности. Маскирование — в контуре клиента: отдаются только агрегаты и счётчики, текст ответов компаний не хранится и не передаётся (доступен лишь факт «ответили: N»), соблюдается порог минимальной группы (срез из одной записи не показывается), метрик открытий нет. Владение моделью остаётся у контекстов.

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

- **`lead-profile`** (AR: `company`): E: `company`, `company-size-profile`; VO: `company-identity`, `confidence-score`, `lead-status`.
  - Инварианты: нормализованный идентификатор компании не меняется молча; профиль размера компании согласован с актуальными атрибутами компании.
  - События: `lead-profile-enriched`.
  - Политики: —.
  - Атрибуты (capability `semantic-lead-search`): `embedding` — векторное представление компании вместе с watermark исходных данных; при изменении данных watermark меняется и вектор пересчитывается. `rich-description` — набор составляющих (продукты, приложения, трафик, сигналы, размер), каждая с источником; составляющая без источника не принимается. Эмбеддинг и rich-описание — атрибуты существующего агрегата, новых агрегатов, VO-идентификаторов и событий не вводят. Близость векторов — recall; вердикт «Подходит/Гипотеза/Не подходит» остаётся за `lead-relevance`.
- **`contact`** (AR: `contact`): E: `contact`, `attribute-provenance`, `email-verification`; VO: `contact-address`.
  - Инварианты: каждое внешнее значение атрибута контакта имеет происхождение; результат проверки адреса имеет время и срок актуальности.
  - События: `lead-verified`.
  - Политики: `attribute-provenance-required`, `verification-freshness`.
- **`outbound-message`** (AR: `outbound-message`): E: `outbound-message`; VO: `message-status`.
  - Инварианты: получатель обязателен; отправка на подавленный адрес запрещена; лог неизменяем после фиксации исхода.
  - События: `message-sent`.
  - Политики: —.
- **`email-template`** (AR: `email-template`): E: `email-template`; VO: `merge-variable`, `template-reference`.
  - Инварианты: имя, тема и тело непусты после обрезки пробелов; объявленные переменные — подмножество каталога переменных; каждая объявленная переменная имеет непустое fallback-значение; каждый плейсхолдер темы и тела объявлен; объявленная, но неиспользованная переменная допустима.
  - События: — (потребителя нет; аудит действий оператора — `audit-recorded` на границе API).
  - Политики: —.
  - Примечание: агрегат переиспользуемого письма редактируется на месте с ростом `revision`; ссылка `template_id@revision` и снимок собранного тела фиксируются в `outbound-message`, поэтому правка шаблона не переписывает прошлые отправки. Библиотека — написанные человеком письма, владелец `lead-management`; сгенерированные и отрендеренные версии контента остаются за `content-production` (граница не затрагивается).
- **`lead-assessment`** (AR: `lead-assessment`): E: `lead-assessment`; VO: `assessment-evidence`, `signal-kind`.
  - Инварианты: оценка привязана к паре (job, company) и не переносится между заданиями; наблюдаемый факт отделён от интерпретации и от неподтверждённого намерения купить; «Не подходит» требует установленного несоответствия; «Гипотеза» требует указания недостающего свидетельства.
  - События: `lead-assessed`, `lead-assessment-revised`.
  - Политики: —.
  - Примечание: оценка — не постоянное свойство компании, а вывод в контексте конкретного задания поиска; свидетельства несут критерий из интерпретации запроса, факт, URL источника и обоснование связи. Наблюдаемый факт, интерпретация и неподтверждённое намерение купить — раздельные поля, поэтому ссылка подтверждает факт, а не существование компании. Значения вердикта и типа сигнала взяты из интерпретации запроса (`search-strategy`) и не смешиваются с техническим планом обхода.

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

### `request-fulfillment`

- **`request`** (AR: `request`): E: `request`, `offer`; VO: `request-status`, `offer-content`, `request-target`, `validation-decision`.
  - Инварианты: заявка без непустого оффера не создаётся; заявка привязана ровно к одному тенанту и не переносится между тенантами; переход в `processing` разрешён только из `validated`; до `validated` оффер правится на месте, а после `validated` не редактируется на месте — любое изменение создаёт новую версию; решение валидации (`approved`/`rejected`) фиксируется внутри агрегата с актором, временем и причиной; статусы `fulfilled`, `rejected`, `cancelled` терминальны; ссылки на чужие агрегаты (включая `call`) — только идентификаторы.
  - События: `request-submitted`, `request-validated`, `request-processing-started`, `request-fulfilled`, `request-rejected`, `request-cancelled`.
  - Политики: —.
  - Примечание: жизненный цикл — `submitted → validating → validated → processing → fulfilled`; терминальные — `rejected` и `cancelled`. `offer` версионируется (`revision`): прежняя версия остаётся неизменной. Фулфилмент-состояние ссылается на артефакты других контекстов (`audience-snapshot`, `campaign`, `deal`) по идентификаторам, а не по указателям. Workflow исполнения (движение по статусной машине) — application service этого контекста, а не отдельная граница (`temporal-execution-platform`, change №2). Валидация оффера админом (capability `request-validation`) — прикладной use case того же контекста: `submitted → validating` (взятие в работу), звонок-уточнение, правки оффера на месте, `validating → validated | rejected` с решением внутри агрегата; звонок принадлежит `outreach-orchestration` и вызывается командой `call-intent`, а заявка хранит его `call_id`. После валидации оффер получает эмбеддинг (контракт `vector`, capability `semantic-lead-search`) как вход recall; правка оффера сбрасывает устаревший вектор.

## Межконтекстные отношения и контракты

`published-language` означает стабильный версионированный контракт владельца. `customer-supplier` означает, что upstream согласует контракт с потребностью downstream. `anticorruption-layer` означает обязательный перевод модели внешней системы в язык потребляющего контекста.

- `data-governance` публикует `data-zone-policy`, `processing-authority` и `suppression-policy`; lead, audience, outreach и delivery применяют решения, но не воспроизводят правила локально.
- `lead-management` предоставляет `lead-profile` для `audience`; аудитория хранит ссылку/снимок необходимых атрибутов, а не редактирует лид. Тот же контекст предоставляет `company-profile` для `sales-workflow`: карточка сделки читает профиль компании, включая размер, не воспроизводя его локально.
- `audience` предоставляет `audience-snapshot`, а `content-production` — `content-version`; оба являются входами кампании.
- `outreach-orchestration` публикует `approved-action` как command intent для delivery и `outreach-outcome` для sales.
- `message-delivery` публикует `delivery-event`; observation квалифицирует его, analytics проецирует его, а `lead-management` по отказу доставки обновляет статус адреса в aggregate `contact` (цикл подавления).
- `engagement-observation` возвращает `engagement-event` в orchestration, audience (поведенческая сегментация потребляет сигналы взаимодействия) и analytics.
- `sales-workflow` публикует `attributed-result` для billing и `deal-state` для analytics.
- `community-intelligence` передаёт `community-digest` в publishing по customer-supplier контракту.
- `platform-governance` публикует `access-decision`; потребители не реализуют собственную RBAC-семантику.
- `integration-platform` предоставляет `provider-port` через ACL для lead, content, delivery, sales, community, publishing и outreach. Он не владеет их workflow. Прямая отправка `lead-email` идёт за портом `mail` (`contracts/integration-platform`) — это внешний адаптер за `provider-port`, а не новый межконтекстный контракт. Тот же контекст публикует контракт `vector` (векторное представление и поиск по близости) за provider-port: `lead-management` использует его для эмбеддингов компаний и recall, `request-fulfillment` — для эмбеддинга оффера заявки. Доменных правил хранилище не содержит.
- `request-fulfillment` — владелец заявки клиента; фулфилмент-состояние читает чужие агрегаты по `id` через синхронные `query`-контракты: `lead-profile` (`lead-management`), `audience-snapshot` (`audience`), `campaign-state` (`outreach-orchestration`), `deal-state` (`sales-workflow`). Право и лимиты приходят policy-контрактом `billing-entitlement` (`billing`), контекст тенанта — `tenant-context` (`platform-governance`). Заявка не владеет чужими агрегатами и не переносит их состояние: она хранит только ссылки. Звонок-уточнение валидации идёт командой `call-intent` в `outreach-orchestration` (владелец агрегата `call`); заявка хранит `call_id`, не владея звонком.

Контракты имеют semantic version. Добавление опционального поля совместимо; изменение смысла, обязательности или типа требует новой major-версии и периода параллельного чтения. Команда принимается идемпотентно; event неизменяем и содержит `event_id`, `occurred_at`, `tenant_id`, `correlation_id`, `causation_id`, имя и версию схемы. Транспорт определяется видом интеграции: `command`/`query` идут синхронно (`sync`/`http-json`), `event`/`policy` — асинхронно (`async`/`amqp`); `policy` доставляется как подписка на изменение правила.

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
backend/contexts/<context-id>/   # модуль контекста: internal/{domain,app,ports,adapters}
  internal/domain/     # агрегаты, VO, domain events, чистые политики
  internal/app/        # commands, queries, use cases, process managers
  internal/ports/      # потребляемые и предоставляемые интерфейсы контекста
  internal/adapters/   # repositories, outbox, provider adapters, HTTP/JSON- и AMQP-обработчики и DTO
contracts/<owner>/               # published language владельца (общий для backend и frontend)
backend/platform/                # общие технические примитивы без доменных правил:
                                 # composition, contracts, messaging, observability,
                                 # reliability, transport, deployment, migrate
backend/cmd/lead-gen/            # единая точка входа; сервис и его роли задаются конфигурацией
frontend/components/             # общий слой серверных UI-компонентов
frontend/public/                 # публичный edge
```

`<context-id>` — точный kebab-case идентификатор контекста из каталога; соответствие пакета контексту фиксируется в README пакета. Внутренние типы одного контекста не импортируются другим. Интеграция идёт через contract DTO, сгенерированные типы схем или application ports. Каталог описывает доменное владение, а `service` и `roles` — размещение: один сервис разворачивает ровно один контекст, роли внутри сервиса задают режим запуска. Сборка сервиса (composition root) — публичный корневой пакет `backend/contexts/<context-id>`, а не общий `backend/platform/composition`. Совмещённый первый деплой может собрать несколько сервисов в одной группе, не объединяя их модели.

Aggregate root инкапсулирует поля, проверяет инварианты в конструкторах и методах и возвращает domain events. Value object неизменяем и валидируется при создании. Repository существует для aggregate root, а не для каждой таблицы. Application service задаёт транзакцию и авторизацию, но не содержит доменных правил. `adapters` и `ports` не импортируются из `domain`.

## Правила эволюции

1. Любой change, затрагивающий домен, называет owner context, capability, aggregate root, инварианты, данные/зону и затронутые контракты.
2. Новый context допускается только если различаются язык, модель, темп изменений или ownership. «Чтобы разложить код» недостаточно.
3. Новая сущность сначала получает owner context, aggregate root и zone; только затем проектируются таблицы/API.
4. Новый межконтекстный вызов добавляется в catalog relationship с направлением, pattern, integration и contract.
5. Изменение published language содержит план совместимости, миграции потребителей и удаления старой версии.
6. Перенос ownership выполняется фазами: новый контракт, backfill, dual-read/controlled dual-write при необходимости, переключение system of record, удаление старого пути. Не допускается скрытая совместная запись.
7. Изменение red/mixed/analytics зоны требует отдельного privacy review и плана удаления уже созданных копий.
8. Новая capability имеет ровно одного owner context; `service` и `roles` не делают сервис или роль владельцем capability.
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
