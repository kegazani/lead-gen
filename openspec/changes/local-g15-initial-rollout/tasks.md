## 1. Observed G1 baseline and restore point

- [ ] 1.1 Add read-only Compose/Docker/PostgreSQL inventory for the local G1 intake baseline; record exact container/image/daemon/network/volume identities, `POSTGRES_USER`, schema versions and DB role names without DSN/password values. Verification: report is generated from current Docker inspect and PostgreSQL catalogs; stale/mismatched plan is rejected before any DB job.
- [ ] 1.2 Add a local-only full `postgres-green` volume backup/restore helper. Verification: with app writers stopped and green PostgreSQL cleanly shut down, the helper checks `pg_controldata`, copies the exact volume to a protected separate backup volume, compares source/copy without exposing file paths or PGDATA hashes, clones it to a disposable volume, starts the clone on the pinned PostgreSQL 17 image, and confirms startup/catalog/table/role/ACL inventory without changing the source volume.
- [ ] 1.3 Bind backup/restore evidence to the signed release plan and provide explicit restore-to-new-volume operation. Verification: missing/stale backup, wrong Docker daemon/volume/image/catalog-role, failed restore, changed source volume or a running writer blocks all DB jobs; restore creates a new uniquely labeled volume and updates only the observed `POSTGRES_GREEN_DATA_VOLUME`, never overwriting source/backup; evidence is sanitized and owner-only.

## 2. G1.5 compatibility evidence

- [ ] 2.1 Build and pin the exact G1.5 v1-only intake image and its migration catalog. Verification: recorded immutable image digest and signed candidate policy match the request migration files 0001–0010 exactly; missing/extra/hash-mismatched migration and any v2 protocol configuration are rejected.
- [ ] 2.2 Rehearse request/consoleauth/vector migration and runtime identities on a disposable clone restored from the observed G1 backup. Verification: exact provisioning/migration set succeeds on PostgreSQL 17; all three runtime DSNs pass authentication, `VerifyRuntimeRole` and read-only `Verify`; G1 backup clone remains unchanged and candidate failure leaves active source untouched.
- [ ] 2.3 Replay all observed open and closed Temporal `Fulfillment` histories using the exact G1.5 image/workflow registrations. Verification: replay is read-only, covers the full observed corpus including legacy workflow name `Fulfillment`, logs only sanitized counts/results, and any nondeterminism/payload mismatch blocks persistent migration.
- [ ] 2.4 Verify v1 route/aggregate compatibility and image cold-start on the restored schema0010/runtime roles. Verification: direct-start, approve, rerun and legacy request read/write scenarios pass using the immutable G1.5 image; G1 protocol v2 remains disabled.

## 3. Shared holder IPC and initial G1 genesis

- [ ] 3.1 Split release holder IPC into authenticated control and signed read-only endpoints for cross-container PID namespaces. The holder alone gets release-state RW and capability-volume RW; runner/control gate receive per-attempt capability volume RO; DB jobs/runtime receive neither capability nor mutation endpoint. Verification: HMAC covers canonical request/scope/revision/action/payload with one-time challenge, response signature binds live ledger read, duplicate challenge/replay fails, same-UID runtime cannot mutate, and separate container PID namespaces work without weakening UID/GID, socket, lock or CAS checks. Activation gate and candidate/active publication mutate release-state only through holder typed IPC.
- [ ] 3.2 Implement signed `observed-legacy-g1` enrollment and one-time genesis ledger state. Verification: snapshot binds exact local environment/deployment/daemon, old intake container/image, mounts/network/DB identity, actual catalog login and schema inventory without credential values; missing/stale/mismatched observation, reused genesis, fabricated active manifest or unsigned snapshot is rejected before staging/DB mutation.

## 4. Initial G1.5 release and database migration

- [ ] 4.1 Implement release-runner initial G1→G1.5 orchestration using the typed genesis contract. Verification: pinned Docker endpoint, external signing/trust inputs, restore evidence, exact candidate image/migration policy and consumed nonce are bound into one ledger release; ordinary G1.5→next release path remains unchanged; tests prove failed preflight has no DB/ledger mutation.
- [ ] 4.2 Run request, consoleauth and vector-only provision/migrate jobs from the signed initial candidate. Verification: provision preserves existing shared/root passwords, creates only generation-scoped roles, applies only allowlisted SQL, verifies role/schema constraints, and emits no credentials in argv/env/logs/ledger.
- [ ] 4.3 Finalize exactly one G1.5 attestation and active state only after all targets pass. Verification: any target/job/DSN failure prevents signing/activation; duplicate/replayed nonce and concurrent bootstrap fail closed; resume after crash uses the same nonce and validated durable boundaries.

## 5. Compose intake cutover

- [ ] 5.1 Add pinned Compose cutover support to the release runner. Verification: while holding the same remote ledger lock, the state-RO/capability-RO Go control gate consumes the signed proof through holder IPC; before Compose can start intake the holder durably sets `write_enable_started`; `docker compose up -d --no-deps --force-recreate --no-build --pull never intake` replaces only intake with exact pinned digest; actual container/image/network/mount identities are inspected, non-intake containers remain unchanged, and failure records `recovery_required` without auto-rollback.
- [ ] 5.2 Acceptance-test Compose cutover on a disposable project/DB/volumes. Verification: candidate state/trust/secret volumes are read-only in runtime, no capability volume is mounted to runtime/DB jobs, stagers are isolated, wrong source/key/generation blocks startup, valid intake becomes ready and can use all three runtime DBs, unauthorized same-UID IPC mutation is rejected, and no test volume/secret remains after cleanup.

## 6. Local persistent rollout

- [ ] 6.1 Repeat fresh read-only G1 enrollment, writer inventory, backup and disposable restore verification immediately before rollout. Verification: all facts match the signed plan and the final backup has a successful restore rehearsal; otherwise the persistent release does not begin.
- [ ] 6.2 Execute the signed initial G1→G1.5 release against the local persistent green PostgreSQL and perform the Compose intake-only cutover. Verification: exact active ledger/manifest/roles/schema/image/container/mount facts match the candidate; only intake is replaced; all other app containers and schemas retain their prior identities; pre-write failure can use explicit backup restore, post-write recovery is G1.5 roll-forward only.
- [ ] 6.3 Smoke-test operator/client request path and G1.5 v1 workflow continuation after cutover. Verification: intake `/readyz`, existing UI route, three DB dependency probes and Temporal workflow history status are healthy; no v2 admission, delivery/SMTP or non-intake schema migration is performed.
- [ ] 6.4 Preserve G1 backup, old images and previous secret/trust volumes through the declared rollback window. Verification: no automatic volume/key cleanup occurs and a sanitized inventory identifies the retained artifacts without secret contents.

## 7. Final verification

- [ ] 7.1 Run `make check`, `go -C tools/archlint run .`, `openspec validate local-g15-initial-rollout --strict` and release/Compose/Tofu configuration tests. Verification: all gates pass or this rollout remains open with exact failing gate and no false completion claims.
- [ ] 7.2 Complete independent review of enrollment, backup/restore, genesis, DB mutations, replay, Compose activation and rollback boundary. Verification: no unresolved critical/high finding remains and report includes actual local G1.5 runtime identity and sanitized evidence paths.
