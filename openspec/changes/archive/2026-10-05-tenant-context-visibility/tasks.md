## 1. Список тенантов (владелец `platform-governance`)

- [x] 1.1 Query-порт и сценарий списка тенантов (id, компания, статус) без персональных данных. Проверка: unit-тест сценария на in-memory и PostgreSQL-хранилище. **Сделано:** `internal/ports/tenant_directory.go`, `internal/app/query/tenant_list.go` (+тест).
- [x] 1.2 Адаптер хранилища: выборка тенантов (PostgreSQL + in-memory); при необходимости — индекс. Проверка: контрактный suite хранилища зелёный (`make test-postgres`). **Сделано:** `List` в обоих адаптерах (сортировка `company_name, id`); индекс не нужен.
- [x] 1.3 Экран/раздел консоли со списком тенантов и выбором. Проверка: golden-тест экрана; выбор доступного тенанта — успех, недоступного — серверный отказ. **Сделано:** `GET /console/tenant` (`console_tenant.html`), выбор через существующий `/console/tenant/switch` (`SectionTenant`/`ActionSwitch`), golden + HTTP-тесты.

## 2. Активный тенант в консолях

- [x] 2.1 Консоль лидов (`lead-management`): активный тенант из запроса с фолбэком на `-tenant`. Проверка: unit/HTTP-тест «tenant_id в запросе меняет выборку, пустой — прежнее поведение». **Сделано:** `activeTenant(r)` (query/форма → фолбэк `-tenant`) + тенант-скоупное хранилище в отдельном change `tenant-scoped-lead-storage` (read-выборка по тенанту достижима). Post: HTTP-тест `TestLeadConsoleScopesCardsByTenant` («два тенанта — витрина показывает только активный, чужой тенант — 404») и контрактный suite хранилища.
- [x] 2.2 Консоль отчётов/дашборда (`analytics`): то же. Проверка: HTTP-тест. **Уже было:** `analytics` читает `tenant_id` из запроса (`handleDashboard`, `reportQueryFrom`, `handleStream`) — доработок не требуется.
- [x] 2.3 Сверка очереди утверждения (`request-fulfillment`) и экранов `platform-governance` на единый способ определения активного тенанта. Проверка: тесты экранов не расходятся. **Проверено:** обе консоли читают `tenant_id` из запроса/формы (`request-fulfillment` — `tenant_id` в запросе/форме; `platform-governance` — `r.URL.Query().Get("tenant_id")`/`PostFormValue`, `handlers_console.go`); расхождений нет.

## 2а. Новый change: тенант-скоупное хранилище лидов (находка 2.1)

- [x] 2a.1 Отдельный change `tenant-scoped-lead-storage`: колонка тенанта в таблицах `lead-management`, фильтрация чтения в ports/memory/postgres, миграции, контрактные suite. **Создан и реализован:** change `tenant-scoped-lead-storage` (proposal/specs/design/tasks, `openspec validate --strict` valid); колонка `tenant_id` в `lead_profile` + индекс, скоупинг в memory/postgres, миграция green, контрактный suite с изоляцией тенантов. Живая приёмка 4.2 пройдена (двухтенантный HTTP-сценарий на раздельной топологии) — все задачи change закрыты.

## 3. Контекст тенанта в навигации

- [x] 3.1 `frontend/components`: ссылки навигации несут `tenant_id`/`actor`. Проверка: unit-тест построения ссылок + обновлённые golden всех консолей. **Сделано:** `ConsoleContext` + `ConsoleNavWithContext`/`ConsoleHrefWithContext` (старые `ConsoleNav`/`ConsoleNavMessages` без контекста не меняют ссылки); unit-тесты в `frontend/components/console_test.go`; golden `lead-management` (10 файлов) и `request-fulfillment` (3) перегенерированы; утверждение `outreach` обновлено.
- [x] 3.2 Проводка контекста в `backend/cmd/lead-gen` и потребителях. Проверка: сборка и тесты консолей зелёные. **Сделано:** `lead-management` (`consoleSidebar(active, tenant, actor)`, `consoleActor`), `request-fulfillment` (`consoleNav(active, tenant, actor)`), `outreach-orchestration` (`consoleNav(active, tenant, actor)`), `analytics` (`navContext(r)`); правка `cmd/lead-gen` не потребовалась (адреса сервисов уже читаются из ENV). Проверено живьём: навигация всех четырёх консолей несёт активный тенант (и актора, где есть сессия) при переходе между сервисами.

## 4. Приёмка и гейт

- [x] 4.1 Сквозной сценарий на живом контуре: клиент регистрируется → админ видит тенант клиента → открывает его заявки и видит заявку. **Проверено живьём (docker compose, раздельная топология):** `POST /register` (platform-governance, `:8087`) создал тенант `tenant-84374cb9eda415643a6f36ea67e33a95`; `GET /console/tenant` для админа вернул список с этим тенантом и компанией; `POST /portal/request` создал заявку `req-20b4a5d6785643958ec487a0527a70c6`; `GET :8085/validation?tenant_id=<тенант клиента>` показал о заявке («Нужны лиды в промышленном секторе»). Шаг «одобряет» не выполнялся: он требует прохождения workflow с внешним движком (по условию задачи не нужен). Предусловия dev-контура: пароль PostgreSQL volume приведён к `.env`; провижининг platform-admin в контуре не задан, поэтому админ-пользователь подсеян в БД как предусловие сценария (вне кода).
- [x] 4.2 `make check` (build/test/vet/importlint/archlint/openspec). Проверка: зелёный. **Проверено:** build/test/vet/importlint — ok; `go -C tools/archlint run .` — код 0; OpenSpec `53 passed, 0 failed`.
- [x] 4.3 `make test-postgres`. Проверка: 11 suite `ok`. **Проверено:** 11 suite `ok`.
- [x] 4.4 `openspec validate tenant-context-visibility --strict`. Проверка: valid. **Проверено:** `Change 'tenant-context-visibility' is valid`.
