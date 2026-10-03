# Задачи: догоняющая документация шага генерации черновиков писем

Код уже реализован; изменение носит документационный характер. Каждый пункт выполнен и содержит способ проверки. Ни одна задача не меняет код и тесты.

## 1. Шаг генерации черновиков в workflow (capability `request-fulfillment-execution`)

- [x] 1.1 Сверить место шага в графе: `DraftEmails` идёт после поиска лидов и до гейта утверждения, фаза `drafting` — между `searching` и `awaiting-approval`, шаг запускается только при наличии лидов (`len(leadIDs) > 0`). Основание — `backend/contexts/request-fulfillment/internal/adapters/workflow/fulfillment.go` (фазы, `changeEmailDrafts`, блок 2.5); проверка — `TestFulfillmentDraftsEmailsBeforeApproval`, `TestFulfillmentSkipsDraftStepWithoutLeads`.
- [x] 1.2 Сверить пропуск шага без контракта и логирование: при `nil`-источнике activity `DraftEmails` штатно пропускается, исполнение не падает. Основание — `internal/adapters/workflow/activities.go` (`DraftEmails`), `internal/adapters/workflow/drafts.go` (`DraftGenerator`); проверка — `TestDraftEmailsNilGeneratorSkips`.
- [x] 1.3 Сверить версионирование шага: `workflow.GetVersion` с идентификатором `email-drafts` и `DefaultVersion` как веткой воспроизведения прежних исполнений. Основание — `fulfillment.go` (`changeEmailDrafts`); проверка — `TestFulfillmentReproducesOldGraph` (шаг не вызывается).

## 2. Толерантность к частичному отказу и payload-гигиена

- [x] 2.1 Сверить толерантность к отказу по отдельному лиду: письма под остальных лидов сохраняются, недобор виден счётчиком `Failed` и логируется. Основание — `internal/adapters/workflow/drafts.go` (`ContractDrafts.Draft`); проверка — `TestContractDraftsPartialFailureKeepsStepAlive`.
- [x] 2.2 Сверить дедуп и пропуск пустых лидов. Основание — `drafts.go` (`seen`-множество, проверка `TrimSpace`); проверка — `TestContractDraftsSkipsDuplicatesAndEmpty`.
- [x] 2.3 Сверить честные ошибки шага: отсутствие заявки и сбой сохранения завершают шаг ошибкой, а не выдуманным набором. Основание — `drafts.go` (`FindByID`, `SaveDrafts`); проверка — `TestContractDraftsMissingRequestFails`.
- [x] 2.4 Сверить payload-гигиену: activity отдаёт наружу только счётчики и идентификаторы лидов, текст письма оседает в хранилище; в `Status` попадает только `Drafted`. Основание — `drafts.go` (`DraftInput`/`DraftResult`), `internal/adapters/workflow/payload.go` (`draftInput`/`draftResult`, `Status`); проверка — `TestDraftEmailsReturnsCountersOnly`.

## 3. Хранилище черновиков (capability `request-fulfillment-execution`)

- [x] 3.1 Сверить порт `EmailDraftStore` и обе реализации: upsert по паре (заявка, лид) с сохранением места в порядке, чтение в порядке найденных лидов, изоляция по тенанту. Основание — `internal/ports/email_draft.go`, `internal/adapters/persistence/inmemory/draft_store.go`, `internal/adapters/persistence/postgres/draft_store.go`, миграция `migrations/green/0001_request.sql` (таблица `email_draft`, `position`, `PRIMARY KEY (request_id, lead_id)`).
- [x] 3.2 Сверить единый контрактный suite против обеих реализаций. Основание — `internal/ports/contracttest/draft_store.go` (`RunEmailDraftStoreContract`); проверка — `TestDraftStoreContract` (in-memory) и `TestEmailDraftStoreContract` (PostgreSQL).

## 4. Операторское чтение и наблюдаемость фазы

- [x] 4.1 Сверить маршрут и запрос чтения черновиков: `GET /requests/{id}/email-drafts`, форма ответа, изоляция по тенанту (404 для чужой/неизвестной заявки), отличие пустого списка от недоступного хранилища. Основание — `internal/adapters/http/handlers.go` (`handleEmailDrafts`), `internal/adapters/http/server.go` (маршрут), `internal/app/query/email_drafts.go`; проверка — `TestHTTPEmailDrafts`, `TestHTTPEmailDraftsEmpty`, `TestHTTPEmailDraftsUnknownRequest`.
- [x] 4.2 Сверить наблюдаемость фазы `drafting` и счётчика `Drafted` через query `status` и чтение состояния исполнения. Основание — `internal/adapters/workflow/fulfillment.go` (`PhaseDrafting`, `QueryStatus`), `internal/adapters/workflow/payload.go` (`Status.Drafted`), `internal/ports/workflow.go` (`WorkflowStatus.Drafted`), `internal/adapters/http/handlers.go` (`handleWorkflowStatus`).

## 5. Документация и проверки

- [x] 5.1 Уточнить описание покрытия `request-fulfillment-execution` в матрице `docs/domain-model.md` (шаг генерации черновиков, хранилище и операторское чтение, фаза `drafting`), не добавляя агрегатов, сущностей, событий и контрактов; `architecture/domain-catalog.yaml` и владение не меняются.
- [x] 5.2 Прогнать `openspec validate --all --strict`. Проверка: 0 ошибок.
- [x] 5.3 Прогнать `go -C tools/archlint run .`. Проверка: archlint зелёный — новых capability нет, покрытие «каждая capability каталога объявлена в OpenSpec» сохраняется.
- [x] 5.4 Синхронизировать дельты в `openspec/specs/request-fulfillment-execution/spec.md` и заархивировать change в `openspec/changes/archive/2026-10-03-document-email-drafting/`.
