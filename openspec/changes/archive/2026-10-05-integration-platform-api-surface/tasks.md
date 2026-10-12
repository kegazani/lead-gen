## 1. Контракт `adapter-status`

- [x] 1.1 Опубликовать контракт `contracts/integration-platform/adapterstatus`: DTO `Status{Adapter,State,Reason}`, конверт `Response`, интерфейс `Port`, ошибка `ErrUnavailable`, закрытые словари состояния/причины. Проверка: `go build ./...` в модуле `contracts/integration-platform`; в контракте нет свободного текста (только значения словарей).
- [x] 1.2 Реализовать сетевой адаптер `adapterstatus/httpport` (`GET {base}/adapter-status`; не-200 и сбой сети → `ErrUnavailable`). Проверка: unit-тесты клиента — успешное чтение и недоступность владельца (`go test ./adapterstatus/httpport`).

## 2. HTTP-поверхность роли `api`

- [x] 2.1 Ввести порт `ports.AdapterStatusReader` и метод `AdapterStatuses` источника владельца; проверить соответствие на этапе компиляции. Проверка: `go build ./...`; тест источника отдаёт состояние в форме контракта и не переносит секрет из текста ошибки проверки.
- [x] 2.2 Реализовать HTTP-сервер роли `api` (`internal/adapters/http`): `GET /adapter-status`, `GET /adapters`, `GET /healthz`; отсутствие источника — честный `503`, пустой реестр — пустой список. Проверка: unit-тесты сервера (состояние, недоступность, реестр, healthz).
- [x] 2.3 Раннер `runAPI` поднимает HTTP-сервер и останавливает его по отмене контекста; роль `ingest` честно сообщает об отсутствии входящего протокола и ничего не принимает. Проверка: тест роли `api` поднимает реальный сервер, отвечает `200` на `/healthz` и `/adapter-status` и завершается по отмене.

## 3. Проводка точки сборки

- [x] 3.1 `integration-platform` получает `APIAddr` и источник состояния адаптеров; сборка сервиса передаёт их в роль `api`. Проверка: сборка сервиса (`TestBuildIntegrationPlatformService`) и тест роли `api` проходят.
- [x] 3.2 `cmd/lead-gen`: флаг `-integration-platform-url` (`LEADGEN_INTEGRATION_PLATFORM_URL`); при заданном адресе раздел интеграций `platform-governance` читает состояние по сети через контракт, иначе — источник владельца in-proc. Проверка: unit-тесты `integrationAdapterCatalog` (сетевой и in-proc пути); `go build`/`go test` в `backend/cmd/lead-gen`.

## 4. Доменная привязка и гейт

- [x] 4.1 Сверить доменную привязку: реализуется уже объявленный контракт `adapter-status` и связь `integrations-status-for-governance`; новых capability/связей нет. Проверка: `go -C tools/archlint run .` — код 0, каталог без изменений (`54 capability / 32 связи`).
- [x] 4.2 Прогнать гейт. Проверка: `make check` (build/test/vet/importlint/archlint/openspec) зелёный; `make test-postgres` (11 suite) `ok`; `go -C tools/archlint run .` — код 0; `openspec validate integration-platform-api-surface --strict` valid.

## Отложено (вне среза)

- Сетевые поверхности `provider-port` и `vector` (владелец — `integration-platform`): без сконфигурированного адаптера поставщика/эмбеддера не несут функции; задача остаётся открытой до выбора внешнего провайдера и не закрывается заглушкой.
- Публикация команд `RegisterAdapter`/`SelectAdapter` по сети: потребителя-команды нет.
- Аутентификация внутренних JSON-поверхностей контекстов — общий вопрос, не специфичный для этого изменения.
