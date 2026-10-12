package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	integrationplatform "lead-gen/backend/contexts/integration-platform"
	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/migrate"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/contracts/integration-platform/vector"
)

func TestResolveIntakeDBPolicyIsFailClosedAndOnlySelectsIntake(t *testing.T) {
	selected := []selectedService{{ID: requestfulfillment.ServiceID}}
	files := intakeRuntimeFileFlags{
		requestDSNFile:     testSecretFile(t, "request-dsn"),
		consoleAuthDSNFile: testSecretFile(t, "console-dsn"),
		vectorDSNFile:      testSecretFile(t, "vector-dsn"),
	}
	noEnv := func(string) (string, bool) { return "", false }
	policy, dsns, err := resolveIntakeDBPolicy("runtime-only", selected, files, nil, noEnv)
	if err == nil || policy != "" || dsns != nil {
		t.Fatalf("runtime-only resolver without signed active manifest must fail: policy=%q dsns=%#v err=%v", policy, dsns, err)
	}
	for _, test := range []struct {
		name     string
		selected []selectedService
		files    intakeRuntimeFileFlags
		legacy   []string
		env      func(string) (string, bool)
	}{
		{"missing intake", nil, files, nil, noEnv},
		{"mixed service group", []selectedService{{ID: requestfulfillment.ServiceID}, {ID: "analytics"}}, files, nil, noEnv},
		{"legacy support DSN", selected, files, []string{"", "", "", "legacy-shared"}, noEnv},
		{"admin DSN env", selected, files, nil, func(name string) (string, bool) { return "must-not-leak", name == "LEADGEN_SUPPORT_GREEN_DSN" }},
		{"partial runtime files", selected, intakeRuntimeFileFlags{requestDSNFile: files.requestDSNFile}, nil, noEnv},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := resolveIntakeDBPolicy("runtime-only", test.selected, test.files, test.legacy, test.env)
			if err == nil || err.Error() == "must-not-leak" {
				t.Fatalf("unsafe config accepted or secret leaked: %v", err)
			}
		})
	}
	if _, dsns, err := resolveIntakeDBPolicy("legacy", selected, intakeRuntimeFileFlags{}, nil, noEnv); err != nil || dsns != nil {
		t.Fatalf("legacy behavior changed: dsns=%#v err=%v", dsns, err)
	}
}

func TestResolveRuntimeOnlyIntakeAcceptsOnlyCompleteSignedBundleRoles(t *testing.T) {
	manifest := candidateFixture(t)
	scope := migrate.RuntimeRoleScope{
		Environment: manifest.Environment, Deployment: manifest.DeploymentID,
		Target: "request-fulfillment", Generation: manifest.Generation,
	}
	roleSets, err := roleSetsWithScopedJobPasswords("request-fulfillment", scope, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	files := intakeRuntimeFileFlags{
		requestDSNFile:         testSecretFile(t, fmt.Sprintf("postgres://%s:secret@127.0.0.1/db", roleSets[0].RuntimeRole)),
		consoleAuthDSNFile:     testSecretFile(t, fmt.Sprintf("postgres://%s:secret@127.0.0.1/db", roleSets[1].RuntimeRole)),
		vectorDSNFile:          testSecretFile(t, fmt.Sprintf("postgres://%s:secret@127.0.0.1/db", roleSets[2].RuntimeRole)),
		candidateManifestFile:  testSecretFile(t, "active-manifest-placeholder"),
		candidatePublicKeyFile: testSecretFile(t, "independent-trust-anchor-placeholder"),
	}
	lookup := func(name string) (string, bool) {
		if name == "LEADGEN_RELEASE_TRUST_KEY_ID" {
			return manifest.KeyID, true
		}
		return "", false
	}
	reader := func(_, _, target, keyID string) (CandidateManifest, error) {
		if target != "request-fulfillment" || keyID != manifest.KeyID {
			return CandidateManifest{}, errors.New("active manifest scope mismatch")
		}
		return manifest, nil
	}
	policy, resolved, err := resolveIntakeDBPolicyWithCandidateReader(
		string(intakeDBPolicyRuntimeOnly), []selectedService{{ID: requestfulfillment.ServiceID}},
		files, nil, lookup, reader,
	)
	if err != nil || policy != intakeDBPolicyRuntimeOnly || resolved == nil || resolved.generation != manifest.Generation {
		t.Fatalf("complete active manifest bundle rejected: policy=%q resolved=%#v err=%v", policy, resolved, err)
	}
	if !verifyCandidateRoleSets(manifest, resolved.roleSets) || len(resolved.roleSets) != 3 {
		t.Fatal("runtime RoleSets are not exactly the signed three-target role registry")
	}

	for name, alter := range map[string]func(*CandidateManifest, *intakeRuntimeFileFlags){
		"target mismatch":   func(m *CandidateManifest, _ *intakeRuntimeFileFlags) { m.Target = "consoleauth" },
		"protocol mismatch": func(m *CandidateManifest, _ *intakeRuntimeFileFlags) { m.ExecutionProtocol = "v2" },
		"policy mismatch": func(m *CandidateManifest, _ *intakeRuntimeFileFlags) {
			m.TargetPolicies["request-fulfillment"] = CandidateTargetPolicy{}
		},
		"DSN role mismatch": func(_ *CandidateManifest, f *intakeRuntimeFileFlags) {
			f.vectorDSNFile = testSecretFile(t, "postgres://wrong-role:secret@127.0.0.1/db")
		},
	} {
		t.Run(name, func(t *testing.T) {
			badManifest := manifest
			badManifest.TargetPolicies = make(map[string]CandidateTargetPolicy, len(manifest.TargetPolicies))
			for key, value := range manifest.TargetPolicies {
				badManifest.TargetPolicies[key] = value
			}
			badFiles := files
			alter(&badManifest, &badFiles)
			badReader := func(_, _, _, _ string) (CandidateManifest, error) { return badManifest, nil }
			if _, _, err := resolveIntakeDBPolicyWithCandidateReader(
				string(intakeDBPolicyRuntimeOnly), []selectedService{{ID: requestfulfillment.ServiceID}},
				badFiles, nil, lookup, badReader,
			); err == nil {
				t.Fatal("mismatched signed bundle accepted")
			}
		})
	}
}

func TestRuntimeOnlyIntakeBuildVerifiesThreePoolsAndPassesEachPoolToItsAdapter(t *testing.T) {
	var opened []*pgxpool.Pool
	var verifiedRoles []string
	var verifiedSchemas []migrate.Schema
	ops := intakeRuntimeDBOps{
		openPool: func(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
			pool, err := pgxpool.New(ctx, dsn)
			if err == nil {
				opened = append(opened, pool)
			}
			return pool, err
		},
		verifyRole: func(_ context.Context, _ *pgxpool.Pool, spec migrate.RoleSet) error {
			verifiedRoles = append(verifiedRoles, spec.RuntimeRole)
			return nil
		},
		verifySchema: func(_ context.Context, _ *pgxpool.Pool, _ string, schema migrate.Schema) error {
			verifiedSchemas = append(verifiedSchemas, schema)
			return nil
		},
	}
	defer func() {
		for _, pool := range opened {
			pool.Close()
		}
	}()
	registry := newRuntimePoolRegistry()
	var requestPool, consolePool, vectorPool *pgxpool.Pool
	factories := intakeRuntimeConstructors{
		request: func(pool *pgxpool.Pool) requestfulfillment.Persistence {
			requestPool = pool
			return requestfulfillment.NewPostgresPersistence(pool)
		},
		console: func(pool *pgxpool.Pool) consoleauth.Store {
			consolePool = pool
			return consoleauth.NewPostgresStore(pool)
		},
		vector: func(pool *pgxpool.Pool, embedder integrationplatform.VectorEmbedder) vector.Port {
			vectorPool = pool
			return integrationplatform.NewVectorPostgres(pool, embedder)
		},
	}
	service, err := buildService(requestfulfillment.ServiceID, "in-process", buildOptions{
		apiAddr:              ":0",
		dbPolicy:             intakeDBPolicyRuntimeOnly,
		intakeRuntimeDSNs:    testIntakeDSNSet(t),
		intakeRuntimeDBOps:   ops,
		intakeRuntimeFactory: factories,
		runtimeDBResources:   registry,
		embeddingKey:         "unit-test-embedding-key",
		logger:               observ.NewLogger(),
		metrics:              observ.NewMetrics(),
	}, amqp.NewInMemoryBroker(1), newPublicIngestStub(":0", observ.NewLogger()))
	if err != nil {
		t.Fatalf("runtime-only request service build: %v", err)
	}
	if requestPool == nil || consolePool == nil || vectorPool == nil || requestPool == consolePool || requestPool == vectorPool || consolePool == vectorPool {
		t.Fatalf("constructors did not receive three distinct runtime pools: request=%p console=%p vector=%p", requestPool, consolePool, vectorPool)
	}
	if len(registry.pools) != 1 || requestPool != registry.pools[0].request || consolePool != registry.pools[0].consoleAuth || vectorPool != registry.pools[0].vector {
		t.Fatal("public persistence constructors did not receive the schema-specific verified runtime pools")
	}
	if len(opened) != 3 || len(verifiedRoles) != 3 || len(verifiedSchemas) != 3 {
		t.Fatalf("runtime sequence: opened=%d roles=%v schemas=%v", len(opened), verifiedRoles, verifiedSchemas)
	}
	wantRoles := []string{testIntakeDSNSet(t).roleSets[0].RuntimeRole, testIntakeDSNSet(t).roleSets[1].RuntimeRole, testIntakeDSNSet(t).roleSets[2].RuntimeRole}
	if fmt.Sprint(verifiedRoles) != fmt.Sprint(wantRoles) {
		t.Fatalf("verified unexpected role order: %v", verifiedRoles)
	}
	if verifiedSchemas[0] != migrate.Schema("request-fulfillment") || verifiedSchemas[1] != migrate.Schema("consoleauth") || verifiedSchemas[2] != migrate.Schema("integration-platform") {
		t.Fatalf("verified unexpected schemas: %v", verifiedSchemas)
	}
	for _, role := range []composition.Role{composition.RoleAPI, composition.RoleWorker} {
		if runner, ok := service.RoleRunner(role); !ok || runner == nil {
			t.Errorf("runtime-only request runner %q missing", role)
		}
	}
	registry.closeAll()
}

func TestOpenVerifiedIntakePoolsClosesPartialPoolsOnRoleOrVersionFailure(t *testing.T) {
	for _, stage := range []string{"role", "schema"} {
		t.Run(stage, func(t *testing.T) {
			var opened []*pgxpool.Pool
			ops := intakeRuntimeDBOps{
				openPool: func(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
					pool, err := pgxpool.New(ctx, dsn)
					if err == nil {
						opened = append(opened, pool)
					}
					return pool, err
				},
				verifyRole: func(_ context.Context, _ *pgxpool.Pool, spec migrate.RoleSet) error {
					if stage == "role" && spec.Schema == migrate.Schema("consoleauth") {
						return errors.New("wrong runtime role")
					}
					return nil
				},
				verifySchema: func(_ context.Context, _ *pgxpool.Pool, _ string, schema migrate.Schema) error {
					if stage == "schema" && schema == migrate.Schema("consoleauth") {
						return errors.New("missing schema version")
					}
					return nil
				},
			}
			dsns := testIntakeDSNSet(t)
			if pools, err := openVerifiedIntakePools(context.Background(), dsns, ops); err == nil || pools != nil {
				t.Fatalf("failed %s verification must abort startup, pools=%v err=%v", stage, pools, err)
			}
			if len(opened) < 2 {
				t.Fatalf("expected partial pool setup before injected %s failure, opened=%d", stage, len(opened))
			}
			for _, pool := range opened {
				if err := pool.Ping(context.Background()); err == nil {
					t.Errorf("partial pool left open after %s failure", stage)
				}
			}
		})
	}
}

func TestOpenVerifiedIntakePoolsRejectsDSNUsernameBeforeAnyConnection(t *testing.T) {
	dsns := testIntakeDSNSet(t)
	dsns.request = "postgres://wrong-generation-role:secret@127.0.0.1/db"
	opened := 0
	ops := defaultIntakeRuntimeDBOps()
	ops.openPool = func(context.Context, string) (*pgxpool.Pool, error) {
		opened++
		return nil, errors.New("unexpected connect")
	}
	if pools, err := openVerifiedIntakePools(context.Background(), dsns, ops); err == nil || pools != nil || opened != 0 {
		t.Fatalf("wrong generation username must fail before dialing: pools=%v opened=%d err=%v", pools, opened, err)
	}
}

func TestOpenVerifiedIntakePoolsFailsClosedForMissingGenerationAndUnavailableTarget(t *testing.T) {
	dsns := testIntakeDSNSet(t)
	missingGeneration := *dsns
	missingGeneration.generation = ""
	if pools, err := openVerifiedIntakePools(context.Background(), &missingGeneration, defaultIntakeRuntimeDBOps()); err == nil || pools != nil {
		t.Fatalf("static/unscoped generation fallback accepted: pools=%v err=%v", pools, err)
	}
	missingRole := *dsns
	missingRole.roleSets = append([]migrate.RoleSet(nil), dsns.roleSets[:2]...)
	if pools, err := openVerifiedIntakePools(context.Background(), &missingRole, defaultIntakeRuntimeDBOps()); err == nil || pools != nil {
		t.Fatalf("partial signed role registry accepted: pools=%v err=%v", pools, err)
	}
	ops := defaultIntakeRuntimeDBOps()
	openCalls := 0
	ops.openPool = func(context.Context, string) (*pgxpool.Pool, error) {
		openCalls++
		return nil, errors.New("target unavailable")
	}
	if pools, err := openVerifiedIntakePools(context.Background(), dsns, ops); err == nil || pools != nil || openCalls != 1 {
		t.Fatalf("unavailable runtime target did not block startup: pools=%v calls=%d err=%v", pools, openCalls, err)
	}
}

func testIntakeDSNSet(t *testing.T) *intakeRuntimeDSNs {
	t.Helper()
	scope := migrate.RuntimeRoleScope{Environment: "test", Deployment: "intake-test", Target: "request-fulfillment", Generation: "g2-test"}
	sets, err := roleSetsWithScopedJobPasswords("request-fulfillment", scope, "", "", "")
	if err != nil || len(sets) != 3 {
		t.Fatalf("build test role sets: sets=%d err=%v", len(sets), err)
	}
	return &intakeRuntimeDSNs{
		request:     fmt.Sprintf("postgres://%s:secret@127.0.0.1/db", sets[0].RuntimeRole),
		consoleAuth: fmt.Sprintf("postgres://%s:secret@127.0.0.1/db", sets[1].RuntimeRole),
		vector:      fmt.Sprintf("postgres://%s:secret@127.0.0.1/db", sets[2].RuntimeRole),
		roleSets:    sets, protocol: "v1", generation: "g2-test",
	}
}

func TestRuntimeIntakeMissingConfigFailsWithoutLegacyFallback(t *testing.T) {
	service, err := buildService(requestfulfillment.ServiceID, "in-process", buildOptions{
		dbPolicy: intakeDBPolicyRuntimeOnly,
		logger:   observ.NewLogger(),
		metrics:  observ.NewMetrics(),
	}, amqp.NewInMemoryBroker(1), newPublicIngestStub(":0", observ.NewLogger()))
	if err == nil || service != nil {
		t.Fatalf("runtime-only intake without all three DSNs must fail, service=%v err=%v", service, err)
	}
}

func TestRuntimeOnlyIntakeAgainstIsolatedPostgres17(t *testing.T) {
	fixture := newIntakeRuntimeTestDatabase(t)
	t.Chdir("../../..")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	requestPass := "request-runtime-test-password"
	requestMigrationPass := "request-migration-test-password"
	consolePass := "console-runtime-test-password"
	consoleMigrationPass := "console-migration-test-password"
	vectorPass := "vector-runtime-test-password"
	vectorMigrationPass := "vector-migration-test-password"
	integrationPass := "integration-runtime-test-password"
	scope := migrate.RuntimeRoleScope{Environment: "test", Deployment: "deployment-1", Target: "request-fulfillment", Generation: "generation-2"}
	sets, err := migrate.RequestIntakeRoleSetsForGeneration(scope, requestMigrationPass, requestPass, consoleMigrationPass, consolePass, vectorMigrationPass, vectorPass, integrationPass)
	if err != nil {
		t.Fatalf("generation-scoped intake RoleSets: %v", err)
	}
	fixture.cleanupSets = sets
	fixture.expectRolesAbsent(t, sets)

	versionNum := 0
	if err := fixture.pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&versionNum); err != nil {
		t.Fatal("PG server version query failed")
	}
	if versionNum < 170000 || versionNum >= 180000 {
		t.Fatalf("PG17 required for isolated acceptance; server_version_num=%d", versionNum)
	}
	for _, spec := range sets {
		if err := migrate.ProvisionSchema(ctx, fixture.pool, spec); err != nil {
			t.Fatalf("target provisioning failed for schema %q: %v", spec.Schema, err)
		}
		target := targetIDForSchema(spec.Schema)
		approved, err := validateCandidateTargetPolicy(releaseMigrationRoot(), target, candidateFixture(t))
		if err != nil {
			t.Fatalf("exact signed-candidate policy fixture for %q: %v", target, err)
		}
		migrationPool, err := pgxpool.New(ctx, fixture.dsnForRole(t, spec.MigratorRole, spec.MigratorPassword))
		if err != nil {
			t.Fatalf("open dedicated migration pool for %q: %v", target, err)
		}
		if err := migrate.ApplyAsAllowlisted(ctx, migrationPool, dbTargets[target].migrationsDir, spec.Schema, spec.OwnerRole, approved.Approved); err != nil {
			migrationPool.Close()
			t.Fatalf("separate exact-set migration failed for target %q: %v", target, err)
		}
		if err := migrate.GrantRuntimeSchemaAs(ctx, migrationPool, spec, spec.OwnerRole); err != nil {
			migrationPool.Close()
			t.Fatalf("runtime grant publication failed for target %q: %v", target, err)
		}
		migrationPool.Close()
	}

	runtimeFileFlags := intakeRuntimeFileFlags{
		requestDSNFile:         testSecretFile(t, fixture.dsnForRole(t, sets[0].RuntimeRole, requestPass)),
		consoleAuthDSNFile:     testSecretFile(t, fixture.dsnForRole(t, sets[1].RuntimeRole, consolePass)),
		vectorDSNFile:          testSecretFile(t, fixture.dsnForRole(t, sets[2].RuntimeRole, vectorPass)),
		candidateManifestFile:  testSecretFile(t, "synthetic-signed-active-candidate"),
		candidatePublicKeyFile: testSecretFile(t, "synthetic-independent-test-trust-key"),
	}
	activeManifest := candidateFixture(t)
	policy, dsns, err := resolveIntakeDBPolicyWithCandidateReader(string(intakeDBPolicyRuntimeOnly), []selectedService{{ID: requestfulfillment.ServiceID}}, runtimeFileFlags, nil, func(name string) (string, bool) {
		if name == "LEADGEN_RELEASE_TRUST_KEY_ID" {
			return activeManifest.KeyID, true
		}
		return "", false
	}, func(_, _, target, keyID string) (CandidateManifest, error) {
		if target != "request-fulfillment" || keyID != activeManifest.KeyID || activeManifest.Target != "intake" {
			return CandidateManifest{}, errors.New("test active manifest scope mismatch")
		}
		return activeManifest, nil
	})
	if err != nil || dsns == nil || policy != intakeDBPolicyRuntimeOnly {
		t.Fatalf("runtime-only secret file resolution failed: policy=%q err=%v", policy, err)
	}
	requestMigrations, err := migrate.Load(dbTargets["request-fulfillment"].migrationsDir, sets[0].Schema)
	if err != nil || len(requestMigrations) == 0 {
		t.Fatalf("request migration inventory unavailable: %v", err)
	}
	adminDSNs := *dsns
	adminDSNs.request = fixture.dsnForAdmin(t)
	if pools, err := openVerifiedIntakePools(ctx, &adminDSNs, defaultIntakeRuntimeDBOps()); err == nil || pools != nil {
		t.Fatal("startup verifier accepted admin identity as request runtime")
	}
	versionTable := `"request-fulfillment".schema_migrations`
	missingVersion := requestMigrations[0].Name
	if _, err := fixture.pool.Exec(ctx, "DELETE FROM "+versionTable+" WHERE version=$1", missingVersion); err != nil {
		t.Fatal("missing-version startup fixture setup failed")
	}
	if pools, err := openVerifiedIntakePools(ctx, dsns, defaultIntakeRuntimeDBOps()); err == nil || pools != nil {
		_, _ = fixture.pool.Exec(ctx, "INSERT INTO "+versionTable+" (version) VALUES ($1)", missingVersion)
		t.Fatal("startup verifier accepted a missing schema version")
	}
	if _, err := fixture.pool.Exec(ctx, "INSERT INTO "+versionTable+" (version) VALUES ($1)", missingVersion); err != nil {
		t.Fatal("missing-version startup fixture restore failed")
	}
	if _, err := fixture.pool.Exec(ctx, "INSERT INTO "+versionTable+" (version) VALUES ('999_runtime_test_unexpected.sql')"); err != nil {
		t.Fatal("unknown-version startup fixture setup failed")
	}
	if pools, err := openVerifiedIntakePools(ctx, dsns, defaultIntakeRuntimeDBOps()); err == nil || pools != nil {
		_, _ = fixture.pool.Exec(ctx, "DELETE FROM "+versionTable+" WHERE version='999_runtime_test_unexpected.sql'")
		t.Fatal("startup verifier accepted an unknown schema version")
	}
	if _, err := fixture.pool.Exec(ctx, "DELETE FROM "+versionTable+" WHERE version='999_runtime_test_unexpected.sql'"); err != nil {
		t.Fatal("unknown-version startup fixture cleanup failed")
	}

	var consoleStore consoleauth.Store
	var vectorStore vector.Port
	factories := defaultIntakeRuntimeConstructors()
	factories.console = func(pool *pgxpool.Pool) consoleauth.Store {
		consoleStore = consoleauth.NewPostgresStore(pool)
		return consoleStore
	}
	factories.vector = func(pool *pgxpool.Pool, _ integrationplatform.VectorEmbedder) vector.Port {
		// Детерминированный embedder — только тестовый двойник; vector PostgreSQL
		// store и все SQL-операции остаются реальными и проходят runtime role.
		vectorStore = integrationplatform.NewVectorPostgres(pool, testEmbeddingProvider{})
		return vectorStore
	}
	resources := newRuntimePoolRegistry()
	defer resources.closeAll()
	service, err := buildService(requestfulfillment.ServiceID, "in-process", buildOptions{
		apiAddr:              fixture.freeAddress(t),
		dbPolicy:             policy,
		intakeRuntimeDSNs:    dsns,
		intakeRuntimeDBOps:   defaultIntakeRuntimeDBOps(),
		intakeRuntimeFactory: factories,
		runtimeDBResources:   resources,
		embeddingKey:         "test-only-embedding-provider-key",
		logger:               observ.NewLogger(),
		metrics:              observ.NewMetrics(),
	}, amqp.NewInMemoryBroker(1), newPublicIngestStub(":0", observ.NewLogger()))
	if err != nil {
		t.Fatalf("runtime-only service build/verification failed: %v", err)
	}
	if consoleStore == nil || vectorStore == nil {
		t.Fatal("runtime constructors did not receive their dedicated pools")
	}
	if _, err := fixture.pool.Exec(ctx, `CREATE SCHEMA intake_audit; CREATE TABLE intake_audit.audit_log (id text PRIMARY KEY, message text NOT NULL)`); err != nil {
		t.Fatal("isolated audit fixture creation failed")
	}
	if err := migrate.VerifyRuntimeRole(ctx, resources.pools[0].request, sets[0]); err != nil {
		t.Fatalf("runtime verifier rejected an unrelated foreign table rowtype: %v", err)
	}
	if err := migrate.VerifyRuntimeRole(ctx, fixture.pool, sets[0]); err == nil {
		t.Fatal("bootstrap/admin connection was accepted as request runtime role")
	}

	for index, spec := range sets {
		pool := []*pgxpool.Pool{resources.pools[0].request, resources.pools[0].consoleAuth, resources.pools[0].vector}[index]
		var currentUser string
		if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&currentUser); err != nil || currentUser != spec.RuntimeRole {
			t.Fatalf("runtime dependency %q connected as unexpected identity (query error=%v)", spec.Schema, err)
		}
	}

	requestPool := resources.pools[0].request
	for _, sql := range []string{
		`SELECT * FROM intake_audit.audit_log`,
		`INSERT INTO intake_audit.audit_log (id,message) VALUES ('x','x')`,
		`CREATE TABLE "request-fulfillment".runtime_intruder (id integer)`,
		`UPDATE "request-fulfillment".execution_resolution SET id=id`,
	} {
		if _, err := requestPool.Exec(ctx, sql); err == nil {
			t.Errorf("runtime request identity unexpectedly executed forbidden statement: %s", sql)
		}
	}
	vectorPool := resources.pools[0].vector
	if _, err := vectorPool.Exec(ctx, `CREATE TABLE "integration-platform".runtime_intruder (id integer)`); err == nil {
		t.Fatal("vector-only runtime identity unexpectedly created DDL")
	}
	var vectorDocumentSequence string
	if err := fixture.pool.QueryRow(ctx, `SELECT COALESCE(pg_get_serial_sequence('"integration-platform".vector_document','id'),'')`).Scan(&vectorDocumentSequence); err != nil {
		t.Fatal("vector_document sequence catalog lookup failed")
	}
	if vectorDocumentSequence != "" {
		t.Fatalf("source migration unexpectedly produced vector_document sequence %q; grants must be reviewed against the actual schema", vectorDocumentSequence)
	}

	if err := consoleStore.Save(ctx, consoleauth.Record{ID: "runtime-session", Username: "operator", CSRF: "token", LastActive: time.Now().UTC()}); err != nil {
		t.Fatal("consoleauth runtime Save failed")
	}
	record, ok, err := consoleStore.Load(ctx, "runtime-session")
	if err != nil || !ok || record.Username != "operator" {
		t.Fatalf("consoleauth runtime Load failed: found=%v err=%v", ok, err)
	}
	if err := consoleStore.Delete(ctx, "runtime-session"); err != nil {
		t.Fatal("consoleauth runtime Delete failed")
	}
	if _, ok, err := consoleStore.Load(ctx, "runtime-session"); err != nil || ok {
		t.Fatalf("consoleauth deleted session still exists: found=%v err=%v", ok, err)
	}
	if _, err := vectorStore.Upsert(ctx, vector.Document{ID: "runtime-vector", Text: "runtime vector fixture"}); err != nil {
		t.Fatal("vector runtime Upsert failed")
	}
	matches, err := vectorStore.Search(ctx, "runtime vector query", 1)
	if err != nil || len(matches) != 1 || matches[0].ID != "runtime-vector" {
		t.Fatalf("vector runtime Search failed: matches=%v err=%v", matches, err)
	}

	apiRunner, ok := service.RoleRunner(composition.RoleAPI)
	if !ok || apiRunner == nil {
		t.Fatal("runtime-only intake api role missing")
	}
	workerRunner, ok := service.RoleRunner(composition.RoleWorker)
	if !ok || workerRunner == nil {
		t.Fatal("runtime-only intake worker role missing")
	}
	apiCtx, stopAPI := context.WithCancel(ctx)
	apiDone := make(chan error, 1)
	go func() { apiDone <- apiRunner.Run(apiCtx) }()
	requestID := "runtime-request-1"
	url := "http://" + fixture.apiAddress + "/requests"
	response, err := postIntakeRequestEventually(url, requestID)
	if err != nil {
		stopAPI()
		<-apiDone
		t.Fatalf("runtime request store HTTP create failed: %v", err)
	}
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		stopAPI()
		<-apiDone
		t.Fatalf("runtime request store HTTP create returned %d: %s", response.StatusCode, body)
	}
	response.Body.Close()
	getResp, err := http.Get("http://" + fixture.apiAddress + "/requests/" + requestID + "?tenant_id=runtime-tenant")
	if err != nil {
		stopAPI()
		<-apiDone
		t.Fatal("runtime request store HTTP read failed")
	}
	getBody, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	stopAPI()
	if err := <-apiDone; err != nil {
		t.Fatalf("request api runner failed on shutdown: %v", err)
	}
	if getResp.StatusCode != http.StatusOK || !bytes.Contains(getBody, []byte(requestID)) {
		t.Fatalf("runtime request store did not persist/read the request: status=%d body=%s", getResp.StatusCode, getBody)
	}
}

type testEmbeddingProvider struct{}

func (testEmbeddingProvider) EmbedText(context.Context, string) ([]float32, error) {
	return []float32{1, 0.5, 0.25}, nil
}

func postIntakeRequestEventually(url, requestID string) (*http.Response, error) {
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		body := strings.NewReader(fmt.Sprintf(`{"request_id":%q,"offer_id":"runtime-offer","tenant_id":"runtime-tenant","actor":"runtime-test","content":"Runtime acceptance offer","target":"IT"}`, requestID))
		req, err := http.NewRequest(http.MethodPost, url, body)
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			resp, requestErr := http.DefaultClient.Do(req)
			if requestErr == nil {
				return resp, nil
			}
			lastErr = requestErr
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, lastErr
}

type intakeRuntimeTestDatabase struct {
	pool        *pgxpool.Pool
	parent      *pgxpool.Pool
	adminConfig *pgx.ConnConfig
	database    string
	apiAddress  string
	cleanupSets []migrate.RoleSet
}

const (
	intakeTestAdminDB           = "leadgen_provision_test_admin"
	intakeTestAdminMarker       = "lead-gen-provision-test-admin-v1"
	intakeTestChildPrefix       = "leadgen_intake_runtime_test_"
	intakeTestChildMarkerPrefix = "lead-gen-intake-runtime-test-child-v1:"
)

func newIntakeRuntimeTestDatabase(t *testing.T) *intakeRuntimeTestDatabase {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("LEADGEN_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("LEADGEN_POSTGRES_TEST_DSN не задан: isolated PG17 intake acceptance skipped")
	}
	if os.Getenv("LEADGEN_POSTGRES_TEST_ALLOW_DATABASE_CREATE") != "1" {
		t.Fatal("isolated intake acceptance requires explicit temporary database creation opt-in")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil || config.Database != intakeTestAdminDB || os.Getenv("LEADGEN_POSTGRES_TEST_DATABASE") != intakeTestAdminDB {
		t.Fatal("test DSN must target the explicitly marked sacrificial database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	parent, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("test PostgreSQL pool creation failed")
	}
	if err := parent.Ping(ctx); err != nil {
		parent.Close()
		t.Fatal("sacrificial test database is unavailable")
	}
	var marker string
	var canCreate bool
	if err := parent.QueryRow(ctx, `SELECT shobj_description(oid,'pg_database') FROM pg_database WHERE datname=current_database()`).Scan(&marker); err != nil || marker != intakeTestAdminMarker {
		parent.Close()
		t.Fatal("test database marker mismatch; refusing to create child database")
	}
	if err := parent.QueryRow(ctx, `SELECT rolsuper OR rolcreatedb FROM pg_roles WHERE rolname=current_user`).Scan(&canCreate); err != nil || !canCreate {
		parent.Close()
		t.Fatal("test database identity lacks disposable database privileges")
	}
	version := 0
	if err := parent.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version); err != nil || version < 170000 || version >= 180000 {
		parent.Close()
		t.Fatalf("isolated runtime acceptance requires PostgreSQL 17; server_version_num=%d", version)
	}

	sets := migrate.RequestIntakeRoleSets("test-request-migrator", "test-request-runtime", "test-console-migrator", "test-console-runtime", "test-vector-migrator", "test-vector-runtime", "test-integration-runtime")
	for _, spec := range sets {
		roles := []string{spec.OwnerRole, spec.MigratorRole, spec.RuntimeRole}
		for _, extra := range spec.AdditionalRuntimes {
			roles = append(roles, extra.Role)
		}
		for _, role := range roles {
			var exists bool
			if err := parent.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil || exists {
				parent.Close()
				t.Fatalf("required test role is not absent; refusing to modify database roles")
			}
		}
	}
	name := fmt.Sprintf("%s%d", intakeTestChildPrefix, time.Now().UnixNano())
	var exists bool
	if err := parent.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&exists); err != nil || exists {
		parent.Close()
		t.Fatal("temporary child database preflight failed")
	}
	if _, err := parent.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		parent.Close()
		t.Fatal("temporary child database creation failed")
	}
	markerValue := intakeTestChildMarkerPrefix + name
	if _, err := parent.Exec(ctx, "COMMENT ON DATABASE "+pgx.Identifier{name}.Sanitize()+" IS '"+markerValue+"'"); err != nil {
		parent.Close()
		t.Fatal("child marker creation failed; database intentionally left for inspection")
	}
	childConfig := config.Copy()
	childConfig.Database = name
	childPoolConfig, err := pgxpool.ParseConfig(config.ConnString())
	if err != nil {
		parent.Close()
		t.Fatal("temporary child database config parsing failed")
	}
	childPoolConfig.ConnConfig = childConfig
	child, err := pgxpool.NewWithConfig(ctx, childPoolConfig)
	if err != nil {
		parent.Close()
		t.Fatal("temporary child database pool creation failed")
	}
	if err := child.Ping(ctx); err != nil {
		child.Close()
		parent.Close()
		t.Fatal("temporary child database unavailable")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		child.Close()
		parent.Close()
		t.Fatal("temporary API listener reservation failed")
	}
	address := listener.Addr().String()
	_ = listener.Close()
	fixture := &intakeRuntimeTestDatabase{pool: child, parent: parent, adminConfig: config, database: name, apiAddress: address, cleanupSets: sets}
	t.Cleanup(func() { fixture.cleanup(t, fixture.cleanupSets) })
	return fixture
}

func (f *intakeRuntimeTestDatabase) expectRolesAbsent(t *testing.T, sets []migrate.RoleSet) {
	t.Helper()
	for _, spec := range sets {
		roles := []string{spec.OwnerRole, spec.MigratorRole, spec.RuntimeRole}
		for _, extra := range spec.AdditionalRuntimes {
			roles = append(roles, extra.Role)
		}
		for _, role := range roles {
			var exists bool
			if err := f.pool.QueryRow(context.Background(), "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil || exists {
				t.Fatalf("runtime role collision/preflight failed for target %q", spec.Schema)
			}
		}
	}
}

func (f *intakeRuntimeTestDatabase) dsnForRole(t *testing.T, role, password string) string {
	t.Helper()
	config := f.adminConfig.Copy()
	config.Database = f.database
	config.User = role
	config.Password = password
	return intakeTestConnString(t, config)
}

func (f *intakeRuntimeTestDatabase) dsnForAdmin(t *testing.T) string {
	t.Helper()
	config := f.adminConfig.Copy()
	config.Database = f.database
	return intakeTestConnString(t, config)
}

func intakeTestConnString(t *testing.T, config *pgx.ConnConfig) string {
	t.Helper()
	host := config.Host
	if strings.TrimSpace(host) == "" {
		t.Fatal("runtime fixture has no TCP host")
	}
	urlObj := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(config.User, config.Password),
		Host:   net.JoinHostPort(host, fmt.Sprint(config.Port)),
		Path:   "/" + config.Database,
	}
	query := url.Values{}
	query.Set("sslmode", "disable")
	urlObj.RawQuery = query.Encode()
	return urlObj.String()
}

func (f *intakeRuntimeTestDatabase) freeAddress(_ *testing.T) string { return f.apiAddress }

func (f *intakeRuntimeTestDatabase) cleanup(t *testing.T, sets []migrate.RoleSet) {
	t.Helper()
	f.pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var marker string
	if err := f.parent.QueryRow(ctx, `SELECT shobj_description(oid,'pg_database') FROM pg_database WHERE datname=$1`, f.database).Scan(&marker); err != nil || marker != intakeTestChildMarkerPrefix+f.database {
		t.Errorf("child database marker mismatch; refusing cleanup")
		f.parent.Close()
		return
	}
	if _, err := f.parent.Exec(ctx, "DROP DATABASE "+pgx.Identifier{f.database}.Sanitize()+" WITH (FORCE)"); err != nil {
		t.Errorf("temporary intake test database cleanup failed")
		f.parent.Close()
		return
	}
	for _, spec := range sets {
		roles := []string{spec.RuntimeRole, spec.MigratorRole, spec.OwnerRole}
		for _, extra := range spec.AdditionalRuntimes {
			roles = append([]string{extra.Role}, roles...)
		}
		if _, err := f.parent.Exec(ctx, "REVOKE "+pgx.Identifier{spec.OwnerRole}.Sanitize()+" FROM "+pgx.Identifier{spec.MigratorRole}.Sanitize()); err != nil {
			t.Errorf("temporary role membership cleanup failed for target %q", spec.Schema)
		}
		for _, role := range roles {
			if _, err := f.parent.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Errorf("temporary role cleanup failed for target %q", spec.Schema)
			}
		}
	}
	f.parent.Close()
}

func targetIDForSchema(schema migrate.Schema) string {
	for id, target := range dbTargets {
		if target.schema == schema {
			return id
		}
	}
	return ""
}
