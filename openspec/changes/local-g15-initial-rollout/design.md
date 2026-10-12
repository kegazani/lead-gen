## Context

See `proposal.md` for the local-only initial rollout scope and `specs/deployment-topology/spec.md` for its observable gates. Current local G1 has no active release manifest/ledger and does not satisfy `enroll-g1.5`; the first G1.5 release therefore needs a distinct typed genesis path. Read-only inventory observed the current request/consoleauth/integration-platform schema versions and legacy intake container; those observations must be refreshed at execution time and cannot be treated as durable evidence.

The existing local PostgreSQL data volume is persistent. The code must not infer its owner identity from `.env`, current image tags or old logs. The already-provisioned local signing/trust inputs and operator password are external to tracked source; runtime/bootstrap DSN bundles remain uncreated until the exact target generation and accepted schema plan are frozen.

## Goals / Non-Goals

**Goals:**

- Add a one-time signed genesis path from actual observed G1 to G1.5 while keeping ordinary G1.5→next releases on the existing strict path.
- Protect the persistent green database with a full-volume backup and verified restore rehearsal before provisioning or migration.
- Run exact G1.5 provisioning/migrations and three-runtime verification under the release runner, then activate only the Compose-managed `intake` container.
- Keep all other existing process-group containers, images and schema lifecycles unchanged; temporarily stop/restart them only to fence writers during backup/migration.
- Preserve v1 Temporal histories and permit only normal worker continuation after G1.5 cutover.

**Non-Goals:**

- Migrating other bounded-context schemas, rebuilding/replacing non-intake groups, enabling request protocol v2, production deployment, Tofu-managed adoption, SMTP/delivery rollout, or automatic retirement/cleanup.
- Automatically restoring G1 after G1.5 has accepted writes; after write-enable, recovery is G1.5 roll-forward.
- Editing or copying Temporal server persistence. History inventory/replay is read-only; stopping/restarting the intake worker leaves histories in Temporal.

## Decisions

### 1. Typed G1 enrollment and one-time genesis

Add a separate `observed-legacy-g1` enrollment contract rather than setting `active_secret_generation=G1.5` by assumption. The release runner collects actual Docker inspect facts for the current intake container, image, network and mounts, and read-only database schema/role identity facts; an operator-authorized enrollment payload is signed by the already staged local release key and bound to the pinned daemon/deployment. It contains IDs, migration versions, role/user names and presence/absence of mounts, but never DSN/password values.

Enrollment uses strict `ObservedG1EnrollmentV1` fields: `schemaVersion`, `purpose`, `kind`, `environment`, `deploymentId`, `dockerEndpoint`, `dockerEngineId`, daemon fingerprint, ledger ID, release nonce, candidate generation, canonical plan digest, Compose project, exact intake/non-intake container IDs/image digests/mounts/networks, green container/volume/database identity, PG major/system identifier, exact schema migration inventory, nonsecret role/owner/ACL inventory reference, observation time, validity window and signing `keyId`. No unknown fields are accepted. Current G1 `request-fulfillment` ledger is 0001–0003; this is recorded from fresh catalog inspection, not replaced with a generic G1 list. The source container's observed `POSTGRES_USER` supplies the catalog login name; backup tools SHALL NOT assume a `postgres` role exists.

`PhysicalGreenBackupEvidenceV1` binds the signed enrollment/plan, exact source/backup/restore volume identities, source PG container/image, clean shutdown/checkpoint, writers-stopped fence, backup helper digest, restore container/image, PG startup and restored schema/role/ACL catalog comparison, and timestamps. Do not include a whole-PGDATA digest or database contents. The evidence is signed only after the restored clone passes.

Only an empty, correctly labeled release volume can accept genesis. Introduce a strict ledger v2 with a `genesis` singleton and typed release record. `bootstrap-g1` creates the initial baseline record and the first `new` release record in one holder CAS; it never fabricates a G1.5 active manifest. Reuse the existing state names for the candidate (`new → staging → verified → activation_in_progress → activated`) and add monotonic record markers `db_mutation_started` and `write_enable_started`. The candidate records `release_kind=initial-g1-to-g15` and a typed `base={kind: observed-legacy-g1,enrollmentSha256:...}`; ordinary v2 candidates retain their required `baseActiveGeneration` and cannot enter genesis.

The initial signed candidate is schemaVersion 3; it binds the exact enrollment, backup/restore and compatibility evidence digests, plan digest, G1.5 image, exact migration policies, generation/nonce, trust key and all three target role sets. The ordinary schemaVersion 2 candidate and G1.5→next release checks remain unchanged. A direct file copy, caller-supplied G1 label or unsigned ledger seed is rejected.

The Go activation gate gets an explicit initial-G1 branch: it accepts the missing prior active manifest only when the same in-progress ledger record references a verified signed G1 enrollment and the exact consumed G1.5 proof. All subsequent release activations require the normal signed previous-active manifest. The G1 bootstrap permission is one-time and cannot be replayed after G1.5 becomes active.

### 2. Full-volume backup and restore rehearsal

Use a physical Docker named-volume snapshot because it preserves every schema, table, role, ACL, extension and PostgreSQL catalog object without extracting user rows into a repository or release artifact. All known app writers are stopped first; PostgreSQL green is cleanly stopped before copying the data volume. The source is mounted read-only and a new owner-only backup volume is the sole writable target of a network-isolated copy helper. Compose binds `postgres-green-data` to the exact observed volume name via `POSTGRES_GREEN_DATA_VOLUME`; it SHALL NOT infer the source identity from a default name during rollout.

Before any persistent DB mutation, copy the backup volume into a second disposable scratch volume, start that clone using the exact PostgreSQL 17 image digest, and verify startup, schema ledgers, role/membership/owner inventory and non-intake schema inventory. Do not connect a migration job to the persistent source until the restore check succeeds. Keep the original backup volume immutable through the G1.5 rollback window. A failed rollout before G1.5 write-enable can be restored only by an explicit maintenance operation with all writers stopped: copy the verified backup into a new unique restore volume, update `POSTGRES_GREEN_DATA_VOLUME` to that observed volume and recreate green PostgreSQL. Never overwrite/remove the original source or backup volume; no automatic restore is allowed.

**Alternative rejected:** host `pg_dumpall` pipelines put a logical credential-bearing dump and a PGPASS handoff into another path; the physical snapshot is an exact, smaller trust boundary on the same local Docker host and is restorable with the same pinned server image.

### 3. Exact DB bootstrap and evidence sequence

Provision only `request-fulfillment`, `consoleauth` and vector-only `integration-platform` through the release runner. The signed target policy contains exact migration names/hashes: request 0001–0010 under `g1.5-schema-0010`, consoleauth exact current catalog and integration-platform exact vector dependency catalog. Unknown/missing/extra files, unexpected current schema, role memberships or object drift block before mutation.

After each target provision/migrate, the runner verifies actual database identity and migration versions with that target's runtime DSN, `VerifyRuntimeRole` and read-only `Verify`. The final proof requires all three targets and records only sanitized result references. Existing owner/migrator/runtime credentials are never silently rotated. Other schemas are recorded and compared but are not mutated.

### 4. Temporal compatibility is read-only before cutover

Run `WorkflowReplayer` against every observed open and closed `Fulfillment` history using the candidate G1.5 workflow registrations, including legacy workflow type `Fulfillment`; emit only aggregate counts/status, never IDs or payloads. The replay test does not start or signal workflows. Stop the G1 intake worker before final DB backup/migration; history records remain in the existing Temporal server. After successful intake cutover, the G1.5 v1 worker may resume them as normal application work. This is the point at which G1 database restore ceases to be a lossless rollback.

### 5. Release ledger IPC между изолированными PID namespaces

Числовой PID из `SO_PEERCRED` не используется для доказательства идентичности cross-container клиента: значение находится в namespace peer и не обязано совпадать с holder-local PID. При этом одной проверки UID/GID недостаточно: runtime с state RO mount не должен иметь возможность вызвать ledger mutation через Unix socket.

Разделить IPC на два fixed Unix sockets внутри release-state:

- `ledger-control.sock` — только control clients runner/Go activation gate; каждый request записи содержит per-release HMAC capability и одноразовый challenge, связанный с holder identity, nonce, owner attempt, revision, action и полным payload. Holder проверяет MAC constant-time и повторно применяет текущую state-machine/CAS validation. Только holder имеет state RW и capability-volume RW; activation gate имеет state/capability/trust RO и просит holder выполнить typed consume/publish action. Candidate/active manifest и ledger пишет только holder.
- `ledger-read.sock` — только строго типизированный `read` request без mutation-полей. Holder возвращает ledger snapshot, подписанный ephemeral session Ed25519 key; публичная часть и epoch находятся в owner marker. DB jobs/runtime имеют state/trust RO, не получают capability volume, проверяют challenge/response signature и expiry. Новые Pod/container namespace-ы при этом не должны сравнивать peer PID.

Capability key хранится отдельно от release-state, trust anchor и credential bundles в unique local volume по environment/deployment/nonce/attempt; внутри — только owner-only `0400` key. Holder создает его при one-time attempt init; control clients получают отдельный RO mount. Key не проходит через argv/ENV/stdout, ledger или attestation. После crash тот же attempt разрешает resume только с тем же capability volume и exact ledger revision; новый attempt/key требует explicit recovery. Runtime никогда не монтирует control capability.

Runner/Go helper clients получают capability только file mount и передают MAC во внутреннем IPC request; socket endpoint, `SO_PEERCRED` UID/GID, holder owner attempt, lock proof, exact revision и transition guard проверяются совместно. Mutation операции типизированы (`bootstrap-g1`, `db-mutation-begin`, signed candidate/active publication, ledger transitions, `write-enable`); универсального произвольного file/JSON writer нет. Holder единственный RW-mounter release-state, поэтому DB jobs, activation gate и runtime не могут напрямую менять ledger/manifests даже при UID `65532`.

`write-enable` — монотонный marker, который holder ставит **до** запуска intake container. Он закрывает G1 restore rollback window. Если activation/cutover не прошёл до marker, runner может explicit restore исходной backup volume при fenced writers; после marker допускается только G1.5 roll-forward, даже если новая Compose readiness не прошла.

Эта схема сохраняет remote flock/CAS как единственный ledger authority, в отличие от ослабления PID проверки до UID/GID. Читатель не получает mutation authority даже при общем runtime UID `65532`.

### 6. Compose-only intake cutover under the release lock

The release runner pins the exact Docker endpoint/Engine ID and Compose project, observes container IDs before mutation, and holds the same remote lock/ledger authority throughout bootstrap and cutover. It launches the Go activation gate in a one-shot nonroot control client with state/capability/trust/attestation RO; all state writes and proof consumption execute in the holder via typed authenticated IPC. The runner verifies the exact candidate image is already available locally, then invokes `docker compose up -d --no-deps --force-recreate --no-build --pull never intake` for the pinned project. No Compose-wide `up` is used: that could rebuild/recreate non-intake services and run unrelated migrations.

After cutover the runner verifies the new intake container ID/image digest/network/secret/state/trust mounts, UID `65532`, `/readyz`, the three runtime DB checks and active ledger. It verifies all non-intake container IDs/images remain unchanged. Any mismatch after proof consumption yields `recovery_required`; it does not auto-start G1 or restore the DB.

**Alternative rejected:** `tofu apply` is not used to replace existing Compose-managed containers; they are not represented in the same Tofu state. Direct `docker compose up` without the lock/Go activation gate cannot consume a release proof and is not the cutover path.

### 7. Rollback boundary and retained artifacts

Before first G1.5 write-enable, a verified G1 backup can be explicitly restored while all app writers/workers are stopped, then only the exact enrolled G1 image can be restarted against the restored volume. The backup is not a G1 binary rollback on schema0010: it is a full data restore to the old schema. Once the new intake passes readiness and is allowed to process requests or resume workflows, no G1 restore is allowed; only G1.5 v1 roll-forward/recovery remains. Keep the G1 image identity, backup volume, trust volume, old secret generations and signing/trust material until an explicit end of the rollback window.

## Risks / Trade-offs

- [Persistent backup is not restorable or its restore changes schema/roles] → disposable clone restore is a hard gate and uses the exact PostgreSQL 17 image digest.
- [Legacy G1 identity/schema differs from stale progress records] → fresh runtime/database inspection is required immediately before enrollment; any mismatch aborts before DB mutation.
- [Postgres snapshot overlaps a writer] → stop the app containers that can write green, confirm stopped IDs, then cleanly stop PostgreSQL before copying; any running writer invalidates backup evidence.
- [Genesis or activation is interrupted] → durable typed ledger state plus one-time proof; resume the same nonce after exact reconciliation, otherwise hold `recovery_required` for explicit restore/forward decision.
- [Existing Temporal work executes after cutover] → read-only replay of open and closed histories before migration; no worker runs during the DB window; after the first resumed workflow write the rollback boundary is permanently forward-only.
- [New intake is incompatible with still-running old demand/platform services] → compatibility acceptance exercises the real Compose HTTP contracts against the exact observed non-intake images before persistent cutover; IDs/digests are checked unchanged after.
- [Local images or trust volume changes between plan and cutover] → pin image digest, trust volume labels, source directory and exact daemon in the signed plan; mismatch blocks replacement.
- [Cross-container PID namespaces make peer PID incomparable] → use authenticated control IPC with per-attempt HMAC capability, signed read responses and no runtime write capability; retain UID/GID/socket/lock/CAS checks.
- [Same-UID runtime reaches the shared socket] → expose only a signed read endpoint to runtime; mutation endpoint requires separate capability not mounted to runtime.
- [Other project checks fail] → retain the release/tasks open; do not claim rollout acceptance or archive until `make check`, archlint and strict validation pass.

## Migration Plan

1. Freeze the observed G1 and Compose project identity; verify current schema inventory and operator-created source/signing/trust paths.
2. Build/pin the immutable G1.5 intake image; run unit/race tests, exact migration/role tests on disposable PostgreSQL 17, and read-only Temporal replay for all existing open/closed histories.
3. In a maintenance window, stop app writers/front door, cleanly stop green PostgreSQL, snapshot the full data volume, restore a clone to disposable PostgreSQL 17 and verify the clone. Restart the original green DB but keep conflicting app writers stopped.
4. Run one-time signed G1 genesis/bootstrap and the exact three-target provision→migrate→runtime-verify sequence under remote holder lock. Do not use Compose `db-admin` jobs or raw credentials.
5. Produce one final attestation; consume it through the Go activation gate; recreate only intake with the exact image and staged volumes; verify active ledger, readiness, DB checks and unchanged non-intake container identities.
6. Start/re-enable the remaining original local containers, then smoke the operator/client UI and intake workflow. After any new G1.5 write, close the G1 restore window and preserve backup/images for the agreed retention period.
7. If any step fails before cutover, leave the system fenced and release state explicit. If it fails after a DB mutation but before G1.5 writes, restore the verified backup only as a separate explicit operation. If it fails after writes, keep G1.5 schema/runtime and repair forward.

## Open Questions

None for the design boundary. Exact enrollment values, backup volume name, candidate image digest, generated runtime DSNs/roles and maintenance timestamp are runtime observations/inputs, not design choices; the release plan SHALL collect and validate them at execution time.
