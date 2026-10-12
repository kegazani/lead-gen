package migrate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestIntegrationInvocationBigserialRuntimeAllowlist(t *testing.T) {
	fixture, database := provisionTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sets := RequestIntakeRoleSets("request-migrator", "request-runtime", "console-migrator", "console-runtime", "vector-migrator", "vector-runtime", "integration-runtime")
	spec := sets[2]
	assertTestRolesAbsent(t, ctx, fixture.pool, spec.OwnerRole, spec.MigratorRole, spec.RuntimeRole, spec.AdditionalRuntimes[0].Role)
	defer fixture.cleanup(t, spec.OwnerRole, spec.MigratorRole, spec.RuntimeRole, spec.AdditionalRuntimes[0].Role)
	if err := ProvisionSchema(ctx, fixture.pool, spec); err != nil {
		t.Fatal("provision integration-platform schema:", err)
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("получение пути теста")
	}
	migrations := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../contexts/integration-platform/migrations"))
	migrator := rolePool(t, ctx, fixtureDSN(t, database), database, spec.MigratorRole, spec.MigratorPassword)
	defer migrator.Close()
	if err := ApplyAs(ctx, migrator, migrations, spec.Schema, spec.OwnerRole); err != nil {
		t.Fatal("integration-platform migrations под NOLOGIN owner:", err)
	}
	if err := ProvisionSchema(ctx, fixture.pool, spec); err != nil {
		t.Fatal("повторный ownership provision после migrations:", err)
	}
	if err := GrantRuntimeSchema(ctx, fixture.pool, spec); err != nil {
		t.Fatal("выдача раздельных vector/integration runtime grants:", err)
	}
	assertObjectOwner(t, ctx, fixture.pool, spec.Schema, "integration_invocation_seq_seq", spec.OwnerRole)

	integrationRuntime := rolePool(t, ctx, fixtureDSN(t, database), database, spec.AdditionalRuntimes[0].Role, spec.AdditionalRuntimes[0].Password)
	defer integrationRuntime.Close()
	var sequenceValue int64
	err := integrationRuntime.QueryRow(ctx, `INSERT INTO "integration-platform".integration_invocation
(id,tenant_id,correlation_ref,integration_type,adapter_name,operation,command_key,idempotency_key,state,started_at)
VALUES ('invocation-test','tenant-test','correlation-test','crm','adapter-test','lookup','command-test','idempotency-test','started',clock_timestamp()) RETURNING seq`).Scan(&sequenceValue)
	if err != nil || sequenceValue < 1 {
		t.Fatalf("integration runtime не смог вставить запись/получить bigserial: seq=%d err=%v", sequenceValue, err)
	}
	var currentSequence int64
	if err := integrationRuntime.QueryRow(ctx, `SELECT currval('"integration-platform".integration_invocation_seq_seq')`).Scan(&currentSequence); err != nil || currentSequence != sequenceValue {
		t.Fatalf("integration runtime sequence SELECT: currval=%d inserted=%d err=%v", currentSequence, sequenceValue, err)
	}

	vectorRuntime := rolePool(t, ctx, fixtureDSN(t, database), database, spec.RuntimeRole, spec.RuntimePassword)
	defer vectorRuntime.Close()
	if _, err := vectorRuntime.Exec(ctx, `SELECT * FROM "integration-platform".integration_invocation`); err == nil {
		t.Fatal("vector-only runtime получил доступ к integration_invocation")
	}
	if _, err := vectorRuntime.Exec(ctx, `SELECT nextval('"integration-platform".integration_invocation_seq_seq')`); err == nil {
		t.Fatal("vector-only runtime получил доступ к чужой bigserial sequence")
	}
	if _, err := vectorRuntime.Exec(ctx, `INSERT INTO "integration-platform".vector_document(id,embedding) VALUES ('vector-only',ARRAY[0.5]::double precision[])`); err != nil {
		t.Fatal("vector-only runtime потерял разрешённую vector_document функцию:", err)
	}
}

func fixtureDSN(t *testing.T, database string) string {
	t.Helper()
	if database == "" {
		t.Fatal("test database не задан")
	}
	return strings.TrimSpace(os.Getenv("LEADGEN_POSTGRES_TEST_DSN"))
}
