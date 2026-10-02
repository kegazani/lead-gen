## Context

Платформа context-first на Go: один сервис на контекст, запрет общих `internal/domain`/`app`/`ports`. Уже есть самописный workflow-движок для поисковых заданий (`search-job-lifecycle`, `search-job-events`, `search-job-progress`). Мотивация и список изменений — в `proposal.md`. Change №1 (`client-request-domain`) дал агрегат `request` и его статусную машину — именно её будет двигать workflow.

## Goals / Non-Goals

**Goals:**

- Ввести Temporal как platform-primitive без нарушения context-first.
- Определить дом workflow фулфилмента заявки и схему «workflow → activities → application services».
- Задать payload-гигиену, версионирование и деплой.

**Non-Goals:**

- Миграция `search-job-lifecycle` на Temporal (отложено).
- Реализация самих шагов агентов (поиск, обогащение, письмо) — это change №4 и существующие capability.
- Граф знаний (данные) — это не граф исполнения, отдельная фаза в change №4.

## Decisions

### Решение 1: Temporal — platform-primitive, не bounded context

Рантайм исполнения не владеет доменной моделью. Клиент, worker host и регистрация лежат в `backend/platform/workflow` — техническом пакете уровня `transport`/`persistence`. Это не нарушает context-first (запрещены только общие доменные пакеты).

- **Альтернатива (отвергнута): отдельный bounded context `workflow-engine`.** Рантайм оркестрации не является границей модели/владения и не имеет своего языка; вводить его как контекст — смешивать процесс с моделью.

### Решение 2: worker на сервис, activities — адаптеры к application services

Каждый сервис поднимает Temporal worker и регистрирует свои activities. Activity — тонкая обёртка над application service контекста-владельца, без доменной логики. Чужие шаги workflow зовёт только через activities/контракты, а не напрямую в чужие пакеты.

### Решение 3: workflow фулфилмента живёт в `request-fulfillment`

Workflow исполнения заявки принадлежит контексту `request-fulfillment`: он владеет состоянием заявки (см. change №1) и двигает её по статусной машине, вызывая activities `lead-management`, `audience`, `outreach-orchestration`, `content-production`, `message-delivery`, `sales-workflow`.

Раскладка пакета — `internal/adapters/workflow`: workflow-код импортирует SDK Temporal, а внешние модули в контексте допустимы только в adapters (`tools/importlint`, правило «внутренний слой, кроме adapters, не импортирует внешние модули»). Это адаптер durable-исполнения, а не application service: он не содержит доменных правил и вызывает application services (`internal/app/command`) через activities. Владение графом исполнения остаётся за контекстом, а не за платформенным пакетом.

### Решение 4: payload — только идентификаторы

В input/output workflow и activities не попадают сырые PII и секреты — только `id` и корреляционные ссылки. Правила `data-governance` (красные данные не поднимаются) и `integration-platform` (секреты не в событиях) соблюдаются на уровне payload.

### Решение 5: версионирование workflow

Workflow-код детерминирован (replay). Любое изменение структуры графа версионируется механизмом Temporal (`GetVersion`/патчеры), чтобы бегущие заявки не ломались при деплое.

### Решение 6: Temporal Cloud

Начинаем с Temporal Cloud (без операционной нагрузки кластера); self-hosted — при масштабе/цене/требованиях data-residency. Workflow-метаданные — идентификаторы, поэтому уход в облако не конфликтует с зонами данных.

### Решение 7: `search-job-lifecycle` не мигрируется сейчас

Существующий самописный движок работает и специфицирован. Temporal вводится только для нового контура; сосуществование фиксируется осознанно, миграция — отдельным решением при росте дублирования.

## Risks / Trade-offs

- **[Детерминированность workflow]** → Запрет недетерминированных вызовов в workflow-коде + версионирование + тесты replay.
- **[Утечка PII в payload]** → Правило «только id», проверка на ревью, redaction на входе в activities.
- **[Два рантайма оркестрации]** → Осознанно задокументировано; порог для миграции — дублирование жизненного цикла, а не «потому что некрасиво».
- **[Операционная зависимость от Cloud]** → Payload на id минимизирует data-residency риск; self-hosted как задокументированный план Б.

## Migration Plan

Поэтапно: поднять `backend/platform/workflow` → подключить worker в `request-fulfillment` → реализовать workflow фулфилмента (первый срез: переход `validated → processing` с вызовом заглушек activities) → включить остальные шаги по мере готовности change №3–№4. Откат: workflow не запускается без Temporal — при недоступности заявка остаётся в `validated`, существующие ручные потоки не затрагиваются.

## Open Questions

- Выбор конкретного пространства имён и правил секционирования по тенантам в Temporal Cloud — уточняется при настройке деплоя, не влияет на структуру этого change.
