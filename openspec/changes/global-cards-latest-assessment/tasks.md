## 1. Порт: последняя оценка по компаниям

- [x] 1.1 Добавить читающий метод `LatestByCompany(ctx, companyIDs []string) (map[string]*domain.LeadAssessment, error)` в `backend/contexts/lead-management/internal/ports/assessment_store.go` с комментарием о выборе последней по `updatedAt`; `go build` модуля `lead-management` проходит
- [x] 1.2 Реализовать метод в memory-хранилище (`internal/adapters/persistence/memory/assessment_store.go`) и покрыть unit-тестом: для нескольких заданий возвращается оценка с максимальным `updatedAt`, компания без оценок отсутствует в результате; тест зелёный
- [x] 1.3 Реализовать метод в postgres-хранилище (`internal/adapters/persistence/postgres/assessment_store.go`) через `DISTINCT ON (company_id) ... ORDER BY company_id, updated_at DESC, created_at DESC, job_id`; запрос выполняется в тесте против одноразовой базы (`make test-postgres`)
- [x] 1.4 Расширить контрактный suite `internal/ports/contracttest/assessment_store.go` сценариями «последняя оценка по компании» и «компания без оценок»; suite проходит для memory и postgres

## 2. Общая витрина карточек по последней оценке

- [x] 2.1 Добавить в `internal/adapters/http/views.go` функцию `cardRowsWithLatestAssessments(cards, latest)`: «Подходит» → `cardGroupFits`; «Гипотеза»/«Не подходит» → `cardGroupOther` с подписью; нет оценки → `cardGroupOther`, подпись «Не оценено», класс `unknown`; unit-тест на три случая зелёный
- [x] 2.2 В `handleCards` (`internal/adapters/http/server.go`) для ветки без `?job=` читать последние оценки видимых компаний через `LatestByCompany` и строить строки новой функцией; добавить тест, что вторая группа непуста при наличии «Не подходит»/«Гипотезы» и содержит «Не оценено» для компании без оценок
- [x] 2.3 Обновить golden-файл `internal/adapters/http/testdata/cards.golden` под новую группировку; `go test ./internal/adapters/http/...` зелёный
- [x] 2.4 Проверить вручную на запущенном инстансе: `/cards` показывает непустую вторую вкладку «Неподходящие и гипотезы», переключение не зависает на пустом списке

## 3. Инкрементальная фиксация ResultRef

- [x] 3.1 В `internal/app/progress.go` дать `progressReporter` поставщик результата (`rep.result(func() []string)`) и записывать `RecordResult` в `flush` только при изменении набора (грязный флаг); ошибка логируется и не прерывает задание; unit-тест репортера зелёный
- [x] 3.2 В `internal/app/run_search.go` подключить поставщик `sortedCompanyIDs(companyIDs)` к репортеру, сохранив финальную запись результата; тест, что во время прогона (после нескольких целей) `ResultRef` непуст, зелёный
- [x] 3.3 Проверить, что отмена по-прежнему фиксирует итоговый `ResultRef` (`finishCancelled`) и что дублирующей записи в финале нет; существующие тесты `run_search*_test.go` зелёные

## 4. Экран задания: живое обновление и подсветка email

- [x] 4.1 Расширить `jobCompanyRow` полями `Email`/`NoEmail` и загружать контакты найденных компаний пакетно в `jobFrom`/`Showcase`, без запроса на каждую компанию; unit-тест `view`-слоя зелёный
- [x] 4.2 В `templates/job.html` добавить htmx-опрос контейнера экрана (`hx-get="/jobs/{id}" hx-trigger="every 2s" hx-swap="outerHTML"`) только для незавершённых заданий; шаблонный тест проверяет наличие опроса у `running` и отсутствие у `done`
- [x] 4.3 В `templates/job.html` рисовать строку найденной компании с email и классом `is-no-email` при отсутствии адреса, с текстовой пометкой и `aria-label` (как в `cards.html`); обновить golden `testdata/job.golden`; `go test ./internal/adapters/http/...` зелёный
- [x] 4.4 Проверить вручную на запущенном инстансе: экран `/jobs/{id}` выполняющегося задания показывает уже извлечённые компании, обновляется без перезагрузки, компании без email выделены красным

## 5. Сквозная проверка

- [x] 5.1 Добавить e2e-сценарий (`backend/contexts/lead-management/e2e_test.go`): глобальная витрина делит компании по последней оценке, а экран выполняющегося задания несёт извлечённые компании; тест зелёный
- [x] 5.2 Запустить `make check` (build, test, vet, fmt, lint) и `go -C tools/archlint run .` — все зелёные; доменных границ не меняли, поэтому `architecture/domain-catalog.yaml` и `docs/domain-model.md` не правим
- [x] 5.3 Запустить `openspec validate global-cards-latest-assessment --strict` и убедиться, что изменение валидно
