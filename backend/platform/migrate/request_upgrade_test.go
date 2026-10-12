package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRequestFulfillmentUpgradeWithRealSchemaPath проверяет bootstrap существующей
// схемы с дефисом, ownership upgrade старых объектов, фактические SQL migrations
// под owner через migrator login и отдельное применение runtime allowlist.
func TestRequestFulfillmentUpgradeWithRealSchemaPath(t *testing.T) {
	fixture, testDatabase := provisionTestDatabase(t)
	admin := fixture.pool
	dsn := os.Getenv("LEADGEN_POSTGRES_TEST_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := admin.Ping(ctx); err != nil {
		t.Fatal("подключение к изолированному PostgreSQL:", err)
	}

	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	spec := RoleSet{
		Schema:           Schema("request-fulfillment"),
		OwnerRole:        "request_owner_" + stamp,
		MigratorRole:     "request_migrator_" + stamp,
		MigratorPassword: "migration-secret-" + stamp,
		RuntimeRole:      "request_runtime_" + stamp,
		RuntimePassword:  "runtime-secret-" + stamp,
		OwnedRelations: []string{
			"schema_migrations", "request", "email_draft", "outbox", "execution_attempt",
			"execution_resolution", "execution_resolution_revision", "legacy_marker", "legacy_marker_seq",
			"legacy_partitioned", "legacy_partitioned_0", "legacy_view", "legacy_matview",
		},
		OwnedTypes:      []string{"legacy_domain", "legacy_composite"},
		OwnedCollations: []string{"legacy_collation"},
		OwnedRoutines: []RoutineRef{
			{Kind: "function", Name: "reject_execution_resolution_mutation"}, {Kind: "function", Name: "validate_execution_resolution_subject"},
			{Kind: "function", Name: "reject_execution_resolution_truncate"}, {Kind: "function", Name: "reject_legacy_execution_resolution_truncate"},
			{Kind: "function", Name: "validate_execution_resolution_revision"}, {Kind: "function", Name: "reject_execution_resolution_revision_mutation"},
			{Kind: "function", Name: "reject_legacy_execution_resolution_insert"}, {Kind: "function", Name: "legacy_marker_fn"},
			{Kind: "function", Name: "legacy_sum_sfunc", Args: "state integer, value integer"},
			{Kind: "function", Name: "legacy_operator_eq", Args: "integer, integer"},
			{Kind: "procedure", Name: "legacy_proc", Args: "IN x integer"},
			{Kind: "aggregate", Name: "legacy_sum", Args: "integer"},
		},
		Grants: []TableGrant{
			{Table: "request", Privileges: []string{"SELECT", "INSERT", "UPDATE"}},
			{Table: "email_draft", Privileges: []string{"SELECT", "INSERT", "UPDATE", "DELETE"}},
			{Table: "outbox", Privileges: []string{"SELECT", "INSERT", "UPDATE"}},
			{Table: "execution_attempt", Privileges: []string{"SELECT", "INSERT", "UPDATE"}},
			{Table: "execution_resolution", Privileges: []string{"SELECT"}},
			{Table: "execution_resolution_revision", Privileges: []string{"SELECT", "INSERT"}},
			{Table: "schema_migrations", Privileges: []string{"SELECT"}},
		},
	}
	legacyRole := "request_legacy_owner_" + stamp
	otherOwner := "request_other_owner_" + stamp
	hostileGroup := "request_hostile_group_" + stamp
	hostileLogin := "request_hostile_login_" + stamp
	aclProbe := "request_acl_probe_" + stamp
	assertTestRolesAbsent(t, ctx, admin, spec.OwnerRole, spec.MigratorRole, spec.RuntimeRole, legacyRole, otherOwner, hostileGroup, hostileLogin, aclProbe)
	if _, err := admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{aclProbe}.Sanitize()+" NOLOGIN"); err != nil {
		t.Fatal("создание ACL probe role:", err)
	}
	databasePublicBefore := databasePublicPrivileges(t, ctx, admin, aclProbe)
	publicSchemaPublicBefore := publicSchemaPublicPrivileges(t, ctx, admin, aclProbe)
	defer fixture.cleanup(t, spec.RuntimeRole, spec.MigratorRole, spec.OwnerRole, legacyRole, otherOwner, hostileGroup, hostileLogin, aclProbe)

	for _, statement := range []string{
		"CREATE ROLE " + pgx.Identifier{legacyRole}.Sanitize() + " LOGIN PASSWORD 'legacy-test-secret'",
		"CREATE ROLE " + pgx.Identifier{otherOwner}.Sanitize() + " NOLOGIN",
		"CREATE SCHEMA " + pgx.Identifier{spec.Schema.String()}.Sanitize() + " AUTHORIZATION " + pgx.Identifier{legacyRole}.Sanitize(),
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("подготовка старой схемы: %v", err)
		}
	}
	if _, err := admin.Exec(ctx, "SET ROLE "+pgx.Identifier{legacyRole}.Sanitize()); err != nil {
		t.Fatal("имитация прежнего владельца: SET ROLE:", err)
	}
	qualifiedSchema := pgx.Identifier{spec.Schema.String()}.Sanitize()
	for _, statement := range []string{
		"CREATE TABLE " + qualifiedSchema + `.request (id text PRIMARY KEY, tenant_id text NOT NULL, status text NOT NULL, version integer NOT NULL, snapshot jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT now())`,
		"INSERT INTO " + qualifiedSchema + `.request (id,tenant_id,status,version,snapshot) VALUES ('existing-request','tenant-existing','submitted',1,'{}'::jsonb)`,
		"INSERT INTO " + qualifiedSchema + `.request (id,tenant_id,status,version,snapshot) VALUES ('resolution-request','tenant-existing','submitted',1,'{}'::jsonb)`,
		"CREATE TABLE " + qualifiedSchema + `.legacy_marker (id text PRIMARY KEY, value text NOT NULL)`,
		"INSERT INTO " + qualifiedSchema + `.legacy_marker VALUES ('preserved','before-upgrade')`,
		"CREATE SEQUENCE " + qualifiedSchema + `.legacy_marker_seq`,
		"CREATE TABLE " + qualifiedSchema + `.legacy_partitioned (id integer, value text) PARTITION BY RANGE (id)`,
		"CREATE TABLE " + qualifiedSchema + `.legacy_partitioned_0 PARTITION OF ` + qualifiedSchema + `.legacy_partitioned FOR VALUES FROM (0) TO (100)`,
		"CREATE VIEW " + qualifiedSchema + `.legacy_view AS SELECT id,value FROM ` + qualifiedSchema + `.legacy_partitioned`,
		"CREATE MATERIALIZED VIEW " + qualifiedSchema + `.legacy_matview AS SELECT id,value FROM ` + qualifiedSchema + `.legacy_partitioned`,
		"CREATE FUNCTION " + qualifiedSchema + `.legacy_marker_fn() RETURNS integer LANGUAGE sql AS 'SELECT 17'`,
		"CREATE DOMAIN " + qualifiedSchema + `.legacy_domain AS integer CHECK (VALUE >= 0)`,
		"CREATE TYPE " + qualifiedSchema + `.legacy_composite AS (id integer, value text)`,
		"CREATE COLLATION " + qualifiedSchema + `.legacy_collation (provider=libc, locale='C')`,
		"CREATE PROCEDURE " + qualifiedSchema + `.legacy_proc(IN x integer) LANGUAGE plpgsql AS $$ BEGIN PERFORM x; END $$`,
		"CREATE FUNCTION " + qualifiedSchema + `.legacy_sum_sfunc(state integer, value integer) RETURNS integer LANGUAGE sql IMMUTABLE AS $$ SELECT state + value $$`,
		"CREATE AGGREGATE " + qualifiedSchema + `.legacy_sum(integer) (SFUNC=` + qualifiedSchema + `.legacy_sum_sfunc, STYPE=integer, INITCOND='0')`,
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("создание существующих объектов от старого владельца (%s): %v", statement, err)
		}
	}
	if _, err := admin.Exec(ctx, "RESET ROLE"); err != nil {
		t.Fatal("сброс legacy owner:", err)
	}

	if err := ProvisionSchema(ctx, admin, spec); err != nil {
		t.Fatal("provision существующей request-схемы:", err)
	}
	if _, err := admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{hostileGroup}.Sanitize()+" NOLOGIN"); err != nil {
		t.Fatal("создание hostile group fixture:", err)
	}
	if _, err := admin.Exec(ctx, "CREATE ROLE "+pgx.Identifier{hostileLogin}.Sanitize()+" LOGIN PASSWORD 'hostile-test-secret'"); err != nil {
		t.Fatal("создание hostile login fixture:", err)
	}
	for _, statement := range []string{
		"GRANT " + pgx.Identifier{hostileGroup}.Sanitize() + " TO " + pgx.Identifier{spec.OwnerRole}.Sanitize(),
		"GRANT " + pgx.Identifier{hostileGroup}.Sanitize() + " TO " + pgx.Identifier{spec.MigratorRole}.Sanitize(),
		"GRANT " + pgx.Identifier{hostileGroup}.Sanitize() + " TO " + pgx.Identifier{spec.RuntimeRole}.Sanitize(),
		"GRANT " + pgx.Identifier{spec.OwnerRole}.Sanitize() + " TO " + pgx.Identifier{hostileLogin}.Sanitize(),
		"GRANT " + pgx.Identifier{spec.MigratorRole}.Sanitize() + " TO " + pgx.Identifier{hostileLogin}.Sanitize(),
		"GRANT " + pgx.Identifier{spec.RuntimeRole}.Sanitize() + " TO " + pgx.Identifier{hostileLogin}.Sanitize(),
		"GRANT USAGE ON SCHEMA " + pgx.Identifier{spec.Schema.String()}.Sanitize() + " TO " + pgx.Identifier{hostileLogin}.Sanitize(),
		"GRANT SELECT ON " + pgx.Identifier{spec.Schema.String(), "request"}.Sanitize() + " TO " + pgx.Identifier{hostileLogin}.Sanitize(),
		"GRANT UPDATE (status) ON TABLE " + pgx.Identifier{spec.Schema.String(), "request"}.Sanitize() + " TO " + pgx.Identifier{hostileLogin}.Sanitize(),
		"GRANT USAGE ON TYPE " + pgx.Identifier{spec.Schema.String(), "legacy_domain"}.Sanitize() + " TO " + pgx.Identifier{hostileLogin}.Sanitize(),
		"GRANT USAGE, SELECT ON SEQUENCE " + pgx.Identifier{spec.Schema.String(), "legacy_marker_seq"}.Sanitize() + " TO " + pgx.Identifier{hostileLogin}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION " + pgx.Identifier{spec.Schema.String(), "legacy_marker_fn"}.Sanitize() + "() TO " + pgx.Identifier{hostileLogin}.Sanitize(),
		"GRANT SELECT ON " + pgx.Identifier{spec.Schema.String(), "request"}.Sanitize() + " TO PUBLIC",
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("установка hostile membership/ACL (%s): %v", statement, err)
		}
	}
	if err := ProvisionSchema(ctx, admin, spec); err != nil {
		t.Fatal("повторный provision с hostile memberships/ACL:", err)
	}
	assertManagedMemberships(t, ctx, admin, spec)
	assertPublicPrivilegesUnchanged(t, ctx, admin, aclProbe, databasePublicBefore, publicSchemaPublicBefore)
	var hostileTableSelect, hostileColumnUpdate, hostileTypeUsage, hostileSequenceUsage, hostileFunctionExecute bool
	if err := admin.QueryRow(ctx, `SELECT has_table_privilege($1,$2,'SELECT'),
has_column_privilege($1,$2,'status','UPDATE'), has_type_privilege($1,$3,'USAGE'),
has_sequence_privilege($1,$4,'USAGE'), has_function_privilege($1,$5,'EXECUTE')`,
		hostileLogin,
		pgx.Identifier{spec.Schema.String(), "request"}.Sanitize(),
		pgx.Identifier{spec.Schema.String(), "legacy_domain"}.Sanitize(),
		pgx.Identifier{spec.Schema.String(), "legacy_marker_seq"}.Sanitize(),
		pgx.Identifier{spec.Schema.String(), "legacy_marker_fn"}.Sanitize()+"()").Scan(&hostileTableSelect, &hostileColumnUpdate, &hostileTypeUsage, &hostileSequenceUsage, &hostileFunctionExecute); err != nil {
		t.Fatal("проверка прежних object ACL:", err)
	}
	if hostileTableSelect || hostileColumnUpdate || hostileTypeUsage || hostileSequenceUsage || hostileFunctionExecute {
		t.Fatalf("unexpected previous grants survive normalization: table=%v column=%v type=%v sequence=%v function=%v", hostileTableSelect, hostileColumnUpdate, hostileTypeUsage, hostileSequenceUsage, hostileFunctionExecute)
	}
	var publicTableGrant bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
CROSS JOIN LATERAL aclexplode(relation.relacl) acl
WHERE namespace.nspname=$1 AND relation.relname='request' AND acl.grantee=0 AND acl.privilege_type='SELECT')`, spec.Schema.String()).Scan(&publicTableGrant); err != nil || publicTableGrant {
		t.Fatalf("target schema PUBLIC table grant не удалён: exists=%v err=%v", publicTableGrant, err)
	}
	var ownerCanLogin, ownerIsSuperuser bool
	if err := admin.QueryRow(ctx, "SELECT rolcanlogin, rolsuper FROM pg_roles WHERE rolname=$1", spec.OwnerRole).Scan(&ownerCanLogin, &ownerIsSuperuser); err != nil || ownerCanLogin || ownerIsSuperuser {
		t.Fatalf("owner обязан быть NOLOGIN и не-superuser: login=%v superuser=%v err=%v", ownerCanLogin, ownerIsSuperuser, err)
	}
	assertObjectOwner(t, ctx, admin, spec.Schema, "request", spec.OwnerRole)
	assertObjectOwner(t, ctx, admin, spec.Schema, "legacy_marker", spec.OwnerRole)
	assertObjectOwner(t, ctx, admin, spec.Schema, "legacy_marker_seq", spec.OwnerRole)
	for _, relation := range []string{"legacy_partitioned", "legacy_partitioned_0", "legacy_view", "legacy_matview"} {
		assertObjectOwner(t, ctx, admin, spec.Schema, relation, spec.OwnerRole)
	}
	var functionOwner string
	if err := admin.QueryRow(ctx, `SELECT pg_get_userbyid(procedure.proowner)
FROM pg_proc procedure JOIN pg_namespace namespace ON namespace.oid=procedure.pronamespace
WHERE namespace.nspname=$1 AND procedure.proname='legacy_marker_fn'`, spec.Schema.String()).Scan(&functionOwner); err != nil || functionOwner != spec.OwnerRole {
		t.Fatalf("function ownership не перенесён к NOLOGIN owner: owner=%q err=%v", functionOwner, err)
	}
	for _, routine := range []string{"legacy_proc", "legacy_sum"} {
		if err := admin.QueryRow(ctx, `SELECT pg_get_userbyid(procedure.proowner)
FROM pg_proc procedure JOIN pg_namespace namespace ON namespace.oid=procedure.pronamespace
WHERE namespace.nspname=$1 AND procedure.proname=$2`, spec.Schema.String(), routine).Scan(&functionOwner); err != nil || functionOwner != spec.OwnerRole {
			t.Fatalf("ownership routine %s не передан NOLOGIN owner: owner=%q err=%v", routine, functionOwner, err)
		}
	}
	var domainOwner string
	for _, typeName := range []string{"legacy_domain", "legacy_composite"} {
		if err := admin.QueryRow(ctx, `SELECT pg_get_userbyid(type.typowner)
FROM pg_type type JOIN pg_namespace namespace ON namespace.oid=type.typnamespace
	WHERE namespace.nspname=$1 AND type.typname=$2`, spec.Schema.String(), typeName).Scan(&domainOwner); err != nil || domainOwner != spec.OwnerRole {
			t.Fatalf("type %s ownership не передан NOLOGIN owner: owner=%q err=%v", typeName, domainOwner, err)
		}
	}
	var collationOwner string
	if err := admin.QueryRow(ctx, `SELECT pg_get_userbyid(coll.collowner)
FROM pg_collation coll JOIN pg_namespace namespace ON namespace.oid=coll.collnamespace
WHERE namespace.nspname=$1 AND coll.collname='legacy_collation'`, spec.Schema.String()).Scan(&collationOwner); err != nil || collationOwner != spec.OwnerRole {
		t.Fatalf("collation ownership не передан NOLOGIN owner: owner=%q err=%v", collationOwner, err)
	}
	var preserved string
	if err := admin.QueryRow(ctx, "SELECT value FROM "+qualifiedSchema+`.legacy_marker WHERE id='preserved'`).Scan(&preserved); err != nil || preserved != "before-upgrade" {
		t.Fatalf("данные старого volume не сохранены: value=%q err=%v", preserved, err)
	}
	for _, statement := range []string{"CREATE SCHEMA authority_external", "CREATE TABLE authority_external.external_row(id integer)"} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("подготовка внешнего authority fixture: %v", err)
		}
	}

	runtimePool := rolePool(t, ctx, dsn, testDatabase, spec.RuntimeRole, spec.RuntimePassword)
	defer runtimePool.Close()
	var effectiveTemp bool
	if err := runtimePool.QueryRow(ctx, "SELECT has_database_privilege(current_user,current_database(),'TEMP')").Scan(&effectiveTemp); err != nil {
		t.Fatal("проверка эффективного residual TEMP privilege:", err)
	}
	t.Logf("effective runtime TEMP inherited from unchanged database ACL: %v", effectiveTemp)
	if _, err := runtimePool.Exec(ctx, "SELECT * FROM "+qualifiedSchema+`.request`); err == nil {
		t.Fatal("runtime получил table access до завершения migrations/grant lifecycle")
	}

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("получение пути теста")
	}
	requestMigrations := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../contexts/request-fulfillment/migrations/green"))
	migratorPool := rolePool(t, ctx, dsn, testDatabase, spec.MigratorRole, spec.MigratorPassword)
	defer migratorPool.Close()
	guardDir := t.TempDir() // Пустой migration set: authority проверяется до version ledger/SQL.
	ownerSession, err := migratorPool.Acquire(ctx)
	if err != nil {
		t.Fatal("acquire migration authority test connection:", err)
	}
	if _, err := ownerSession.Exec(ctx, "SET ROLE "+pgx.Identifier{spec.OwnerRole}.Sanitize()); err != nil {
		ownerSession.Release()
		t.Fatal("SET ROLE own NOLOGIN owner:", err)
	}
	if _, err := ownerSession.Exec(ctx, "CREATE TABLE public.static_escape(id integer)"); err == nil {
		_, _ = ownerSession.Exec(ctx, "RESET ROLE")
		ownerSession.Release()
		t.Fatal("PostgreSQL allowed owner CREATE in public without schema privilege")
	}
	if _, err := ownerSession.Exec(ctx, `DO $$ BEGIN EXECUTE 'CREATE TABLE public.dynamic_native_escape(id integer)'; END $$;`); err == nil {
		_, _ = ownerSession.Exec(ctx, "RESET ROLE")
		ownerSession.Release()
		t.Fatal("PostgreSQL allowed dynamic DO CREATE outside managed schema")
	}
	if _, err := ownerSession.Exec(ctx, `DO $$ BEGIN EXECUTE 'CREATE TABLE public.dynamic_escape(id integer)'; END $$;`); err == nil {
		_, _ = ownerSession.Exec(ctx, "RESET ROLE")
		ownerSession.Release()
		t.Fatal("PostgreSQL allowed dynamic DO CREATE outside managed schema")
	}
	if _, err := ownerSession.Exec(ctx, "RESET ROLE"); err != nil {
		_ = ownerSession.Hijack().Close(ctx)
		t.Fatal("RESET ROLE after native PostgreSQL denied DDL:", err)
	}
	ownerSession.Release()
	var outsideTable bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace WHERE namespace.nspname='public' AND relation.relname IN ('static_escape','dynamic_escape','dynamic_native_escape'))`).Scan(&outsideTable); err != nil || outsideTable {
		t.Fatalf("native PostgreSQL DDL denial must leave public unchanged: exists=%v err=%v", outsideTable, err)
	}
	assertRejected := func(label, contains string) {
		t.Helper()
		err := ApplyAs(ctx, migratorPool, guardDir, spec.Schema, spec.OwnerRole)
		if err == nil || !strings.Contains(err.Error(), contains) {
			t.Fatalf("ApplyAs должен отклонить %s: %v", label, err)
		}
		var ledgerExists bool
		if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace WHERE namespace.nspname=$1 AND relation.relname='schema_migrations')`, spec.Schema.String()).Scan(&ledgerExists); err != nil || ledgerExists {
			t.Fatalf("отказ %s должен произойти до migration ledger: exists=%v err=%v", label, ledgerExists, err)
		}
	}
	if _, err := admin.Exec(ctx, "GRANT "+pgx.Identifier{spec.OwnerRole}.Sanitize()+" TO "+pgx.Identifier{spec.MigratorRole}.Sanitize()+" WITH ADMIN OPTION, INHERIT TRUE, SET TRUE"); err != nil {
		t.Fatal("создание membership option drift fixture:", err)
	}
	assertRejected("membership ADMIN/SET/INHERIT option drift", "membership")
	if err := ProvisionSchema(ctx, admin, spec); err != nil {
		t.Fatal("нормализация membership option drift:", err)
	}
	assertManagedMemberships(t, ctx, admin, spec)
	var targetACLBefore string
	if err := admin.QueryRow(ctx, `SELECT COALESCE(relacl::text,'<default>') FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace WHERE namespace.nspname=$1 AND relation.relname='request'`, spec.Schema.String()).Scan(&targetACLBefore); err != nil {
		t.Fatal("snapshot target request ACL:", err)
	}
	for _, privilegeSQL := range []string{
		`GRANT SELECT ON "request-fulfillment".request TO PUBLIC;`,
		`GRANT ALL ON "request-fulfillment".request TO hostile_login;`,
		`REVOKE SELECT ON "request-fulfillment".request FROM PUBLIC;`,
	} {
		privilegeDir := writeMigrationDir(t, "request-fulfillment", "0001_forbidden_acl.sql", privilegeSQL)
		if err := ApplyAs(ctx, migratorPool, privilegeDir, spec.Schema, spec.OwnerRole); err == nil || !strings.Contains(err.Error(), "GRANT/REVOKE") {
			t.Fatalf("ApplyAs должен блокировать migration %q до ledger: %v", privilegeSQL, err)
		}
	}
	var targetACLAfter string
	if err := admin.QueryRow(ctx, `SELECT COALESCE(relacl::text,'<default>') FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace WHERE namespace.nspname=$1 AND relation.relname='request'`, spec.Schema.String()).Scan(&targetACLAfter); err != nil || targetACLAfter != targetACLBefore {
		t.Fatalf("запрещённый GRANT/REVOKE изменил target ACL: before=%s after=%s err=%v", targetACLBefore, targetACLAfter, err)
	}
	routineGrantDir := writeMigrationDir(t, "request-fulfillment", "0001_routine_acl.sql", `CREATE FUNCTION "request-fulfillment".unsafe_acl() RETURNS void LANGUAGE plpgsql AS $$ BEGIN EXECUTE 'GRANT SELECT ON "request-fulfillment".request TO PUBLIC'; END $$;`)
	if err := ApplyAs(ctx, migratorPool, routineGrantDir, spec.Schema, spec.OwnerRole); err == nil || !strings.Contains(err.Error(), "GRANT/REVOKE") {
		t.Fatalf("routine body with dynamic GRANT must fail before ledger/ACL mutation: %v", err)
	}
	var ledgerCreated bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace WHERE namespace.nspname=$1 AND relation.relname='schema_migrations')`, spec.Schema.String()).Scan(&ledgerCreated); err != nil || ledgerCreated {
		t.Fatalf("routine body GRANT должен быть отклонён до ledger: exists=%v err=%v", ledgerCreated, err)
	}
	if err := admin.QueryRow(ctx, `SELECT COALESCE(relacl::text,'<default>') FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace WHERE namespace.nspname=$1 AND relation.relname='request'`, spec.Schema.String()).Scan(&targetACLAfter); err != nil || targetACLAfter != targetACLBefore {
		t.Fatalf("routine-body GRANT changed target ACL: before=%s after=%s err=%v", targetACLBefore, targetACLAfter, err)
	}
	if _, err := admin.Exec(ctx, "GRANT CREATE ON SCHEMA authority_external TO "+pgx.Identifier{spec.OwnerRole}.Sanitize()); err != nil {
		t.Fatal("создание прямого внешнего CREATE grant:", err)
	}
	assertRejected("direct CREATE на посторонней схеме", "authority")
	if _, err := admin.Exec(ctx, "REVOKE CREATE ON SCHEMA authority_external FROM "+pgx.Identifier{spec.OwnerRole}.Sanitize()); err != nil {
		t.Fatal("удаление прямого внешнего CREATE grant:", err)
	}
	if _, err := admin.Exec(ctx, "GRANT SELECT ON authority_external.external_row TO "+pgx.Identifier{spec.OwnerRole}.Sanitize()); err != nil {
		t.Fatal("создание прямого внешнего table grant:", err)
	}
	assertRejected("direct SELECT на посторонней таблице", "authority")
	if _, err := admin.Exec(ctx, "REVOKE ALL ON authority_external.external_row FROM "+pgx.Identifier{spec.OwnerRole}.Sanitize()); err != nil {
		t.Fatal("удаление прямого внешнего table grant:", err)
	}
	if _, err := admin.Exec(ctx, "ALTER TABLE authority_external.external_row OWNER TO "+pgx.Identifier{spec.OwnerRole}.Sanitize()); err != nil {
		t.Fatal("создание внешнего ownership fixture:", err)
	}
	assertRejected("ownership постороннего relation", "ownership")
	if _, err := admin.Exec(ctx, "ALTER TABLE authority_external.external_row OWNER TO CURRENT_USER"); err != nil {
		t.Fatal("возврат внешнего relation bootstrap role:", err)
	}
	if _, err := admin.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+pgx.Identifier{spec.OwnerRole}.Sanitize()+" IN SCHEMA authority_external GRANT SELECT ON TABLES TO "+pgx.Identifier{hostileLogin}.Sanitize()); err != nil {
		t.Fatal("создание external per-schema default ACL fixture:", err)
	}
	assertRejected("stale per-schema default ACL", "default ACL")
	if _, err := admin.Exec(ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE "+pgx.Identifier{spec.OwnerRole}.Sanitize()+" GRANT SELECT ON TABLES TO "+pgx.Identifier{hostileLogin}.Sanitize()); err != nil {
		t.Fatal("создание global default ACL fixture:", err)
	}
	assertRejected("stale global default ACL", "default ACL")
	if err := ProvisionSchema(ctx, admin, spec); err != nil {
		t.Fatal("очистка owner-scoped stale default ACL:", err)
	}
	if _, err := admin.Exec(ctx, "GRANT CREATE ON SCHEMA public TO PUBLIC"); err != nil {
		t.Fatal("создание PUBLIC CREATE fixture в child DB:", err)
	}
	assertRejected("effective PUBLIC CREATE на public schema", "CREATE")
	if _, err := admin.Exec(ctx, "REVOKE CREATE ON SCHEMA public FROM PUBLIC"); err != nil {
		t.Fatal("восстановление исходного ACL schema public в child DB:", err)
	}
	if _, err := admin.Exec(ctx, `CREATE FUNCTION authority_external.dangerous_definer() RETURNS void
LANGUAGE plpgsql SECURITY DEFINER AS $$ BEGIN CREATE TABLE authority_external.definer_side_effect(id integer); END $$`); err != nil {
		t.Fatal("создание внешней SECURITY DEFINER fixture:", err)
	}
	securityDefinerDir := writeMigrationDir(t, "request-fulfillment", "0001_call_external_definer.sql", `SELECT authority_external.dangerous_definer();`)
	if err := ApplyAs(ctx, migratorPool, securityDefinerDir, spec.Schema, spec.OwnerRole); err == nil || !strings.Contains(err.Error(), "SECURITY DEFINER") {
		t.Fatalf("внешняя callable SECURITY DEFINER function должна блокировать ApplyAs: %v", err)
	}
	var definerSideEffect bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace WHERE namespace.nspname='authority_external' AND relation.relname='definer_side_effect')`).Scan(&definerSideEffect); err != nil || definerSideEffect {
		t.Fatalf("SECURITY DEFINER migration side effect не должен происходить: exists=%v err=%v", definerSideEffect, err)
	}
	if _, err := admin.Exec(ctx, "DROP FUNCTION authority_external.dangerous_definer()"); err != nil {
		t.Fatal("удаление внешней SECURITY DEFINER fixture:", err)
	}
	if _, err := admin.Exec(ctx, `CREATE SCHEMA "pgXtempY"`); err != nil {
		t.Fatal("создание pgXtempY regression schema:", err)
	}
	if _, err := admin.Exec(ctx, `CREATE FUNCTION "pgXtempY".dangerous_definer() RETURNS void LANGUAGE plpgsql SECURITY DEFINER AS $$ BEGIN CREATE TABLE "pgXtempY".definer_side_effect(id integer); END $$`); err != nil {
		t.Fatal("создание pgXtempY SECURITY DEFINER regression fixture:", err)
	}
	pgPatternDir := writeMigrationDir(t, "request-fulfillment", "0001_pg_prefix_definer.sql", `SELECT "pgXtempY".dangerous_definer();`)
	if err := ApplyAs(ctx, migratorPool, pgPatternDir, spec.Schema, spec.OwnerRole); err == nil || !strings.Contains(err.Error(), "SECURITY DEFINER") {
		t.Fatalf("pgXtempY не должен ошибочно классифицироваться как системная схема: %v", err)
	}
	var pgPatternSideEffect bool
	if err := admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace WHERE namespace.nspname='pgXtempY' AND relation.relname='definer_side_effect')`).Scan(&pgPatternSideEffect); err != nil || pgPatternSideEffect {
		t.Fatalf("pgXtempY SECURITY DEFINER side effect не должен исполняться: exists=%v err=%v", pgPatternSideEffect, err)
	}
	if _, err := admin.Exec(ctx, `DROP FUNCTION "pgXtempY".dangerous_definer()`); err != nil {
		t.Fatal("удаление pgXtempY SECURITY DEFINER regression fixture:", err)
	}
	dynamicDir := writeMigrationDir(t, "request-fulfillment", "0001_dynamic_public_ddl.sql", `DO $$ BEGIN EXECUTE 'CREATE TABLE public.dynamic_escape(id integer)'; END $$;`)
	if err := ApplyAs(ctx, migratorPool, dynamicDir, spec.Schema, spec.OwnerRole); err == nil || !strings.Contains(err.Error(), "постороннюю схему") {
		t.Fatalf("dynamic DO DDL вне target должен быть отвергнут scanner-ом: %v", err)
	}
	staticDir := writeMigrationDir(t, "request-fulfillment", "0001_static_public_ddl.sql", `CREATE TABLE public.static_escape(id integer);`)
	if err := ApplyAs(ctx, migratorPool, staticDir, spec.Schema, spec.OwnerRole); err == nil || !strings.Contains(err.Error(), "постороннюю схему") {
		t.Fatalf("static public DDL должен быть отвергнут до исполнения: %v", err)
	}
	if err := ApplyAs(ctx, runtimePool, requestMigrations, spec.Schema, spec.OwnerRole); err == nil {
		t.Fatal("runtime identity не должна мочь принять owner role и запускать миграции")
	}
	if err := ApplyAs(ctx, migratorPool, requestMigrations, spec.Schema, spec.OwnerRole); err != nil {
		t.Fatal("выполнение полной request-fulfillment migration path под owner:", err)
	}
	if err := ProvisionSchema(ctx, admin, spec); err != nil {
		t.Fatal("повторный provision существующей полностью мигрированной схемы:", err)
	}
	assertObjectOwner(t, ctx, admin, spec.Schema, "execution_resolution_revision", spec.OwnerRole)
	runtimeGrants := spec
	runtimeGrants.MigratorPassword = ""
	runtimeGrants.RuntimePassword = ""
	if err := GrantRuntimeSchema(ctx, admin, runtimeGrants); err != nil {
		t.Fatal("выдача runtime allowlist после migrations:", err)
	}
	hostilePool := rolePool(t, ctx, dsn, testDatabase, hostileLogin, "hostile-test-secret")
	defer hostilePool.Close()
	for _, role := range []string{spec.OwnerRole, spec.MigratorRole, spec.RuntimeRole} {
		if _, err := hostilePool.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize()); err == nil {
			t.Errorf("hostile login сохранил membership для SET ROLE %q", role)
		}
	}
	if _, err := hostilePool.Exec(ctx, "SELECT * FROM "+qualifiedSchema+`.request`); err == nil {
		t.Fatal("former grantee сохранил ACL на управляемую таблицу")
	}
	if err := Verify(ctx, runtimePool, requestMigrations, spec.Schema); err != nil {
		t.Fatal("read-only version verification на dash schema:", err)
	}
	if _, err := runtimePool.Exec(ctx, "UPDATE "+qualifiedSchema+`.request SET version=2 WHERE id='existing-request'`); err != nil {
		t.Fatal("runtime UPDATE разрешённого request aggregate:", err)
	}
	tx, err := runtimePool.Begin(ctx)
	if err != nil {
		t.Fatal("начало runtime attempt транзакции:", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO "+qualifiedSchema+`.execution_attempt (request_id,tenant_id,generation,command_id,request_version,intent,target_state,state) VALUES ('existing-request','tenant-existing',1,'command-1',2,'{}'::jsonb,'unspecified','queued')`); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal("runtime INSERT execution_attempt:", err)
	}
	if _, err := tx.Exec(ctx, "UPDATE "+qualifiedSchema+`.execution_attempt SET state='claimed' WHERE request_id='existing-request' AND generation=1`); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal("runtime UPDATE execution_attempt:", err)
	}
	var attemptState string
	if err := tx.QueryRow(ctx, "SELECT state FROM "+qualifiedSchema+`.execution_attempt WHERE request_id='existing-request' AND generation=1`).Scan(&attemptState); err != nil || attemptState != "claimed" {
		_ = tx.Rollback(ctx)
		t.Fatalf("runtime read attempt in transaction: state=%q err=%v", attemptState, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal("фиксация runtime attempt транзакции:", err)
	}
	if _, err := runtimePool.Exec(ctx, "INSERT INTO "+qualifiedSchema+`.outbox (id,exchange,routing_key,body,headers) VALUES ('outbox-1','events','request.created',decode('00','hex'),'{}'::jsonb)`); err != nil {
		t.Fatal("runtime INSERT outbox:", err)
	}
	if _, err := runtimePool.Exec(ctx, "UPDATE "+qualifiedSchema+`.outbox SET published_at=clock_timestamp() WHERE id='outbox-1'`); err != nil {
		t.Fatal("runtime UPDATE outbox:", err)
	}
	if _, err := runtimePool.Exec(ctx, "INSERT INTO "+qualifiedSchema+`.execution_resolution_revision (resolution_id,tenant_id,request_id,resolution_kind,revision,expected_revision,supersedes_id,generation,attempt_id,target_state,lead_target,runtime_outcome,observed_run_id,actor,resolved_at,evidence_fingerprint) VALUES ('resolution-1','tenant-existing','resolution-request','target',1,0,NULL,NULL,NULL,'explicit',2,NULL,NULL,'operator',clock_timestamp(),repeat('a',64))`); err != nil {
		t.Fatal("runtime INSERT append-only resolution revision:", err)
	}
	var resolutionID string
	if err := runtimePool.QueryRow(ctx, "SELECT resolution_id FROM "+qualifiedSchema+`.execution_resolution_revision WHERE resolution_id='resolution-1'`).Scan(&resolutionID); err != nil || resolutionID != "resolution-1" {
		t.Fatalf("runtime read append-only resolution: id=%q err=%v", resolutionID, err)
	}
	for _, statement := range []string{
		"UPDATE " + qualifiedSchema + `.execution_resolution_revision SET actor='rewritten' WHERE resolution_id='resolution-1'`,
		"DELETE FROM " + qualifiedSchema + `.execution_resolution_revision WHERE resolution_id='resolution-1'`,
		"TRUNCATE " + qualifiedSchema + `.execution_resolution_revision`,
		"ALTER TABLE " + qualifiedSchema + `.execution_resolution_revision ADD COLUMN unsafe text`,
		"ALTER TABLE " + qualifiedSchema + `.execution_resolution_revision DISABLE TRIGGER execution_resolution_revision_immutable`,
	} {
		if _, err := runtimePool.Exec(ctx, statement); err == nil {
			t.Errorf("runtime operation должна быть запрещена: %s", statement)
		}
	}

	// Migrator подключается отдельными credentials: membership только в owner своей
	// схемы позволяет ApplyAs, но не открывает другие identities/owner roles.
	var ownOwner, otherOwnerMember bool
	var otherMemberships int
	if err := migratorPool.QueryRow(ctx, `SELECT pg_has_role(current_user,$1,'MEMBER'), pg_has_role(current_user,$2,'MEMBER'),
(SELECT count(*)::int FROM pg_auth_members membership JOIN pg_roles parent ON parent.oid=membership.roleid WHERE membership.member=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND parent.rolname<>$1)`, spec.OwnerRole, otherOwner).Scan(&ownOwner, &otherOwnerMember, &otherMemberships); err != nil {
		t.Fatal("проверка migrator memberships:", err)
	}
	if !ownOwner || otherOwnerMember || otherMemberships != 0 {
		t.Fatalf("migrator должен иметь membership только в своём owner: own_owner=%v other_owner=%v other_memberships=%d", ownOwner, otherOwnerMember, otherMemberships)
	}
	if _, err := migratorPool.Exec(ctx, "SET ROLE "+pgx.Identifier{otherOwner}.Sanitize()); err == nil {
		t.Fatal("migrator смог SET ROLE в посторонний owner")
	}
	if _, err := migratorPool.Exec(ctx, "SET ROLE "+pgx.Identifier{spec.OwnerRole}.Sanitize()); err != nil {
		t.Fatal("migrator не может SET ROLE в свой owner:", err)
	}
	if _, err := migratorPool.Exec(ctx, "CREATE TABLE "+qualifiedSchema+`.migrator_probe (id int)`); err != nil {
		t.Fatal("свой owner role не позволил миграционный DDL:", err)
	}
	if _, err := migratorPool.Exec(ctx, "DROP TABLE "+qualifiedSchema+`.migrator_probe`); err != nil {
		t.Fatal("очистка migrator probe:", err)
	}
	if _, err := migratorPool.Exec(ctx, "RESET ROLE"); err != nil {
		t.Fatal("сброс owner role у migrator:", err)
	}

	var runtimeOwner, runtimeMigrator bool
	if err := runtimePool.QueryRow(ctx, `SELECT pg_has_role(current_user,$1,'MEMBER'), pg_has_role(current_user,$2,'MEMBER')`, spec.OwnerRole, spec.MigratorRole).Scan(&runtimeOwner, &runtimeMigrator); err != nil {
		t.Fatal("проверка runtime memberships:", err)
	}
	if runtimeOwner || runtimeMigrator {
		t.Fatalf("runtime не должен быть членом owner/migrator: owner=%v migrator=%v", runtimeOwner, runtimeMigrator)
	}
	for _, role := range []string{spec.OwnerRole, spec.MigratorRole} {
		if _, err := runtimePool.Exec(ctx, "SET ROLE "+pgx.Identifier{role}.Sanitize()); err == nil {
			t.Errorf("runtime смог SET ROLE в %q", role)
		}
	}
	for _, statement := range []string{
		"CREATE FUNCTION " + qualifiedSchema + `.legacy_operator_eq(integer,integer) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$ SELECT $1=$2 $$`,
		"CREATE OPERATOR " + qualifiedSchema + `.=== (PROCEDURE = ` + qualifiedSchema + `.legacy_operator_eq, LEFTARG=integer, RIGHTARG=integer)`,
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("подготовка unsupported operator класса: %v", err)
		}
	}
	if err := ProvisionSchema(ctx, admin, spec); err == nil || !strings.Contains(err.Error(), "unsupported") && !strings.Contains(err.Error(), "неподдерживаемый") {
		t.Fatalf("provision обязан fail-closed на неподдерживаемом operator class: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE TYPE "+qualifiedSchema+`.legacy_int_range AS RANGE (subtype=integer)`); err != nil {
		t.Fatal("создание range fixture для fail-closed проверки:", err)
	}
	if err := ProvisionSchema(ctx, admin, spec); err == nil || !strings.Contains(err.Error(), "range/multirange") {
		t.Fatalf("provision обязан fail-closed на range/multirange ownership: %v", err)
	}
}

func rolePool(t *testing.T, ctx context.Context, dsn, database, role, password string) *pgxpool.Pool {
	t.Helper()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("разбор тестового DSN:", err)
	}
	config.User = role
	config.Password = password
	config.Database = database
	pool, err := pgxpool.NewWithConfig(ctx, pgxpoolConfig(config))
	if err != nil {
		t.Fatalf("создание пула роли %q: %v", role, err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("подключение роли %q: %v", role, err)
	}
	return pool
}

func writeMigrationDir(t *testing.T, schema, filename, body string) string {
	t.Helper()
	directory := t.TempDir()
	content := "-- schema: " + schema + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(directory, filename), []byte(content), 0600); err != nil {
		t.Fatal("создание migration guard fixture:", err)
	}
	return directory
}

func assertObjectOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema Schema, relation, expected string) {
	t.Helper()
	var actual string
	if err := pool.QueryRow(ctx, `SELECT pg_get_userbyid(relation.relowner)
FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace
WHERE namespace.nspname=$1 AND relation.relname=$2`, schema.String(), relation).Scan(&actual); err != nil || actual != expected {
		t.Fatalf("ownership %s.%s=%q, требуется %q: %v", schema, relation, actual, expected, err)
	}
}

func databasePublicPrivileges(t *testing.T, ctx context.Context, pool *pgxpool.Pool, probeRole string) string {
	t.Helper()
	var acl string
	if err := pool.QueryRow(ctx, `SELECT has_database_privilege($1,current_database(),'CONNECT')::text || ':' ||
has_database_privilege($1,current_database(),'TEMP')::text || ':' ||
has_database_privilege($1,current_database(),'CREATE')::text`, probeRole).Scan(&acl); err != nil {
		t.Fatal("чтение эффективных PUBLIC database privileges:", err)
	}
	return acl
}

func publicSchemaPublicPrivileges(t *testing.T, ctx context.Context, pool *pgxpool.Pool, probeRole string) string {
	t.Helper()
	var acl string
	if err := pool.QueryRow(ctx, `SELECT has_schema_privilege($1,'public','USAGE')::text || ':' ||
has_schema_privilege($1,'public','CREATE')::text`, probeRole).Scan(&acl); err != nil {
		t.Fatal("чтение эффективных PUBLIC schema privileges:", err)
	}
	return acl
}

func assertPublicPrivilegesUnchanged(t *testing.T, ctx context.Context, pool *pgxpool.Pool, probeRole, beforeDatabase, beforePublic string) {
	t.Helper()
	if after := databasePublicPrivileges(t, ctx, pool, probeRole); after != beforeDatabase {
		t.Fatalf("provision изменил эффективные PUBLIC database privileges: before=%s after=%s", beforeDatabase, after)
	}
	if after := publicSchemaPublicPrivileges(t, ctx, pool, probeRole); after != beforePublic {
		t.Fatalf("provision изменил эффективные PUBLIC privileges schema public: before=%s after=%s", beforePublic, after)
	}
}

func assertManagedMemberships(t *testing.T, ctx context.Context, pool *pgxpool.Pool, spec RoleSet) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM pg_auth_members membership
JOIN pg_roles granted ON granted.oid=membership.roleid JOIN pg_roles member ON member.oid=membership.member
WHERE (granted.rolname=ANY($1::text[]) OR member.rolname=ANY($1::text[]))
  AND NOT (granted.rolname=$2 AND member.rolname=$3)`, []string{spec.OwnerRole, spec.MigratorRole, spec.RuntimeRole}, spec.OwnerRole, spec.MigratorRole).Scan(&count); err != nil {
		t.Fatal("чтение memberships managed roles:", err)
	}
	if count != 0 {
		t.Fatalf("найдены неожиданные memberships managed identities, count=%d", count)
	}
	var approved bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_auth_members membership
JOIN pg_roles granted ON granted.oid=membership.roleid JOIN pg_roles member ON member.oid=membership.member
WHERE granted.rolname=$1 AND member.rolname=$2 AND NOT membership.inherit_option AND membership.set_option)`, spec.OwnerRole, spec.MigratorRole).Scan(&approved); err != nil || !approved {
		t.Fatalf("отсутствует approved migrator→owner SET-only membership: approved=%v err=%v", approved, err)
	}
}
