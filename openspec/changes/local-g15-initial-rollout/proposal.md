## Why

Локальный Compose сейчас работает на legacy G1-контейнерах и persistent PostgreSQL, а новый intake runtime требует подписанный active release state, отдельные runtime identities и точный G1.5 verifier. Без отдельного bootstrap-перехода невозможно фактически установить G1.5, подтвердить его как начальное состояние и безопасно включить последующий G2 release; создание пустого ledger или фиктивное enrollment не является допустимой заменой.

## What Changes

- Добавить ограниченный local-only путь начального перехода фактически наблюдённого G1 deployment в immutable G1.5 bridge с request protocol v1; не объявлять G1 rollback target после применения schema0010.
- Перед изменением persistent `postgres-green` создавать проверяемую резервную копию, восстанавливать её в disposable PostgreSQL 17 и сверять exact migration set 0001–0010, role grants и read-only runtime checks трёх intake dependencies.
- В bootstrap release связывать pinned Docker daemon, observed G1 runtime/container/image/volume/network/DB identity, staged trust key и G1.5 image digest; запускать только проверенные provision/migrate/verify jobs и атомарно публиковать signed G1.5 manifest/ledger state.
- До остановки intake worker проверять холодный запуск G1.5 runtime и фактический replay доступных `Fulfillment` histories; переключать только Compose service `intake` через pinned release runner, сохраняя остальные текущие process-group containers/images без replacement и без изменения их schema lifecycle.
- Перед началом persistent DB maintenance временно остановить локальные app writers и front door; после успешного G1.5 acceptance вернуть остальные прежние контейнеры. Проверенный G1 backup сохранять на время явно заданного rollback window; после первого G1.5 write переходить только roll-forward.
- Не выполнять автоматическую очистку старых томов и секретов. Не включать request protocol v2, production deployment, SMTP/delivery, либо общую миграцию остальных PostgreSQL-контекстов.

## Capabilities

### New Capabilities

### Modified Capabilities

- `service-topology`: ограничить межконтейнерные release ledger mutations holder-ом через per-attempt control capability, отделить аутентифицированный read-only ledger endpoint и убрать предположение о сопоставимости PID из разных container PID namespaces.
- `deployment-topology`: добавить проверяемый local-only bootstrap lifecycle для установки G1.5 из наблюдённого G1 без фиктивного active state, с backup/restore acceptance, signed enrollment и gated Compose cutover.
- `service-topology`: зафиксировать holder-only release ledger mutation с per-attempt capability и signed read-only IPC, совместимый с изолированными Docker PID namespaces и runtime без write authority.

## Impact

Затронутые bounded contexts: `request-fulfillment`, `integration-platform` (vector-only intake identity) и platform `consoleauth`; доменные модели и межконтекстные бизнес-контракты не меняются. Изменяются `backend/cmd/lead-gen` release/bootstrap checks, `deploy/scripts/release-runner.py`, `compose.yaml` Compose activation wiring, local backup/restore helper и acceptance scripts. Изменение затрагивает persistent local PostgreSQL и замену только фактически enrolled G1 intake container; остальные process-group containers/images и их schema lifecycle не меняются этим rollout. Production-системы не затрагиваются.
