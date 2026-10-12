# Задачи — campaign-scoped-approved-action-id

## 1. Контракт `approved-action` (`contracts/outreach-orchestration/approvedaction`)

- [x] 1.1 Добавить `ApprovedActionIdentity(campaignID, draftID string, version int64) string` — кампанийно-уникальный идентификатор с percent-кодированием компонентов; версия схемы MINOR `1.1.0`; обновить комментарии поля `ApprovedActionID`.
  - **Верификация:** `go test ./...` в `contracts/outreach-orchestration`; тест на коллизии разделителей.
- [x] 1.2 Расширить `ApprovedAction.IdempotencyKey()` до `(tenant_id, campaign_id, draft_id, draft_version)`; обновить комментарии.
  - **Верификация:** юнит-тест ключа: повтор действия — тот же ключ, другая кампания — другой, другая версия — другой.

## 2. Владелец `outreach-orchestration`

- [x] 2.1 В `internal/ports/contracts/approved_action.go::FromActionApproved` строить `ApprovedActionID` через `approvedaction.ApprovedActionIdentity` (от кампании, черновика и версии).
  - **Верификация:** `go test ./...` в `backend/contexts/outreach-orchestration`; тест `TestFromActionApprovedUsesCampaignScopedIdentity`.
- [x] 2.2 Проверить, что чтение решения (`app/query/approved_action.go::buildApprovedAction`) и relay публикуют совпадающий идентификатор.
  - **Верификация:** тесты query/relay зелёные.

## 3. `message-delivery` (поведение и формулировки)

- [x] 3.1 Уточнить комментарии/документацию ключа идемпотентности моста: он опирается на кампанийно-уникальный `approved_action_id`; формула `(tenant_id, approved_action_id)` не меняется.
  - **Верификация:** `go test ./...` в `backend/contexts/message-delivery`.

## 4. Тесты (первопричина)

- [x] 4.1 Контракт: идентификаторы разных кампаний к одному получателю различаются; повтор одного действия сохраняет идентификатор и ключ.
  - **Верификация:** тест красный до правки, зелёный после.
- [x] 4.2 Мост `message-delivery`: два утверждённых действия разных кампаний к одному получателю → два сообщения и два `delivery-event`; повтор того же действия → `Duplicate`, без второго эффекта.
  - **Верификация:** тест `commandsource/approvedaction` и `TestApprovedActionBridgeDeliversSecondCampaignToSameRecipient` зелёные.

## 5. Документация домена

- [x] 5.1 Уточнить `docs/domain-model.md` (идентичность `approved-action` кампанийно-скоупна; инвариант `message` про ключ идемпотентности).
  - **Верификация:** согласованность с `architecture/domain-catalog.yaml` (изменений каталога нет).

## 6. Гейт и приёмка

- [x] 6.1 `make check` exit 0 (build/test/vet/importlint/archlint/openspec).
  - **Верификация:** вывод команды, exit 0.
- [x] 6.2 `make test-postgres PG_TEST_PORT=5544` exit 0.
  - **Верификация:** все suite `ok`.
- [x] 6.3 `go -C tools/archlint run .` код 0; `go -C tools/importlint run .` код 0.
  - **Верификация:** вывод команд, код 0.
- [x] 6.4 `openspec validate campaign-scoped-approved-action-id --strict` valid.
  - **Верификация:** вывод команды.
- [x] 6.5 Живой прогон: пересобрать контур (`docker compose up -d --build`), подать две заявки с недобором на одного получателя, утвердить оба черновика, убедиться, что **оба** письма доставлены (mailpit) и **обе** заявки дошли до `fulfilled`; зафиксировать идентификаторы и статусы.
  - **Верификация:** `req-cs-c`/`req-cs-d` → `fulfilled`; два письма `info@respondo.ru` в mailpit; два `delivery-event`.
- [x] 6.6 Отметить задачи, заархивировать change, обновить `docs/progress-log.md`.
  - **Верификация:** `openspec archive campaign-scoped-approved-action-id -y`; запись в журнале.
