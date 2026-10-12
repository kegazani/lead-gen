## ADDED Requirements

### Requirement: Проверяемый initial rollout G1 в G1.5 для локального Compose

Начальный rollout локального Compose из существующего G1 в G1.5 SHALL выполняться как отдельная gated release transaction. До любой записи в persistent PostgreSQL операторский runner SHALL проверить фактические container/image/volume/network/daemon identities G1, точную применённую схему, external trust/signing inputs и наличие проверяемой резервной копии, восстановленной в disposable PostgreSQL 17. Неизвестная либо несовпадающая identity SHALL блокировать provisioning, миграции и replacement runtime.

Initial G1.5 SHALL сохранить request protocol v1, включая старое Temporal workflow type и существующие activity/payload contracts. Release SHALL применять только точный migration set 0001–0010 и generation-scoped runtime identities для request-fulfillment, consoleauth и vector-only integration-platform; после migration SHALL аутентифицировать все три runtime DSN и выполнять `VerifyRuntimeRole` и read-only `Verify` до выпуска одной signed final attestation. Existing Temporal histories SHALL replay-иться read-only текущим v1 worker без создания новых executions или изменений истории.

Переход SHALL сохранять старые G1 image/volume и резервную копию на объявленный rollback window. После применения schema0010 G1 SHALL NOT запускаться поверх новой схемы или объявляться binary rollback target; при неуспехе до successful G1.5 activation release SHALL оставаться в явном in-progress/recovery-required состоянии и SHALL NOT автоматически переключать runtime. Runtime G1.5 остаётся v1-only; protocol v2 SHALL оставаться закрытым.

#### Scenario: Неполная observed G1 identity блокирует bootstrap

- **WHEN** release plan не совпадает с фактическими G1 container/image/volume/network/daemon или точным schema inventory
- **THEN** runner прекращает работу до DB provisioning, migration или replacement runtime; guessed enrollment не принимается

#### Scenario: Backup нельзя восстановить в disposable PostgreSQL 17

- **WHEN** резервную копию persistent green DB не удаётся восстановить и проверить на отдельной disposable PostgreSQL 17
- **THEN** runner не начинает мутацию пользовательской persistent DB

#### Scenario: G1.5 migration set отличается от exact allowlist

- **WHEN** в candidate manifest отсутствует, добавлена, переименована либо имеет другой digest любая миграция request-fulfillment 0001–0010 или связанных intake targets
- **THEN** signed pre-mutation gate отвергает release до DB mutation

#### Scenario: Все G1.5 targets проходят миграцию и role/schema verification

- **WHEN** exact migration set применена для request, consoleauth и vector, все три candidate runtime DSN аутентифицируются, `VerifyRuntimeRole`, read-only `Verify` и v1 cold-start проходят
- **THEN** runner выпускает одну signed G1.5 attestation, атомарно фиксирует active ledger state и разрешает замену intake runtime; non-intake Compose groups и их schema migrations не меняются этим bootstrap

#### Scenario: Существующие Temporal histories replay-ятся без записи

- **WHEN** закрытая существующая Temporal history типа `Fulfillment` replay-ится новым bridge worker
- **THEN** replay завершается без nondeterminism, не создаёт execution/signal и не изменяет Temporal history; при несовместимости activation gate остаётся закрыт

#### Scenario: Сбой после schema migration не запускает G1 над несовместимой схемой

- **WHEN** migration 0001–0010 завершилась, но G1.5 runtime verification или activation не прошли
- **THEN** runner сохраняет old image/backup и explicit recovery-required state; G1 runtime не стартует над schema0010, а автоматический rollback не выполняется

#### Scenario: Успешный rollout оставляет G1.5 v1-only

- **WHEN** G1.5 стал active после полного release gate
- **THEN** runtime использует только `ExecutionProtocol=v1`, сохраняет старые поколения до окончания rollback window, а v2 admission остаётся запрещённым
