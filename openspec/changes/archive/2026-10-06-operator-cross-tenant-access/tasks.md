## 1. Платформенный оператор в решении доступа

- [x] 1.1 `platformgovernance.Config` принимает состав платформенных операторов; решение о доступе (`DecideAccessHandler.Handle`/`CanAct`/`Roles`) выдаёт оператору права администратора платформы для любого активного тенанта, а `IsOperator` отвечает за право на список тенантов. Проверка: тесты `internal/adapters/http/handlers_operator_test.go` — оператор получает доступ к чужому активному тенанту и видит список тенантов; не-оператор без тенанта получает отказ.
- [x] 1.2 Проводка точки входа `backend/cmd/lead-gen`: логин оператора консолей (`-console-user`) передаётся в конфиг `platform-governance` как платформенный оператор (`cfg.Operators`). Проверка: `go build ./...` в `backend/cmd/lead-gen`; живой прогон (см. 4.1).

## 2. Активный тенант оператора в консолях

- [x] 2.1 Общий пакет `backend/platform/consolescope`: cookie `active_tenant` (`SetActiveTenant`/`ActiveTenant`) переносит выбор тенанта между консолями одного хоста (cookie не различает порт). Проверка: `go build ./...` в `backend/platform`.
- [x] 2.2 Экран выбора/переключения тенанта `platform-governance` открывает список без активного тенанта для оператора и записывает выбранный тенант в cookie. Проверка: тест `TestOperatorSeesTenantListWithoutActiveTenant`, `TestNonOperatorWithoutTenantDenied`, `TestOperatorSwitchRecordsConsoleScope`.
- [x] 2.3 Консоли `lead-management` и `request-fulfillment` определяют активный тенант в порядке: параметр запроса/формы → cookie консоли → конфигурация процесса. Проверка: тесты `TestLeadConsoleUsesSelectedTenantCookie`, `TestApprovalQueueUsesSelectedTenantCookie`; прежние тесты фолбэка на конфигурацию зелёные.

## 3. Доменная модель и документация

- [x] 3.1 `architecture/domain-catalog.yaml`: агрегат `tenant-access` получает политику `platform-operator`. Проверка: `go -C tools/archlint run .` не называет мою capability (остаётся не связанная чужая ошибка `outreach-handoff`, см. 4.2).
- [x] 3.2 `docs/domain-model.md`: термин «Платформенный оператор» в ubiquitous language и политика `platform-operator` в `tenant-access`.
- [x] 3.3 Обновить `docs/progress-log.md` (секция изменения и «Фокус сейчас») при завершении.

## 4. Приёмка и гейт

- [x] 4.1 Живой сценарий на контуре `docker compose`: вход `admin/admin`; экран выбора тенанта показывает тенанты клиентов; выбор тенанта клиента переключает консоли, и очередь заявок и карточки лидов показывают данные клиента **без ручного `tenant_id`** в адресе. Проверка: живой прогон. **Проверено:** `8087/console/tenant?actor=admin` — 200, все 4 тенанта; `POST /console/tenant/switch` — 303 + cookie `active_tenant`; `8085/approvals` без параметра — заявка `tenant-b8719…` видна; `8082/cards` без параметра — карточки лидов видны; без cookie — прежний фолбэк (пусто); `?actor=stranger` без тенанта — 400.
- [x] 4.2 Гейт: `make check` зелёный; `openspec validate operator-cross-tenant-access --strict` — valid. **Выполнено:** `make check` — exit 0 (build/vet/importlint/archlint/openspec); archlint `12 контекстов / 58 capability / 46 связей`; `openspec validate --all --strict` — 60 passed, 0 failed.
- [x] 4.3 Отсутствие заглушек на пути задачи (`AGENTS.md` §3). Проверка: доступ оператора — реальное решение владельца `platform-governance`, не подмена.
