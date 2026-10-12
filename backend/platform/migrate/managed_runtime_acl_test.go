package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestManagedOwnerGlobalFunctionDefaultsAndRuntimeTypeScope(t *testing.T) {
	fixture, testDatabase := provisionTestDatabase(t)
	admin := fixture.pool
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	spec := RoleSet{
		Schema:           Schema("routine_acl_" + stamp),
		OwnerRole:        "routine_acl_owner_" + stamp,
		MigratorRole:     "routine_acl_migrator_" + stamp,
		MigratorPassword: "migration-secret-" + stamp,
		RuntimeRole:      "routine_acl_runtime_" + stamp,
		RuntimePassword:  "runtime-secret-" + stamp,
		OwnedRelations:   []string{migrationsTable, "probe_table", "probe_seq"},
		OwnedRoutines:    []RoutineRef{{Kind: "function", Name: "runtime_probe"}},
		OwnedTypes:       []string{"runtime_domain", "runtime_enum"},
		OwnedCollations:  []string{"runtime_collation"},
		Grants:           []TableGrant{{Table: migrationsTable, Privileges: []string{"SELECT"}}, {Table: "probe_table", Privileges: []string{"SELECT", "INSERT"}}},
		SequenceGrants:   []SequenceGrant{{Sequence: "probe_seq", Privileges: []string{"USAGE", "SELECT"}}},
	}
	otherOwner := "routine_acl_other_owner_" + stamp
	probeRole := "routine_acl_public_probe_" + stamp
	otherSchema := "routine_acl_other_schema_" + stamp
	assertTestRolesAbsent(t, ctx, admin, spec.OwnerRole, spec.MigratorRole, spec.RuntimeRole, otherOwner, probeRole)
	defer fixture.cleanup(t, spec.RuntimeRole, spec.MigratorRole, spec.OwnerRole, otherOwner, probeRole)
	for _, role := range []string{otherOwner, probeRole} {
		if _, err := admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN"); err != nil {
			t.Fatal("изолированная test role не создана")
		}
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{otherSchema}.Sanitize()+" AUTHORIZATION "+pgx.Identifier{otherOwner}.Sanitize()); err != nil {
		t.Fatal("foreign-owner schema fixture creation failed")
	}
	createRoutineAsRole(t, ctx, admin, otherOwner, otherSchema, "before_provision")
	if !runtimeCanExecuteRoutine(t, ctx, admin, probeRole, Schema(otherSchema), "before_provision") {
		t.Fatal("baseline global PUBLIC EXECUTE was not observable for the foreign owner")
	}
	databasePublicBefore := databasePublicPrivileges(t, ctx, admin, probeRole)
	publicSchemaBefore := publicSchemaPublicPrivileges(t, ctx, admin, probeRole)
	if err := ProvisionSchema(ctx, admin, spec); err != nil {
		t.Fatal("provision managed schema failed")
	}
	if ownerHasGlobalPublicExecute(t, ctx, admin, spec.OwnerRole) {
		t.Fatal("managed owner сохранил global default EXECUTE на functions для PUBLIC")
	}
	createRoutineAsRole(t, ctx, admin, otherOwner, otherSchema, "after_provision")
	if !runtimeCanExecuteRoutine(t, ctx, admin, probeRole, Schema(otherSchema), "after_provision") {
		t.Fatal("provision changed another owner's global function defaults")
	}
	if _, err := admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{otherSchema}.Sanitize()+" CASCADE"); err != nil {
		t.Fatal("foreign-owner routine fixture cleanup failed")
	}
	assertPublicPrivilegesUnchanged(t, ctx, admin, probeRole, databasePublicBefore, publicSchemaBefore)

	migrationsDir := t.TempDir()
	migration := []byte("-- schema: " + spec.Schema.String() + `
CREATE TABLE ` + pgx.Identifier{spec.Schema.String(), "probe_table"}.Sanitize() + ` (id text PRIMARY KEY, value text NOT NULL);
CREATE SEQUENCE ` + pgx.Identifier{spec.Schema.String(), "probe_seq"}.Sanitize() + `;
CREATE DOMAIN ` + pgx.Identifier{spec.Schema.String(), "runtime_domain"}.Sanitize() + ` AS text;
CREATE TYPE ` + pgx.Identifier{spec.Schema.String(), "runtime_enum"}.Sanitize() + ` AS ENUM ('red','blue');
CREATE COLLATION ` + pgx.Identifier{spec.Schema.String(), "runtime_collation"}.Sanitize() + ` (provider = libc, locale = 'C');
CREATE FUNCTION ` + pgx.Identifier{spec.Schema.String(), "runtime_probe"}.Sanitize() + `() RETURNS integer LANGUAGE sql AS $$ SELECT 7 $$;
`)
	if err := os.WriteFile(filepath.Join(migrationsDir, "001_managed_objects.sql"), migration, 0o600); err != nil {
		t.Fatal("test migration file creation failed")
	}
	config, err := pgx.ParseConfig(os.Getenv("LEADGEN_POSTGRES_TEST_DSN"))
	if err != nil {
		t.Fatal("test connection config unavailable")
	}
	config.Database = testDatabase
	migrationPool := openProvisionTestRolePool(t, config, spec.MigratorRole, spec.MigratorPassword)
	defer migrationPool.Close()
	if err := ApplyAs(ctx, migrationPool, migrationsDir, spec.Schema, spec.OwnerRole); err != nil {
		t.Fatalf("migration identity ApplyAs failed: %v", err)
	}
	if ownerHasGlobalPublicExecute(t, ctx, admin, spec.OwnerRole) {
		t.Fatal("ApplyAs introduced global default EXECUTE for PUBLIC on managed owner")
	}
	if runtimeCanExecuteRoutine(t, ctx, admin, spec.RuntimeRole, spec.Schema, "runtime_probe") {
		t.Fatal("new ordinary routine is executable by runtime before GrantRuntimeSchemaAs")
	}
	if runtimeHasTypeUsage(t, ctx, admin, spec.RuntimeRole, spec.Schema, "runtime_domain") || runtimeHasTypeUsage(t, ctx, admin, spec.RuntimeRole, spec.Schema, "runtime_enum") {
		t.Fatal("new domain/enum is usable by runtime before explicit type grants")
	}

	if err := GrantRuntimeSchemaAs(ctx, migrationPool, spec, spec.OwnerRole); err != nil {
		t.Fatalf("GrantRuntimeSchemaAs failed under owner capability: %v", err)
	}
	if runtimeCanExecuteRoutine(t, ctx, admin, spec.RuntimeRole, spec.Schema, "runtime_probe") {
		t.Fatal("runtime received routine EXECUTE without an explicit runtime routine grant")
	}
	if runtimeHasTypeUsage(t, ctx, admin, spec.RuntimeRole, spec.Schema, "runtime_domain") || runtimeHasTypeUsage(t, ctx, admin, spec.RuntimeRole, spec.Schema, "runtime_enum") {
		t.Fatal("runtime received TYPE/DOMAIN USAGE without an explicit runtime type grant")
	}
	if err := verifyRuntimeRoleAsTest(t, ctx, config, spec); err != nil {
		t.Fatal("runtime verifier rejected allowlisted tables with no routine/type rights:", err)
	}
	runtimePool := openProvisionTestRolePool(t, config, spec.RuntimeRole, spec.RuntimePassword)
	sequenceConn, err := runtimePool.Acquire(ctx)
	if err != nil {
		runtimePool.Close()
		t.Fatal("runtime sequence connection failed")
	}
	sequenceName := pgx.Identifier{spec.Schema.String(), "probe_seq"}.Sanitize()
	var sequenceValue int64
	if err := sequenceConn.QueryRow(ctx, "SELECT nextval($1::regclass)", sequenceName).Scan(&sequenceValue); err != nil {
		sequenceConn.Release()
		runtimePool.Close()
		t.Fatalf("explicitly allowlisted runtime nextval failed: %v", err)
	}
	if err := sequenceConn.QueryRow(ctx, "SELECT currval($1::regclass)", sequenceName).Scan(&sequenceValue); err != nil {
		sequenceConn.Release()
		runtimePool.Close()
		t.Fatalf("explicitly allowlisted runtime currval failed: %v", err)
	}
	sequenceConn.Release()
	runtimePool.Close()
	assertRuntimeOwnershipRejected(t, ctx, admin, config, spec, "table", "CREATE TABLE %s (id text)", "ALTER TABLE %s OWNER TO %s", "DROP TABLE %s")
	assertRuntimeOwnershipRejected(t, ctx, admin, config, spec, "sequence", "CREATE SEQUENCE %s", "ALTER SEQUENCE %s OWNER TO %s", "DROP SEQUENCE %s")
	assertRuntimeOwnershipRejected(t, ctx, admin, config, spec, "routine", "CREATE FUNCTION %s() RETURNS integer LANGUAGE sql AS $$ SELECT 1 $$", "ALTER FUNCTION %s() OWNER TO %s", "DROP FUNCTION %s()")
	assertRuntimeOwnershipRejected(t, ctx, admin, config, spec, "domain", "CREATE DOMAIN %s AS text", "ALTER TYPE %s OWNER TO %s", "DROP DOMAIN %s")
	assertRuntimeOwnershipRejected(t, ctx, admin, config, spec, "collation", "CREATE COLLATION %s (provider=libc, locale='C')", "ALTER COLLATION %s OWNER TO %s", "DROP COLLATION %s")
	if _, err := admin.Exec(ctx, "GRANT USAGE ON TYPE "+pgx.Identifier{spec.Schema.String(), "runtime_domain"}.Sanitize()+" TO "+pgx.Identifier{spec.RuntimeRole}.Sanitize()); err != nil {
		t.Fatal("type privilege injection fixture failed")
	}
	runtimePool = openProvisionTestRolePool(t, config, spec.RuntimeRole, spec.RuntimePassword)
	if err := VerifyRuntimeRole(ctx, runtimePool, spec); err == nil {
		runtimePool.Close()
		t.Fatal("runtime verifier accepted an unallowlisted domain USAGE grant")
	}
	runtimePool.Close()
	if _, err := admin.Exec(ctx, "REVOKE USAGE ON TYPE "+pgx.Identifier{spec.Schema.String(), "runtime_domain"}.Sanitize()+" FROM "+pgx.Identifier{spec.RuntimeRole}.Sanitize()); err != nil {
		t.Fatal("type privilege fixture cleanup failed")
	}
	assertPublicPrivilegesUnchanged(t, ctx, admin, probeRole, databasePublicBefore, publicSchemaBefore)
}

func ownerHasGlobalPublicExecute(t *testing.T, ctx context.Context, pool *pgxpool.Pool, owner string) bool {
	t.Helper()
	var present bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM pg_default_acl defaults CROSS JOIN LATERAL aclexplode(defaults.defaclacl) acl
WHERE defaults.defaclrole=(SELECT oid FROM pg_roles WHERE rolname=$1)
  AND defaults.defaclnamespace=0 AND defaults.defaclobjtype='f'
  AND acl.grantee=0 AND acl.privilege_type='EXECUTE')`, owner).Scan(&present); err != nil {
		t.Fatal("managed-owner global function default ACL inspection failed")
	}
	return present
}

func runtimeCanExecuteRoutine(t *testing.T, ctx context.Context, pool *pgxpool.Pool, role string, schema Schema, routine string) bool {
	t.Helper()
	var canExecute bool
	if err := pool.QueryRow(ctx, `SELECT has_function_privilege($1,
to_regprocedure(format('%I.%I()', $2::text, $3::text)),'EXECUTE')`, role, schema.String(), routine).Scan(&canExecute); err != nil {
		t.Fatal("runtime routine privilege inspection failed")
	}
	return canExecute
}

func runtimeHasTypeUsage(t *testing.T, ctx context.Context, pool *pgxpool.Pool, role string, schema Schema, typ string) bool {
	t.Helper()
	var canUse bool
	if err := pool.QueryRow(ctx, `SELECT has_type_privilege($1,
(SELECT oid FROM pg_type WHERE typnamespace=(SELECT oid FROM pg_namespace WHERE nspname=$2) AND typname=$3),'USAGE')`, role, schema.String(), typ).Scan(&canUse); err != nil {
		t.Fatal("runtime type privilege inspection failed")
	}
	return canUse
}

func assertRuntimeOwnershipRejected(t *testing.T, ctx context.Context, admin *pgxpool.Pool, config *pgx.ConnConfig, spec RoleSet, label, createTemplate, transferTemplate, dropTemplate string) {
	t.Helper()
	name := "runtime_owned_" + label
	qualified := pgx.Identifier{spec.Schema.String(), name}.Sanitize()
	if _, err := admin.Exec(ctx, fmt.Sprintf(createTemplate, qualified)); err != nil {
		t.Fatalf("%s ownership fixture creation failed", label)
	}
	dropStatement := fmt.Sprintf(dropTemplate, qualified)
	if label == "routine" {
		if _, err := admin.Exec(ctx, "REVOKE ALL PRIVILEGES ON FUNCTION "+qualified+"() FROM PUBLIC"); err != nil {
			_, _ = admin.Exec(context.Background(), dropStatement)
			t.Fatalf("routine PUBLIC ACL fixture setup failed")
		}
	}
	if label == "domain" {
		if _, err := admin.Exec(ctx, "REVOKE USAGE ON TYPE "+qualified+" FROM PUBLIC"); err != nil {
			_, _ = admin.Exec(context.Background(), dropStatement)
			t.Fatalf("domain PUBLIC ACL fixture setup failed")
		}
	}
	transferStatement := fmt.Sprintf(transferTemplate, qualified, pgx.Identifier{spec.RuntimeRole}.Sanitize())
	if _, err := admin.Exec(ctx, transferStatement); err != nil {
		_, _ = admin.Exec(context.Background(), dropStatement)
		t.Fatalf("%s ownership drift fixture setup failed", label)
	}
	if err := verifyRuntimeRoleAsTest(t, ctx, config, spec); err == nil {
		_, _ = admin.Exec(context.Background(), dropStatement)
		t.Fatalf("runtime verifier accepted %s ownership", label)
	}
	if _, err := admin.Exec(ctx, dropStatement); err != nil {
		t.Fatalf("%s ownership fixture cleanup failed", label)
	}
	if err := verifyRuntimeRoleAsTest(t, ctx, config, spec); err != nil {
		t.Fatalf("runtime verifier rejected restored %s scope: %v", label, err)
	}
}

func createRoutineAsRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool, role, schema, routine string) {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal("foreign-owner connection unavailable")
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatal("foreign owner role setup failed")
	}
	statement := "CREATE FUNCTION " + pgx.Identifier{schema, routine}.Sanitize() + "() RETURNS integer LANGUAGE sql AS $$ SELECT 1 $$"
	if _, err := conn.Exec(ctx, statement); err != nil {
		_, _ = conn.Exec(context.Background(), "RESET ROLE")
		t.Fatal("foreign owner routine creation failed")
	}
	if _, err := conn.Exec(ctx, "RESET ROLE"); err != nil {
		t.Fatal("foreign owner role reset failed")
	}
}

func openProvisionTestRolePool(t *testing.T, base *pgx.ConnConfig, role, password string) *pgxpool.Pool {
	t.Helper()
	config := base.Copy()
	config.User = role
	config.Password = password
	pool, err := pgxpool.NewWithConfig(context.Background(), pgxpoolConfig(config))
	if err != nil {
		t.Fatal("isolated test role pool could not be opened")
	}
	return pool
}

func verifyRuntimeRoleAsTest(t *testing.T, ctx context.Context, config *pgx.ConnConfig, spec RoleSet) error {
	t.Helper()
	runtimePool := openProvisionTestRolePool(t, config, spec.RuntimeRole, spec.RuntimePassword)
	defer runtimePool.Close()
	return VerifyRuntimeRole(ctx, runtimePool, spec)
}
