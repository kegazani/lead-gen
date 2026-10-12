## 1. Домен и событие

- [x] 1.1 Добавить поле материала (`Material domain.MaterialID`) в `domain.ActionApproved` и заполнять его в `commitDecision` из состава утверждённого черновика (`draft.Spec().Material`) (верификация: тесты `internal/domain` и `internal/app/command` проходят; событие `action-approved` несёт материал утверждённого черновика)

## 2. Отображение relay

- [x] 2.1 Заполнять `ApprovedAction.MaterialVersionRef` из материала события в `relay.MapActionApproved` (верификация: тест отображения утверждает непустой `MaterialVersionRef`, равный ссылке материала черновика)

## 3. Проводимая форма outbox

- [x] 3.1 Расширить проводимую форму `approvedEvent` и кодек (`encodeEvent`/`decodeEvent`) необязательным полем материала (верификация: PostgreSQL-suite outbox сохраняет и читает событие с материалом; `make test-postgres` зелёный)

## 4. Сквозная приёмка основного пути

- [x] 4.1 Обновить `backend/cmd/lead-gen/e2e_approved_to_sent_test.go`: черновик несёт реальную ссылку материала (`asset_id@version_id`), основной путь `approve → outbox → relay → мост → SMTP` даёт непустые тему и тело (верификация: E2E-тест основного пути проходит; проверка пустой темы на основном пути убрана как устаревшая)

## 5. Гейт

- [x] 5.1 Прогнать `go -C tools/archlint run .` (верификация: код 0)
- [x] 5.2 Прогнать `make check` (верификация: build/test/vet/importlint/archlint/openspec зелёные)
- [x] 5.3 Прогнать `make test-postgres` (верификация: PostgreSQL-suite outbox `outreach-orchestration` зелёный)
- [x] 5.4 Убедиться в отсутствии заглушки на пути «утверждено → отправлено» (`AGENTS.md` §3) (верификация: материал реально разрешается из ссылки события; нет `deterministic`/`unavailable`-подмены на продуктовом пути)
- [x] 5.5 Выровнять стейл-ожидание числа связей в `backend/platform/deployment/topology_test.go` с каноническим каталогом (30) (верификация: `go -C backend/platform test ./deployment/...` зелёный)
