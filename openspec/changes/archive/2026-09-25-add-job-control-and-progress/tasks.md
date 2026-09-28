## 1. Примитив jobs: отмена и статус

- [x] 1.1 Добавить `jobs.StatusCancelled` и расширить `Status.Valid()` в `backend/platform/jobs/jobs.go`; проверить тестом валидности статуса.
- [x] 1.2 Объявить sentinel `jobs.ErrCancelled` и маппинг исходов в `Runner.RunOnce`: `nil → done`, `ErrCancelled → cancelled`, иная ошибка → `failed`; проверить unit-тестами на все три исхода.
- [x] 1.3 Ввести `jobs.CancelRegistry` (регистрация `context.CancelFunc` по id, сигнал, снятие регистрации) без доменных терминов; проверить unit-тестами регистрации/сигнала/идемпотентного снятия.
- [x] 1.4 В `Runner` создавать дочерний `context.WithCancel` на задание, регистрировать его в `CancelRegistry`, различать отмену задания и остановку процесса по признаку запроса отмены; проверить тестом «отмена во время выполнения отдаёт статус cancelled, остановка процесса — нет».
- [x] 1.5 Расширить интерфейс `jobs.Store` методом `RequestCancel(ctx, id) error` (атомарно `queued → cancelled`, для `running` — флаг `cancel_requested`, для терминальных — no-op); проверить через контрактный suite хранилища заданий.

## 2. Порты и прикладной слой lead-management

- [x] 2.1 В `backend/contexts/lead-management/internal/ports/search_job_store.go` добавить `MaxCredits int` и `Progress *SearchJobProgress` в `SearchJob`, тип `SearchJobProgress` (фаза, URL, страницы, компании, кредиты, время), методы `CreateSearchJob(ctx, query, maxCredits)`, `RecordProgress(ctx, id, progress)` и валидацию бюджета; проверить компиляцией порта и unit-тестами.
- [x] 2.2 В `Payload` задания добавить опциональный `maxCredits` (кодирование/декодирование), сохранив совместимость со старыми заданиями; проверить unit-тестом round-trip и чтения старого payload.
- [x] 2.3 В `RunSearch` брать бюджет из задания, а при нуле — из `RunSearch.MaxCredits` при `Budget.Begin`; проверить unit-тестом «бюджет задания переопределяет дефолт».
- [x] 2.4 Добавить `RunSearch.Cancel(ctx, jobID)`, вызывающий `Jobs.RequestCancel` и сигнал реестра отмены; проверить unit-тестами отмены `queued`, `running` и идемпотентности.
- [x] 2.5 Расставить проверки отмены на границах шагов в `handle`/`processTarget`/`crawlURL`/`processPage` и возвращать `jobs.ErrCancelled` без отката сохранённых карточек; проверить unit-тестом «отмена на середине оставляет карточки уже обойдённых страниц».
- [x] 2.6 Ввести константы фаз и запись прогресса в `RunSearch` (на смене фазы и по каждому URL, с коалесцированием постраничных счётчиков); проверить unit-тестом последовательности фаз и итогового значения.
- [x] 2.7 Прокинуть общий `jobs.CancelRegistry` в `RunSearch` и в `jobs.Runner` через composition root (`backend/contexts/lead-management/service.go`, `backend/cmd/lead-gen/runners.go`); проверить сборкой и тестом `TestBuildLeadManagementService`.

## 3. Хранилища заданий

- [x] 3.1 Добавить миграцию `backend/contexts/lead-management/migrations/green/0005_search_job_control.sql` (колонки `max_credits`, `cancel_requested`, `progress`); проверить тестом миграций.
- [x] 3.2 Реализовать в postgres-адаптере `search_job_store.go` запись/чтение `max_credits`, `cancel_requested`, `progress`, а также `RequestCancel` и `RecordProgress`; проверить контрактным suite и тестом postgres-адаптера.
- [x] 3.3 Отразить те же поля и методы в memory-адаптере `search_job_store.go`; проверить контрактным suite.
- [x] 3.4 Расширить контрактный suite `internal/ports/contracttest/search_job_store.go` сценариями отмены `queued`, отказа отмены терминального задания и записи прогресса; проверить прогоном suite на обеих реализациях.

## 4. Адаптер Firecrawl: сохранение результата при отмене

- [x] 4.1 В `backend/contexts/integration-platform/internal/adapters/firecrawl/firecrawl.go` при отменённом контексте возвращать уже разобранные страницы вместе с признаком отмены и делать одну финальную попытку снять текущий снимок асинхронного задания; проверить unit-тестом на httptest-сервере.
- [x] 4.2 Научить `lead-management` трактовать признак отмены адаптера как чистую остановку (обработать возвращённые страницы, затем вернуть `jobs.ErrCancelled`); проверить unit-тестом с фейковым crawl-портом.

## 5. Консоль: отмена, прогресс, бюджет запроса

- [x] 5.1 Добавить маршрут `POST /jobs/{id}/cancel` и обработчик в `backend/contexts/lead-management/internal/adapters/http/server.go`, вызывающий `RunSearch.Cancel`; проверить тестом обработчика.
- [x] 5.2 Добавить в форму поиска поле бюджета кредитов и валидацию неположительного/некорректного значения до постановки в очередь; проверить тестом формы.
- [x] 5.3 Показать в журнале прогресс (фаза, текущий URL, страницы, компании, кредиты), статус `cancelled` и кнопку отмены в `views.go` и шаблонах компонентов; проверить тестом рендера и golden-тестом.
- [x] 5.4 Включить автообновление фрагмента `/jobs` через HTMX-опрос, пока есть выполняющееся задание; проверить тестом, что фрагмент содержит атрибут опроса при наличии `running`-задания.
- [x] 5.5 Убедиться, что прогресс и логи не содержат ключей, токенов и персональных данных; проверить тестом на отсутствие чувствительных полей.

## 6. Архитектура и документация

- [x] 6.1 Добавить capability `search-job-control` и `search-job-progress` в список `capabilities` контекста `lead-management` в `architecture/domain-catalog.yaml`; проверить `go -C tools/archlint run .`.
- [x] 6.2 Обновить `docs/domain-model.md` (привязка новых capability, размещение api/worker, отсутствие новых сущностей); проверить согласованность с каталогом.
- [x] 6.3 Прогнать `go -C tools/archlint run .` и `go -C tools/importlint run .` и убедиться в отсутствии нарушений границ.

## 7. Сквозная проверка

- [x] 7.1 Прогнать `go test ./...` в модулях `backend/platform`, `backend/contexts/lead-management`, `backend/contexts/integration-platform`, `contracts/integration-platform` и `backend/cmd/lead-gen`.
- [x] 7.2 Сценарий end-to-end: создать задание с бюджетом, увидеть прогресс, отменить выполняющийся обход и убедиться, что карточки по обойдённым страницам сохранены, а задание имеет статус `cancelled`; зафиксировать результат прогона.
