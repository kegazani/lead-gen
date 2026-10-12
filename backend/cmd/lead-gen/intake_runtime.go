package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	integrationplatform "lead-gen/backend/contexts/integration-platform"
	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/migrate"
	"lead-gen/contracts/integration-platform/vector"
)

type intakeDBPolicy string

const (
	intakeDBPolicyLegacy      intakeDBPolicy = "legacy"
	intakeDBPolicyRuntimeOnly intakeDBPolicy = "runtime-only"
)

type intakeRuntimeFileFlags struct {
	requestDSNFile         string
	consoleAuthDSNFile     string
	vectorDSNFile          string
	candidateManifestFile  string
	candidatePublicKeyFile string
}

type intakeRuntimeDSNs struct {
	request     string
	consoleAuth string
	vector      string
	roleSets    []migrate.RoleSet
	protocol    string
	generation  string
}

func resolveIntakeDBPolicy(policy string, selected []selectedService, files intakeRuntimeFileFlags, legacyDSNs []string, lookupEnv func(string) (string, bool)) (intakeDBPolicy, *intakeRuntimeDSNs, error) {
	return resolveIntakeDBPolicyWithCandidateReader(policy, selected, files, legacyDSNs, lookupEnv, readActiveCandidateManifest)
}

type activeCandidateManifestReader func(path, publicKeyPath, target, keyID string) (CandidateManifest, error)

func resolveIntakeDBPolicyWithCandidateReader(policy string, selected []selectedService, files intakeRuntimeFileFlags,
	legacyDSNs []string, lookupEnv func(string) (string, bool), readCandidate activeCandidateManifestReader) (intakeDBPolicy, *intakeRuntimeDSNs, error) {
	parsed := intakeDBPolicy(strings.TrimSpace(policy))
	if parsed == "" {
		parsed = intakeDBPolicyLegacy
	}
	if parsed != intakeDBPolicyLegacy && parsed != intakeDBPolicyRuntimeOnly {
		return "", nil, errors.New("lead-gen: неизвестная DB policy")
	}
	if parsed == intakeDBPolicyLegacy {
		if files.requestDSNFile != "" || files.consoleAuthDSNFile != "" || files.vectorDSNFile != "" || files.candidateManifestFile != "" || files.candidatePublicKeyFile != "" {
			return "", nil, errors.New("lead-gen: runtime intake DSN files заданы без db-policy runtime-only")
		}
		for _, name := range []string{"LEADGEN_REQUEST_RUNTIME_DSN_FILE", "LEADGEN_CONSOLEAUTH_RUNTIME_DSN_FILE", "LEADGEN_VECTOR_RUNTIME_DSN_FILE"} {
			if _, ok := lookupEnv(name); ok {
				return "", nil, errors.New("lead-gen: runtime intake DSN file задан без db-policy runtime-only")
			}
		}
		for _, name := range []string{"LEADGEN_CANDIDATE_MANIFEST_FILE", "LEADGEN_CANDIDATE_PUBLIC_KEY_FILE"} {
			if _, ok := lookupEnv(name); ok {
				return "", nil, errors.New("lead-gen: signed candidate manifest не применяется к legacy DB policy")
			}
		}
		return parsed, nil, nil
	}
	if len(selected) != 1 || selected[0].ID != requestfulfillment.ServiceID {
		return "", nil, errors.New("lead-gen: db-policy runtime-only допустима только для отдельной intake-группы request-fulfillment")
	}
	for _, dsn := range legacyDSNs {
		if strings.TrimSpace(dsn) != "" {
			return "", nil, errors.New("lead-gen: db-policy runtime-only запрещает legacy/shared DSN flags")
		}
	}
	for _, name := range []string{"LEADGEN_LEAD_GREEN_DSN", "LEADGEN_LEAD_RED_DSN", "LEADGEN_REQUEST_GREEN_DSN", "LEADGEN_SUPPORT_GREEN_DSN"} {
		if _, ok := lookupEnv(name); ok {
			return "", nil, errors.New("lead-gen: db-policy runtime-only запрещает legacy/shared DSN ENV")
		}
	}
	requestFile, err := resolveRuntimeDSNFile(files.requestDSNFile, "LEADGEN_REQUEST_RUNTIME_DSN_FILE", lookupEnv)
	if err != nil {
		return "", nil, err
	}
	consoleFile, err := resolveRuntimeDSNFile(files.consoleAuthDSNFile, "LEADGEN_CONSOLEAUTH_RUNTIME_DSN_FILE", lookupEnv)
	if err != nil {
		return "", nil, err
	}
	vectorFile, err := resolveRuntimeDSNFile(files.vectorDSNFile, "LEADGEN_VECTOR_RUNTIME_DSN_FILE", lookupEnv)
	if err != nil {
		return "", nil, err
	}
	requestDSN, err := readRuntimeDSN(requestFile)
	if err != nil {
		return "", nil, err
	}
	consoleDSN, err := readRuntimeDSN(consoleFile)
	if err != nil {
		return "", nil, err
	}
	vectorDSN, err := readRuntimeDSN(vectorFile)
	if err != nil {
		return "", nil, err
	}
	manifestFile, err := resolveRuntimeDSNFile(files.candidateManifestFile, "LEADGEN_CANDIDATE_MANIFEST_FILE", lookupEnv)
	if err != nil {
		return "", nil, err
	}
	publicKeyFile, err := resolveRuntimeDSNFile(files.candidatePublicKeyFile, "LEADGEN_CANDIDATE_PUBLIC_KEY_FILE", lookupEnv)
	if err != nil {
		return "", nil, err
	}
	if readCandidate == nil {
		return "", nil, errors.New("lead-gen: active candidate verifier is unavailable")
	}
	manifest, err := readCandidate(manifestFile, publicKeyFile, "request-fulfillment", lookupEnvValue(lookupEnv, "LEADGEN_RELEASE_TRUST_KEY_ID"))
	requestPolicy := manifest.TargetPolicies["request-fulfillment"]
	if err != nil || manifest.Target != "intake" || manifest.ExecutionProtocol != "v1" || requestPolicy.PolicyVersion != g15MigrationPolicyVersion {
		return "", nil, errors.New("lead-gen: intake requires an active signed G1.5 manifest with ExecutionProtocol=v1")
	}
	scope := migrate.RuntimeRoleScope{Environment: manifest.Environment, Deployment: manifest.DeploymentID, Target: "request-fulfillment", Generation: manifest.Generation}
	sets, err := roleSetsWithScopedJobPasswords("request-fulfillment", scope, "", "", "")
	if err != nil || !verifyCandidateRoleSets(manifest, sets) {
		return "", nil, errors.New("lead-gen: active candidate role registry does not match runtime RoleSet")
	}
	for _, item := range []struct {
		dsn    string
		target string
	}{{requestDSN, "request-fulfillment"}, {consoleDSN, "consoleauth"}, {vectorDSN, "integration-platform"}} {
		roleSet, ok := roleSetForTarget(sets, dbTargets[item.target])
		if !ok || !dsnUsernameMatches(item.dsn, roleSet.RuntimeRole) {
			return "", nil, fmt.Errorf("lead-gen: runtime DSN username differs from active manifest for %s", item.target)
		}
	}
	return parsed, &intakeRuntimeDSNs{
		request: requestDSN, consoleAuth: consoleDSN, vector: vectorDSN,
		roleSets: sets, protocol: manifest.ExecutionProtocol, generation: manifest.Generation,
	}, nil
}

func dsnUsernameMatches(dsn, expected string) bool {
	config, err := pgx.ParseConfig(dsn)
	return err == nil && config.User == expected
}

func verifyCandidateRoleSets(manifest CandidateManifest, sets []migrate.RoleSet) bool {
	if len(sets) != len(dbTargets) || len(manifest.Roles) != len(dbTargets) || len(manifest.TargetPolicies) != len(dbTargets) {
		return false
	}
	seenSchemas := make(map[migrate.Schema]struct{}, len(sets))
	for _, set := range sets {
		if _, duplicate := seenSchemas[set.Schema]; duplicate {
			return false
		}
		seenSchemas[set.Schema] = struct{}{}
		registry, ok := manifest.Roles[string(set.Schema)]
		if !ok || registry.Owner != set.OwnerRole || registry.Migrator != set.MigratorRole {
			return false
		}
		key := string(set.Schema)
		if key == "integration-platform" {
			key = "vector"
		}
		if registry.Runtime[key] != set.RuntimeRole {
			return false
		}
		wantRuntimeCount := 1
		if key == "vector" {
			wantRuntimeCount = 2
		}
		if len(registry.Runtime) != wantRuntimeCount {
			return false
		}
		for _, additional := range set.AdditionalRuntimes {
			if registry.Runtime["integration-platform"] != additional.Role {
				return false
			}
		}
	}
	return true
}

func resolveRuntimeDSNFile(flagPath, envName string, lookupEnv func(string) (string, bool)) (string, error) {
	envPath, hasEnv := lookupEnv(envName)
	if flagPath != "" && hasEnv {
		return "", errors.New("lead-gen: runtime DSN file задан одновременно через flag и ENV")
	}
	if hasEnv {
		flagPath = envPath
	}
	if strings.TrimSpace(flagPath) == "" {
		return "", errors.New("lead-gen: отсутствует обязательный runtime DSN file")
	}
	return flagPath, nil
}

func readRuntimeDSN(path string) (string, error) {
	dsn, err := readSecretFile(path)
	if err != nil || strings.TrimSpace(dsn) == "" {
		return "", errors.New("lead-gen: runtime DSN file отсутствует, пуст или имеет небезопасные права")
	}
	return strings.TrimSpace(dsn), nil
}

type intakeRuntimePools struct {
	request     *pgxpool.Pool
	consoleAuth *pgxpool.Pool
	vector      *pgxpool.Pool
}

func (p *intakeRuntimePools) close() {
	if p == nil {
		return
	}
	if p.vector != nil {
		p.vector.Close()
	}
	if p.consoleAuth != nil {
		p.consoleAuth.Close()
	}
	if p.request != nil {
		p.request.Close()
	}
}

type intakeRuntimeDBOps struct {
	openPool     func(context.Context, string) (*pgxpool.Pool, error)
	verifyRole   func(context.Context, *pgxpool.Pool, migrate.RoleSet) error
	verifySchema func(context.Context, *pgxpool.Pool, string, migrate.Schema) error
}

func defaultIntakeRuntimeDBOps() intakeRuntimeDBOps {
	return intakeRuntimeDBOps{
		openPool: func(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
			pool, err := pgxpool.New(ctx, dsn)
			if err != nil {
				return nil, errors.New("не удалось открыть runtime pool")
			}
			return pool, nil
		},
		verifyRole:   migrate.VerifyRuntimeRole,
		verifySchema: migrate.Verify,
	}
}

func openVerifiedIntakePools(ctx context.Context, dsnSet *intakeRuntimeDSNs, ops intakeRuntimeDBOps) (*intakeRuntimePools, error) {
	if dsnSet == nil || strings.TrimSpace(dsnSet.request) == "" || strings.TrimSpace(dsnSet.consoleAuth) == "" || strings.TrimSpace(dsnSet.vector) == "" {
		return nil, errors.New("request-fulfillment: отсутствует одна из трёх runtime DB dependencies")
	}
	if ops.openPool == nil || ops.verifyRole == nil || ops.verifySchema == nil {
		return nil, errors.New("request-fulfillment: runtime DB operations не собраны")
	}
	sets := dsnSet.roleSets
	if len(sets) != len(dbTargets) || dsnSet.protocol != "v1" || strings.TrimSpace(dsnSet.generation) == "" {
		return nil, errors.New("request-fulfillment: signed generation role set or v1 protocol is missing")
	}
	requestSpec, ok := roleSetForTarget(sets, dbTargets["request-fulfillment"])
	if !ok {
		return nil, errors.New("request-fulfillment: runtime role registry отсутствует")
	}
	consoleSpec, ok := roleSetForTarget(sets, dbTargets["consoleauth"])
	if !ok {
		return nil, errors.New("request-fulfillment: consoleauth role registry отсутствует")
	}
	vectorSpec, ok := roleSetForTarget(sets, dbTargets["integration-platform"])
	if !ok {
		return nil, errors.New("request-fulfillment: vector runtime role registry отсутствует")
	}
	definitions := []struct {
		dsn  string
		dir  string
		spec migrate.RoleSet
		put  func(*intakeRuntimePools, *pgxpool.Pool)
	}{
		{dsnSet.request, dbTargets["request-fulfillment"].migrationsDir, requestSpec, func(p *intakeRuntimePools, pool *pgxpool.Pool) { p.request = pool }},
		{dsnSet.consoleAuth, dbTargets["consoleauth"].migrationsDir, consoleSpec, func(p *intakeRuntimePools, pool *pgxpool.Pool) { p.consoleAuth = pool }},
		{dsnSet.vector, dbTargets["integration-platform"].migrationsDir, vectorSpec, func(p *intakeRuntimePools, pool *pgxpool.Pool) { p.vector = pool }},
	}
	pools := &intakeRuntimePools{}
	for _, definition := range definitions {
		if !dsnUsernameMatches(definition.dsn, definition.spec.RuntimeRole) {
			pools.close()
			return nil, fmt.Errorf("request-fulfillment: runtime DSN username mismatch for schema %q", definition.spec.Schema)
		}
		pool, err := ops.openPool(ctx, definition.dsn)
		if err != nil {
			pools.close()
			return nil, errors.New("request-fulfillment: не удалось открыть runtime DB dependency")
		}
		definition.put(pools, pool)
		if err := ops.verifyRole(ctx, pool, definition.spec); err != nil {
			pools.close()
			return nil, fmt.Errorf("request-fulfillment: runtime role verification failed for schema %q: %w", definition.spec.Schema, err)
		}
		if err := ops.verifySchema(ctx, pool, definition.dir, definition.spec.Schema); err != nil {
			pools.close()
			return nil, fmt.Errorf("request-fulfillment: runtime schema verification failed for schema %q: %w", definition.spec.Schema, err)
		}
	}
	return pools, nil
}

type runtimePoolRegistry struct {
	mu          sync.Mutex
	pools       []*intakeRuntimePools
	servicePool []*pgxpool.Pool
	closed      bool
}

func newRuntimePoolRegistry() *runtimePoolRegistry { return &runtimePoolRegistry{} }

func (r *runtimePoolRegistry) add(pools *intakeRuntimePools) error {
	if r == nil || pools == nil {
		return errors.New("request-fulfillment: runtime pool registry не собран")
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		pools.close()
		return errors.New("request-fulfillment: runtime pool registry уже закрыт")
	}
	r.pools = append(r.pools, pools)
	r.mu.Unlock()
	return nil
}

func (r *runtimePoolRegistry) addServicePool(pool *pgxpool.Pool) error {
	if r == nil || pool == nil {
		return errors.New("runtime pool registry или service pool не собран")
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		pool.Close()
		return errors.New("runtime pool registry уже закрыт")
	}
	r.servicePool = append(r.servicePool, pool)
	r.mu.Unlock()
	return nil
}

func (r *runtimePoolRegistry) closeAll() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	pools := append([]*intakeRuntimePools(nil), r.pools...)
	servicePools := append([]*pgxpool.Pool(nil), r.servicePool...)
	r.pools = nil
	r.servicePool = nil
	r.mu.Unlock()
	for i := len(servicePools) - 1; i >= 0; i-- {
		servicePools[i].Close()
	}
	for i := len(pools) - 1; i >= 0; i-- {
		pools[i].close()
	}
}

type intakeRuntimeConstructors struct {
	request func(*pgxpool.Pool) requestfulfillment.Persistence
	console func(*pgxpool.Pool) consoleauth.Store
	vector  func(*pgxpool.Pool, integrationplatform.VectorEmbedder) vector.Port
}

func defaultIntakeRuntimeConstructors() intakeRuntimeConstructors {
	return intakeRuntimeConstructors{
		request: requestfulfillment.NewPostgresPersistence,
		console: func(pool *pgxpool.Pool) consoleauth.Store {
			return consoleauth.NewPostgresStore(pool)
		},
		vector: func(pool *pgxpool.Pool, embedder integrationplatform.VectorEmbedder) vector.Port {
			return integrationplatform.NewVectorPostgres(pool, embedder)
		},
	}
}

func (c intakeRuntimeConstructors) normalized() intakeRuntimeConstructors {
	defaults := defaultIntakeRuntimeConstructors()
	if c.request == nil {
		c.request = defaults.request
	}
	if c.console == nil {
		c.console = defaults.console
	}
	if c.vector == nil {
		c.vector = defaults.vector
	}
	return c
}
