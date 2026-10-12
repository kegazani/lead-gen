## 1. Каталог, модель и диаграммы

- [x] 1.1 Объявить capability `search-recommendation` и контракт `recommendation-apply` у контекста `lead-management` в `architecture/domain-catalog.yaml`; добавить связь `search-feedback-from-analytics` (`upstream: lead-management`, `downstream: analytics`, `pattern: customer-supplier`, `integration: command`, `transport: sync`, `mechanism: http-json`, `contract: recommendation-apply`). Проверка: `go -C tools/archlint run .` — код 0.
- [x] 1.2 Обновить `docs/domain-model.md`: строка capability matrix `search-recommendation`, абзац «не добавляет модель» и абзац межконтекстных отношений про обратную связь анализа и read-контракт `lead-relevance`. Проверка: описание согласовано с каталогом.
- [x] 1.3 Добавить ребро `analytics → lead-management` (контракт `recommendation-apply`) в `architecture/diagrams/context-relationships.puml`. Проверка: `go -C tools/archlint run .` — код 0.

## 2. Контракт `recommendation-apply` (владелец lead-management)

- [x] 2.1 Опубликовать пакет `contracts/lead-management/recommendationapply`: `Change{Kind, Criteria, Targets, Limit}`, `Application{TenantID, ChangeID, ScopeKind, ScopeKey, Reason, Basis, Change}`, `Receipt{JobID}`, `Port`, `ErrRejected`/`ErrUnavailable`, `InProc` + `httpport.Client` (`POST {base}/recommendation-apply`). Проверка: round-trip тест контракта (InProc) и httpport (`httptest`).
- [x] 2.2 Отличать дубликат задания от прочих сбоев: `ports.ErrSearchJobExists` в memory- и postgres-адаптерах `SearchJobStore` (postgres — `ON CONFLICT DO NOTHING` с `RowsAffected()==0`). Проверка: тест `Create` с существующим `id` даёт `ErrSearchJobExists`.
- [x] 2.3 Реализовать сценарий `app.ApplyRecommendation` поверх `ports.SearchJobStore`: валидация (`Criteria` непуст, `Limit >= 0`), детерминированный `id` по (тенант, `ChangeID`), постановка задания, идемпотентный повтор через `FindByID`. Проверка: тесты создания, идемпотентности, отказа (пустой критерий) и недоступности.
- [x] 2.4 Фабрика `leadmanagement.NewRecommendationApply(deps)` + эндпоинт роли `api` `POST /recommendation-apply`; перевод ошибок сценария в `ErrRejected`/`ErrUnavailable`. Проверка: HTTP round-trip тест.

## 3. Read-контракт `lead-relevance` (владелец lead-management)

- [x] 3.1 Опубликовать пакет `contracts/lead-management/leadrelevance`: `Request{JobID, CompanyID}`, `Assessment{JobID, CompanyID, Verdict, SignalKind, Evidence[], AssessedAt}`, `Evidence`, `Port`, `ErrRejected`/`ErrNotFound`/`ErrUnavailable`, `InProc` + `httpport.Client` (`GET {base}/lead-relevance`). Проверка: round-trip тест контракта и httpport.
- [x] 3.2 Фабрика `leadmanagement.NewLeadRelevance(deps)` поверх `Repositories.Assessments` (`GetByJobAndCompany`) + эндпоинт роли `api` `GET /lead-relevance`; перевод `ports.ErrAssessmentNotFound` в `ErrNotFound`. Проверка: HTTP round-trip тест; отсутствие оценки — `ErrNotFound`, а не выдуманный вердикт.

## 4. Потребитель (владелец контура `analytics`)

- [x] 4.1 Расширить `analytics/internal/ports.RecommendationChange` до структурного `Change SearchChange{Kind, Criteria, Targets, Limit}`; обновить `command.ApplyRecommendationHandler` (проброс структурного изменения, запись подписи применения) и HTTP-тело применения. Проверка: тесты `command` и `http` зелёные.
- [x] 4.2 Реализовать адаптер потребителя `analytics/internal/adapters/feedback` (`ports.QualityFeedback` поверх `recommendationapply.Port`): вывод ключа идемпотентности, перевод `Receipt`/ошибок. Подключить в `analytics.Build` (`Config.RecommendationApply`). Проверка: тест адаптера (успех/отказ/недоступность) и что `ApplyRecommendationHandler` перестаёт отдавать `503` при подключённом контракте.

## 5. Гейт

- [x] 5.1 `cd backend/contexts/lead-management && go test ./... && go vet ./...`. Проверка: зелёные.
- [x] 5.2 `cd backend/contexts/analytics && go test ./... && go vet ./...`. Проверка: зелёные.
- [x] 5.3 `go -C tools/importlint run .` — 0 запрещённых рёбер; `go -C tools/archlint run .` — код 0; `openspec validate search-quality-feedback --strict` и `intelligent-lead-operations --strict` — valid.

## Отложено (вне зоны этого change)

- Проводка в `backend/cmd/lead-gen`: `analytics.Config.RecommendationApply` ← `leadmanagement.NewRecommendationApply(...)` (co-location) или `recommendationapply/httpport.New(...)`; имена API даны в отчёте.
- Потребление read-контракта `lead-relevance` блоком `classify` (`agent-orchestration`) — зона владельца контекста.
- Производные счётчики каталога в `backend/platform/{topology,deployment}` (46 → 47 связей) — вне зоны; обновляются головным агентом.
