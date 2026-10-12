## 1. Супервизор ролей (`backend/cmd/lead-gen`)

- [x] 1.1 `supervisor.go`: тип `roleSupervisor` — цикл «запуск → ожидание → backoff → повтор», экспоненциальная задержка `0.5s→…→30s` с jitter, `sleepContext` для ожидания/отмены (верификация: `go build ./...`, `go vet ./...`; юнит-тест `TestRoleSupervisorBackoffGrowsAndCaps`).
- [x] 1.2 `roleSupervisor.run`: перезапуск только при ошибке и живом `ctx`; возврат после отмены `ctx` (в т.ч. `context.Canceled`) — плановый останов без перезапуска (верификация: `TestRoleSupervisorRestartsAfterError`, `TestRoleSupervisorStopsOnContextCancel`).
- [x] 1.3 `startRoles` запускает роль под супервизором; параметр `cancel` убран — ошибка роли больше не отменяет процесс (верификация: `TestStartSelectedServicesRunsRolesUnderSupervisor`).
- [x] 1.4 `startSelectedServices`: сборка сервиса и запуск ролей разделены; ошибка сборки возвращается вызывающему, а не превращается в перезапуск (верификация: `TestStartSelectedServicesReturnsBuildError`).
- [x] 1.5 `main.go`: цикл по выбранным сервисам переведён на `startSelectedServices`, ошибка сборки завершает процесс `os.Exit(1)` — фатальность сборки сохранена (верификация: `go build ./...`, ревью вызова).

## 2. Проверка

- [x] 2.1 `cd backend/cmd/lead-gen && gofmt -l . && go build ./... && go vet ./... && go test ./...` — зелёный.
- [x] 2.2 `openspec validate role-runner-supervision --strict` — valid.
- [x] 2.3 `go -C tools/archlint run .` — код 0 (доменная граница не менялась).
