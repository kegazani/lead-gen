## 1. Охраняемые переходы в примитиве `jobs`

- [x] 1.1 Изменить контракт `jobs.Store.UpdateStatus` на `UpdateStatus(ctx, id string, from, to Status, reason, code string) error` и добавить sentinel `jobs.ErrTransitionRejected`; убедиться, что `go -C backend/platform build ./...` проходит и компилятор требует исходное состояние у вызывающих.
- [x] 1.2 Обновить `Runner.finish` (и `Runner.RunOnce`) под новую сигнатуру: завершение идёт как `running -> to`, а `ErrTransitionRejected` трактуется как «задание уже терминально» (info-лог), не останавливая цикл; проверить тестами `go -C backend/platform test ./jobs/...`.
- [x] 1.3 Обновить хранилище-память `backend/contexts/lead-management/internal/adapters/persistence/memory/search_job_store.go`: переход применяется только при совпадении текущего состояния с `from`, иначе `jobs.ErrTransitionRejected`; терминальные состояния неизменяемы.
- [x] 1.4 Обновить postgres-хранилище `.../persistence/postgres/search_job_store.go`: `updateSearchJobStatus` получает `AND status = $from`, ноль строк даёт `ErrTransitionRejected`; проверить контракт-тестами.
- [x] 1.5 Обновить контракт-тест `internal/ports/contracttest/search_job_store.go`: убрать прямой `queued -> done` (сначала `TakeQueued`), добавить проверки «недопустимый переход отклонён» и «позднее завершение не переворачивает терминальное состояние»; проверить `go -C backend/contexts/lead-management test ./...`.

## 2. Решительная отмена

- [x] 2.1 В обоих хранилищах изменить `RequestCancel`: `running` переходит в `cancelled` и получает `cancel_requested = TRUE`; `queued` — как прежде; терминальные — no-op; проверить, что после отмены `running`-задание сразу читается как `cancelled`.
- [x] 2.2 Обновить контракт-тест отмены: `RequestCancel` для `running` даёт `cancelled` немедленно, повторная отмена идемпотентна, позднее `UpdateStatus(..., done)` отклоняется с `ErrTransitionRejected`.
- [x] 2.3 Ограничить дренаж уже полученных страниц при отмене в `internal/app/run_search.go` явным пределом (вместо мёртвой проверки `pageCtx.Err()` после `context.WithoutCancel`) и покрыть тестом «отмена во время обхода сохраняет карточки по уже обойдённым страницам».
- [x] 2.4 Проверить e2e-сценарий `TestCancelRunningJobEndToEnd` в `backend/contexts/lead-management/e2e_test.go`: задание завершается `cancelled`, результат сохранён, ошибка не записывается.

## 3. Распознавание отмены по данным

- [x] 3.1 Добавить в `jobs.Store` метод `FindByID(ctx, id string) (*Job, error)` и sentinel `jobs.ErrNotFound`; реализовать в обоих хранилищах `lead-management`.
- [x] 3.2 В `Runner.RunOnce` запустить наблюдатель: пока обработчик работает, он по интервалу опроса читает задание и при `Status == cancelled` или `CancelRequested == true` отменяет контекст задания; закрыть наблюдатель по завершении обработчика.
- [x] 3.3 Покрыть тестами примитива гонку «взято → сигнал не доставлен»: отмена, зафиксированная в хранилище после `TakeQueued` и до реакции, приводит к остановке обработчика и исходу `cancelled`.
- [x] 3.4 Проверить сценарий раздельных процессов на интеграционном уровне: отмена через хранилище без сигнала в процессе-обработчике останавливает задание на границе шага.

## 4. Согласование осиротевших заданий

- [x] 4.1 Добавить в `jobs.Store` метод `ReconcileRunning(ctx) (int, error)`: `running + cancel_requested` → `cancelled`, иначе → `queued`, с сохранением `attempts`; реализовать в обоих хранилищах.
- [x] 4.2 Вызвать согласование однократно при старте роли `worker` (перед первым `TakeQueued`) в `backend/contexts/lead-management/service.go`; проверить, что задание, оставшееся `running` от прежнего процесса, перестаёт быть активным.
- [x] 4.3 Покрыть тестом: согласование не трогает `queued` и терминальные задания, а осиротевшее `running` становится `queued`/`cancelled`.

## 5. Наблюдаемость отмены

- [x] 5.1 В `RunSearch.Handle` различать `jobs.ErrCancelled`: info-лог «задание отменено» вместо `Error` «задание упало» и метрика исхода `cancelled` вместо `failed`; проверить тестом на отсутствие error-уровня при отмене.
- [x] 5.2 Убедиться, что при отмене не записывается событие ошибки задания, а журнал показывает `cancelled` без текста ошибки (контракт-тест и тест витрины).

## 6. Схема данных

- [x] 6.1 Добавить миграцию `backend/contexts/lead-management/migrations/green/0008_job_state_transitions.sql`: `CHECK (status IN ('queued','running','done','failed','cancelled'))` и частичный индекс `(created_at, id) WHERE status = 'queued'`; проверить `go -C backend/contexts/lead-management test ./migrations/...`.

## 7. Интеграция и проверки

- [x] 7.1 Обновить затронутые тесты и эталоны витрины журнала (`internal/adapters/http`), если разметка/статусы изменились; проверить `go -C backend/contexts/lead-management test ./...`.
- [x] 7.2 Прогнать архитектурную проверку `go -C tools/archlint run .` и убедиться в отсутствии нарушений границ.
- [x] 7.3 Прогнать полный набор: `go -C backend/platform test ./...`, `go -C backend/contexts/lead-management test ./...`, `go -C tools/archlint run .`; все тесты зелёные.
- [x] 7.4 Завести capability `search-job-lifecycle` в `architecture/domain-catalog.yaml` и `docs/domain-model.md` (владелец `lead-management`); проверить `go -C tools/archlint run .`.
