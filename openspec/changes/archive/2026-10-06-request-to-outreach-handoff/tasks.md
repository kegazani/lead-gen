## 1. Контракт передачи и расширение генерации

- [x] 1.1 Опубликовать `contracts/request-fulfillment/outreach-handoff`... **Сделано:** пакет `contracts/outreach-orchestration/outreachhandoff` (владелец поверхности — outreach-orchestration, контракт публикуется им; initiator — request-fulfillment) с формой `Command`/`Item`/`Result`/`Port`, `ErrRejected`/`ErrUnavailable`, `Validate()`; `httpport`-клиент и `inproc`; unit-тесты зелёные.
- [x] 1.2 Расширить `email-generation` (MINOR)... **Сделано:** `emailgeneration.Email.RecipientRef` (непрозрачная ссылка), `SchemaVersion` → `1.1.0`, клиент `httpport` читает `recipient_ref`; round-trip-тесты зелёные.
- [x] 1.3 Владелец `email-generation` заполняет `RecipientRef`... **Сделано:** `DraftEmailForLead` возвращает `RecipientRef` (id контакта с email, иначе пусто); HTTP-адаптер владельца отдаёт `recipient_ref`; unit/HTTP-тесты зелёные (`TestDraftEmailForLeadFillsRecipientRef`, `TestEmailGenerationHTTPRecipientRef`).

## 2. Приём передачи в `outreach-orchestration` (capability `outreach-handoff`)

- [x] 2.1 Прикладной сценарий приёма... **Сделано:** `command.SubmitHandoffHandler` создаёт кампанию `handoff-<request_id>` и черновики в `pending-approval`; идентификатор черновика — ссылка получателя (`release` переносит `draft_id` в `RecipientRef` approved-action), сегмент — лид; тесты in-memory.
- [x] 2.2 Идемпотентность и пустой состав... **Сделано:** повтор по `request_id` → `Repeated`, без дублей; частичный повтор сохраняет черновики; пустой состав кампанию не создаёт; тесты зелёные.
- [x] 2.3 Различение отказа и недоступности, наблюдаемость... **Сделано:** некорректная команда → доменный отказ; сбой транзакции/хранилища → недоступность; исход наблюдаем (`accepted`/`skipped`/`repeated`); тесты зелёные.
- [x] 2.4 Владельческий HTTP-адаптер приёма... **Сделано:** `POST /outreach-handoff` вне сессии консоли; round-trip тестом через контрактный клиент; 400/503/200 различены; ПД в ответе нет.
- [x] 2.5 Регистрация сценария и сетевая точка приёма... **Сделано:** сценарий собран в `Build` (`Slice.Handoff`), маршрут в `routes()`; `go build ./...` и тесты контекста зелёные.

## 3. Передача в `request-fulfillment`

- [x] 3.1 Хранить получателя письма... **Сделано:** миграция `0002_email_draft_recipient.sql` (`recipient_ref text NOT NULL DEFAULT ''`), поле в порту и адаптерах; контрактный suite (memory) зелёный; `make test-postgres` применил миграцию (`request-fulfillment/.../postgres` — ok).
- [x] 3.2 Заполнять `recipient_ref` при генерации... **Сделано:** `ContractDrafts.Draft` пробрасывает `email.RecipientRef`; тест `TestContractDraftsSavesRecipientRef`.
- [x] 3.3 Activity передачи (`HandoffDrafts`)... **Сделано:** `handoff.go` (`ContractHandoff`) читает черновики, для непустого `RecipientRef` производит материал через `content-request` (`Body = subject + "\n\n" + body`) и шлёт одну команду `outreach-handoff`; nil-контракты — штатный пропуск; workflow-тесты (недобор→передача, цель→нет, пропуск без получателя) зелёные.
- [x] 3.4 Версионирование графа workflow... **Сделано:** маркер `changeOutreachHandoff`; тест воспроизведения прежнего графа без передачи.

## 4. Проводка по топологии

- [x] 4.1 `backend/cmd/lead-gen`: собрать контракты... **Сделано:** порты `content-request` и `outreach-handoff` (сетевые клиенты при заданных адресах, nil — честный пропуск) + флаги/ENV `LEADGEN_CONTENT_REQUEST_URL`/`LEADGEN_OUTREACH_HANDOFF_URL`; добавлен `contentrequest/httpport`; `docker compose build` собирает весь продукт; compose задаёт адреса для `intake`.
- [x] 4.2 Раздельная топология: приём передачи по сети и передача сетью. Проверка: живой прогон. **Выполнено (живьём, раздельная топология `compose`):** `intake` сходил по сети `POST http://core:8080/outreach-handoff`; workflow заявки с недобором создал кампанию `handoff-req-handoff-e2e-20261006a` (тенант Дельта) с черновиком (`info@norbit.ru`, материал `email-req-handoff-e2e-20261006a-norbit.ru@…`); черновик доведён до `state=approved`, письмо захвачено mailpit (`To: info@norbit.ru`). Полная авто-цепочка «недобор → HandoffDrafts из workflow → очередь → утверждение → SMTP» замкнута.

## 5. Доменная модель и документация

- [x] 5.1 `architecture/domain-catalog.yaml`... **Сделано:** capability `outreach-handoff` у `outreach-orchestration`, контракт `outreach-handoff` в его `contracts`, связи `requests-to-outreach` и `content-for-requests`; archlint подтверждает корректность этого change (в списке ошибок только чужие `company-agent-orchestration`/`lead-quality-insights`/`operations-cockpit` из параллельного change `intelligent-lead-operations`; связи — 36).
- [x] 5.2 `docs/domain-model.md` и диаграммы... **Сделано:** capability и связи отражены; `context-relationships.puml`, `outreach-flow.puml` дополнены.
- [x] 5.3 `docs/implementation-matrix.md`, `docs/e2e-plan.md`, `deploy/README.md` при необходимости... **Сделано в `compose.yaml`/README-контуре** (адреса `content-request`/`outreach-handoff`); отдельной правки матрицы не требовалось.

## 6. Гейт и приёмка

- [x] 6.1 `make check`. **Выполнено:** `make check` — exit 0 (build/vet/importlint/archlint/openspec; внешние причины устранены синхронизацией каталога `intelligent-lead-operations` и правками другой сессии).
- [x] 6.2 `make test-postgres`. **Проверено:** все 11 suite `ok` (порт `PG_TEST_PORT=5439`, чтобы не конфликтовать с контуром).
- [x] 6.3 `openspec validate request-to-outreach-handoff --strict`. **Проверено:** valid (глобально `--all --strict` — 60 passed, 0 failed).
- [x] 6.4 Живая приёмка: заявка с недобором → письма автоматически попадают в очередь утверждения → оператор утверждает → письмо захвачено SMTP. **Выполнено (живьём):** полная авто-цепочка подтверждена: заявка `req-handoff-e2e-20261006a` с недобором → workflow `HandoffDrafts` → сетевая передача → кампания `handoff-req-handoff-e2e-20261006a` с черновиком `info@norbit.ru` → `state=approved` → relay → сетевой `approved-action` → `delivery` разрешил получателя/материал → mailpit захватил письма (`To: info@norbit.ru`, в т.ч. материал `email-req-handoff-e2e-20261006a-norbit.ru@…` и ранее `Тема передачи 3`).
