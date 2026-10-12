## 1. Контракт

- [x] 1.1 Объявить в `contracts/platform-governance/accessdecision` wire-форму решения: `PathCanAct = "/access-decision"`, `CanActRequest{actor, tenant}`, `CanActResponse{allowed}`; обновить doc-комментарий пакета (синхронный транспорт наряду с policy-снимком). Проверка: `go build ./...` и `go test ./...` в модуле контракта.
- [x] 1.2 Реализовать subpackage `accessdecision/httpport`: клиент `Decider` поверх `net/http`, проверка абсолютного адреса при сборке, `ErrUnavailable` на любой сбой и не-2xx, таймаут по умолчанию. Проверка: unit-тесты клиента (успешное решение, отрицательное решение, недоступность, неразборчивый ответ) против `httptest`.

## 2. Владелец

- [x] 2.1 Добавить маршрут `POST /access-decision` в HTTP-адаптер `platform-governance`; обработчик вызывает use case `DecideAccess`, возвращает `{"allowed": bool}`, при незамонтированном use case отвечает `503`. Проверка: HTTP-тест round-trip «клиент `httpport` → сервер владельца → решение».
- [x] 2.2 Проверить совпадение решения по сети и in-proc для одного и того же тенанта/актора (положительное, отрицательное, недоступность). Проверка: тест сравнивает исходы обоих транспортов.

## 3. Проводка

- [x] 3.1 Добавить флаг/ENV `-access-decision-url` (`LEADGEN_ACCESS_DECISION_URL`) в `backend/cmd/lead-gen/main.go` и поле в `buildOptions`. Проверка: `go build ./...` в `backend/cmd/lead-gen`.
- [x] 3.2 В `assembleAccessDecision` выбирать транспорт по топологии: адрес задан → сетевой клиент; иначе прежнее in-proc связывание/`Unavailable`. Проверка: unit-тест выбора транспорта (адрес → клиент, пусто → in-proc) и логирование честной недоступности.

## 4. Каталог и модель

- [x] 4.1 Добавить в `architecture/domain-catalog.yaml` связи `platform-access-check-for-outreach` и `platform-access-check-for-data-governance` (`integration: query`, `transport: sync`, `mechanism: http-json`, контракт `access-decision`). Проверка: `go -C tools/archlint run .` код 0 (34 связи).
- [x] 4.2 Обновить `docs/domain-model.md` (число связей, пояснение синхронного транспорта решения) и `architecture/diagrams/context-relationships.puml` (рёбра синхронного решения). Проверка: `go -C tools/archlint run .` зелёный; диаграмма и каталог согласованы.
- [x] 4.3 Убедиться, что новых Go-модулей нет и правка встроенного `go.work` в `Dockerfile` не требуется. Проверка: `httpport` — subpackage уже встроенного модуля `contracts/platform-governance`; `go list` показывает пакет.

## 5. Проверки

- [x] 5.1 Выполнить `gofmt -l`, `go build ./...`, `go test ./...` по затронутым модулям (`contracts/platform-governance`, `backend/contexts/platform-governance`, `backend/cmd/lead-gen`, `backend/contexts/outreach-orchestration`, `backend/contexts/data-governance`). Проверка: все команды зелёные.
- [x] 5.2 Выполнить `go -C tools/archlint run .` и `openspec validate access-decision-network-transport --strict`. Проверка: archlint код 0, OpenSpec valid.
