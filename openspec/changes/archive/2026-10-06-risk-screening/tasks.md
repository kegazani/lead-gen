## 1. Каталог и карты

- [x] 1.1 Capability `risk-screening` объявлена в `capabilities` контекста `outreach-orchestration` каталога `architecture/domain-catalog.yaml`, в capability matrix `docs/domain-model.md` и кросс-таблице `docs/system-map.md` §5 — выполнено на этапе планирования, чтобы `tools/archlint` оставался зелёным. Состав контекстов, агрегатов и связей не меняется, диаграммы без изменений. Проверка: `go -C tools/archlint run .` завершается кодом 0 и учитывает `risk-screening` в числе capability.

## 2. Контракт верификации адреса (владелец lead-management)

- [x] 2.1 Опубликовать контракт владельца верификации адреса: снимок/запрос статуса верификации получателя (без PII, ссылочно, по `tenant_id` и ссылке на получателя) с envelope, semver и ключом идемпотентности. Регистрация в модуле `contracts/lead-management`, при необходимости — связи в каталоге. Проверка: `Validate` и тест ключа идемпотентности; потребитель не импортирует внутренние пакеты владельца. — Контракт `contracts/lead-management/addressverification` (`Validate`, `IdempotencyKey`, semver `1.0.0`); связь `lead-verification-for-outreach` в каталоге; потребитель (`riskfacts`) импортирует только контракт.
- [x] 2.2 Связать контракт с реализацией владельца в корневом пакете `backend/contexts/lead-management` (in-process при co-location). Проверка: тест переводит статус верификации домена в форму контракта; недоступность владельца — ошибка, а не «проверен». — `NewAddressVerification`; тесты `addressverification_test.go` (статус → снимок; недоступность → `ErrUnavailable`; чужой тенант → `ErrRejected`).

## 3. Форма предиката риска (домен outreach-orchestration)

- [x] 3.1 Пересмотреть форму `domain.CanBeDecidedInPackage`: предикат несёт запрос фактов (тенант, получатель, версия черновика) и ошибку; `nil` означает сборку без проверки и отвергается на сборке, а не трактуется как «рисков нет». **BREAKING** для внутреннего домена. Проверка: тесты пакетного пути подтверждают, что недоступность фактов не даёт исключения и не трактуется как «рисков нет». — `func(ctx, RiskFactsRequest) ([]RiskReason, error)`; `nil` → `ErrRiskFactsUnavailable`; тесты `TestBuildApprovalBatchNilPredicateIsUnavailable`, `TestBuildApprovalBatchUnavailableFactsIsErrorNotExclusion`, `TestApproveBatchUnavailableFactsIsError`.
- [x] 3.2 Обновить пакетные сценарии (`BuildApprovalBatch`, `ApproveBatch`, проекции очереди), чтобы новую форму получали все ветви, и решение отвергало то, что исключил состав. Проверка: существующие тесты пакетного пути адаптированы и зелёные. — `BuildApprovalBatch`/`ApproveBatch`/`BuildBatch`/`BatchQueue`/`DraftQueue` получают контекст и запрос; все тесты зелёные.

## 4. Сборщик фактов риска

- [x] 4.1 Реализовать сборщик фактов: получатель → адрес (`delivery-target`) → проверка стоп-листа (`suppression-policy`), статус верификации — по контракту задачи 2. Сборщик живёт вне домена и передаёт признаки в предикат. Проверка: unit-тесты на факты: активная запись стоп-листа → `stop-rule-triggered`; неподтверждённый адрес → `address-unverified`; чисто → пусто. — `internal/adapters/riskfacts`; тесты `riskfacts_test.go`.
- [x] 4.2 Реализовать семантику недоступности: ошибка/недоступность владельца не превращается в «рисков нет» и не превращается в «разрешено». Проверка: тест с недоступным владельцем подтверждает честный исход (пакетное утверждение не выдаётся как свободное от риска). — ошибки владельцев → `domain.ErrRiskFactsUnavailable`; тест `TestUnavailableOwnerIsErrorNotEmpty`.
- [x] 4.3 Признак `personal-data-detected` и `required-facts-missing`: либо подключить владельческое решение, либо снять признак из перечня как не имеющий владельца (решение зафиксировать в design.md). Проверка: перечень `RiskReason` соответствует реально получаемым признакам. — Признаки сняты из `domain.RiskReason`; решение зафиксировано в `design.md` (Решение 1, Open Question 3).

## 5. Сборка сервиса

- [x] 5.1 Собрать сборщик фактов в `backend/contexts/outreach-orchestration` (composition root) и провести в `backend/cmd/lead-gen/runners.go`, заменив `NoUpstreamRiskChecks` на реальный предикат. Проверка: `go build ./...`; на продуктовом пути нет заглушки риска. — `outreachorchestration.NewRiskFacts`; `NoUpstreamRiskChecks` удалён; `assembleRiskFacts` в `cmd/lead-gen` собирает предикат поверх контрактов; `go build ./...` зелёный.
- [x] 5.2 Недоступность владельца фактов — честная деградация без маскировки: сборка сообщает о недоступном владельце. Проверка: при не подключённых владельцах пакетное утверждение честно недоступно. — `assembleRiskFacts` логирует отсутствующих владельцев; сборка без владельцев даёт честно недоступный предикат; `TestRiskScreeningUnavailableOwnerIsHonestRefusal` → 503.

## 6. Валидация

- [x] 6.1 Сквозной сценарий: черновик с активной записью стоп-листа исключается из пакета с причиной; чистый черновик в пакет входит; недоступный владелец не даёт «рисков нет». Проверка: интеграционный тест проходит. — `risk_screening_e2e_test.go`: `TestRiskScreeningExcludesSuppressedAndUnverifiedDrafts` (стоп-лист → `stop-rule-triggered`; верификация → `address-unverified`; чистые в пакете), `TestRiskScreeningUnavailableOwnerIsHonestRefusal`.
- [x] 6.2 Выполнить `make check` и `go -C tools/archlint run .`. Проверка: обе команды завершаются с кодом 0; `openspec validate risk-screening --strict` valid. — `make check` exit 0; `archlint` exit 0 (связей 51); `openspec validate risk-screening --strict` valid.

## Отложено

- Полная семантика признака «персональные данные» — до появления владельческого решения (открытый вопрос 3 design.md).
- Сетевой (не in-process) транспорт контракта верификации — при разделении сервисов по процессам.
