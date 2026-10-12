package postgres_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/migrate"
)

// TestExecutionAttemptMigrationPreservesLegacyRequests starts from the schema
// immediately before 0004, inserts a durable legacy request, applies the normal
// migrator and verifies that the row remains readable and no attempt is invented.
// Legacy request snapshots contain no execution intent/LeadTarget, so 0004 cannot
// infer a target and must not synthesize an attempt with an implicit zero target.
func TestExecutionAttemptMigrationPreservesLegacyRequests(t *testing.T) {
	baseDSN := os.Getenv(dsnEnv)
	if baseDSN == "" {
		t.Skipf("%s не задан: migration upgrade test требует PostgreSQL", dsnEnv)
	}

	ctx := context.Background()
	adminCfg, err := pgxpool.ParseConfig(baseDSN)
	if err != nil {
		t.Fatalf("разобрать %s: %v", dsnEnv, err)
	}
	databaseName := "rf_upgrade_" + randomUpgradeSuffix(t)
	adminCfg.ConnConfig.Database = "postgres"
	admin, err := pgxpool.NewWithConfig(ctx, adminCfg)
	if err != nil {
		t.Fatalf("подключиться к административной БД: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize()); err != nil {
		t.Fatalf("создать отдельную временную БД %s: %v", databaseName, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1`, databaseName)
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{databaseName}.Sanitize())
	})

	upgradeCfg, err := pgxpool.ParseConfig(baseDSN)
	if err != nil {
		t.Fatalf("разобрать DSN временной БД: %v", err)
	}
	upgradeCfg.ConnConfig.Database = databaseName
	pool, err := pgxpool.NewWithConfig(ctx, upgradeCfg)
	if err != nil {
		t.Fatalf("подключиться к временной БД: %v", err)
	}
	defer pool.Close()

	migrationsDir := filepath.Join("..", "..", "..", "..", "migrations", "green")
	migrations, err := migrate.Load(migrationsDir, migrate.OwnSchema(postgres.Schema))
	if err != nil {
		t.Fatalf("загрузить миграции: %v", err)
	}
	if len(migrations) < 9 {
		t.Fatalf("получено %d миграций, ожидалось не менее 9 для проверки upgrade через 0009", len(migrations))
	}
	// Первые три миграции — зафиксированная предыдущая физическая схема.
	for _, migration := range migrations[:3] {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("применить предыдущую миграцию %s: %v", migration.Name, err)
		}
	}

	legacy, err := domain.NewRequest(domain.NewRequestParams{
		ID: "req-existing-before-0004", OfferID: "offer-before-0004", TenantID: "tenant-before-0004",
		Title:   "Legacy offer",
		Content: "legacy offer", Target: "legacy target", Actor: "migration-test",
		At: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), EventID: "event-before-0004",
	})
	if err != nil {
		t.Fatalf("создать legacy aggregate: %v", err)
	}
	legacySnapshot := legacy.Snapshot()
	legacySnapshot.Title = "" // Имитируем сохранённую до появления имени заявку.
	snapshot, err := json.Marshal(legacySnapshot)
	if err != nil {
		t.Fatalf("сериализовать legacy snapshot: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO "request-fulfillment".request(id,tenant_id,status,version,snapshot)
VALUES($1,$2,$3,$4,$5::jsonb)`, legacy.ID().String(), legacy.TenantID().String(), string(legacy.Status()), 1, string(snapshot)); err != nil {
		t.Fatalf("сохранить существующую legacy-заявку: %v", err)
	}
	// Сформировать предыдущую миграционную схему и реальную запись 0005 перед
	// запуском upgrade обычным мигратором.
	for _, migration := range migrations[3:5] {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("создать исходную схему %s: %v", migration.Name, err)
		}
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE "request-fulfillment".schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatalf("создать ledger предыдущих миграций: %v", err)
	}
	for _, migration := range migrations[:5] {
		if _, err := pool.Exec(ctx, `INSERT INTO "request-fulfillment".schema_migrations(version) VALUES($1)`, migration.Name); err != nil {
			t.Fatalf("зафиксировать предыдущую версию %s: %v", migration.Name, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO "request-fulfillment".execution_resolution
(resolution_id,tenant_id,request_id,resolution_kind,target_state,lead_target,actor,resolved_at,evidence_fingerprint)
VALUES('pre-0006-target',$1,$2,'target','explicit',4,'legacy-operator',$3,$4)`, legacy.TenantID().String(), legacy.ID().String(), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err != nil {
		t.Fatalf("создать legacy resolution до 0006: %v", err)
	}

	if err := migrate.Apply(ctx, pool, migrationsDir, postgres.Schema); err != nil {
		t.Fatalf("применить upgrade через обычный мигратор: %v", err)
	}
	loaded, err := postgres.NewRepository(pool).FindByID(ctx, legacy.ID())
	if err != nil {
		t.Fatalf("прочитать legacy-заявку после upgrade: %v", err)
	}
	if loaded.ID() != legacy.ID() || loaded.TenantID() != legacy.TenantID() || loaded.Status() != legacy.Status() {
		t.Fatalf("legacy aggregate изменился после upgrade: id=%s tenant=%s status=%s", loaded.ID(), loaded.TenantID(), loaded.Status())
	}
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "request-fulfillment".execution_attempt WHERE request_id=$1`, legacy.ID().String()).Scan(&attempts); err != nil {
		t.Fatalf("проверить отсутствие синтетической попытки: %v", err)
	}
	if attempts != 0 {
		t.Fatalf("upgrade создал %d execution_attempt без исходной intent; ожидалось 0", attempts)
	}
	legacyOwnerState, err := postgres.NewExecutionAttemptRepository(pool).ReadOwnerState(ctx, legacy.TenantID(), legacy.ID())
	if err != nil || legacyOwnerState.HasAttempt || legacyOwnerState.Target != (domain.LeadTarget{State: domain.LeadTargetExplicit, Value: 4}) || legacyOwnerState.Runtime != ports.AttemptUnknown {
		t.Fatalf("legacy resolution не прочитана как ревизия, а runtime остался unknown: state=%+v err=%v", legacyOwnerState, err)
	}
	var resolutionRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "request-fulfillment".execution_resolution WHERE request_id=$1`, legacy.ID().String()).Scan(&resolutionRows); err != nil {
		t.Fatalf("проверить новую resolution schema: %v", err)
	}
	if resolutionRows != 1 {
		t.Fatalf("upgrade должен сохранить исходную legacy resolution, rows=%d", resolutionRows)
	}

	resolution := domain.ExecutionResolution{
		ResolutionID: "legacy-target-resolution", TenantID: legacy.TenantID(), RequestID: legacy.ID(),
		Kind: domain.ResolutionTarget, Target: domain.LeadTarget{State: domain.LeadTargetExplicit, Value: 0},
		Actor: "migration-test-operator", ResolvedAt: time.Date(2026, 3, 4, 5, 6, 7, 123456789, time.UTC),
		EvidenceFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpectedRevision:    1, SupersedesID: "pre-0006-target",
	}
	resolutionRepo := postgres.NewExecutionResolutionRepository(pool)
	if err := resolutionRepo.AppendResolution(ctx, resolution); err != nil {
		t.Fatalf("добавить target resolution к legacy request без attempt: %v", err)
	}
	legacyOwnerState, err = postgres.NewExecutionAttemptRepository(pool).ReadOwnerState(ctx, legacy.TenantID(), legacy.ID())
	if err != nil || legacyOwnerState.HasAttempt || legacyOwnerState.Runtime != ports.AttemptUnknown || legacyOwnerState.Target != resolution.Target {
		t.Fatalf("target resolution изменила runtime gate или создала attempt: state=%+v err=%v", legacyOwnerState, err)
	}
	if err := resolutionRepo.AppendResolution(ctx, resolution); err != nil {
		t.Fatalf("повторить resolution_id идентичным payload: %v", err)
	}
	nextResolution := resolution
	nextResolution.ResolutionID = "legacy-target-resolution-revision-2"
	nextResolution.ExpectedRevision = 2
	nextResolution.SupersedesID = resolution.ResolutionID
	nextResolution.Target.Value = 7
	if err := resolutionRepo.AppendResolution(ctx, nextResolution); err != nil {
		t.Fatalf("добавить следующую revision с тем же timestamp: %v", err)
	}
	legacyOwnerState, err = postgres.NewExecutionAttemptRepository(pool).ReadOwnerState(ctx, legacy.TenantID(), legacy.ID())
	if err != nil || legacyOwnerState.Target != nextResolution.Target || legacyOwnerState.Runtime != ports.AttemptUnknown {
		t.Fatalf("current resolution выбирается не по revision или меняет runtime gate: state=%+v err=%v", legacyOwnerState, err)
	}
	staleResolution := nextResolution
	staleResolution.ResolutionID = "legacy-target-resolution-stale"
	staleResolution.ExpectedRevision = 0
	staleResolution.SupersedesID = ""
	staleResolution.ExpectedRevision = 1
	staleResolution.SupersedesID = "pre-0006-target"
	if err := resolutionRepo.AppendResolution(ctx, staleResolution); !errors.Is(err, ports.ErrExecutionResolutionConflict) {
		t.Fatalf("устаревшая expectedRevision не конфликтует: %v", err)
	}
	conflict := resolution
	conflict.Target.Value = 7
	if err := resolutionRepo.AppendResolution(ctx, conflict); !errors.Is(err, ports.ErrExecutionResolutionConflict) {
		t.Fatalf("resolution_id с отличающимся payload: %v", err)
	}
	storedResolutions, err := resolutionRepo.ListResolutions(ctx, legacy.TenantID(), legacy.ID())
	if err != nil || len(storedResolutions) != 3 || storedResolutions[0].Revision != 1 || storedResolutions[2].Revision != 3 {
		t.Fatalf("target resolution не хранится отдельно: rows=%+v err=%v", storedResolutions, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "request-fulfillment".execution_attempt WHERE request_id=$1`, legacy.ID().String()).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("target resolution должна оставить отсутствие попытки; attempts=%d err=%v", attempts, err)
	}

	runtimeRequest, err := domain.NewRequest(domain.NewRequestParams{
		ID: "req-runtime-resolution", OfferID: "offer-runtime-resolution", TenantID: "tenant-runtime-resolution",
		Title:   "Runtime resolution",
		Content: "runtime test", Actor: "migration-test", At: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC), EventID: "runtime-request-event",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewRepository(pool).Save(ctx, runtimeRequest, 0); err != nil {
		t.Fatalf("сохранить request для runtime evidence: %v", err)
	}
	intent, err := domain.NewExecutionIntent("runtime-command", runtimeRequest.ID(), runtimeRequest.TenantID(), 1,
		domain.LeadTarget{State: domain.LeadTargetUnspecified}, []byte(`{"revision":1}`))
	if err != nil {
		t.Fatal(err)
	}
	attemptsRepo := postgres.NewExecutionAttemptRepository(pool)
	if err := attemptsRepo.SaveIntent(ctx, intent, 0); err != nil {
		t.Fatalf("сохранить runtime attempt: %v", err)
	}
	lease, err := attemptsRepo.ClaimNext(ctx, "migration-test-dispatcher", time.Minute)
	if err != nil {
		t.Fatalf("захватить runtime attempt: %v", err)
	}
	if err := attemptsRepo.BindRun(ctx, lease, "verified-temporal-run"); err != nil {
		t.Fatalf("привязать проверенный RunID: %v", err)
	}
	if err := attemptsRepo.SetState(ctx, lease, ports.AttemptTerminal); err != nil {
		t.Fatalf("перевести тестовую попытку в terminal: %v", err)
	}
	withoutRuntimeEvidence, err := attemptsRepo.ReadOwnerState(ctx, runtimeRequest.TenantID(), runtimeRequest.ID())
	if err != nil || withoutRuntimeEvidence.Runtime != ports.AttemptUnknown {
		t.Fatalf("attempt terminal без runtime resolution ошибочно снял gate: state=%+v err=%v", withoutRuntimeEvidence, err)
	}
	storedAttempt, err := attemptsRepo.FindByCommand(ctx, runtimeRequest.TenantID(), intent.CommandID())
	if err != nil {
		t.Fatal(err)
	}
	runtimeResolution := domain.ExecutionResolution{
		ResolutionID: "runtime-resolution-1", TenantID: runtimeRequest.TenantID(), RequestID: runtimeRequest.ID(),
		Kind: domain.ResolutionRuntimeObservation, Generation: 1, AttemptID: storedAttempt.AttemptID,
		ObservedOutcome: domain.RuntimeResolutionFailed, ObservedRunID: "verified-temporal-run",
		Actor: "migration-test-reconciler", ResolvedAt: time.Date(2026, 3, 4, 5, 6, 8, 0, time.UTC),
		EvidenceFingerprint: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	}
	if err := resolutionRepo.AppendResolution(ctx, runtimeResolution); err != nil {
		t.Fatalf("записать runtime resolution с verified RunID: %v", err)
	}
	wrongRun := runtimeResolution
	wrongRun.ResolutionID = "runtime-resolution-wrong-run"
	wrongRun.ObservedRunID = "foreign-run"
	if err := resolutionRepo.AppendResolution(ctx, wrongRun); !errors.Is(err, ports.ErrExecutionFenceConflict) {
		t.Fatalf("runtime resolution с чужим RunID принята: %v", err)
	}
	runtimeRows, err := resolutionRepo.ListResolutions(ctx, runtimeRequest.TenantID(), runtimeRequest.ID())
	if err != nil || len(runtimeRows) != 1 || runtimeRows[0].Kind != domain.ResolutionRuntimeObservation {
		t.Fatalf("runtime и target resolution смешаны: rows=%+v err=%v", runtimeRows, err)
	}
	runtimeOwnerState, err := attemptsRepo.ReadOwnerState(ctx, runtimeRequest.TenantID(), runtimeRequest.ID())
	if err != nil || runtimeOwnerState.Runtime != ports.AttemptUnknown {
		t.Fatalf("runtime observation ошибочно сняло gate без доверенного verifier: state=%+v err=%v", runtimeOwnerState, err)
	}
	terminalEvidence := domain.TerminalIntentEvidence{
		Version: 1, WorkflowID: "fulfillment-" + runtimeRequest.ID().String(), RunID: "verified-temporal-run",
		RequestID: runtimeRequest.ID().String(), TenantID: runtimeRequest.TenantID().String(), AttemptID: storedAttempt.AttemptID,
		Generation: 1, CommandID: string(intent.CommandID()), ReasonCode: "search_failed",
		OriginalReason: "исполнение прервано: поиск лидов завершился ошибкой",
	}
	terminalEvidence.Checksum = testTerminalChecksum(t, terminalEvidence)
	terminalEvidenceJSON, err := json.Marshal(terminalEvidence)
	if err != nil {
		t.Fatal(err)
	}
	verifier := staticVerifiedHistory{result: ports.TerminalHistoryVerification{Intent: terminalEvidence, CloseStatus: "FAILED", Evidence: terminalEvidenceJSON}}
	trustedService := command.NewVerifiedRuntimeResolutionService(postgres.NewUnitOfWork(pool), verifier, fixedPGClock{now: time.Date(2026, 3, 4, 5, 6, 9, 0, time.UTC)})
	identity := ports.TerminalResolutionIdentity{
		WorkflowID: terminalEvidence.WorkflowID, RunID: terminalEvidence.RunID, RequestID: runtimeRequest.ID(),
		TenantID: runtimeRequest.TenantID(), AttemptID: storedAttempt.AttemptID, Generation: 1, CommandID: string(intent.CommandID()),
	}
	if err := trustedService.VerifyAndAppend(ctx, "verified-runtime-1", 0, "", identity); err != nil {
		t.Fatalf("trusted app UoW не добавил verified runtime record: %v", err)
	}
	if err := trustedService.VerifyAndAppend(ctx, "verified-runtime-1", 0, "", identity); err != nil {
		t.Fatalf("идентичный повтор verified runtime resolution не идемпотентен: %v", err)
	}
	if err := trustedService.VerifyAndAppend(ctx, "verified-runtime-2", 1, "verified-runtime-1", identity); err != nil {
		t.Fatalf("добавить следующую CAS revision: %v", err)
	}
	runtimeOwnerState, err = attemptsRepo.ReadOwnerState(ctx, runtimeRequest.TenantID(), runtimeRequest.ID())
	if err != nil || runtimeOwnerState.Runtime != ports.AttemptTerminal {
		t.Fatalf("verified record не стала текущим terminal state: state=%+v err=%v", runtimeOwnerState, err)
	}
	if err := trustedService.VerifyAndAppend(ctx, "verified-runtime-stale", 0, "", identity); !errors.Is(err, ports.ErrExecutionResolutionConflict) {
		t.Fatalf("устаревшая verified CAS revision должна конфликтовать: %v", err)
	}
	var verifiedCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "request-fulfillment".execution_verified_runtime_resolution WHERE request_id=$1`, runtimeRequest.ID().String()).Scan(&verifiedCount); err != nil || verifiedCount != 2 {
		t.Fatalf("verified resolution revision count=%d err=%v, ожидалось 2", verifiedCount, err)
	}
	for _, statement := range []struct {
		name string
		sql  string
	}{
		{"legacy update", `UPDATE "request-fulfillment".execution_resolution SET actor='rewritten' WHERE resolution_id='pre-0006-target'`},
		{"legacy delete", `DELETE FROM "request-fulfillment".execution_resolution WHERE resolution_id='pre-0006-target'`},
		{"legacy truncate", `TRUNCATE "request-fulfillment".execution_resolution`},
		{"revision update", `UPDATE "request-fulfillment".execution_resolution_revision SET actor='rewritten' WHERE resolution_id='legacy-target-resolution'`},
		{"revision delete", `DELETE FROM "request-fulfillment".execution_resolution_revision WHERE resolution_id='legacy-target-resolution'`},
		{"revision truncate", `TRUNCATE "request-fulfillment".execution_resolution_revision`},
		{"verified update", `UPDATE "request-fulfillment".execution_verified_runtime_resolution SET actor='spoofed' WHERE resolution_id='verified-runtime-1'`},
		{"verified delete", `DELETE FROM "request-fulfillment".execution_verified_runtime_resolution WHERE resolution_id='verified-runtime-1'`},
		{"verified truncate", `TRUNCATE "request-fulfillment".execution_verified_runtime_resolution`},
		{"missing audit", `INSERT INTO "request-fulfillment".execution_resolution(resolution_id,tenant_id,request_id,resolution_kind,target_state,lead_target,resolved_at,evidence_fingerprint) VALUES('bad-audit','tenant-before-0004','req-existing-before-0004','target','explicit',2,clock_timestamp(),repeat('a',64))`},
		{"delete referenced request", `DELETE FROM "request-fulfillment".request WHERE id='req-existing-before-0004' AND tenant_id='tenant-before-0004'`},
		{"revision tenant mismatch", `INSERT INTO "request-fulfillment".execution_resolution_revision
(resolution_id,tenant_id,request_id,resolution_kind,revision,expected_revision,generation,attempt_id,runtime_outcome,observed_run_id,actor,resolved_at,evidence_fingerprint)
VALUES('wrong-tenant-revision','foreign-tenant','req-existing-before-0004','runtime',1,0,1,'attempt','failed','run','migration-test',clock_timestamp(),repeat('b',64))`},
	} {
		if _, err := pool.Exec(ctx, statement.sql); err == nil {
			t.Errorf("DB приняла запрещённую операцию %s", statement.name)
		}
	}
	for _, trigger := range []struct {
		table    string
		name     string
		function string
	}{
		{table: "execution_resolution", name: "execution_resolution_no_truncate", function: `"request-fulfillment".reject_execution_resolution_truncate()`},
		{table: "execution_resolution_revision", name: "execution_resolution_revision_no_truncate", function: `"request-fulfillment".reject_execution_resolution_truncate()`},
		{table: "execution_verified_runtime_resolution", name: "execution_verified_runtime_no_truncate", function: `"request-fulfillment".reject_verified_runtime_mutation()`},
	} {
		var valid bool
		query := `SELECT tgtype=34 AND tgfoid=$3::regprocedure::oid AND tgenabled IN ('O','A')
FROM pg_trigger WHERE tgrelid=to_regclass($1) AND tgname=$2 AND NOT tgisinternal`
		if err := pool.QueryRow(ctx, query, `"request-fulfillment".`+trigger.table, trigger.name, trigger.function).Scan(&valid); err != nil || !valid {
			t.Fatalf("trigger %s имеет неверное событие/функцию/состояние: valid=%v err=%v", trigger.name, valid, err)
		}
	}

	// Проверить именно FK revision -> request: у этой заявки нет ни legacy
	// resolution, ни execution_attempt, поэтому запрет удаления обеспечивается
	// только составной FK миграции 0008.
	fkRequest, err := domain.NewRequest(domain.NewRequestParams{
		ID: "req-revision-fk-only", OfferID: "offer-revision-fk-only", TenantID: "tenant-revision-fk-only",
		Title:   "Foreign key request",
		Content: "FK test", Actor: "migration-test", At: time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC), EventID: "revision-fk-only-event",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewRepository(pool).Save(ctx, fkRequest, 0); err != nil {
		t.Fatalf("сохранить request для FK test: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO "request-fulfillment".execution_resolution_revision
(resolution_id,tenant_id,request_id,resolution_kind,revision,expected_revision,target_state,lead_target,actor,resolved_at,evidence_fingerprint)
VALUES('revision-fk-only',$1,$2,'target',1,0,'explicit',0,'migration-test',clock_timestamp(),repeat('c',64))`,
		fkRequest.TenantID().String(), fkRequest.ID().String()); err != nil {
		t.Fatalf("добавить audit revision для FK test: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "request-fulfillment".request WHERE id=$1 AND tenant_id=$2`, fkRequest.ID().String(), fkRequest.TenantID().String()); err == nil {
		t.Fatal("FK execution_resolution_revision разрешил удалить request без других дочерних строк")
	}
	var legacyRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "request-fulfillment".execution_resolution WHERE resolution_id='pre-0006-target'`).Scan(&legacyRows); err != nil || legacyRows != 1 {
		t.Fatalf("legacy audit row должна сохраниться в исходной таблице, rows=%d err=%v", legacyRows, err)
	}
	var carriedRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "request-fulfillment".execution_resolution_revision WHERE resolution_id='pre-0006-target'`).Scan(&carriedRows); err != nil || carriedRows != 1 {
		t.Fatalf("legacy audit row должна сохраниться в revision table, rows=%d err=%v", carriedRows, err)
	}
}

type staticVerifiedHistory struct {
	result ports.TerminalHistoryVerification
}

func (v staticVerifiedHistory) VerifyTerminalHistory(_ context.Context, _ ports.TerminalResolutionIdentity) (ports.TerminalHistoryVerification, error) {
	return v.result, nil
}

type fixedPGClock struct{ now time.Time }

func (c fixedPGClock) Now() time.Time { return c.now }

func testTerminalChecksum(t *testing.T, evidence domain.TerminalIntentEvidence) string {
	t.Helper()
	evidence.Checksum = ""
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func randomUpgradeSuffix(t *testing.T) string {
	t.Helper()
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		t.Fatal(fmt.Errorf("сгенерировать суффикс временной БД: %w", err))
	}
	return hex.EncodeToString(bytes[:])
}
