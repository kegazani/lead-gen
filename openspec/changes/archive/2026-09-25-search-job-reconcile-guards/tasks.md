## 1. Примитив заданий

- [x] 1.1 Расширить `jobs.Store.ReconcileRunning` параметрами (срок аренды, лимит попыток, непрозрачные код и формулировка отказа) и заменить возвращаемое число на счётчики исходов; обновить тестовый `memoryStore` в `jobs_test.go`. Проверка: `go -C backend/platform test ./jobs/...`.
- [x] 1.2 Добавить в `Runner` поля `LeaseTTL`, `MaxAttempts`, `FailCode`, `FailReason` со значениями по умолчанию и подставлять их в `Runner.Reconcile`. Проверка: тест примитива — согласование получает политику из раннера.
- [x] 1.3 Покрыть тестами: живое задание (свежий `updated_at`) не согласуется; осиротевшее с исчерпанными попытками → `failed` с кодом; осиротевшее с запасом попыток → `queued`; с признаком отмены → `cancelled`. Проверка: `go -C backend/platform test ./jobs/...`.

## 2. Хранилища заданий

- [x] 2.1 Обновить `memory.SearchJobStore.ReconcileRunning`: аренда по `updatedAt`, лимит по `attempts`, исход `failed` с кодом и формулировкой, счётчики исходов. Проверка: тесты in-memory хранилища.
- [x] 2.2 Обновить `postgres.SearchJobStore.ReconcileRunning`: согласование по `status='running' AND updated_at < staleBefore` с ветвлением на отмену, лимит попыток и возврат в очередь; вернуть счётчики. Проверка: тесты postgres-хранилища.
- [x] 2.3 Обновить контрактный тест `internal/ports/contracttest/search_job_store.go` под новую сигнатуру и новые гарантии (аренда, лимит, отмена). Проверка: `go -C backend/contexts/lead-management test ./...`.

## 3. Код отказа прерывания

- [x] 3.1 Добавить в `internal/app/search_job_error.go` код `lifecycle.interrupted` и его формулировку; сделать формулировку доступной извне пакета. Проверка: тест соответствия кода и сообщения.

## 4. Сборка

- [x] 4.1 В `service.go` передать в раннер срок аренды, лимит попыток, код и формулировку прерывания; обновить логирование результата согласования под счётчики исходов. Проверка: сборка сервиса и тест роли worker.

## 5. Проверка

- [x] 5.1 Прогнать `go -C backend/platform test ./...`, `go -C backend/contexts/lead-management test ./...`, `go -C tools/archlint run .`. Проверка: всё зелёное, нарушений границ нет.
