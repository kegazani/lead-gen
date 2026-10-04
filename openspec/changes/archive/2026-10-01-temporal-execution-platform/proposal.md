## Why

Агентный конвейер исполнения заявки — это durable workflow: ветвления (нашлось ли достаточно лидов), повторные попытки, таймеры (snooze, «не отвечал N дней», расписание цепочек) и человеческие паузы (валидация админа, утверждение оператора). Писать это вручную — значит воспроизводить то, что уже самописно сделано в `search-job-lifecycle` (единственность взятия из очереди, согласование осиротевших заданий), но в бо́льшем масштабе и с большей хрупкостью. Temporal берёт на себя durable execution, retry, таймеры, сигналы и observability. Принимается как platform-primitive, а не доменная модель: поведение, которое исполняет граф, уже описано другими capability (`client-request`, `search-strategy`, `lead-relevance`, `outreach-approval`), поэтому spec-дельта не требуется.

## What Changes

- Добавляется platform-primitive `backend/platform/workflow` — рантайм Temporal (клиент, регистрация activities/workflows, worker host). Это не bounded context и не provider-port, а техническая библиотека уровня `transport`.
- Каждый сервис-контекст поднимает свой Temporal worker и регистрирует свои activities; чужие шаги вызываются через контракты.
- Workflow «фулфилмент заявки» живёт в owning context `request-fulfillment` и двигает заявку по статусной машине, вызывая activities других контекстов.
- Activities — тонкие адаптеры к application services контекстов; доменная логика в activities не дублируется.
- Раскладка: workflow-код живёт в `internal/adapters/workflow` owning context (внешний SDK в контексте допустим только в adapters); activities вызывают application services (`internal/app/command`).
- Payload-гигиена: в workflow/activity payload — только идентификаторы, без сырых PII и секретов.
- Версионирование workflow: изменения детерминированного кода оформляются через механизм версионирования Temporal.
- Деплой: Temporal Cloud (self-hosted — отложенная альтернатива при масштабе/цене/data-residency).
- `search-job-lifecycle` сейчас не мигрируется — два рантайма оркестрации сосуществуют осознанно (зафиксировано; пересмотр при росте дублирования).
- Наблюдаемость: состояние исполнения отдаётся через API (`GET /requests/{id}/workflow`) и метрики. Рендер состояния в операторской консоли отнесён к change `web-frontend-architecture`: самой консоли в репозитории ещё нет, поэтому этот change ограничивается серверной стороной.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

Нет.

**Обоснование.** Temporal — инфраструктура исполнения, а не новое поведение. Наблюдаемое поведение (жизненный цикл заявки, шаги агентов, человеческие паузы) уже закреплено capability `client-request` и существующими `search-strategy`, `lead-relevance`, `outreach-approval`. Поэтому change помечен `skip_specs: true` — так же, как `web-frontend-architecture` и `system-map-and-cpp-baseline`.

## Impact

**Затрагиваемые bounded contexts.** Новых контекстов нет. `request-fulfillment` — владелец workflow фулфилмента; остальные контексты (`lead-management`, `audience`, `outreach-orchestration`, `content-production`, `message-delivery`, `integration-platform`) предоставляют activities через свои application services и опубликованные контракты. **Доменная модель не меняется.**

**Код и структура.** Добавляется `backend/platform/workflow` (клиент и worker host Temporal, уровень `transport`/`persistence`). Сервисные сборки в `backend/contexts/<context-id>` получают worker. Workflow-код фулфилмента — application service контекста `request-fulfillment`.

**Инфраструктура.** Новый внешний компонент: Temporal Cloud (namespace на тенант/окружение). В `deploy/` добавляется конфигурация подключения; self-hosted кластер — отложен.

**Риски и зависимости.** Зависит от change №1 (`client-request-domain`: агрегат и статусная машина, которую двигает workflow). Основной риск — детерминированность workflow (replay): компенсация — версионирование и запрет недетерминированных вызовов в workflow. Второй риск — утечка PII в payload: компенсация — правило «только идентификаторы» и проверка на ревью.

## Roadmap

Change №2 из шести. См. `openspec/changes/client-request-domain/proposal.md` → Roadmap.
