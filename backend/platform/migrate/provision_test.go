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

func TestProvisionSchemaWithIsolatedPostgres(t *testing.T) {
	fixture, testDatabase := provisionTestDatabase(t)
	admin := fixture.pool
	dsn := os.Getenv("LEADGEN_POSTGRES_TEST_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := admin.Ping(ctx); err != nil {
		t.Fatal("подключение к PostgreSQL:", err)
	}

	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	schema := "spc_" + stamp
	foreignSchema := "spc_foreign_" + stamp
	spec := RoleSet{
		Schema:           Schema(schema),
		OwnerRole:        "spc_owner_" + stamp,
		MigratorRole:     "spc_migrator_" + stamp,
		MigratorPassword: "migration-secret-" + stamp,
		RuntimeRole:      "spc_runtime_" + stamp,
		RuntimePassword:  "runtime-secret-" + stamp,
		OwnedRelations:   []string{"request", "resolution", migrationsTable},
		OwnedRoutines:    []RoutineRef{{Kind: "function", Name: "deny_trigger_change"}},
		Grants: []TableGrant{
			{Table: "request", Privileges: []string{"SELECT", "INSERT", "UPDATE"}},
			{Table: "resolution", Privileges: []string{"SELECT", "INSERT"}},
			{Table: migrationsTable, Privileges: []string{"SELECT"}},
		},
	}
	assertTestRolesAbsent(t, ctx, admin, spec.RuntimeRole, spec.MigratorRole, spec.OwnerRole)
	defer fixture.cleanup(t, spec.RuntimeRole, spec.MigratorRole, spec.OwnerRole)

	if err := ProvisionSchema(ctx, admin, spec); err != nil {
		t.Fatal("первый provision:", err)
	}
	owner := pgx.Identifier{spec.OwnerRole}.Sanitize()
	qualifiedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "SET ROLE "+owner); err != nil {
		t.Fatal("SET ROLE owner:", err)
	}
	for _, statement := range []string{
		"CREATE TABLE " + qualifiedSchema + `.request (id text PRIMARY KEY, value text NOT NULL)`,
		"CREATE TABLE " + qualifiedSchema + `.resolution (id text PRIMARY KEY, value text NOT NULL)`,
		"CREATE TABLE " + qualifiedSchema + `.` + pgx.Identifier{migrationsTable}.Sanitize() + ` (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`,
		"INSERT INTO " + qualifiedSchema + `.` + pgx.Identifier{migrationsTable}.Sanitize() + ` (version) VALUES ('001_runtime_check.sql')`,
		"CREATE FUNCTION " + qualifiedSchema + `.deny_trigger_change() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'append-only'; END $$`,
		"CREATE TRIGGER resolution_immutable BEFORE UPDATE OR DELETE ON " + qualifiedSchema + `.resolution FOR EACH ROW EXECUTE FUNCTION ` + qualifiedSchema + `.deny_trigger_change()`,
	} {
		if _, err := admin.Exec(ctx, statement); err != nil {
			t.Fatalf("создание тестового объекта от owner (%s): %v", statement, err)
		}
	}
	if _, err := admin.Exec(ctx, "RESET ROLE"); err != nil {
		t.Fatal("RESET ROLE:", err)
	}
	// Второй проход имитирует безопасное обновление уже существующего volume.
	if err := ProvisionSchema(ctx, admin, spec); err != nil {
		t.Fatal("повторный provision:", err)
	}
	// Повтор с иным desired password не является ротацией: действующий пароль
	// остаётся прежним, а pool с новым значением не аутентифицируется.
	changedCredentials := spec
	changedCredentials.RuntimePassword = "different-candidate-password-" + stamp
	changedCredentials.MigratorPassword = "different-migration-password-" + stamp
	if err := ProvisionSchema(ctx, admin, changedCredentials); err != nil {
		t.Fatal("provision не должен менять пароль существующей роли:", err)
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("разбор admin DSN:", err)
	}
	config.Database = testDatabase
	config.User = spec.MigratorRole
	config.Password = spec.MigratorPassword
	migration, err := pgxpool.NewWithConfig(ctx, pgxpoolConfig(config))
	if err != nil {
		t.Fatal("создание migration-пула:", err)
	}
	defer migration.Close()
	if err := GrantRuntimeSchemaAs(ctx, migration, spec, spec.OwnerRole); err != nil {
		t.Fatal("runtime grants от owner через migration identity:", err)
	}
	config.User = spec.RuntimeRole
	config.Password = spec.RuntimePassword
	runtime, err := pgxpool.NewWithConfig(ctx, pgxpoolConfig(config))
	if err != nil {
		t.Fatal("создание runtime-пула:", err)
	}
	defer runtime.Close()
	if err := runtime.Ping(ctx); err != nil {
		t.Fatal("подключение runtime identity:", err)
	}
	if _, err := runtime.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatal("запрос существующей runtime identity после повторного provision:", err)
	}
	config.Password = changedCredentials.RuntimePassword
	wrongPasswordPool, err := pgxpool.NewWithConfig(ctx, pgxpoolConfig(config))
	if err != nil {
		t.Fatal("создание отрицательного runtime-пула:", err)
	}
	if err := wrongPasswordPool.Ping(ctx); err == nil {
		wrongPasswordPool.Close()
		t.Fatal("повторный provision изменил пароль существующей runtime role")
	}
	wrongPasswordPool.Close()
	config.Password = spec.RuntimePassword
	if err := VerifyRuntimeRole(ctx, runtime, spec); err != nil {
		t.Fatal("runtime role/schema scope verification:", err)
	}
	// Подготовка G2 добавляет generation-scoped login, не заменяя G1
	// credentials и не прерывая существующий G1 runtime connection.
	g2Scope := RuntimeRoleScope{Environment: "test", Deployment: "release", Target: schema, Generation: "G2"}
	g2Role, err := RuntimeRoleName(g2Scope)
	if err != nil {
		t.Fatal("derive G2 runtime role:", err)
	}
	g2Spec := spec
	g2Spec.RuntimeRole = g2Role
	g2Spec.RuntimePassword = "g2-runtime-secret-" + stamp
	g2Spec.RuntimeScope = &g2Scope
	if err := ProvisionSchema(ctx, admin, g2Spec); err != nil {
		t.Fatal("G2 provisioning must be additive and preserve G1:", err)
	}
	if err := GrantRuntimeSchemaAs(ctx, migration, g2Spec, spec.OwnerRole); err != nil {
		t.Fatal("G2 runtime grants:", err)
	}
	if err := runtime.Ping(ctx); err != nil {
		t.Fatal("G1 runtime connection stopped working after G2 provision:", err)
	}
	if _, err := runtime.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatal("G1 runtime query stopped working after G2 provision:", err)
	}
	config.User = g2Role
	config.Password = g2Spec.RuntimePassword
	g2Runtime, err := pgxpool.NewWithConfig(ctx, pgxpoolConfig(config))
	if err != nil {
		t.Fatal("create G2 runtime pool:", err)
	}
	defer g2Runtime.Close()
	if err := g2Runtime.Ping(ctx); err != nil {
		t.Fatal("G2 candidate credentials rejected:", err)
	}
	if _, err := g2Runtime.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatal("G2 candidate query failed:", err)
	}
	config.Password = "wrong-g2-runtime-secret-" + stamp
	wrongG2, err := pgxpool.NewWithConfig(ctx, pgxpoolConfig(config))
	if err != nil {
		t.Fatal("create wrong-password G2 pool:", err)
	}
	if err := wrongG2.Ping(ctx); err == nil {
		wrongG2.Close()
		t.Fatal("wrong G2 candidate password authenticated")
	}
	wrongG2.Close()
	config.User, config.Password = spec.RuntimeRole, spec.RuntimePassword
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{foreignSchema}.Sanitize()); err != nil {
		t.Fatal("создание чужой схемы для проверки границы:", err)
	}
	if _, err := admin.Exec(ctx, "CREATE TABLE "+pgx.Identifier{foreignSchema, "private"}.Sanitize()+" (id int)"); err != nil {
		t.Fatal("создание таблицы чужой схемы:", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DOMAIN "+pgx.Identifier{foreignSchema, "public_type_probe"}.Sanitize()+" AS text"); err != nil {
		t.Fatal("создание domain в чужой схеме:", err)
	}
	// Foreign row type имеет PUBLIC USAGE по глобальному PostgreSQL default;
	// verifier замечает этот residual, но не меняет ACL чужой схемы/owner-а.
	if err := VerifyRuntimeRole(ctx, runtime, spec); err == nil {
		t.Fatal("runtime verifier accepted an effective type privilege outside target schema")
	}
	migrationsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(migrationsDir, "001_runtime_check.sql"), []byte("-- schema: "+schema+"\n-- проверка read-only версии\n"), 0600); err != nil {
		t.Fatal("создание фикстуры миграции:", err)
	}
	if err := Verify(ctx, runtime, migrationsDir, Schema(schema)); err != nil {
		var adminFound bool
		var adminUsage, adminRead bool
		var runtimeDatabase string
		var schemaUsage, versionRead bool
		_ = admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_class relation JOIN pg_namespace namespace ON namespace.oid=relation.relnamespace WHERE namespace.nspname=$1 AND relation.relname=$2 AND relation.relkind IN ('r','p'))`, schema, migrationsTable).Scan(&adminFound)
		_ = admin.QueryRow(ctx, `SELECT has_schema_privilege($1,$2,'USAGE'), has_table_privilege($1,$3,'SELECT')`, spec.RuntimeRole, schema, pgx.Identifier{schema, migrationsTable}.Sanitize()).Scan(&adminUsage, &adminRead)
		runtimeErr := runtime.QueryRow(ctx, `SELECT current_database(), has_schema_privilege(current_user,$1,'USAGE'), has_table_privilege(current_user,$2,'SELECT')`, schema, pgx.Identifier{schema, migrationsTable}.Sanitize()).Scan(&runtimeDatabase, &schemaUsage, &versionRead)
		t.Fatalf("read-only проверка схемы runtime-ролью: %v (admin sees table=%v/admin usage=%v read=%v runtime db=%q usage=%v read=%v queryErr=%v)", err, adminFound, adminUsage, adminRead, runtimeDatabase, schemaUsage, versionRead, runtimeErr)
	}
	if _, err := runtime.Exec(ctx, "INSERT INTO "+qualifiedSchema+`.request VALUES ('r1', 'created')`); err != nil {
		t.Fatal("runtime INSERT request:", err)
	}
	if _, err := runtime.Exec(ctx, "UPDATE "+qualifiedSchema+`.request SET value='updated' WHERE id='r1'`); err != nil {
		t.Fatal("runtime UPDATE request:", err)
	}
	if _, err := runtime.Exec(ctx, "INSERT INTO "+qualifiedSchema+`.resolution VALUES ('x1', 'append')`); err != nil {
		t.Fatal("runtime INSERT resolution:", err)
	}
	var superuser, ownerMember, migratorMember bool
	if err := runtime.QueryRow(ctx, "SELECT rolsuper FROM pg_roles WHERE rolname=current_user").Scan(&superuser); err != nil || superuser {
		t.Fatalf("runtime не должен быть superuser: superuser=%v err=%v", superuser, err)
	}
	if err := runtime.QueryRow(ctx, `SELECT pg_has_role(current_user, $1, 'MEMBER')`, spec.OwnerRole).Scan(&ownerMember); err != nil || ownerMember {
		t.Fatalf("runtime не должен быть членом owner: member=%v err=%v", ownerMember, err)
	}
	if err := runtime.QueryRow(ctx, `SELECT pg_has_role(current_user, $1, 'MEMBER')`, spec.MigratorRole).Scan(&migratorMember); err != nil || migratorMember {
		t.Fatalf("runtime не должен быть членом migrator: member=%v err=%v", migratorMember, err)
	}
	for _, statement := range []string{
		"SELECT * FROM " + pgx.Identifier{foreignSchema, "private"}.Sanitize(),
		"INSERT INTO " + pgx.Identifier{foreignSchema, "private"}.Sanitize() + " VALUES (1)",
		"UPDATE " + qualifiedSchema + `.resolution SET value='rewrite' WHERE id='x1'`,
		"DELETE FROM " + qualifiedSchema + `.resolution WHERE id='x1'`,
		"TRUNCATE " + qualifiedSchema + `.resolution`,
		"ALTER TABLE " + qualifiedSchema + `.resolution ADD COLUMN extra text`,
		"DROP TABLE " + qualifiedSchema + `.resolution`,
		"ALTER TABLE " + qualifiedSchema + `.resolution DISABLE TRIGGER resolution_immutable`,
		"SET ROLE " + owner,
		"SET ROLE " + pgx.Identifier{spec.MigratorRole}.Sanitize(),
	} {
		if _, err := runtime.Exec(ctx, statement); err == nil {
			t.Errorf("runtime statement должен быть запрещён: %s", statement)
		}
	}
	if _, err := runtime.Exec(ctx, "CREATE TABLE "+qualifiedSchema+`.intruder (id int)`); err == nil {
		t.Fatal("runtime не должен создавать DDL")
	}
}

func TestRuntimeRoleNameUsesCanonicalScopeWithinPostgresLimit(t *testing.T) {
	scope := RuntimeRoleScope{Environment: "prod", Deployment: "deployment-123", Target: "request-fulfillment", Generation: "g2"}
	first, err := RuntimeRoleName(scope)
	if err != nil {
		t.Fatal("derive runtime role:", err)
	}
	second, err := RuntimeRoleName(scope)
	if err != nil {
		t.Fatal("derive repeated runtime role:", err)
	}
	if first != second || len(first) > postgresIdentifierLimit {
		t.Fatalf("role derivation is not stable/valid: same=%v bytes=%d", first == second, len(first))
	}
	if first == "" || first == scope.Environment || first == scope.Deployment || first == scope.Target || first == scope.Generation {
		t.Fatal("role name must not expose raw scope")
	}
	changed := scope
	changed.Generation = "g3"
	third, err := RuntimeRoleName(changed)
	if err != nil {
		t.Fatal("derive next generation runtime role:", err)
	}
	if first == third {
		t.Fatal("разные generations получили одну runtime role")
	}
	invalid := scope
	invalid.Target = "request role arbitrary"
	if _, err := RuntimeRoleName(invalid); err == nil {
		t.Fatal("произвольный target должен быть отклонён")
	}
}

func TestGenerationRoleSetRejectsRuntimeRoleScopeMismatch(t *testing.T) {
	scope := RuntimeRoleScope{Environment: "test", Deployment: "deployment", Target: "ignored", Generation: "g2"}
	sets, err := RequestIntakeRoleSetsForGeneration(scope, "migrator-a", "runtime-a", "migrator-b", "runtime-b", "migrator-c", "runtime-c", "integration-runtime")
	if err != nil {
		t.Fatal("generation role sets:", err)
	}
	for _, set := range sets {
		if err := validateRoleSet(set); err != nil {
			t.Fatalf("generated role set %q invalid: %v", set.Schema, err)
		}
	}
	sets[0].RuntimeRole = "leadgen_request_fulfillment_runtime"
	if err := validateRoleSet(sets[0]); err == nil {
		t.Fatal("arbitrary/non-generation runtime role was accepted")
	}
}

func TestRequestIntakeRoleSetsSeparateRealDependencies(t *testing.T) {
	sets := RequestIntakeRoleSets("request-migrator-secret", "request-runtime-secret", "console-migrator-secret", "console-runtime-secret", "vector-migrator-secret", "vector-runtime-secret", "integration-runtime-secret")
	if len(sets) != 3 {
		t.Fatalf("получено %d RoleSet; требуется request, consoleauth и vector", len(sets))
	}
	wantSchemas := []string{"request-fulfillment", "consoleauth", "integration-platform"}
	for index, want := range wantSchemas {
		if sets[index].Schema.String() != want {
			t.Errorf("RoleSet[%d].Schema=%q, ожидается %q", index, sets[index].Schema, want)
		}
		if sets[index].OwnerRole == sets[index].RuntimeRole || sets[index].MigratorRole == sets[index].RuntimeRole {
			t.Errorf("runtime identity схемы %q совпала с owner/migrator", want)
		}
		for _, grant := range sets[index].Grants {
			for _, privilege := range grant.Privileges {
				if privilege == "TRUNCATE" || privilege == "TRIGGER" || privilege == "REFERENCES" {
					t.Errorf("схема %q выдаёт лишнюю операцию %s на %s", want, privilege, grant.Table)
				}
			}
		}
	}
	if sets[0].RuntimePassword == sets[1].RuntimePassword || sets[1].RuntimePassword == sets[2].RuntimePassword || sets[0].RuntimePassword == sets[2].RuntimePassword {
		t.Fatal("для зависимостей должны задаваться разные runtime credentials")
	}
	integration := sets[2]
	if len(integration.AdditionalRuntimes) != 1 || integration.AdditionalRuntimes[0].Role != "leadgen_integration_platform_runtime" {
		t.Fatalf("integration-platform service должен иметь отдельный runtime identity: %#v", integration.AdditionalRuntimes)
	}
	if integration.AdditionalRuntimes[0].Password == integration.RuntimePassword {
		t.Fatal("vector-only и integration service runtime используют общий credential")
	}
	sequenceGrants := integration.AdditionalRuntimes[0].SequenceGrants
	if len(integration.SequenceGrants) != 0 || len(sequenceGrants) != 1 || sequenceGrants[0].Sequence != "integration_invocation_seq_seq" || len(sequenceGrants[0].Privileges) != 2 || sequenceGrants[0].Privileges[0] != "USAGE" || sequenceGrants[0].Privileges[1] != "SELECT" {
		t.Fatalf("bigserial sequence grants должны быть только у integration runtime: primary=%#v additional=%#v", integration.SequenceGrants, integration.AdditionalRuntimes[0].SequenceGrants)
	}
}

func assertTestRolesAbsent(t *testing.T, ctx context.Context, pool *pgxpool.Pool, roles ...string) {
	t.Helper()
	for _, role := range roles {
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil || exists {
			t.Fatalf("role collision or preflight failure for test role %q: exists=%v err=%v", role, exists, err)
		}
	}
}

// pgxpoolConfig создаёт пул из уже разобранного config без повторной сериализации
// DSN и без отображения секретных параметров в логах теста.
func pgxpoolConfig(config *pgx.ConnConfig) *pgxpool.Config {
	poolConfig, err := pgxpool.ParseConfig(config.ConnString())
	if err != nil {
		panic("некорректная тестовая конфигурация PostgreSQL")
	}
	poolConfig.ConnConfig = config
	return poolConfig
}
