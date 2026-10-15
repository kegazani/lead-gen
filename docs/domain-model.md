# Доменная модель платформы

## Назначение и статус

Этот документ задаёт целевую DDD-модель платформы и правила её реализации на Go. Машиночитаемым источником истины для состава bounded contexts, владения данными, межконтекстных контрактов и обязательных диаграмм служит [`architecture/domain-catalog.yaml`](../architecture/domain-catalog.yaml). Документ раскрывает смысл каталога, но не вводит параллельную таксономию.

Платформа строится по принципу **context first**: сначала определяется bounded context и его язык, затем агрегаты и use cases, и только после этого transport, persistence и адаптеры. Go-пакеты отражают эту границу. Общие технические библиотеки допустимы; общая «универсальная» доменная модель — нет.

## Ubiquitous language

| Термин | Точное значение | Не означает |
|---|---|---|
| Тенант | Изолированная область клиента, в которой действуют пользователи, роли, данные и настройки | Юридическое лицо вне платформы |
| Пользователь и роль | Назначение пользователя тенанта с набором ролей, определяющих его доступ | Учётная запись вне тенанта или контакт из базы лидов |
| Платформенный оператор | Субъект, заданный конфигурацией и действующий во всех активных тенантах (администратор платформы) | Роль тенанта или пользователь отдельного клиента |
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
| Снимок отчёта | Воспроизводимая аналитическая read-модель на момент времени | Авторитетное состояние исходного домена |
| Provider port | Стабильный интерфейс потребности домена во внешней системе | SDK конкретного поставщика |
| Регистрация адаптера | Запись адаптера с объявленными возможностями и версией manifest | Код адаптера или учётные данные поставщика |
| Вызов интеграции | Единичный вызов внешней зависимости через provider port с нормализованным результатом | Конфигурация адаптера или сырой ответ SDK поставщика |
| Статус лида | Текущая стадия воронки лида и срок отложенности (snooze), после которого лид возвращается в активную воронку | Пользователь платформы или история переходов между стадиями |
| Исходящее сообщение | Неизменяемый журнал прямой отправки письма: получатель, шаблон, тело, исход доставки и причина отказа | Сообщение delivery-конвейера или переписка целиком |
| Заявка | Верхнеуровневый запрос клиента на лидогенерацию с оффером, статусом обработки и ссылками на артефакты фулфилмента | Задание поиска, кампания или сделка |
| Оффер | Описание того, что клиент предлагает и кому: исходный текст, из которого выводятся ICP и содержание обращений | Утверждённое действие, версия контента или сгенерированное письмо |
| Execution intent | Неизменяемое принятое бизнес-намерение `request-fulfillment` с полным семантическим payload и квитанцией admission | Старт Temporal или гарантия успешного исполнения |
| Execution attempt | Поколение исполнения с `attempt_id`, `generation`, `command_id`, стабильным `workflow_id`, отдельным server-assigned `run_id` и собственными handoff/receipt данными | Идентификатор заявки или workflow как таковой |
| Target resolution | Append-only уточнение `LeadTarget` (`explicit`, `unspecified`, `unknownLegacy`) | Доказательство завершения Temporal execution |
| Verified runtime evidence | Результат проверки identity и полной Temporal history доверенным verifier-ом | Совпадение ID, actor, fingerprint или обычное observation |
| Агент компании | Долговечная единица обработки одной компании в контексте задания и тенанта, ведущая её по пайплайну | LLM, управляющий оркестрацией, или постоянное свойство компании |
| Пайплайн агента | Версионируемый упорядоченный набор блоков обработки | Код супервизора или конкретное выполнение |
| Блок пайплайна | Именованный элемент обработки из реестра (search, classify, enrich, …) | Шаг конкретного запуска |
| Запуск агента | Долговечное состояние прохождения компанией фиксированной версии пайплайна | Фоновая задача или cron job |
| Инсайт качества поиска | Обезличенный агрегат анализа исходов классификации с рекомендацией оператору | Сырое содержимое красной зоны или персональные данные |

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
| `outreach-orchestration` | core | Кампании, цепочки, campaign approval и планирование | Кампания, Сценарий цепочки, Выполнение цепочки, Черновик действия, Решение оператора, Звонок |
| `agent-orchestration` | core | Автономная обработка компании: супервизор, агент на компанию и пайплайн из блоков | Пайплайн агента, Запуск агента |
| `message-delivery` | core | Исполнение утверждённых email-действий и deliverability | Сообщение, Репутация отправителя |
| `engagement-observation` | supporting | Квалификация сигналов доставки, переходов и ответов | Событие взаимодействия |
| `analytics` | supporting | Обезличенные read-модели и отчётность | Снимок отчёта |
| `request-fulfillment` | core | Жизненный цикл заявки и durable execution intents/attempts с безопасным terminal gate | Заявка, Оффер, Execution intent, Execution attempt, Handoff manifest |
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

### Планируемое исследование и входящие обращения

Изменение [intelligent-lead-research](../openspec/changes/intelligent-lead-research/proposal.md) регистрирует **назначения primary owners**, а не реализованные функции. Runtime, текущий Firecrawl и demo не меняются; новые агрегаты/контракты ниже ещё не зарегистрированы как существующая модель. Полная [концепция](concepts/intelligent-lead-research/README.md), [предлагаемые агрегаты и события](concepts/intelligent-lead-research/07-architecture-and-delivery-plan.md) и [открытые задачи](../openspec/changes/intelligent-lead-research/tasks.md) определяют будущий apply.

| Запланированная capability | Primary owner | Предметная граница |
|---|---|---|
| `lead-entity-resolution` | `lead-management` | Юрлицо/бренд/сайт и типизированные доказательные связи; домен не универсальная юридическая идентичность |
| `lead-research-memory` | `lead-management` | Источники, временные утверждения, открытые потребности, сеть зависимостей и производная память в разрешённой области |
| `inbound-lead-conversations` | `lead-management` | Клиентские входящие обращения, связь с лидом/контактом и переписка; qualification — у `engagement-observation` |
| `adaptive-lead-research` | `agent-orchestration` | Миссия, допустимые действия LLM, долговечность и бюджет; факты принадлежат `lead-management` |
| `international-market-discovery` | `audience` | Рекомендации рынков по офферу; человеческий выбор стран заявки — у `request-fulfillment` |
| `regional-web-acquisition` | `integration-platform` | Search/fetch/registry/browser/document/OCR, capabilities и receipts без предметного вердикта |
| `research-to-outreach` | `outreach-orchestration` | Передача исследования в существующие материал, точное решение и доставку; владельцы текста/адреса не меняются |

В уточнении CL-14 `OpenNeed` и зависимости утверждений планируются у `lead-management`: компания без email сохраняется между запусками, поздний контакт дополняет прежнее досье. Статусы относятся к конкретному факту и методу проверки. Миссия и разрешение нового внешнего действия остаются у `agent-orchestration`; обновление знания не открывает бюджет и не переписывает завершённый run. Подробности — [сеть во времени](concepts/intelligent-lead-research/02-identity-and-evidence.md#213-сеть-отношений-и-зависимостей-во-времени).

Диаграмма bounded contexts помечает эти назначения как план. Добавление entity/aggregate/event/relationship при реализации требует синхронного обновления каталога, этой модели и затронутых диаграмм; ни тело входящего письма, ни контактные данные не становятся общей зелёной памятью платформы.

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
| `lead-relevance` | `lead-management` | `lead-management` | api, worker | Оценка компании относительно конкретного задания: вердикт («Подходит»/«Гипотеза»/«Не подходит»), тип сигнала и свидетельства со ссылками на источники; переоценка уже найденных компаний по критериям, выведенным заново из запроса, без повторного обхода |
| `search-job-control` | `lead-management` | `lead-management` | api, worker | Отмена задания поиска из консоли (в очереди и во время выполнения) с сохранением уже обойдённых результатов |
| `search-job-lifecycle` | `lead-management` | `lead-management` | api, worker | Гарантии жизненного цикла задания поиска: легальность и неизменяемость переходов статуса, единственность взятия из очереди, согласование осиротевших заданий |
| `search-job-progress` | `lead-management` | `lead-management` | api, worker | Наблюдаемый прогресс задания поиска: фаза, адрес, обойдённые страницы, найденные компании и кредиты |
| `search-job-error-codes` | `lead-management` | `lead-management` | api, worker | Стабильные машинные коды отказа задания поиска с бизнес-формулировкой для оператора |
| `search-job-events` | `lead-management` | `lead-management` | api, worker | Персистентный журнал событий жизненного цикла задания поиска |
| `job-journal-console` | `lead-management` | `lead-management` | api | Эксплуатационная проекция журнала заданий поиска: компактные строки, раскрытие со сводкой и экран подробностей с ходом выполнения |
| `lead-cards-console` | `lead-management` | `lead-management` | api | Эксплуатационная проекция витрины карточек: плитки и строки, краткое описание, email и подсветка компании без email; экран «Лиды» и выгрузка списка (автономный HTML, CSV с BOM) |
| `console-appearance` | `lead-management` | `lead-management` | api | Оформление и тема консоли: выбор светлой/тёмной/системной темы с сохранением и переключателем, осмысленная палитра статусов |
| `semantic-lead-search` | `lead-management` | `lead-management` | api, worker | Векторный recall и rich-описание: эмбеддинг компании и оффера заявки, поиск кандидатов по близости (без вердикта релевантности), rich-описание из составляющих с источниками |
| `lead-search` | `lead-management` | `lead-management` | api, worker | Опубликованный контракт поиска лидов под заявку: сначала существующий пул, при недоборе — ограниченный краулинг; происхождение лида, лимит и дедлайн, токен-матчинг критерия, дедуп по домену, отсутствие PII |
| `email-generation` | `lead-management` | `lead-management` | api, worker | Опубликованный контракт генерации персонализированного письма под лида и оффер через LLM: обезличенный промпт, разбор ответа, доменный отказ вместо выдуманного письма |
| `search-recommendation` | `lead-management` | `lead-management` | api | Применение рекомендации анализа как нового запуска поиска: структурное изменение (вид, критерий, цели обхода, лимит) ставится заданием `search_job`, идемпотентность по ключу, идентификатор задания как ссылка на применение |
| `lead-segmentation` | `audience` | `audience` | api, worker, schedule | `audience-snapshot` |
| `content-production` | `content-production` | `content-production` | api, worker | Создание immutable `content-version`, включая exact subject/body в режиме version-only без задания рендеринга |
| `linguistics` | `content-production` | `content-production` | api, worker | `content-version` |
| `presentation-generator` | `content-production` | `content-production` | api, worker | `rendered-artifact` |
| `landing-page-generator` | `content-production` | `content-production` | api, worker | `rendered-artifact` |
| `outreach-approval` | `outreach-orchestration` | `outreach-orchestration` | api, worker, chains, schedule | `approved-action` |
| `outreach-sequences` | `outreach-orchestration` | `outreach-orchestration` | api, worker, chains, schedule | `campaign-command`, `outreach-outcome` |
| `outreach-channel-form` | `outreach-orchestration` | `outreach-orchestration` | api, worker, chains, schedule | Утверждённое form-действие |
| `risk-screening` | `outreach-orchestration` | `outreach-orchestration` | api, worker, chains, schedule | Признаки риска черновика для пакетного утверждения (стоп-лист, верификация адреса); недоступность владельца — честный отказ |
| `outreach-handoff` | `outreach-orchestration` | `outreach-orchestration` | api, worker, chains, schedule | Приём передачи писем заявки: идемпотентное создание одной кампании на заявку с черновиками в очереди утверждения |
| `outreach-channel-email` | `message-delivery` | `message-delivery` | sender, worker, schedule | `delivery-event` |
| `domain-deliverability` | `message-delivery` | `message-delivery` | sender, worker, schedule | `deliverability-policy` |
| `delivery-bridge` | `message-delivery` | `message-delivery` | sender, worker, schedule | Мост `approved-action → delivery-command`: приём утверждённого действия, разрешение адреса через `delivery-target` (`lead-management`) и материала (тема, тело) через `content-version` (`content-production`), формирование команды доставки; идемпотентность и честная недоступность |
| `read-tracking` | `engagement-observation` | `engagement-observation` | ingest, worker | `engagement-event`, `read-signal` |
| `campaign-reporting` | `analytics` | `analytics` | api, worker | `campaign-report` |
| `client-dashboard` | `analytics` | `analytics` | api, worker | Дашборд метрик клиента: real-time проекция счётчиков прогресса и операций |
| `client-request` | `request-fulfillment` | `request-fulfillment` | api, worker | `request-status`: жизненный цикл заявки, оффер и ссылки на артефакты фулфилмента |
| `request-validation` | `request-fulfillment` | `request-fulfillment` | api, worker | Валидация оффера админом: взятие в работу, звонок-уточнение через `call-intent`, правки оффера до `validated`, одобрение или отклонение с причиной; решение живёт в агрегате `request` |
| `request-fulfillment-execution` | `request-fulfillment` | `request-fulfillment` | api, worker | Durable-исполнение заявки: идемпотентность повторной доставки переходов и повторного старта, классификация отказов activity, уникальный подсчёт лидов при доборе, таймауты и heartbeat длинных activities, ограниченная сроком пауза утверждения, наблюдаемость исхода «0 лидов», генерация черновиков писем (шаг и фаза `drafting`, хранилище `EmailDraftStore` и операторское чтение) |
| `request-shortfall` | `request-fulfillment` | `request-fulfillment` | api, worker | Расчёт наблюдаемого недобора (цель минус уникальные найденные) и авто-триггер генерации писем при недоборе; честный остаток без выдуманных лидов |
| `request-delivery-conclusion` | `request-fulfillment` | `request-fulfillment` | api, worker | Завершение заявки по доставке писем недобора: полное покрытие `delivered` ведёт к `fulfilled`, недоставка — честный терминальный исход без выдуманного успеха |
| `provider-agnostic-integrations` | `integration-platform` | `integration-platform` | api, ingest, sender, worker, chains, schedule | `provider-port`, `adapter-capability`, `integration-error` |
| `firecrawl-credit-budget` | `integration-platform` | `integration-platform` | api, ingest, sender, worker, chains, schedule | Бюджет кредитов Firecrawl на задачу: атомарное резервирование, подтверждённый остаток аккаунта, лимиты частоты/параллелизма/размера обхода |
| `crawler-proxy-pool` | `integration-platform` | `integration-platform` | api, ingest, sender, worker, chains, schedule | Пул прокси для обхода: источники (статический список, провайдер), уровни стоимости, ротация и привязка к домену, здоровье и карантин, эскалация на премиум при блокировке |
| `integration-call-resilience` | `integration-platform` | `integration-platform` | api, ingest, sender, worker, chains, schedule | Устойчивость исходящих вызовов внешних провайдеров: явный ограниченный таймаут HTTP-клиента (Firecrawl, LLM) и уважение контекста запроса (дедлайн, отмена) |
| `provider-token-budget` | `integration-platform` | `integration-platform` | api, ingest, worker, chains, schedule | Опубликованный LLM budget contract: ограничение токенов, резерв на каждую платную попытку и учёт подтверждённой usage; без обещания общего billing cap |
| `vector-recall` | `integration-platform` | `integration-platform` | api, ingest, worker, chains, schedule | Реальный эмбеддер и персистентный векторный индекс за портом `vector`: эмбеддинг текста, upsert документа, поиск k ближайших соседей, нормализация ошибок, отсутствие доменных правил |
| `company-agent-orchestration` | `agent-orchestration` | `agent-orchestration` | api, worker, chains, schedule | `agent-command`, `agent-run-state`, `agent-event`: супервизор, агент на компанию, версионируемый пайплайн из блоков, идемпотентность шагов и обязательное утверждение необратимых действий |
| `lead-quality-insights` | `analytics` | `analytics` | api, worker | `quality-insight`: распределение вердиктов и причин «не подходит», различение «нет сигнала»/«опровергнуто»/«ошибка извлечения», рекомендации с основанием |
| `operations-cockpit` | `platform-governance` | `platform-governance` | api | Экран операций: цифры процесса, живая лента агентов, провал в логи и события, граф процессов Temporal, полноэкранный дашборд; доступ по `access-decision` |

**`service-topology-console` не добавляет модель.** Консоль — эксплуатационная проекция роли `api` сервиса `platform-governance`: она строит граф из объявлений каталога и `deploy/services.yaml` и читает метрики Prometheus через серверный прокси. Консоль **не владеет сущностями и не является bounded context**: она не вводит агрегатов, value objects, domain events, policies и межконтекстных контрактов, а её состояние узлов (`up`/`degraded`/`down`/`unknown`) и раскладка — результат наблюдения и состояние интерфейса, а не агрегат или событие. Узлы-сервисы — те же 12 bounded contexts, рёбра — 50 связей каталога; capability лишь назначена существующему владельцу как уточнение покрытия. Владение моделью остаётся у контекстов: владелец capability не получает ни новых сущностей, ни нового языка.

**`client-portal` не добавляет модель.** Capability принадлежит `platform-governance` и описывает самообслуживание клиента — эксплуатационную проекцию роли `api` по образцу `service-topology-console`. Она **не владеет сущностями и не является bounded context**: не вводит агрегатов, value objects, domain events, policies и межконтекстных контрактов. Регистрация использует модель `platform-access` (агрегат `tenant-access`), подача заявки идёт опубликованным контрактом `request-intent` владельца `request-fulfillment`, а маскирование выполняется на сервере: клиент получает только агрегаты и состояние заявки (идентификатор, статус), без сырых лидов, карточек компаний, контактов и процесса краулинга. Владение моделью остаётся у контекстов.

**`job-journal-console` и `lead-cards-console` не добавляют модель.** Эти capability принадлежат `lead-management` и описывают эксплуатационные проекции роли `api` — консольные экраны журнала заданий и витрины карточек (включая экран «Лиды» и выгрузку того же набора данных в автономный HTML и CSV). Действие переоценки компаний на экране задания — та же проекция над существующим агрегатом `lead-assessment`, новых событий оно не вводит: ревизия оценки переиспользует `lead-assessment-revised`. Они **не владеют сущностями и не являются bounded context**: не вводят агрегатов, value objects, domain events, policies и межконтекстных контрактов. Журнал показывает уже существующую операционную запись задания (`search_job`, `search_job_event`), витрина — уже существующие агрегаты `lead-profile` и `contact`; источники данных и их владельцы не меняются. Capability лишь назначена существующему контексту как уточнение покрытия, владение моделью остаётся у контекстов.

**`console-appearance` не добавляет модель.** Capability принадлежит `lead-management` и описывает общий слой оформления серверных экранов роли `api` (светлая/тёмная/системная тема, переключатель на экранах с общей навигацией, палитра статусов). Слой ведёт происхождение из консоли оператора, но поставляется общим модулем серверных экранов и применяется ко всем экранам, которые его используют: операторская консоль, клиентский портал (`platform-governance`) и дашборд клиента (`analytics`). Capability **не владеет сущностями и не является bounded context**: не вводит агрегатов, value objects, domain events, policies и межконтекстных контрактов; цвет и тема — проекция представления над уже существующими данными, владение моделью остаётся у контекстов.

**`lead-search` и `email-generation` не добавляют модель.** Обе capability принадлежат `lead-management` и описывают прикладной слой и межконтекстные контракты поверх существующих агрегатов `lead-profile` и `contact`: поиск лидов под заявку (пул → дозабор краулингом, происхождение, лимит/дедлайн, матчинг, дедуп, отсутствие PII) и генерация персонализированного письма через LLM (обезличенный промпт, доменный отказ вместо выдуманного письма). Потребитель — workflow `request-fulfillment`. Capability **не владеют сущностями и не являются bounded context**: не вводят агрегатов, value objects, domain events, policies и новых relationships; контракты объявлены у владельца, владение моделью остаётся у контекстов.

**`search-recommendation` не добавляет агрегат.** Capability принадлежит `lead-management` и описывает применение рекомендации анализа качества поиска как **новый запуск поиска** поверх существующей операционной записи задания (`search_job`): изменяемого агрегата цели/источника/лимита у контекста нет, поэтому изменение в контуре поиска выражается новым заданием с рекомендованными критериями, а не правкой несуществующей сущности. Capability не вводит агрегатов, value objects, domain events и policies; новизна — контракт `recommendation-apply` (владелец `lead-management`) и связь `search-feedback-from-analytics` (`lead-management → analytics`; инициатор команды — `analytics`). Идемпотентность держится на детерминированном идентификаторе задания и уникальности первичного ключа — второго хранилища ключей не заводится. Read-контракт `lead-relevance` того же владельца публикует чтение вердикта и свидетельств оценки по паре (задание, компания) без новых агрегатов: оценка остаётся выводом в контексте задания.

**`search-job-control`, `search-job-lifecycle`, `search-job-progress`, `search-job-error-codes` и `search-job-events` не добавляют модель.** Эти capability принадлежат `lead-management` и описывают управление уже существующей операционной записью — заданием поиска (таблицы `search_job` и `search_job_event`), а не доменным агрегатом. Задание не является сущностью каталога: отмена, переходы состояний, прогресс, коды ошибок и журнал событий не вводят агрегатов, value objects, domain events, policies и межконтекстных контрактов. Роли `api` и `worker` одного сервиса `lead-management` лишь разделяют обязанности: `api` фиксирует запрос отмены и показывает прогресс, `worker` исполняет задание, ведёт переходы состояния и публикует прогресс; владельцем capability остаётся контекст, а не роль.

**`company-quota-search` не добавляет модель.** Capability принадлежит `lead-management` и описывает поведение задания поиска, а не доменный агрегат: целевое число компаний, добор целей обхода и причины остановки — свойства операционной записи задания (`search_job`, `search_job_event`). Capability не вводит агрегатов, value objects, domain events, policies и межконтекстных контрактов; владение моделью по-прежнему у контекстов `lead-management` и `integration-platform`, а само задание остаётся операционным примитивом, а не сущностью каталога.

**`crawler-proxy-pool` не добавляет модель.** Capability принадлежит `integration-platform` и описывает адаптерный механизм обхода, а не доменный агрегат: пул прокси, уровни стоимости, ротация, привязка к домену, здоровье и карантин — политика адаптера за портом `provider-port`. Capability не вводит агрегатов, value objects, domain events, policies и межконтекстных контрактов; владение моделью остаётся у `integration-platform`.

**`request-fulfillment-execution` добавляет ограниченную доменную модель.** Владелец `request-fulfillment` публикует событие `execution-intent-accepted` агрегата `request-execution`; `request-execution-failed` остаётся событием агрегата `request`. Это отражено в каталоге, но полная обёртка `RequestExecution` ещё не реализована. **`integration-call-resilience` не добавляет модель:** capability описывает устойчивость исходящих вызовов провайдеров — ограниченный таймаут HTTP-клиента и уважение контекста запроса за портом `provider-port`; владение остаётся у `integration-platform`.

**`provider-token-budget` не добавляет модель.** Это техническая capability `integration-platform`: контекст владеет внешним LLM-адаптером и опубликованным budget contract, но capability не вводит бизнес-агрегат, value object или domain event. `lead-management` потребляет опубликованный LLM budget contract через собственную границу адаптера; он не импортирует агрегаты, типы или внутреннюю доменную модель `integration-platform`. В каталоге у capability один primary context — `integration-platform`; supporting contexts в схему каталога не добавляются.

**`client-dashboard` не добавляет модель.** Capability принадлежит `analytics` и описывает real-time read-модель (проекцию) дашборда клиента — эксплуатационную витрину роли `api` по образцу `job-journal-console`. Она **не владеет сущностями и не является агрегатом**: не вводит агрегатов, value objects, domain events, policies и межконтекстных контрактов, а лишь проецирует нормализованные доменные события (`lead-workflow`, `engagement-observed`, `message-sent`, `delivery-event`) в счётчики в памяти процесса. `campaign-reporting` (снимки) не меняется: это второй режим того же контекста, снимки остаются источником исторической отчётности. Маскирование — в контуре клиента: отдаются только агрегаты и счётчики, текст ответов компаний не хранится и не передаётся (доступен лишь факт «ответили: N»), соблюдается порог минимальной группы (срез из одной записи не показывается), метрик открытий нет. Проекция питается двумя путями: в рабочем контуре — воркером из очередей брокера, а в dev- и тест-контуре — write-эндпоинтом `POST /dashboard/events` (доменные поля совпадают с сообщением очереди; `event_id` и дедупликация остаются атрибутами очереди). В рабочем контуре воркер читает два контракта владельцев: нормализованный `analytics-event` (ручные события `lead-management`) и `delivery-event` (`message-delivery`) на очереди `analytics.delivery-event` — по связи `delivery-to-analytics`, поэтому витрина отражает реальный ход цикла (доставленное письмо учитывается как коммуникация, отказ доставки — как отбой). Владение моделью остаётся у контекстов.

**Тактическая модель `request-execution` в `request-fulfillment`.** Каталог отражает отдельный от `request` агрегат с root `execution-intent` и событием `execution-intent-accepted`; `request-execution-failed` остаётся событием агрегата `request`. Одна UoW может атомарно сохранять переход `request`, intent и outbox, но это не меняет владельца события. Реализованы доменный тип `ExecutionIntent`, attempt storage и application admission handler; полной aggregate wrapper-модели `RequestExecution` нет. `terminal-intent`, `handoff-manifest`, `delivery-receipt` и соответствующие события остаются только целями OpenSpec и не включены в текущий каталог как реализованные сущности/события. Dispatcher и trusted verifier также не заявляются реализованными. Идентичности разделены: `request_id` — стабильная заявка, `workflow_id` — стабильный Temporal WorkflowID, `attempt_id`/generation/`command_id` — принятая попытка/команда, `run_id` назначает Temporal server после старта. Target resolution отдельно от verified runtime evidence: первое уточняет `LeadTarget`, второе требует проверки полной Temporal history доверенным verifier-ом; target resolution не доказывает terminal. Состояния заявки, attempt и доказательности runtime — независимы; UNKNOWN блокирует новый rerun до подтверждения.

**`vector-recall`, `request-shortfall` и `delivery-bridge` не добавляют модель.** Эти capability назначены существующим контекстам как уточнение покрытия.
`vector-recall` (`integration-platform`) описывает реальный эмбеддер и персистентный векторный индекс за уже объявленным контрактом `vector`: индекс — инфраструктурный адаптер, а не домен; новых агрегатов, сущностей, value objects, domain events, policies и связей не вводит, связи `vector-for-leads`/`vector-for-requests` остаются как есть.
`request-shortfall` (`request-fulfillment`) описывает недобор как производное значение агрегата `request` (`target − найденные`): новых агрегатов, сущностей, событий и контрактов не вводит, наблюдаемое состояние — проекция.
`request-delivery-conclusion` (`request-fulfillment`) описывает завершение заявки по наблюдаемому исходу доставки её писем: полное покрытие `delivered` ведёт `processing → fulfilled`, недоставка — честный терминальный исход. Новых агрегатов, сущностей и domain events не вводит: переход выполняет существующий агрегат `request`, накопление исходов живёт в durable-графе исполнения, а сопоставление — по кампании заявки. Новизна — связь `delivery-events-for-requests` (`message-delivery → request-fulfillment`, контракт `delivery-event`) и аддитивное поле `campaign_id` в этом контракте (MINOR): исход доставки сопоставляется с заявкой по кампании, без персональных данных.
`delivery-bridge` (`message-delivery`) описывает приём `approved-action` и формирование `delivery-command` через разрешение ссылок: команда живёт в существующем агрегате `message-delivery` (root `message`), новых агрегатов, сущностей и событий не вводит. Новизна моста — контракты `delivery-target` (владелец `lead-management`, адрес получателя) и `content-version` (владелец `content-production`, материал письма: тема — необязательное поле схемы `1.1.0`, тело), связи `leads-for-delivery` (`lead-management → message-delivery`) и `content-to-delivery` (`content-production → message-delivery`): адрес и материал разрешают свои владельцы в момент доставки, а `approved-action` остаётся ссылочным и без PII.

**`outreach-handoff` не добавляет агрегат.** Capability принадлежит `outreach-orchestration` и описывает приём команды `outreach-handoff` от `request-fulfillment`: по `request_id` идемпотентно создаётся одна кампания заявки (существующий агрегат `campaign`) с черновиками в состоянии ожидания утверждения. Новых агрегатов, сущностей, value objects, domain events и policies не вводится, отправка остаётся за человеком (`operator-approval-required`). Новизна — контракт `outreach-handoff` (владелец поверхности `outreach-orchestration`) и связи `requests-to-outreach` (`outreach-orchestration → request-fulfillment`; инициатор команды — `request-fulfillment`) и `content-for-requests` (`content-production → request-fulfillment`, контракт `content-request`). Черновик несёт только непрозрачные ссылки на получателя и материал: получателя прикладывает владелец письма в ответе `email-generation` (`recipient_ref`), а разрешает `lead-management` при доставке; материал производится через `content-request` у `content-production`. Чужих сущностей и ПД приём не заводит.

**`agent-orchestration` добавляет модель.** Новый bounded context и сервис владеет координацией обработки компании, а не предметными решениями: агрегаты `agent-pipeline` (реестр блоков и версии пайплайна) и `agent-run` (долговечное состояние прохождения) с событиями `agent-pipeline-published`, `agent-run-*`, `agent-step-*` и политиками `extensible-block-registry`, `step-idempotency`, `irreversible-step-approval`. Владение лидом и оценкой (`lead-management`), кампанией, черновиком и решением оператора (`outreach-orchestration`), доставкой (`message-delivery`) и реакциями (`engagement-observation`) не переносится: агент потребляет их по объявленным контрактам (`lead-search`, `lead-relevance`, `email-generation`, `campaign-command`, `delivery-event`, `engagement-event`, `access-decision`, `tenant-context`) и ссылается на чужие агрегаты только по идентификаторам. Необратимая отправка идёт только через очередь утверждения (`campaign-command` → `operator-approval-required`), LLM применяется внутри блоков, а не как оркестратор.

**`lead-quality-insights` добавляет read-модель инсайтов.** Capability `analytics` вводит агрегат `quality-insight` (AR `quality-insight`) с VO `insight-scope`, `rejection-reason`, `recommendation`, событиями `quality-insight-refreshed`, `quality-recommendation-issued` и политикой `minimum-group-threshold`. Read-модель хранит только агрегаты и обезличенные признаки: без ПД и красной зоны, с порогом минимальной группы и «недоступно» вместо нуля; различает «нет сигнала», «опровергнуто» и «ошибку извлечения». Политика `anonymized-reporting` остаётся у агрегата `campaign-report`, новых владельцев не появляется.

**`operations-cockpit` не добавляет модель.** Capability принадлежит `platform-governance` и описывает эксплуатационную проекцию роли `api` по образцу `service-topology-console`: экран композирует данные разных владельцев (агрегаты аналитики, состояние и события агентов, процессы Temporal, метрики Prometheus) и требует административного доступа. Она **не владеет сущностями и не является bounded context**: не вводит агрегатов, value objects, domain events, policies и межконтекстных контрактов; её узлы, цифры и панели — эксплуатационная проекция, доступ проверяется сервером по снимку `access-decision`. Владение моделью остаётся у контекстов.

## Тактическая модель

Ниже **AR** означает aggregate root, **E** — entity, **VO** — value object. Только aggregate root принимает команды извне агрегата. Ссылки на другие агрегаты и контексты хранятся как идентификаторы или снимки опубликованного контракта, а не как указатели на чужие объекты. Раздел является представлением [`architecture/domain-catalog.yaml`](../architecture/domain-catalog.yaml): состав агрегатов, сущностей, value objects, инвариантов, событий и политик приводится строго по каталогу.

### `platform-governance`

- **`tenant-access`** (AR: `tenant`): E: `tenant`, `user-role`, `audit-record`; VO: `tenant-id`, `role-set`.
  - Инварианты: роль принадлежит ровно одному тенанту; отключённый тенант не выдаёт новых решений доступа.
  - События: `tenant-access-changed`, `audit-recorded`.
  - Политики: `tenant-isolation`, `least-privilege`, `platform-operator`.
  - Примечание: политика `platform-operator` — субъект, заданный конфигурацией (оператор платформы), действует во всех активных тенантах и получает права администратора тенанта независимо от состава пользователей; отключённый тенант закрыт и для него. Изоляция остальных акторов не ослабляется.

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
  - Примечание: оценка — не постоянное свойство компании, а вывод в контексте конкретного задания поиска; свидетельства несут критерий из интерпретации запроса, факт, URL источника и обоснование связи. Наблюдаемый факт, интерпретация и неподтверждённое намерение купить — раздельные поля, поэтому ссылка подтверждает факт, а не существование компании. Значения вердикта и типа сигнала взяты из интерпретации запроса (`search-strategy`) и не смешиваются с техническим планом обхода. Переоценка уже найденных компаний задания — повторное применение того же вывода по критериям, выведенным заново из запроса, без повторного обхода: интерпретация обновляется, прежняя оценка ревизуется тем же событием `lead-assessment-revised`, ключ пары (job, company) сохраняется; новых агрегатов, событий и контрактов не вводится.

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
  - Инварианты: действие нельзя исполнить до положительного решения оператора; решение относится к точной версии черновика; изменённый черновик требует нового решения; идентификатор утверждённого действия (`approved-action`) уникален в рамках кампании — повтор одного действия не исполняется дважды, а действие другой кампании к тому же получателю не считается повтором; завершённое выполнение цепочки не возобновляется.
  - События: `action-approved`, `campaign-state-changed`, `outreach-outcome-recorded`.
  - Политики: `operator-approval-required`, `outreach-scheduling`.
- **`call`** (AR: `call`): E: `call`; VO: `call-window`.
  - Инварианты: звонок планируется только внутри разрешённого временного окна.
  - События: `call-scheduled`.
  - Политики: —.

### `agent-orchestration`

- **`agent-pipeline`** (AR: `agent-pipeline`): E: `agent-pipeline`, `pipeline-step`; VO: `block-ref`, `step-config`, `pipeline-version`.
  - Инварианты: опубликованная версия пайплайна неизменяема — изменение состава шагов создаёт новую версию; шаг ссылается только на зарегистрированный блок, незарегистрированный блок не исполняется и не подменяется другим.
  - События: `agent-pipeline-published`.
  - Политики: `extensible-block-registry`.
- **`agent-run`** (AR: `agent-run`): E: `agent-run`, `agent-step-run`; VO: `agent-mission`, `run-status`, `block-outcome`.
  - Инварианты: запуск ссылается на неизменяемую версию пайплайна, к которой привязан; каждый шаг даёт эффект не более одного раза (повтор после сбоя продолжает обработку, а не удваивает результат); терминальное состояние не возобновляется; необратимый шаг (отправка) требует положительного решения оператора по точной версии черновика, изменённый черновик требует нового решения; запуск одного тенанта не виден другому.
  - События: `agent-run-started`, `agent-step-completed`, `agent-step-failed`, `agent-run-completed`, `agent-run-blocked`.
  - Политики: `step-idempotency`, `irreversible-step-approval`.
  - Примечание: супервизор — application service контекста (предел параллелизма, очередность, повтор с ограничением попыток, команда остановки), отдельного агрегата у него нет. Долговечность обеспечивает порт долгих процессов (текущий рантайм — Temporal); LLM применяется внутри блоков принятия решений, а не как оркестратор. Блок, зависящий от невыбранного или недоступного провайдера, сообщает явную недоступность и не закрывает процесс успехом.

### `message-delivery`

- **`message-delivery`** (AR: `message`): E: `message`; VO: `delivery-address`, `delivery-attempt`.
  - Инварианты: сообщение создаётся только для утверждённого действия; повторная отправка с тем же idempotency key запрещена (ключ — тенант и идентификатор утверждённого действия, уникальный в рамках кампании-владельца, поэтому действия разных кампаний к одному получателю различимы); активный стоп-лист блокирует попытку непосредственно перед доставкой.
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

### `analytics`

- **`campaign-report`** (AR: `report-snapshot`): E: `report-snapshot`; VO: `report-period`, `funnel-metric`.
  - Инварианты: аналитика не является владельцем операционного состояния; красные данные не копируются в read-модель без агрегации или обезличивания; снимок воспроизводим по watermark и версии формул.
  - События: `report-snapshot-created`.
  - Политики: `anonymized-reporting`.
- **`quality-insight`** (AR: `quality-insight`): E: `quality-insight`; VO: `insight-scope`, `rejection-reason`, `recommendation`.
  - Инварианты: инсайт не содержит ПД и содержимого красной зоны (только агрегаты и обезличенные признаки); различаются «нет сигнала», «опровергнуто» и «ошибка извлечения»; рекомендация выдаётся только с наблюдаемым основанием; снимок воспроизводим по watermark и версии формул; срез ниже порога минимальной группы не показывается.
  - События: `quality-insight-refreshed`, `quality-recommendation-issued`.
  - Политики: `minimum-group-threshold`.

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

- **`request`** (AR: `request`): E: `request`, `offer`; VO: `request-status`, `offer-content`, `request-target`, `validation-decision`. Заявки, созданные только при явном `ExecutionProtocol v2`, помечаются `ExecutionHistoryKnown`; v1 и legacy snapshots без маркера не интерпретируются по статусу и блокируются как неизвестная execution history.
  - Инварианты: заявка без непустого оффера не создаётся; заявка привязана ровно к одному тенанту и не переносится между тенантами; переход в `processing` разрешён только из `validated`; до `validated` оффер правится на месте, а после `validated` не редактируется на месте — любое изменение создаёт новую версию; решение валидации (`approved`/`rejected`) фиксируется внутри агрегата с актором, временем и причиной; статусы `fulfilled`, `rejected`, `cancelled`, `failed` терминальны; `needs-revision` — нетерминальный возврат на доработку; `failed` означает необратимо сломанное исполнение (с кодом и текстом причины) и отличим от `needs-revision`; явный повторный запуск (re-run) переводит `failed → validated` отдельной операцией; ссылки на чужие агрегаты (включая `call`) — только идентификаторы.
   - События: `request-submitted`, `request-validated`, `request-processing-started`, `request-fulfilled`, `request-rejected`, `request-cancelled`, `request-returned-for-revision`, `request-resubmitted`, `request-execution-failed`.
  - Политики: —.
  - Примечание: жизненный цикл — `submitted → validating → validated → processing → fulfilled`; терминальные — `rejected`, `cancelled` и `failed`; возврат на доработку — нетерминальный `needs-revision` (`validating | processing → needs-revision → submitted` повторной подачей либо `cancelled` отменой с комментарием). `failed` — терминальный исход необратимого сбоя исполнения (`processing → failed` с кодом из набора `search_failed`/`draft_failed`/`handoff_failed`/`transition_rejected`/`workflow_timed_out` и текстом причины): переход инициируется доменным решением (команда `FailRequest`, событие `request-execution-failed`), а не чтением Temporal-статуса; причина персистится в агрегате и доступна через контракт `request-intent`. `failed` не «хоронит» восстановимую заявку: явный re-run (`failed → validated` отдельной операцией, вне `canTransitionTo`) возвращает заявку на стартовую позицию, после чего `validated → processing` запускает новое исполнение. Для заявки с недобором `fulfilled` означает полное покрытие доставкой переданных писем: пауза исполнения снимается не только сигналом консоли заявок, но и наблюдаемым завершением доставки (`delivery-event` по кампании заявки), а недоставка в срок даёт `cancelled` с явной причиной (`request-delivery-conclusion`). `offer` версионируется (`revision`): прежняя версия остаётся неизменной. Фулфилмент-состояние ссылается на артефакты других контекстов (`audience-snapshot`, `campaign`) по идентификаторам, а не по указателям. Workflow исполнения (движение по статусной машине) — application service этого контекста, а не отдельная граница (`temporal-execution-platform`, change №2). Валидация оффера админом (capability `request-validation`) — прикладной use case того же контекста: `submitted → validating` (взятие в работу), звонок-уточнение, правки оффера на месте, `validating → validated | rejected` с решением внутри агрегата; звонок принадлежит `outreach-orchestration` и вызывается командой `call-intent`, а заявка хранит его `call_id`. После валидации оффер получает эмбеддинг (контракт `vector`, capability `semantic-lead-search`) как вход recall; правка оффера сбрасывает устаревший вектор.

## Межконтекстные отношения и контракты

`published-language` означает стабильный версионированный контракт владельца. `customer-supplier` означает, что upstream согласует контракт с потребностью downstream. `anticorruption-layer` означает обязательный перевод модели внешней системы в язык потребляющего контекста.

- `data-governance` публикует `data-zone-policy`, `processing-authority` и `suppression-policy`; lead, audience, outreach и delivery применяют решения, но не воспроизводят правила локально.
- `lead-management` предоставляет `lead-profile` для `audience`; аудитория хранит ссылку/снимок необходимых атрибутов, а не редактирует лид.
- `audience` предоставляет `audience-snapshot`, а `content-production` — `content-version`; оба являются входами кампании.
- `outreach-orchestration` публикует `approved-action` как command intent для delivery. `message-delivery` потребляет его (capability `delivery-bridge`): адрес получателя разрешается через `delivery-target` (владелец `lead-management`, связь `leads-for-delivery`), материал письма (тема, тело) — через `content-version` (владелец `content-production`, связь `content-to-delivery`). Ссылку на материал несёт уже событие `action-approved` (материал утверждённого черновика), поэтому основной путь `approve → outbox → relay` передаёт непустой материал. Владельцы разрешают то, чем владеют, в момент доставки, а `approved-action` остаётся ссылочным и без PII.
- `outreach-orchestration` перед пакетным утверждением получает признаки риска черновика у владельцев (capability `risk-screening`): стоп-лист — `suppression-policy` (`data-governance`), статус верификации адреса — `address-verification` (владелец `lead-management`, связь `lead-verification-for-outreach`), адрес получателя для проверки стоп-листа — `delivery-target` (связь `leads-for-delivery`). Правило риска локально не выводится: признак `stop-rule-triggered` берётся из решения стоп-листа, `address-unverified` — из вердикта владельца верификации. Недоступность владельца — честный отказ: черновик не признаётся свободным от риска и пакетное решение не выдаётся `«рисков нет»`.
- `message-delivery` публикует `delivery-event`; observation квалифицирует его, analytics проецирует его, а `lead-management` по отказу доставки обновляет статус адреса в aggregate `contact` (цикл подавления). Событие несёт непрозрачную ссылку на кампанию (`campaign_id`, MINOR), унаследованную от `approved-action`, поэтому `request-fulfillment` по связи `delivery-events-for-requests` сопоставляет исход доставки со своей заявкой и завершает её (`request-delivery-conclusion`).
- `engagement-observation` возвращает `engagement-event` в orchestration, audience (поведенческая сегментация потребляет сигналы взаимодействия) и analytics.
- `platform-governance` публикует `access-decision`; потребители (`outreach-orchestration`, `data-governance`) не реализуют собственную RBAC-семантику — решение о доступе выдаёт владелец, а недоступность владельца не трактуется как разрешение. Контракт несёт две грани: снимок правил (policy, связи `platform-access-for-outreach`/`platform-access-for-data-governance`, `async · amqp`) и синхронную проверку решения (query). Синхронная проверка при co-location идёт in-process адаптером, а при разделении сервисов — сетевым HTTP-вызовом владельца (связи `platform-access-check-for-outreach`, `platform-access-check-for-data-governance`, `sync · http-json`); непустой адрес владельца задаётся точке входа, код потребителя не меняется.
- `integration-platform` предоставляет `provider-port` через ACL для lead, content, delivery и outreach. Он не владеет их workflow. Прямая отправка `lead-email` идёт за портом `mail` (`contracts/integration-platform`) — это внешний адаптер за `provider-port`, а не новый межконтекстный контракт. Тот же контекст публикует контракт `vector` (векторное представление и поиск по близости) за provider-port: `lead-management` использует его для эмбеддингов компаний и recall, `request-fulfillment` — для эмбеддинга оффера заявки. Доменных правил хранилище не содержит. Состояние адаптеров отдаётся консоли через контракт `adapter-status` (связь `integrations-status-for-governance`, `integration-platform → platform-governance`): консоль не заводит собственного реестра адаптеров, а получает нормализованные состояния без секретов.
- `request-fulfillment` — владелец заявки клиента; фулфилмент-состояние читает чужие агрегаты по `id` через синхронные `query`-контракты: `lead-profile` (`lead-management`), `audience-snapshot` (`audience`), `campaign-state` (`outreach-orchestration`). Контекст тенанта — `tenant-context` (`platform-governance`). Заявка не владеет чужими агрегатами и не переносит их состояние: она хранит только ссылки. Звонок-уточнение валидации идёт командой `call-intent` в `outreach-orchestration` (владелец агрегата `call`); заявка хранит `call_id`, не владея звонком. При недоборе `request-fulfillment` передаёт сгенерированные письма в `outreach-orchestration` командой `outreach-handoff` (связь `requests-to-outreach`; контракт публикует владелец поверхности `outreach-orchestration`, инициатор — `request-fulfillment`), а материал письма производит контрактом `content-request` (связь `content-for-requests`); получателя несёт непрозрачная ссылка `recipient_ref` из `email-generation`, а разрешают получателя и материал их владельцы (`lead-management`, `content-production`) в момент доставки.

- `lead-management` публикует `lead-search`, `lead-relevance` и `email-generation`; `agent-orchestration` потребляет их для блоков поиска, оценки и генерации письма (связи `lead-search-for-agents`, `lead-relevance-for-agents`, `email-generation-for-agents`, `sync · http-json`). `lead-relevance` — read-контракт оценки, добавленный вместе с контекстом: оценка остаётся выводом в контексте задания и не переносится между заданиями. `lead-management` также публикует `recommendation-apply` для `analytics` (связь `search-feedback-from-analytics`, `command · sync · http-json`): применение рекомендации анализа — новый запуск поиска, а факт применения фиксирует `analytics`.
- `agent-orchestration` инициирует необратимое действие только через существующий вход утверждения `outreach-orchestration` (связь `draft-approval-for-agents`, контракт `campaign-command`, `sync · http-json`): черновик порождается, отправка ждёт положительного решения оператора, изменённый черновик требует нового решения. Исходы шагов агента публикуются как `agent-event` для `analytics` (связь `agent-events-to-analytics`, `async · amqp`).
- `message-delivery` и `engagement-observation` публикуют `delivery-event` и `engagement-event`, которые `agent-orchestration` потребляет как исходы шагов наблюдения (связи `delivery-events-for-agents`, `engagement-events-for-agents`, `async · amqp`). Доступ и тенанта агент получает от `platform-governance` (`access-decision`, `tenant-context`; связи `platform-access-for-agents`, `tenant-context-for-agents`, `async · amqp`), а `analytics` отдаёт `platform-governance` агрегаты отчётности для экрана операций (связь `analytics-metrics-for-governance`, контракт `campaign-report`, `sync · http-json`).
- `content-production` отдаёт агенту производство материала письма (`content-request`; связь `content-request-for-agents`, `command · sync · http-json`), а `outreach-orchestration` — чтение состояния утверждённого действия (`approved-action`; связь `approved-action-for-agents`, `query · sync · http-json`). Материал и решение оператора остаются у своих владельцев: агент прикладывает ссылки и не подменяет ни производство письма, ни положительное решение.

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

- **Human-in-the-loop:** ни письмо, ни form submit не исполняются на основании одного результата генерации. Решение оператора связано с точной версией действия.
- **Privacy by boundary:** red-данные не появляются в публичных событиях. Межконтекстный контракт передаёт минимальный идентификатор или разрешённый снимок.
- **Idempotency:** команды внешнего эффекта имеют стабильный idempotency key; обработчики событий дедуплицируют `event_id`.
- **Transactional outbox:** изменение агрегата и запись его события атомарны. В `platform-governance` committed-записи читает владелецкий PostgreSQL relay и отправляет в RabbitMQ с publisher confirm; `published` фиксируется только после confirm, ошибки приводят к backoff и at-least-once повтору с прежним `event_id`.
- **Eventual consistency:** проекции и downstream-процессы могут отставать; интерфейсы показывают freshness/watermark там, где это влияет на решение.
- **No distributed aggregate:** транзакция не охватывает два bounded contexts. Длинные процессы моделируются saga/process manager в контексте, который владеет целью процесса.
- **Auditability:** операторские решения, смена основания обработки и suppression сохраняют actor, time, reason и evidence.

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

В demo-пути `lead-management` сохраняет телефонный контекст в необязательном
`AttributeProvenance.Evidence` существующего агрегата contact. Это свидетельство
извлечения, а не проверка доставки/доступности. Старые снимки без поля читаются
совместимо; неподтверждённые телефоны доступны отдельно и не входят в пригодный
пул. Повторный crawl подтверждает тот же контакт без смены владения или связей.
