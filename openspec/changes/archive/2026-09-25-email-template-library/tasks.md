## 1. Доменная модель: агрегат `email-template`

- [x] 1.1 Реализовать value objects агрегата: имя, тема, тело шаблона (непустые после обрезки пробелов) и `template-reference` (ссылка вида `template_id@revision`) (верификация: unit-тесты отвергают пустые имя/тему/тело и собирают ссылку из идентификатора и ревизии)
- [x] 1.2 Реализовать value object `merge-variable` (ключ каталога + непустой fallback) и канонический набор ключей переменных в домене (верификация: тест отвергает ключ вне набора и пустой fallback, принимает известный ключ)
- [x] 1.3 Реализовать чистую функцию разбора плейсхолдеров `{{ … }}` в домене на основе существующего наивного разбора `renderMerge` (верификация: тест извлекает все плейсхолдеры темы и тела, включая повторяющиеся, и документирует отсутствие поддержки вложенности)
- [x] 1.4 Реализовать aggregate root `email-template` с методами создания, правки и удаления (`CreateTemplate`, `EditTemplate`, `DeleteTemplate`), увеличивающими `revision` при правке (верификация: тест подтверждает рост `revision` при каждой правке и неизменность идентификатора)
- [x] 1.5 Реализовать инварианты агрегата: объявленные переменные — подмножество каталога; у каждой объявленной переменной непустой fallback; каждый плейсхолдер темы и тела объявлен (верификация: тесты I1 — шаблон с необъявленным плейсхолдером не создаётся; I2 — переменная вне каталога и переменная без fallback отвергаются; I3 — объявленная, но неиспользуемая переменная допустима)
- [x] 1.6 Реализовать Snapshot и Rehydrate агрегата с проверкой инвариантов при восстановлении (верификация: тест восстанавливает шаблон и отвергает снимок с нарушением инварианта)

**Выполнено:** `internal/domain/email_template.go` (VO `TemplateID`, `MergeVariable`, `TemplateReference`, функция `Placeholders`, AR `EmailTemplate` с `CreateTemplate`/`EditTemplate`/`DeleteTemplate`, снимок и `RehydrateEmailTemplate`), ошибки в `errors.go`, тесты `email_template_test.go`.

## 2. Каталог переменных и сборка письма

- [x] 2.1 Реализовать в прикладном слое каталог переменных: для каждого ключа — резолвер значения из `domain.Company`/`domain.Contact` и человекочитаемая подпись (верификация: тест сопоставляет каждый ключ каталога с полем-источником; ключ без источника резолвится в пусто)
- [x] 2.2 Реализовать `ComposeEmail(template, company, contact)`, берущий значения из каталога, а fallback — из объявленных переменных шаблона; тема и тело собираются одним разбором (верификация: тест собирает письмо, подставляет fallback при пустом значении и не оставляет необъявленных плейсхолдеров)
- [x] 2.3 Заменить глобальные merge-поля и `defaultFallbacks` на объявленные переменные шаблона; сохранить или удалить `RenderEmail`/`renderMerge` без потери покрытия тестами (верификация: существующие тесты подстановки и fallback переведены на `ComposeEmail` и проходят)
- [x] 2.4 Покрыть тестами серверную валидацию: шаблон с необъявленным плейсхолдером отклоняется независимо от проверок в представлении (верификация: тест прикладного слоя отклоняет сохранение и сообщает необъявленный плейсхолдер)

**Выполнено:** `internal/app/render.go` (каталог `VariableCatalog`, `resolveVariable`, `ComposeEmail`/`ComposeEmailWithValues`); глобальные `mergeCompany`/`mergeContact`/`mergeIndustry`, `defaultFallbacks` и `RenderEmail` удалены; `render_test.go` переписан на `ComposeEmail`.

## 3. Порты и хранилище шаблонов

- [x] 3.1 Определить порт `EmailTemplateRepository`: загрузка и сохранение агрегата, поиск по имени, теме, телу и объявленным переменным (верификация: пакет компилируется без импорта драйвера БД; сигнатуры отражают поисковый запрос)
- [x] 3.2 Реализовать in-memory адаптер `EmailTemplateRepository` (верификация: contract suite поиска и сохранения проходит против in-memory-реализации)
- [x] 3.3 Реализовать PostgreSQL-адаптер `EmailTemplateRepository` и миграцию зелёной зоны (верификация: тот же contract suite проходит против PostgreSQL; миграция применяется и откатывается на тестовом DSN)
- [x] 3.4 Проверить, что поиск выполняется на сервере и нормализованно, по имени, теме, телу и объявленным переменным (верификация: тесты находят шаблон по каждому полю и по вхождению в объявленную переменную)

**Выполнено:** `internal/ports/email_template_repository.go` (порт + общие `NormalizeTemplateQuery`/`TemplateMatches`/`SortTemplates`), `adapters/persistence/memory/email_template_repository.go`, `adapters/persistence/postgres/email_template_repository.go`, миграция `migrations/green/0009_email_template.sql`, contract suite `contracttest/email_template_repository.go` (прогнан против памяти и PostgreSQL). Миграция применена и откатана на тестовой базе через `psql` (порт контейнера не опубликован на хост, поэтому прогон шёл через `make test-postgres` и разовый контейнер `postgres:16-alpine` с DSN).

## 4. Прикладные сервисы

- [x] 4.1 Реализовать use cases `CreateTemplate`, `EditTemplate`, `DeleteTemplate`, `GetTemplate` (верификация: тесты создают, правят и удаляют шаблон; правка повышает `revision`)
- [x] 4.2 Реализовать use case `ListTemplates(query)` с поиском и пустым состоянием (верификация: запрос фильтрует список; пустой результат возвращается как пустой список без ошибки)
- [x] 4.3 Реализовать use case предпросмотра/сборки `ComposeEmail` на основе выбранного шаблона и лида (верификация: тест возвращает собранные тему и тело с подставленными значениями и fallback)

**Выполнено:** `internal/app/template.go` — сервис `Templates` с методами `CreateTemplate`, `EditTemplate`, `DeleteTemplate`, `GetTemplate`, `ListTemplates`, `Catalog`, `Compose`; тесты `template_test.go`.

## 5. Отправка: связь с `outbound-message`

- [x] 5.1 Перевести `SendToLead`/`SendToSegment` с свободных `templateRef`/`templateBody` на идентификатор шаблона библиотеки (верификация: тест отправки собирает письмо из шаблона библиотеки и фиксирует в `outbound-message` ссылку `template_id@revision` и собранное тело)
- [x] 5.2 Сохранить свойство неизменяемости истории: правка шаблона после отправки не меняет ранее зафиксированные ссылку и тело (верификация: тест правит шаблон после отправки и подтверждает неизменность сохранённого сообщения)
- [x] 5.3 Обновить существующие тесты отправки, сегмента и истории под новую семантику ссылки (верификация: `go test ./...` в контексте проходит)

**Выполнено:** `SendToLead.Send(ctx, leadID, templateID, overrides, idempotencyKey)` и `SendToSegment.Send(ctx, leadIDs, templateID, batchKey)` собирают письмо через `ComposeEmail`/`ComposeEmailWithValues` и фиксируют `template.Reference().String()`; тесты отправки и сегмента переведены на библиотеку, добавлен тест неизменяемости.

## 6. HTTP-адаптеры и экраны

- [x] 6.1 Реализовать маршруты библиотеки: `GET /templates`, `POST /templates`, `GET /templates/{id}/edit`, `POST /templates/{id}`, `POST /templates/{id}/delete` (верификация: интеграционный тест создаёт и правит шаблон через HTTP)
- [x] 6.2 Реализовать экран редактирования шаблона: имя, тема, тело, перечень переменных каталога и вставка переменной в тему/тело (верификация: golden-тест экрана содержит перечень переменных; вставка переменной добавляет её в объявленный набор)
- [x] 6.3 Реализовать список шаблонов сбоку как общий фрагмент и встроить его в `/send` и в карточку лида `/cards/{id}/send` (верификация: golden-тесты обоих экранов содержат список шаблонов; разметка списка не дублируется)
- [x] 6.4 Реализовать поиск по шаблонам через GET с HTMX-фрагментом и полной страницей по прямому запросу (верификация: тест фрагмента фильтрует список; тест без заголовка HTMX отдаёт полную страницу)
- [x] 6.5 Реализовать выбор шаблона, подстановку значений переменных и предпросмотр собранного письма на экранах отправки (верификация: интеграционный тест показывает предпросмотр с подставленными значениями и fallback)
- [x] 6.6 Обеспечить, что ошибки валидации возвращаются сервером и отображаются у формы, а доменные правила не дублируются в `html/template` и JS (верификация: тест на отсутствие доменных импортов в представлении; golden-тест показывает серверную ошибку необъявленного плейсхолдера)

**Выполнено:** страница `templates/templates.html` (форма, общие фрагменты `template-list`, `template-picker`, `variable-values`, `email-preview`), обновлённые `send.html` и `card.html`, обработчики и маршруты в `server.go`, модели в `views.go`, вставка переменной в `static/console.js`, стили `.template-list`/`.variables`/`.preview` в `console.css`. Golden-тесты: `card.golden`, `send.golden`, `template-edit.golden`, `template-error.golden`; тесты `templates_test.go` и `views_rules_test.go`.

## 7. Доменная привязка и каталог

- [x] 7.1 Добавить в `architecture/domain-catalog.yaml` агрегат `email-template` (owner context `lead-management`, root entity `email-template`, entities, value objects `merge-variable`/`template-reference`, пустой список domain events и policies, инварианты) (верификация: `go -C tools/archlint run .` завершается кодом 0)
- [x] 7.2 Зарегистрировать сущность `email-template` в разделе `entities` каталога (owner context `lead-management`, зона `green`, storage `relational`) (верификация: archlint принимает сущность и её единственного владельца)
- [x] 7.3 Добавить capability `email-template-library` в список capabilities контекста `lead-management` (верификация: archlint находит capability в `openspec/changes/email-template-library/specs/` и не сообщает об отсутствии)
- [x] 7.4 Обновить `docs/domain-model.md`: строка capability matrix, тактическая модель `lead-management` (новый агрегат, VO, инварианты), при необходимости — межконтекстные отношения (верификация: описание согласовано с каталогом, изменение границы `content-production` зафиксировано как «не затрагивается»)
- [x] 7.5 Обновить затронутые диаграммы, в частности `architecture/diagrams/data-ownership.puml` (новый владелец сущности), и проверить остальные обязательные диаграммы (верификация: `go -C tools/archlint run .` подтверждает маркеры контекстов на диаграммах)
- [x] 7.6 Выполнить `go -C tools/archlint run .` после правок каталога, модели и диаграмм (верификация: команда завершается кодом 0 и печатает число контекстов, агрегатов, сущностей и capability)

**Выполнено:** каталог (capability, сущность, агрегат, VO), `docs/domain-model.md` (строка capability matrix и тактическая модель `email-template`), `architecture/diagrams/data-ownership.puml` (сущность `email-template [green]`); `archlint: OK — контекстов: 14, агрегатов: 24, сущностей: 38, capability: 41, связей: 28`. Граница `content-production` не затрагивается — межконтекстный контракт не вводился.

## 8. Сквозная проверка

- [x] 8.1 Прогнать сквозной сценарий: создать шаблон с объявленными переменными → найти его поиском при отправке → собрать и предпросмотреть письмо → отправить → убедиться, что `outbound-message` зафиксировал ссылку и тело (верификация: интеграционный тест проходит целиком)
- [x] 8.2 Проверить запрет необъявленного плейсхолдера на сервере в обход представления (верификация: прямой POST с необъявленным плейсхолдером отклоняется)
- [x] 8.3 Проверить неизменяемость истории при правке шаблона после отправки (верификация: тест подтверждает, что прошлые сообщения не изменились)
- [x] 8.4 Выполнить `go test ./...`, `go test -race ./...`, `go vet ./...` и `go build ./...` (верификация: команды завершаются без ошибок)
- [x] 8.5 Выполнить `openspec validate email-template-library --strict` (верификация: команда завершается без ошибок)

**Выполнено:** `TestTemplateLibraryEndToEnd` (8.1), `TestTemplatesRejectUndeclaredPlaceholderViaServer` (8.2), `TestSendToLeadKeepsHistoryImmutableAfterTemplateEdit` (8.3); `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...` в модуле и в `backend/cmd/lead-gen` — без ошибок; `openspec validate email-template-library --strict` — «Change 'email-template-library' is valid».
