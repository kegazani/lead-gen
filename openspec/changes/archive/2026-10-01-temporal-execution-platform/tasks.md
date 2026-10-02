## 1. Platform-primitive Temporal

- [x] 1.1 Добавить `backend/platform/workflow` (клиент Temporal, worker host, регистрация activities/workflows) без доменных зависимостей. Проверка: `go build ./...` проходит, archlint не находит доменных импортов в platform-пакете.
- [x] 1.2 Добавить конфигурацию подключения к Temporal Cloud в `deploy/`. Проверка: worker стартует и регистрируется в namespace.
- [x] 1.3 Задать правило payload-гигиены (только id, без PII/секретов) и оформить проверку на ревью. Проверка: ревью-чек при PR не пропускает PII в payload.

## 2. Worker и activities

- [x] 2.1 Подключить Temporal worker в сервисе `request-fulfillment`. Проверка: сервис поднимает worker, health-эндпоинт отвечает.
- [x] 2.2 Реализовать схему «activity = тонкий адаптер к application service» на одном примере (заглушка шага поиска). Проверка: activity вызывает application service через контракт, без доменной логики в activity.

## 3. Workflow фулфилмента

- [x] 3.1 Реализовать workflow исполнения заявки в `request-fulfillment`: переход `validated → processing` с вызовом activities-заглушек. Проверка: запуск workflow переводит заявку в `processing` (тест с Temporal test framework).
- [x] 3.2 Реализовать ветвление «достаточно лидов / добор» и человеческие паузы (сигнал валидации, сигнал утверждения). Проверка: тест ветвления и паузы с сигналом.
- [x] 3.3 Добавить версионирование workflow (механизм Temporal) для изменений структуры графа. Проверка: тест совместимости бегущего workflow при изменении версии.

## 4. Наблюдаемость и гарантии

- [x] 4.1 Подключить observability workflow (состояние, история запусков, DLQ) к существующему контуру `observability`. Проверка: состояние бегущего workflow видно через API (`GET /requests/{id}/workflow`) и метрики (`workflow_started`, `workflow_start_error`); история запусков и DLQ — штатные возможности Temporal Cloud UI. Рендер в операторской консоли отнесён к change `web-frontend-architecture` (в репозитории консоли ещё нет).
- [x] 4.2 Зафиксировать поведение при недоступности Temporal: заявка остаётся в `validated`, без потери состояния. Проверка: тест отказа Temporal не портит агрегат.

## 5. Валидация

- [x] 5.1 Прогнать `go -C tools/archlint run .` и убедиться, что `backend/platform/workflow` не тянет доменные пакеты контекстов. Проверка: команда завершается без ошибок.
