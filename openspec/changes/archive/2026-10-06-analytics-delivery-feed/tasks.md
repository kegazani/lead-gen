## 1. Транспортный подписчик и декодер delivery-event

- [x] 1.1 Обобщить `transport.Subscriber`: декодер контракта как параметр, `Run` использует декодер; `NewSubscriber` (`analytics-event`) сохраняет поведение. Проверка: `go test ./internal/adapters/transport/...` в `backend/contexts/analytics` зелёный, существующие тесты подписчика не изменены.
- [x] 1.2 Добавить `NewDeliveryEventSubscriber` с очередью `analytics.delivery-event` и декодером контракта `delivery-event`: `delivered` → вид `message-sent` (статус `sent`), `failed` → вид `delivery-event` (исход `failed`), ключ записи — `recipient_ref` (фолбэк `message_id`), `tenant_id`/`campaign_id`/`occurred_at` из конверта. Проверка: тест публикует `delivery-event`-сообщение и получает ожидаемое `command.ApplyEvent`; событие без `tenant_id` или получателя не создаёт запись. **Сделано:** `TestDeliveryEventSubscriberProjectsCycleOutcomes`, `TestDeliveryEventSubscriberIgnoresUnattributed`.

## 2. Подписка роли worker сервиса analytics

- [x] 2.1 `analytics.Config`: поле очереди доставки; в `New` собирать второго подписчика только при заданном `Consumer`; в `runWorker` запускать обе подписки вместе с обработкой выгрузок. Проверка: `go test ./...` в `backend/contexts/analytics` зелёный; тест роли worker подтверждает, что `delivery-event` из очереди доходит до проекции (счётчик прокоммуницированных растёт). **Сделано:** `TestWorkerAppliesDeliveryEventsToProjection` (Communicated == 2).
- [x] 2.2 Проброс очереди доставки из `backend/cmd/lead-gen` (флаг/ENV по образцу `-request-delivery-queue`), если требуется конфигурируемость. Проверка: `go build ./...` в `backend/cmd/lead-gen`; при пустом значении берётся очередь по умолчанию. **Сделано:** флаг `-analytics-delivery-queue` / ENV `LEADGEN_ANALYTICS_DELIVERY_QUEUE`, поле `buildOptions.analyticsDeliveryQueue` → `Config.DeliveryQueue`.

## 3. Документация и доменная модель

- [x] 3.1 Уточнить `docs/domain-model.md`: витрина клиента питается исходами доставки цикла (связь `delivery-to-analytics`, контракт `delivery-event`); каталог/агрегаты/связи не меняются. Проверка: текст модели соответствует реализации.
- [x] 3.2 Убедиться, что `architecture/domain-catalog.yaml` уже содержит связь `delivery-to-analytics` (правка не требуется) и `go -C tools/archlint run .` — код 0. **Сделано:** archlint — код 0 (`12 / 23 / 40 / 60 / 51 / 8`), каталог не менялся.

## 4. Гейт и живая проверка

- [x] 4.1 `make check` (build/test/vet/importlint/archlint/openspec) — зелёный. Проверка: команда завершилась кодом 0. **Сделано:** exit 0; openspec 63 passed, 0 failed.
- [x] 4.2 `openspec validate analytics-delivery-feed --strict` — valid.
- [x] 4.3 Живая проверка: пересобрать образ (`docker compose build`) и пересоздать контур; прогнать сквозной цикл в клиентском тенанте с пулом (заявка → реальный `lead-search` → недобор → письма → утверждение → отправка → SMTP), затем открыть `:8081/dashboard?tenant_id=<клиентский тенант>` и подтвердить непустой срез (метрики видимы, срез не скрыт). Проверка: числа дашборда отражают прогон (например, прокоммуницировано > 0). **Сделано:** заявка `req-f3fa5221fad697244ccb84b5f067262b`, реальный `lead-search` found 5 / target 20 / shortfall 15, 4 черновика утверждены, mailpit 12 → 16, заявка `fulfilled`; дашборд `:8081/dashboard/stream?tenant_id=tenant-0b828ff5…` → `visible=true, found=4, communicated=4, in_progress=4`.
- [x] 4.4 Отметить `openspec/changes/live-e2e-contour/tasks.md` 3.1/3.2 по фактическому результату (закрыть при непустом дашборде, иначе оставить открытыми с точной причиной) и обновить `docs/progress-log.md`.
