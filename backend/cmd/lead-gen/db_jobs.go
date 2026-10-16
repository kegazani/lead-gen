package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sys/unix"

	"lead-gen/backend/platform/migrate"
)

type dbJob string

const (
	dbJobProvision          dbJob = "provision"
	dbJobMigrate            dbJob = "migrate"
	dbJobVerify             dbJob = "verify"
	dbJobStage              dbJob = "stage-secrets"
	dbJobStageTrustAnchor   dbJob = "stage-trust-anchor"
	dbJobVerifyBundle       dbJob = "verify-secrets"
	dbJobValidateStage      dbJob = "validate-stage-sources"
	dbJobValidateMigrations dbJob = "validate-release-migrations"
)

type releaseMigrationManifest struct {
	PolicyVersion string                      `json:"policy_version"`
	Target        string                      `json:"target"`
	Approved      []migrate.ApprovedMigration `json:"approved"`
	DeferredG1    []string                    `json:"deferred_for_g1_rollback,omitempty"`
}

const g15MigrationPolicyVersion = "g1.5-schema-0010"

// G1 rollback allowlist v1 uses exact file names and SHA-256 bytes. Request
// migrations 0005-0010 remain outside that legacy rollback window.
var additiveReleaseMigrations = map[string][]migrate.ApprovedMigration{
	"request-fulfillment": {
		{Name: "0001_request.sql", SHA256: "70d7c70f166e83b3e6a3577e7e12e3dfd2b257014f331650beb5cfbdeb488911"},
		{Name: "0002_email_draft_recipient.sql", SHA256: "1de715d09c2965e4e3a4632d8d34b7985da0f6d374154dbeb9371d42a0b29758"},
		{Name: "0003_request_campaign.sql", SHA256: "2581c21e916c42604558b61466921a1cb169b21f86f674702216e4b0c47ab7a6"},
		{Name: "0004_execution_attempt.sql", SHA256: "eef95dbe93329316b04d6968c19b8a65075b603c210e9618fd711f71cb4d23a0"},
	},
	"consoleauth": {
		{Name: "001_console_sessions.sql", SHA256: "bd05fe9dfb0d32e78d9adfdb5450b94ab7d8bda450b66c6ab02e9255682b09b3"},
	},
	"integration-platform": {
		{Name: "0001_registry.sql", SHA256: "d7600048bc429675b8e9b25640926cdd39cd5c08ecf90a44a24277f6aa855891"},
		{Name: "0002_vector_index.sql", SHA256: "ca86bcbf11c27bc876726a53fe0db0c032ff64970c85909d2d88d58ab03734b2"},
	},
}

// Research mode не входит в утверждённый G1/G1.5 набор. Его hash учитывается
// отдельно: наличие файла не расширяет signed release allowlist.
var deferredResearchRequestMigrations = map[string]string{
	"0011_research_execution_mode.sql": "68c2ad4138b736adecd48e495eaea7d2b0bdf6816a6ad0fbf47134153451ab42",
}

var deferredG1RequestMigrations = map[string]string{
	"0005_execution_resolution.sql":                    "a100a40102e9aa205264ea1a44424728fa4aa6e0b8dacd369368252bd4d35690",
	"0006_execution_resolution_revisions.sql":          "ca16960b0e9e0a3ab0a807ddf36e4068e2dfb793f5b072414df7671236fe03ef",
	"0007_execution_resolution_integrity.sql":          "f3d8b0c845ddc3c0d1f89c7e4a610954f6060a275d23a8acf41a68753a60afc9",
	"0008_execution_resolution_integrity_recheck.sql":  "dfda9197da1316ff9a9b6f63a2a6a9d2c3ff4d58b9d7b5706812c7b1f5f8dbcf",
	"0009_execution_resolution_trigger_definition.sql": "2df84bef75437599ae7170bdf530bb8cadbbf22b48a835eca5838db7b32660d9",
	"0010_verified_runtime_resolution.sql":             "36e9873af6d074632c9eb68c64ba4e304f0f2957144782400d7a1b9fe26669ad",
	"0011_research_execution_mode.sql":                 "68c2ad4138b736adecd48e495eaea7d2b0bdf6816a6ad0fbf47134153451ab42",
}

// Exact G1.5 catalog. Release stays deferred until the compatibility
// acceptance gate is independently recorded and the v1 business path is
// exercised on PostgreSQL 17.
var g15RequestMigrations = []migrate.ApprovedMigration{
	{Name: "0001_request.sql", SHA256: "70d7c70f166e83b3e6a3577e7e12e3dfd2b257014f331650beb5cfbdeb488911"},
	{Name: "0002_email_draft_recipient.sql", SHA256: "1de715d09c2965e4e3a4632d8d34b7985da0f6d374154dbeb9371d42a0b29758"},
	{Name: "0003_request_campaign.sql", SHA256: "2581c21e916c42604558b61466921a1cb169b21f86f674702216e4b0c47ab7a6"},
	{Name: "0004_execution_attempt.sql", SHA256: "eef95dbe93329316b04d6968c19b8a65075b603c210e9618fd711f71cb4d23a0"},
	{Name: "0005_execution_resolution.sql", SHA256: "a100a40102e9aa205264ea1a44424728fa4aa6e0b8dacd369368252bd4d35690"},
	{Name: "0006_execution_resolution_revisions.sql", SHA256: "ca16960b0e9e0a3ab0a807ddf36e4068e2dfb793f5b072414df7671236fe03ef"},
	{Name: "0007_execution_resolution_integrity.sql", SHA256: "f3d8b0c845ddc3c0d1f89c7e4a610954f6060a275d23a8acf41a68753a60afc9"},
	{Name: "0008_execution_resolution_integrity_recheck.sql", SHA256: "dfda9197da1316ff9a9b6f63a2a6a9d2c3ff4d58b9d7b5706812c7b1f5f8dbcf"},
	{Name: "0009_execution_resolution_trigger_definition.sql", SHA256: "2df84bef75437599ae7170bdf530bb8cadbbf22b48a835eca5838db7b32660d9"},
	{Name: "0010_verified_runtime_resolution.sql", SHA256: "36e9873af6d074632c9eb68c64ba4e304f0f2957144782400d7a1b9fe26669ad"},
}

func validateReleaseMigrationManifest(root, target string) (releaseMigrationManifest, error) {
	approved, ok := additiveReleaseMigrations[target]
	definition, targetExists := dbTargets[target]
	if !ok || !targetExists {
		return releaseMigrationManifest{}, errors.New("lead-gen: DB target has no G1 additive migration policy")
	}
	directory := filepath.Join(root, definition.migrationsDir)
	loaded, err := migrate.Load(directory, definition.schema)
	if err != nil {
		return releaseMigrationManifest{}, errors.New("lead-gen: release migration manifest could not load owned migrations")
	}
	approvedByName := make(map[string]string, len(approved))
	for _, item := range approved {
		approvedByName[item.Name] = item.SHA256
	}
	deferredByName := map[string]string{}
	if target == "request-fulfillment" {
		deferredByName = deferredG1RequestMigrations
	}
	seen := make(map[string]struct{}, len(loaded))
	deferred := make([]string, 0)
	for _, migration := range loaded {
		digest := sha256.Sum256([]byte(migration.SQL))
		checksum := hex.EncodeToString(digest[:])
		if expected, found := approvedByName[migration.Name]; found && checksum == expected {
			seen[migration.Name] = struct{}{}
			continue
		}
		if expected, found := deferredByName[migration.Name]; found && checksum == expected {
			seen[migration.Name] = struct{}{}
			deferred = append(deferred, migration.Name)
			continue
		}
		return releaseMigrationManifest{}, fmt.Errorf("lead-gen: migration %q is outside exact release allowlist", migration.Name)
	}
	for _, item := range approved {
		if _, found := seen[item.Name]; !found {
			return releaseMigrationManifest{}, fmt.Errorf("lead-gen: approved migration %q is missing", item.Name)
		}
	}
	for name := range deferredByName {
		if _, found := seen[name]; !found {
			return releaseMigrationManifest{}, fmt.Errorf("lead-gen: deferred G1 migration %q is missing from pinned source set", name)
		}
	}
	manifest := releaseMigrationManifest{PolicyVersion: "g1-additive-v1", Target: target, Approved: approved, DeferredG1: deferred}
	if len(deferred) > 0 {
		return manifest, fmt.Errorf("lead-gen: G1 rollback window rejects pending request migrations outside additive allowlist")
	}
	return manifest, nil
}

func validateG15MigrationManifest(root string) (releaseMigrationManifest, error) {
	target := "request-fulfillment"
	directory := filepath.Join(root, dbTargets[target].migrationsDir)
	loaded, err := migrate.Load(directory, dbTargets[target].schema)
	if err != nil || len(loaded) != len(g15RequestMigrations)+len(deferredResearchRequestMigrations) {
		return releaseMigrationManifest{}, errors.New("lead-gen: G1.5 migration catalog is incomplete")
	}
	approved := make(map[string]string, len(g15RequestMigrations))
	for _, migration := range g15RequestMigrations {
		approved[migration.Name] = migration.SHA256
	}
	deferred := []string{}
	for _, migration := range loaded {
		digest := sha256.Sum256([]byte(migration.SQL))
		checksum := hex.EncodeToString(digest[:])
		if expected, ok := approved[migration.Name]; ok && checksum == expected {
			delete(approved, migration.Name)
			continue
		}
		if expected, ok := deferredResearchRequestMigrations[migration.Name]; ok && checksum == expected {
			deferred = append(deferred, migration.Name)
			continue
		}
		return releaseMigrationManifest{}, errors.New("lead-gen: G1.5 migration catalog differs from pinned exact hashes")
	}
	if len(approved) > 0 || len(deferred) != len(deferredResearchRequestMigrations) {
		return releaseMigrationManifest{}, errors.New("lead-gen: G1.5 migration catalog is incomplete")
	}
	return releaseMigrationManifest{PolicyVersion: g15MigrationPolicyVersion, Target: target, Approved: g15RequestMigrations, DeferredG1: deferred}, nil
}

func validateCandidateTargetPolicy(root, target string, manifest CandidateManifest) (releaseMigrationManifest, error) {
	policy, ok := manifest.TargetPolicies[target]
	if !ok {
		return releaseMigrationManifest{}, errors.New("lead-gen: target is absent from signed intake manifest")
	}
	var approved releaseMigrationManifest
	var err error
	if target == "request-fulfillment" && policy.PolicyVersion == g15MigrationPolicyVersion {
		approved, err = validateG15MigrationManifest(root)
	} else if policy.PolicyVersion == "g1-additive-v1" {
		approved, err = validateReleaseMigrationManifest(root, target)
	} else {
		return releaseMigrationManifest{}, errors.New("lead-gen: unsupported signed migration policy")
	}
	if err != nil || !equalCandidateMigrations(policy.Migrations, approved.Approved) {
		return releaseMigrationManifest{}, errors.New("lead-gen: signed migration policy differs from pinned exact migration set")
	}
	return approved, nil
}

func releaseMigrationRoot() string {
	if _, err := os.Stat(filepath.Join("/backend", "contexts", "request-fulfillment", "migrations", "green")); err == nil {
		return "/"
	}
	root, err := os.Getwd()
	if err != nil {
		return "."
	}
	return root
}

// targetDefinition — закрытая allowlist DB-target. Пути и роли никогда не
// принимаются из CLI.
type targetDefinition struct {
	schema         migrate.Schema
	migrationsDir  string
	runtimeRole    string
	ownedRelations []string
}

var dbTargets = map[string]targetDefinition{
	"request-fulfillment": {
		schema:         migrate.Schema("request-fulfillment"),
		migrationsDir:  "backend/contexts/request-fulfillment/migrations/green",
		runtimeRole:    "leadgen_request_fulfillment_runtime",
		ownedRelations: []string{"schema_migrations", "request", "email_draft", "outbox", "execution_attempt", "execution_resolution", "execution_resolution_revision", "execution_verified_runtime_resolution"},
	},
	"consoleauth": {
		schema:         migrate.Schema("consoleauth"),
		migrationsDir:  "backend/platform/consoleauth/migrations/green",
		runtimeRole:    "leadgen_consoleauth_runtime",
		ownedRelations: []string{"schema_migrations", "console_sessions"},
	},
	"integration-platform": {
		schema:         migrate.Schema("integration-platform"),
		migrationsDir:  "backend/contexts/integration-platform/migrations",
		runtimeRole:    "leadgen_integration_platform_runtime",
		ownedRelations: []string{"schema_migrations", "adapter_registration", "adapter_capability", "integration_invocation", "integration_invocation_seq_seq", "vector_document"},
	},
}

type dbJobFlags struct {
	job                           string
	target                        string
	candidateManifestFile         string
	candidatePublicKeyFile        string
	stageTarget                   string
	stageSourceDir                string
	stageVolumeDir                string
	stageGeneration               string
	stageSourceName               string
	stageKeyID                    string
	bootstrapDSNFile              string
	migrationDSNFile              string
	runtimeDSNFile                string
	migratorPasswordFile          string
	runtimePasswordFile           string
	additionalRuntimePasswordFile string
}

const (
	stagingSourcePath = "/run/secret-source"
	stagedSecretRoot  = "/stage"
	trustSourcePath   = "/run/trust-source"
	trustStageRoot    = "/stage"
	trustRuntimeRoot  = "/run/leadgen-trust/active"
)

type stagedSecret struct {
	source string
	target string
}

// stagingBundles — закрытый перечень потребителей и имен файлов. Никакой путь
// к секрету назначения или произвольный target не принимается из CLI.
var stagingBundles = map[string][]stagedSecret{
	"intake": {
		{source: "intake-request-runtime-dsn", target: "request-runtime-dsn"},
		{source: "intake-consoleauth-runtime-dsn", target: "consoleauth-runtime-dsn"},
		{source: "intake-vector-runtime-dsn", target: "vector-runtime-dsn"},
		{source: "console-password", target: "console-password"},
	},
	"core":                           {{source: "console-password", target: "console-password"}},
	"demand":                         {{source: "console-password", target: "console-password"}},
	"platform":                       {{source: "console-password", target: "console-password"}},
	"request-provision":              {{source: "request-bootstrap-dsn", target: "bootstrap-dsn"}, {source: "request-migrator-password", target: "migrator-password"}, {source: "request-runtime-password", target: "runtime-password"}},
	"request-migrate":                {{source: "request-migration-dsn", target: "migration-dsn"}},
	"consoleauth-provision":          {{source: "consoleauth-bootstrap-dsn", target: "bootstrap-dsn"}, {source: "consoleauth-migrator-password", target: "migrator-password"}, {source: "consoleauth-runtime-password", target: "runtime-password"}},
	"consoleauth-migrate":            {{source: "consoleauth-migration-dsn", target: "migration-dsn"}},
	"integration-platform-provision": {{source: "integration-platform-bootstrap-dsn", target: "bootstrap-dsn"}, {source: "integration-platform-migrator-password", target: "migrator-password"}, {source: "integration-platform-runtime-password", target: "runtime-password"}, {source: "intake-vector-runtime-password", target: "additional-runtime-password"}},
	"integration-platform-migrate":   {{source: "integration-platform-migration-dsn", target: "migration-dsn"}},
}

type dbJobEnvironment struct {
	bootstrapDSN string
	migrationDSN string
	runtimeDSN   string
	runtimeScope migrate.RuntimeRoleScope
	candidate    CandidateManifest
}

func validateDBJobConfig(flags dbJobFlags, lookupEnv func(string) (string, bool)) (dbJob, targetDefinition, dbJobEnvironment, error) {
	if flags.job == "" {
		if flags.target != "" || flags.candidateManifestFile != "" || flags.candidatePublicKeyFile != "" || flags.stageTarget != "" || flags.stageSourceDir != "" || flags.stageVolumeDir != "" || flags.stageGeneration != "" || flags.stageSourceName != "" || flags.stageKeyID != "" || anyDBCredentialFile(flags) || anyDBCredentialEnvironment(lookupEnv) {
			return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: параметры DB job запрещены в режиме приложения")
		}
		return "", targetDefinition{}, dbJobEnvironment{}, nil
	}
	job := dbJob(flags.job)
	if job == dbJobStage {
		return job, targetDefinition{}, dbJobEnvironment{}, nil
	}
	if job != dbJobProvision && job != dbJobMigrate && job != dbJobVerify {
		return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: неизвестный режим DB job")
	}
	target, ok := dbTargets[flags.target]
	if !ok {
		return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: DB target отсутствует или не разрешён")
	}
	if flags.candidateManifestFile == "" || flags.candidatePublicKeyFile == "" {
		return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: DB jobs требуют signed candidate manifest и trust key")
	}
	if _, ok := lookupEnv("LEADGEN_BOOTSTRAP_DSN"); ok && flags.bootstrapDSNFile != "" {
		return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: bootstrap credential задан одновременно через ENV и файл")
	}
	if _, ok := lookupEnv("LEADGEN_MIGRATION_DSN"); ok && flags.migrationDSNFile != "" {
		return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: migration credential задан одновременно через ENV и файл")
	}
	if _, ok := lookupEnv("LEADGEN_RUNTIME_DSN"); ok && flags.runtimeDSNFile != "" {
		return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: runtime credential задан одновременно через ENV и файл")
	}
	if anyDBCredentialEnvironment(lookupEnv) {
		return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: DSN credentials разрешены только из файлов, ENV запрещён")
	}
	manifest, err := readCandidateManifest(flags.candidateManifestFile, flags.candidatePublicKeyFile, flags.target, lookupEnvValue(lookupEnv, "LEADGEN_RELEASE_TRUST_KEY_ID"))
	if err != nil {
		return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: candidate manifest verification failed")
	}
	_, err = validateCandidateTargetPolicy(releaseMigrationRoot(), flags.target, manifest)
	if err != nil {
		return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: signed migration manifest does not match pinned migration files")
	}
	scope := migrate.RuntimeRoleScope{Environment: manifest.Environment, Deployment: manifest.DeploymentID, Target: flags.target, Generation: manifest.Generation}
	env := dbJobEnvironment{runtimeScope: scope, candidate: manifest}
	switch job {
	case dbJobProvision:
		if flags.bootstrapDSNFile == "" || flags.migrationDSNFile != "" || flags.runtimeDSNFile != "" {
			return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: provision требует только bootstrap DSN file")
		}
		if flags.migratorPasswordFile == "" || flags.runtimePasswordFile == "" || (flags.target == "integration-platform" && flags.additionalRuntimePasswordFile == "") || (flags.target != "integration-platform" && flags.additionalRuntimePasswordFile != "") {
			return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: некорректный набор файлов паролей для provision target")
		}
		dsn, err := resolveCredential("bootstrap DSN", flags.bootstrapDSNFile)
		if err != nil {
			return "", targetDefinition{}, dbJobEnvironment{}, err
		}
		env.bootstrapDSN = dsn
	case dbJobMigrate:
		if flags.migrationDSNFile == "" || flags.bootstrapDSNFile != "" || flags.runtimeDSNFile != "" || anyRolePasswordFile(flags) {
			return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: migrate требует только migration DSN file")
		}
		dsn, err := resolveCredential("migration DSN", flags.migrationDSNFile)
		if err != nil {
			return "", targetDefinition{}, dbJobEnvironment{}, err
		}
		env.migrationDSN = dsn
	case dbJobVerify:
		if flags.runtimeDSNFile == "" || flags.bootstrapDSNFile != "" || flags.migrationDSNFile != "" || anyRolePasswordFile(flags) {
			return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: verify требует только runtime DSN file")
		}
		roleSets, roleErr := roleSetsForDBJob(flags.target, scope, "", "", "")
		if roleErr != nil {
			return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: runtime verify role registry unavailable")
		}
		runtimeSpec, found := roleSetForTarget(roleSets, target)
		if !found || verifyCandidateRoleBinding(manifest, runtimeSpec) != nil {
			return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: runtime verify role does not match signed candidate")
		}
		dsn, err := resolveCredential("runtime DSN", flags.runtimeDSNFile)
		if err != nil {
			return "", targetDefinition{}, dbJobEnvironment{}, err
		}
		if !dsnUsernameMatches(dsn, runtimeSpec.RuntimeRole) {
			return "", targetDefinition{}, dbJobEnvironment{}, errors.New("lead-gen: runtime DSN username differs from signed candidate role")
		}
		env.runtimeDSN = dsn
	}
	return job, target, env, nil
}

func equalCandidateMigrations(candidate []CandidateMigration, approved []migrate.ApprovedMigration) bool {
	if len(candidate) != len(approved) {
		return false
	}
	for i := range candidate {
		if candidate[i].Name != approved[i].Name || candidate[i].SHA256 != approved[i].SHA256 {
			return false
		}
	}
	return true
}

func resolveCredential(label, file string) (string, error) {
	if file == "" {
		return "", fmt.Errorf("lead-gen: файл %s не задан", label)
	}
	value, err := readSecretFile(file)
	if err != nil {
		return "", fmt.Errorf("lead-gen: не удалось прочитать файл %s", label)
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("lead-gen: файл %s пуст", label)
	}
	return strings.TrimSpace(value), nil
}

func readSecretFile(path string) (string, error) {
	return readSecretFileFromDescriptor(path, nil, false)
}

func readSecretFileFromDescriptor(path string, afterOpen func(), allowStickySystemTempForTests bool) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil || filepath.Base(absPath) == "." || filepath.Base(absPath) == string(filepath.Separator) {
		return "", errors.New("invalid secret file")
	}
	// Разрешается канонический путь родительских каталогов, но не конечный
	// компонент: он будет открыт относительно проверенного descriptor с NOFOLLOW.
	parentPath, err := filepath.EvalSymlinks(filepath.Dir(absPath))
	if err != nil || !filepath.IsAbs(parentPath) {
		return "", errors.New("invalid secret file")
	}
	parentFD, err := openAndCheckSecretParent(parentPath, allowStickySystemTempForTests)
	if err != nil {
		return "", errors.New("invalid secret file")
	}
	defer func() { _ = unix.Close(parentFD) }()

	fd, err := unix.Openat(parentFD, filepath.Base(absPath), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", errors.New("invalid secret file")
	}
	file := os.NewFile(uintptr(fd), "")
	if file == nil {
		_ = unix.Close(fd)
		return "", errors.New("invalid secret file")
	}
	defer func() { _ = file.Close() }()
	if afterOpen != nil {
		afterOpen()
	}
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil || !safeSecretFileStat(&info, uint32(os.Geteuid())) {
		return "", errors.New("invalid secret file")
	}
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return "", errors.New("invalid secret file")
	}
	return string(data), nil
}

func openAndCheckSecretParent(parentPath string, allowStickyTempForTest bool) (int, error) {
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, err
	}
	var root unix.Stat_t
	if err := unix.Fstat(fd, &root); err != nil || !safeSecretDirectoryStat(&root, os.Geteuid(), string(filepath.Separator), allowStickyTempForTest) {
		_ = unix.Close(fd)
		return -1, errors.New("unsafe secret root")
	}
	currentPath := string(filepath.Separator)
	for _, component := range strings.Split(strings.Trim(parentPath, string(filepath.Separator)), string(filepath.Separator)) {
		if component == "" || component == "." || component == ".." {
			_ = unix.Close(fd)
			return -1, errors.New("invalid secret parent")
		}
		nextFD, err := unix.Openat(fd, component, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil {
			_ = unix.Close(fd)
			return -1, err
		}
		_ = unix.Close(fd)
		fd = nextFD
		currentPath = filepath.Join(currentPath, component)
		var info unix.Stat_t
		if err := unix.Fstat(fd, &info); err != nil || !safeSecretDirectoryStat(&info, os.Geteuid(), currentPath, allowStickyTempForTest) {
			_ = unix.Close(fd)
			return -1, errors.New("unsafe secret parent")
		}
	}
	return fd, nil
}

func safeSecretDirectoryStat(info *unix.Stat_t, euid int, path string, allowStickyTempForTest bool) bool {
	if info == nil || info.Mode&unix.S_IFMT != unix.S_IFDIR || (info.Uid != 0 && info.Uid != uint32(euid)) {
		return false
	}
	writeByGroupOrOther := info.Mode & (unix.S_IWGRP | unix.S_IWOTH)
	if writeByGroupOrOther == 0 {
		return true
	}
	// t.TempDir может располагаться под sticky /tmp на Linux. Разрешение
	// применяется только из тестового seam и только к системному каталогу,
	// принадлежащему root и защищённому sticky bit.
	return allowStickyTempForTest && (path == "/tmp" || path == "/var/tmp") && info.Uid == 0 &&
		info.Mode&unix.S_ISVTX != 0 && info.Mode&unix.S_IWOTH != 0
}

func safeSecretFileStat(info *unix.Stat_t, expectedUID uint32) bool {
	if info == nil || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Uid != expectedUID {
		return false
	}
	return info.Mode&0o7777 == 0o400
}

func anyDBCredentialFile(flags dbJobFlags) bool {
	return flags.bootstrapDSNFile != "" || flags.migrationDSNFile != "" || flags.runtimeDSNFile != "" || anyRolePasswordFile(flags)
}

func anyRolePasswordFile(flags dbJobFlags) bool {
	return flags.migratorPasswordFile != "" || flags.runtimePasswordFile != "" || flags.additionalRuntimePasswordFile != ""
}

func anyDBCredentialEnvironment(lookupEnv func(string) (string, bool)) bool {
	for _, name := range []string{"LEADGEN_BOOTSTRAP_DSN", "LEADGEN_MIGRATION_DSN", "LEADGEN_RUNTIME_DSN"} {
		if _, ok := lookupEnv(name); ok {
			return true
		}
	}
	return false
}

func runDBJob(ctx context.Context, flags dbJobFlags) error {
	if flags.job == string(dbJobStageTrustAnchor) {
		if flags.target != "" || flags.stageTarget != "" || flags.stageGeneration != "" ||
			flags.candidateManifestFile != "" || flags.candidatePublicKeyFile != "" ||
			anyDBCredentialFile(flags) || anyDBCredentialEnvironment(os.LookupEnv) {
			return errors.New("lead-gen: trust staging не принимает DB credentials, target или generation")
		}
		if os.Geteuid() != 0 {
			return errors.New("lead-gen: trust staging требует root helper")
		}
		return stageTrustAnchor(flags.stageSourceDir, flags.stageSourceName, flags.stageVolumeDir, flags.stageKeyID, true)
	}
	if flags.job == string(dbJobValidateMigrations) {
		if flags.target == "" || flags.stageTarget != "" || flags.stageSourceDir != "" || flags.stageGeneration != "" ||
			anyDBCredentialFile(flags) || anyDBCredentialEnvironment(os.LookupEnv) {
			return errors.New("lead-gen: migration manifest validation accepts only a DB target")
		}
		var manifest releaseMigrationManifest
		var err error
		if flags.target == "request-fulfillment" {
			manifest, err = validateG15MigrationManifest(releaseMigrationRoot())
		} else {
			manifest, err = validateReleaseMigrationManifest(releaseMigrationRoot(), flags.target)
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(manifest)
	}
	if flags.job == string(dbJobValidateStage) {
		if flags.target != "" || flags.stageVolumeDir != stagedSecretRoot || anyDBCredentialFile(flags) || anyDBCredentialEnvironment(os.LookupEnv) {
			return errors.New("lead-gen: stage source validation accepts only target/source metadata")
		}
		return validateStagingSourceBundle(flags.stageTarget, flags.stageSourceDir, flags.stageGeneration, true)
	}
	if flags.job == string(dbJobStage) {
		if flags.target != "" || anyDBCredentialFile(flags) || anyDBCredentialEnvironment(os.LookupEnv) {
			return errors.New("lead-gen: staging не принимает DB credentials или DB target")
		}
		if os.Geteuid() != 0 {
			return errors.New("lead-gen: staging требует root-контейнер")
		}
		return stageSecretBundle(flags.stageTarget, flags.stageSourceDir, flags.stageVolumeDir, flags.stageGeneration)
	}
	if flags.job == string(dbJobVerifyBundle) {
		if flags.target != "" || flags.stageSourceDir != "" || anyDBCredentialFile(flags) || anyDBCredentialEnvironment(os.LookupEnv) {
			return errors.New("lead-gen: bundle verification не принимает DB credentials или DB target")
		}
		if os.Geteuid() == 0 {
			return errors.New("lead-gen: bundle verification должна выполняться nonroot")
		}
		return verifyStagedSecretBundle(flags.stageTarget, flags.stageVolumeDir, flags.stageGeneration)
	}
	job, target, credentials, err := validateDBJobConfig(flags, os.LookupEnv)
	if err != nil {
		return err
	}
	if job == "" {
		return nil
	}
	switch job {
	case dbJobProvision:
		migratorPassword, err := readRequiredPassword(flags.migratorPasswordFile)
		if err != nil {
			return err
		}
		runtimePassword, err := readRequiredPassword(flags.runtimePasswordFile)
		if err != nil {
			return err
		}
		additionalPassword := ""
		if flags.additionalRuntimePasswordFile != "" {
			additionalPassword, err = readRequiredPassword(flags.additionalRuntimePasswordFile)
			if err != nil {
				return err
			}
		}
		sets, err := roleSetsForDBJob(flags.target, credentials.runtimeScope, migratorPassword, runtimePassword, additionalPassword)
		if err != nil {
			return errors.New("lead-gen: не удалось получить generation runtime identity")
		}
		spec, ok := roleSetForTarget(sets, target)
		if !ok {
			return errors.New("lead-gen: DB target отсутствует в grant registry")
		}
		if err := verifyCandidateRoleBinding(credentials.candidate, spec); err != nil {
			return err
		}
		pool, closePool, err := connectDB(credentials.bootstrapDSN)
		if err != nil {
			return err
		}
		defer closePool()
		if err := migrate.ProvisionSchema(ctx, pool, spec); err != nil {
			return errors.New("lead-gen: provision job завершился ошибкой")
		}
	case dbJobMigrate:
		manifest, err := validateCandidateTargetPolicy(releaseMigrationRoot(), flags.target, credentials.candidate)
		if err != nil {
			return err
		}
		sets, err := roleSetsForDBJob(flags.target, credentials.runtimeScope, "", "", "")
		if err != nil {
			return errors.New("lead-gen: не удалось получить generation runtime identity")
		}
		spec, ok := roleSetForTarget(sets, target)
		if !ok {
			return errors.New("lead-gen: DB target отсутствует в grant registry")
		}
		if err := verifyCandidateRoleBinding(credentials.candidate, spec); err != nil {
			return err
		}
		pool, closePool, err := connectDB(credentials.migrationDSN)
		if err != nil {
			return err
		}
		defer closePool()
		if err := migrate.ApplyAsAllowlisted(ctx, pool, target.migrationsDir, target.schema, spec.OwnerRole, manifest.Approved); err != nil {
			return errors.New("lead-gen: migrate job завершился ошибкой")
		}
		if err := migrate.GrantRuntimeSchemaAs(ctx, pool, spec, spec.OwnerRole); err != nil {
			return errors.New("lead-gen: выдача runtime grants завершилась ошибкой")
		}
		if err := json.NewEncoder(os.Stdout).Encode(manifest); err != nil {
			return errors.New("lead-gen: migration manifest result serialization failed")
		}
	case dbJobVerify:
		pool, closePool, err := connectDB(credentials.runtimeDSN)
		if err != nil {
			return err
		}
		defer closePool()
		sets, err := roleSetsForDBJob(flags.target, credentials.runtimeScope, "", "", "")
		if err != nil {
			return errors.New("lead-gen: не удалось получить generation runtime identity")
		}
		spec, ok := roleSetForTarget(sets, target)
		if !ok {
			return errors.New("lead-gen: DB target отсутствует в grant registry")
		}
		if err := verifyCandidateRoleBinding(credentials.candidate, spec); err != nil {
			return err
		}
		if err := verifyRuntimeRole(ctx, pool, spec.RuntimeRole); err != nil {
			return err
		}
		if err := migrate.Verify(ctx, pool, target.migrationsDir, target.schema); err != nil {
			return errors.New("lead-gen: read-only проверка версии схемы завершилась ошибкой")
		}
	}
	return nil
}

// validateStagingSourceBundle проверяет allowlisted source files через открытый
// descriptor и не читает secret bytes. Runner вызывает это до begin-release, чтобы
// отсутствующий или небезопасный источник не расходовал nonce/generation.
func validateStagingSourceBundle(bundle, sourceDir, generation string, requireMount bool) error {
	files, ok := stagingBundles[bundle]
	if !ok || sourceDir == "" || !validSecretGeneration(generation) {
		return errors.New("lead-gen: invalid stage source validation metadata")
	}
	dirFD, err := openStageSourceDirectory(sourceDir)
	if err != nil {
		return errors.New("lead-gen: unsafe source directory")
	}
	defer unix.Close(dirFD)
	if requireMount {
		if err := verifyStagingMount(sourceDir, true); err != nil {
			return errors.New("lead-gen: source mount missing or not read-only")
		}
	}
	for _, file := range files {
		if err := validateStagingSourceAt(dirFD, file.source); err != nil {
			return errors.New("lead-gen: staging source missing or unsafe")
		}
	}
	return nil
}

func validateStagingSourceAt(dirFD int, name string) error {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return errors.New("invalid source name")
	}
	var parent unix.Stat_t
	if err := unix.Fstat(dirFD, &parent); err != nil || parent.Mode&unix.S_IFMT != unix.S_IFDIR || parent.Mode&0o022 != 0 {
		return errors.New("unsafe source directory")
	}
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || !validStagingSourceStat(&stat, parent.Uid) || stat.Size <= 0 || stat.Size > 1<<20 {
		return errors.New("unsafe source metadata")
	}
	return nil
}

// verifyStagedSecretBundle проверяет опубликованный bundle как runtime uid. Он
// печатает только подтверждение и имена файлов, никогда содержимое и его hash.
func verifyStagedSecretBundle(bundle, volumeDir, generation string) error {
	if volumeDir != "/run/secrets" {
		return errors.New("lead-gen: недопустимая конфигурация проверки bundle")
	}
	return verifyStagedSecretBundleAt(bundle, volumeDir, generation, readSecretFile)
}

// verifyStagedSecretBundleAt отделяет проверку состава от production reader.
// Runtime-обёртка сохраняет фиксированный путь и descriptor/UID-проверки.
func verifyStagedSecretBundleAt(bundle, volumeDir, generation string, readFile func(string) (string, error)) error {
	files, ok := stagingBundles[bundle]
	if !ok || volumeDir == "" || !validSecretGeneration(generation) || readFile == nil {
		return errors.New("lead-gen: недопустимая конфигурация проверки bundle")
	}
	root := filepath.Join(volumeDir, generation)
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o500 {
		return errors.New("lead-gen: bundle отсутствует, неполон или устарел")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != 65532 || stat.Gid != 65532 {
		return errors.New("lead-gen: bundle отсутствует, неполон или устарел")
	}
	marker, err := readFile(filepath.Join(root, "generation"))
	if err != nil || string(marker) != generation+"\n" {
		return errors.New("lead-gen: bundle отсутствует, неполон или устарел")
	}
	expected := map[string]struct{}{"generation": {}}
	for _, item := range files {
		expected[item.target] = struct{}{}
		if _, err := readFile(filepath.Join(root, item.target)); err != nil {
			return errors.New("lead-gen: bundle отсутствует, неполон или устарел")
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != len(expected) {
		return errors.New("lead-gen: bundle отсутствует, неполон или устарел")
	}
	for _, entry := range entries {
		if _, ok := expected[entry.Name()]; !ok || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("lead-gen: bundle содержит неизвестный файл")
		}
	}
	return nil
}

func stageSecretBundle(bundle, sourceDir, volumeDir, generation string) error {
	if sourceDir != stagingSourcePath || volumeDir != stagedSecretRoot {
		return errors.New("lead-gen: stage source/target paths are fixed")
	}
	return stageSecretBundleAt(bundle, stagingSourcePath, stagedSecretRoot, generation, true)
}

func stageSecretBundleAt(bundle, sourceDir, volumeDir, generation string, requireMounts bool) error {
	files, ok := stagingBundles[bundle]
	if !ok || sourceDir == "" || volumeDir == "" || (requireMounts && (sourceDir != stagingSourcePath || volumeDir != stagedSecretRoot)) || !validSecretGeneration(generation) {
		return errors.New("lead-gen: недопустимая конфигурация staging")
	}
	sourceFD, err := openStageSourceDirectory(sourceDir)
	if err != nil {
		return errors.New("lead-gen: небезопасный source directory")
	}
	defer unix.Close(sourceFD)
	if requireMounts {
		if err := verifyStagingMount(sourceDir, true); err != nil {
			return errors.New("lead-gen: source mount отсутствует или не read-only")
		}
	}
	if err := checkStageDirectory(volumeDir); err != nil {
		return fmt.Errorf("lead-gen: недоступен target volume (%w)", err)
	}
	if requireMounts {
		if err := verifyStagingMount(volumeDir, false); err != nil {
			return errors.New("lead-gen: target volume mount отсутствует")
		}
	}

	stageName := ".stage-" + generation
	tempDir := filepath.Join(volumeDir, stageName)
	if err := os.Mkdir(tempDir, 0o700); err != nil {
		return errors.New("lead-gen: не удалось создать временный bundle")
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(tempDir)
		}
	}()

	for _, item := range files {
		data, err := readStagingSourceAt(sourceFD, item.source)
		if err != nil {
			return errors.New("lead-gen: staging source недоступен или небезопасен")
		}
		if err := writeStagedSecret(tempDir, item.target, data); err != nil {
			return errors.New("lead-gen: не удалось подготовить bundle")
		}
	}
	marker := []byte(generation + "\n")
	if err := writeStagedSecret(tempDir, "generation", marker); err != nil {
		return errors.New("lead-gen: не удалось записать generation marker")
	}
	dir, err := os.Open(tempDir)
	if err != nil {
		return errors.New("lead-gen: не удалось синхронизировать bundle")
	}
	if err = dir.Sync(); err == nil {
		err = unix.Fchown(int(dir.Fd()), 65532, 65532)
	}
	if err == nil {
		err = unix.Fchmod(int(dir.Fd()), 0o500)
	}
	if err == nil {
		err = dir.Sync()
	}
	closeErr := dir.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("lead-gen: не удалось синхронизировать bundle")
	}
	finalDir := filepath.Join(volumeDir, generation)
	if _, err := os.Lstat(finalDir); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("lead-gen: generation уже существует")
	}
	if err := os.Rename(tempDir, finalDir); err != nil {
		return errors.New("lead-gen: публикация bundle завершилась ошибкой")
	}
	volume, err := os.Open(volumeDir)
	if err != nil {
		return errors.New("lead-gen: публикация bundle не подтверждена")
	}
	err = volume.Sync()
	_ = volume.Close()
	if err != nil {
		return errors.New("lead-gen: публикация bundle не подтверждена")
	}
	published = true
	return nil
}

// stageTrustAnchor publishes one independently provisioned public trust key.
// The source bytes never enter arguments, environment, or diagnostics.
func stageTrustAnchor(sourceDir, sourceName, volumeDir, keyID string, requireMounts bool) error {
	return stageTrustAnchorForOwner(sourceDir, sourceName, volumeDir, keyID, requireMounts, 65532, 65532)
}

func stageTrustAnchorForOwner(sourceDir, sourceName, volumeDir, keyID string, requireMounts bool, owner, group uint32) error {
	if requireMounts && (sourceDir != trustSourcePath || volumeDir != trustStageRoot) {
		return errors.New("lead-gen: trust source/target paths are fixed")
	}
	if sourceName != "trusted-release.pub" || !candidateIDPattern.MatchString(keyID) {
		return errors.New("lead-gen: trust source name or key ID rejected")
	}
	if sourceDir == "" || volumeDir == "" {
		return errors.New("lead-gen: trust source and target are required")
	}
	sourceFD, err := openStageSourceDirectory(sourceDir)
	if err != nil {
		return errors.New("lead-gen: trust source directory rejected")
	}
	defer unix.Close(sourceFD)
	if requireMounts {
		if err := verifyStagingMount(sourceDir, true); err != nil {
			return errors.New("lead-gen: trust source mount is missing or writable")
		}
	}
	if err := checkStageDirectory(volumeDir); err != nil {
		return errors.New("lead-gen: trust target volume rejected")
	}
	if requireMounts {
		if err := verifyStagingMount(volumeDir, false); err != nil {
			return errors.New("lead-gen: trust target volume mount is missing")
		}
	}
	keyBytes, err := readTrustSourceAt(sourceFD, sourceName)
	if err != nil {
		return errors.New("lead-gen: trust source is missing or unsafe")
	}
	if _, err := parseEd25519PublicKey(keyBytes); err != nil {
		return errors.New("lead-gen: trust source encoding rejected")
	}
	activeDir := filepath.Join(volumeDir, "active")
	if _, err := os.Lstat(activeDir); err == nil {
		return verifyStagedTrustAnchor(activeDir, keyID, keyBytes, owner, group)
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("lead-gen: existing trust bundle cannot be inspected")
	}
	tempDir, err := os.MkdirTemp(volumeDir, ".trust-stage-")
	if err != nil {
		return errors.New("lead-gen: trust bundle staging failed")
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(tempDir)
		}
	}()
	for name, data := range map[string][]byte{
		"trusted-release.pub": keyBytes,
		"key-id":              []byte(keyID + "\n"),
		"complete":            []byte("complete\n"),
	} {
		if err := writeTrustStagedFile(tempDir, name, data, owner, group); err != nil {
			return errors.New("lead-gen: trust bundle preparation failed")
		}
	}
	dir, err := os.Open(tempDir)
	if err != nil {
		return errors.New("lead-gen: trust bundle preparation failed")
	}
	if err = dir.Sync(); err == nil {
		err = unix.Fchown(int(dir.Fd()), int(owner), int(group))
	}
	if err == nil {
		err = unix.Fchmod(int(dir.Fd()), 0o500)
	}
	if err == nil {
		err = dir.Sync()
	}
	closeErr := dir.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("lead-gen: trust bundle preparation failed")
	}
	// Rename cannot replace a populated active directory. A concurrent publisher
	// therefore fails closed; it never replaces an already visible trust anchor.
	if err := os.Rename(tempDir, activeDir); err != nil {
		return errors.New("lead-gen: trust bundle publication failed")
	}
	volume, err := os.Open(volumeDir)
	if err != nil {
		return errors.New("lead-gen: trust bundle publication was not confirmed")
	}
	err = volume.Sync()
	_ = volume.Close()
	if err != nil {
		return errors.New("lead-gen: trust bundle publication was not confirmed")
	}
	published = true
	return nil
}

func writeTrustStagedFile(dir, name string, data []byte, owner, group uint32) error {
	if name != "trusted-release.pub" && name != "key-id" && name != "complete" {
		return errors.New("invalid trust bundle file")
	}
	fd, err := unix.Open(filepath.Join(dir, name), unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), "trust-bundle-file")
	if file == nil {
		_ = unix.Close(fd)
		return errors.New("invalid trust bundle file")
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := unix.Fchown(fd, int(owner), int(group)); err != nil {
		return err
	}
	if err := unix.Fchmod(fd, 0o400); err != nil {
		return err
	}
	return file.Sync()
}

func readTrustSourceAt(dirFD int, name string) ([]byte, error) {
	if name != "trusted-release.pub" {
		return nil, errors.New("invalid trust source name")
	}
	var parent unix.Stat_t
	if err := unix.Fstat(dirFD, &parent); err != nil || parent.Mode&unix.S_IFMT != unix.S_IFDIR || parent.Mode&0o022 != 0 {
		return nil, errors.New("unsafe trust source directory")
	}
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "trust-public-key")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("invalid trust source")
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || !validTrustSourceStat(&stat, parent.Uid) || stat.Size <= 0 || stat.Size > 4096 {
		return nil, errors.New("unsafe trust source metadata")
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) == 0 || len(data) > 4096 {
		return nil, errors.New("invalid trust source size")
	}
	return data, nil
}

func validTrustSourceStat(stat *unix.Stat_t, owner uint32) bool {
	if stat == nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != owner {
		return false
	}
	mode := stat.Mode & 0o7777
	return mode == 0o400 || mode == 0o600
}

func verifyStagedTrustAnchor(dir, expectedKeyID string, expectedKey []byte, owner, group uint32) error {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o500 {
		return errors.New("lead-gen: existing trust bundle is incomplete or unsafe")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != owner || stat.Gid != group {
		return errors.New("lead-gen: existing trust bundle owner rejected")
	}
	keyID, err := readTrustBundleFile(dir, "key-id", owner, group)
	if err != nil || string(keyID) != expectedKeyID+"\n" {
		return errors.New("lead-gen: existing trust bundle key ID differs")
	}
	marker, err := readTrustBundleFile(dir, "complete", owner, group)
	if err != nil || string(marker) != "complete\n" {
		return errors.New("lead-gen: existing trust bundle completion marker rejected")
	}
	key, err := readTrustBundleFile(dir, "trusted-release.pub", owner, group)
	if err != nil || !equalBytes(key, expectedKey) {
		return errors.New("lead-gen: existing trust bundle key differs")
	}
	if _, err := parseEd25519PublicKey(key); err != nil {
		return errors.New("lead-gen: existing trust bundle key encoding rejected")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 3 {
		return errors.New("lead-gen: existing trust bundle contents rejected")
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || (entry.Name() != "trusted-release.pub" && entry.Name() != "key-id" && entry.Name() != "complete") {
			return errors.New("lead-gen: existing trust bundle contents rejected")
		}
	}
	return nil
}

func readTrustBundleFile(dir, name string, owner, group uint32) ([]byte, error) {
	if name != "trusted-release.pub" && name != "key-id" && name != "complete" {
		return nil, errors.New("invalid trust bundle file")
	}
	dirFD, err := unix.Open(dir, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dirFD)
	var directoryStat unix.Stat_t
	if err := unix.Fstat(dirFD, &directoryStat); err != nil || directoryStat.Mode&unix.S_IFMT != unix.S_IFDIR ||
		directoryStat.Uid != owner || directoryStat.Gid != group || directoryStat.Mode&0o7777 != 0o500 {
		return nil, errors.New("unsafe trust bundle directory")
	}
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "trust-bundle-file")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("invalid trust bundle file")
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != owner || stat.Gid != group || stat.Mode&0o7777 != 0o400 || stat.Size <= 0 || stat.Size > 4096 {
		return nil, errors.New("unsafe trust bundle file")
	}
	return io.ReadAll(io.LimitReader(file, 4097))
}

// readStagedTrustAnchor enforces the runtime's strict secret-file owner/mode
// policy for both the key and its separate identity marker.
func readStagedTrustAnchor(directory, configuredKeyID string) (ed25519.PublicKey, error) {
	return readStagedTrustAnchorAt(directory, configuredKeyID, true)
}

func readStagedTrustAnchorAt(directory, configuredKeyID string, requireFixedPath bool) (ed25519.PublicKey, error) {
	if (requireFixedPath && directory != trustRuntimeRoot) || !candidateIDPattern.MatchString(configuredKeyID) {
		return nil, errors.New("lead-gen: staged trust path or key ID rejected")
	}
	if err := verifyTrustBundleDirectory(directory, 65532, 65532); err != nil {
		return nil, errors.New("lead-gen: staged trust bundle metadata rejected")
	}
	dirFD, err := unix.Open(directory, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("lead-gen: staged trust bundle unavailable")
	}
	defer unix.Close(dirFD)
	if err := verifyTrustBundleDescriptor(dirFD, 65532, 65532); err != nil {
		return nil, errors.New("lead-gen: staged trust bundle metadata rejected")
	}
	keyID, err := readTrustBundleFileAt(dirFD, "key-id", 65532, 65532)
	if err != nil || string(keyID) != configuredKeyID+"\n" {
		return nil, errors.New("lead-gen: staged trust key ID mismatch")
	}
	marker, err := readTrustBundleFileAt(dirFD, "complete", 65532, 65532)
	if err != nil || string(marker) != "complete\n" {
		return nil, errors.New("lead-gen: staged trust bundle is incomplete")
	}
	keyText, err := readTrustBundleFileAt(dirFD, "trusted-release.pub", 65532, 65532)
	if err != nil {
		return nil, errors.New("lead-gen: staged trust key unavailable")
	}
	key, err := parseEd25519PublicKey(keyText)
	if err != nil {
		return nil, errors.New("lead-gen: staged trust key encoding rejected")
	}
	return key, nil
}

func verifyTrustBundleDirectory(directory string, owner, group uint32) error {
	dirFD, err := unix.Open(directory, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer unix.Close(dirFD)
	return verifyTrustBundleDescriptor(dirFD, owner, group)
}

func verifyTrustBundleDescriptor(dirFD int, owner, group uint32) error {
	var stat unix.Stat_t
	if err := unix.Fstat(dirFD, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR ||
		stat.Uid != owner || stat.Gid != group || stat.Mode&0o7777 != 0o500 {
		return errors.New("unsafe trust bundle directory")
	}
	readFD, err := unix.Dup(dirFD)
	if err != nil {
		return errors.New("invalid trust bundle directory")
	}
	directory := os.NewFile(uintptr(readFD), "trust-bundle-directory")
	if directory == nil {
		_ = unix.Close(readFD)
		return errors.New("invalid trust bundle directory")
	}
	defer directory.Close()
	entries, err := directory.ReadDir(-1)
	if err != nil || len(entries) != 3 {
		return errors.New("unexpected trust bundle entries")
	}
	wanted := map[string]bool{"trusted-release.pub": false, "key-id": false, "complete": false}
	for _, entry := range entries {
		if _, ok := wanted[entry.Name()]; !ok || entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("unexpected trust bundle entry")
		}
		wanted[entry.Name()] = true
	}
	for _, found := range wanted {
		if !found {
			return errors.New("missing trust bundle entry")
		}
	}
	return nil
}

func readTrustBundleFileAt(dirFD int, name string, owner, group uint32) ([]byte, error) {
	if name != "trusted-release.pub" && name != "key-id" && name != "complete" {
		return nil, errors.New("invalid trust bundle file")
	}
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "trust-bundle-file")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("invalid trust bundle file")
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != owner ||
		stat.Gid != group || stat.Mode&0o7777 != 0o400 || stat.Size <= 0 || stat.Size > 4096 {
		return nil, errors.New("unsafe trust bundle file")
	}
	data, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(data) == 0 || len(data) > 4096 {
		return nil, errors.New("unsafe trust bundle file")
	}
	return data, nil
}

func validSecretGeneration(generation string) bool {
	if len(generation) == 0 || len(generation) > 64 {
		return false
	}
	for _, r := range generation {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func checkStageDirectory(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("not a directory")
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return errors.New("not a directory")
	}
	// Владелец корня named volume зависит от Docker host/driver; доверие здесь
	// задаётся тем, что helper получает только один выделенный volume. Запрещаем
	// групповой/общий write, но не требуем host UID == container UID.
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o022 != 0 {
		return errors.New("unsafe directory")
	}
	return nil
}

func verifyStagingMount(path string, requireReadOnly bool) error {
	contents, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return errors.New("mount information unavailable")
	}
	for _, line := range strings.Split(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || unescapeMountField(fields[4]) != path {
			continue
		}
		readOnly := strings.Contains(","+fields[5]+",", ",ro,")
		if readOnly == requireReadOnly {
			return nil
		}
		return errors.New("mount has unexpected access mode")
	}
	return errors.New("path is not a mountpoint")
}

func unescapeMountField(field string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(field)
}

func readStagingSource(sourceDir, name string) ([]byte, error) {
	dirFD, err := openStageSourceDirectory(sourceDir)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dirFD)
	return readStagingSourceAt(dirFD, name)
}

func openStageSourceDirectory(path string) (int, error) {
	abs, err := filepath.Abs(path)
	if err != nil || !filepath.IsAbs(abs) {
		return -1, errors.New("invalid source directory")
	}
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, err
	}
	components := strings.Split(strings.Trim(abs, string(filepath.Separator)), string(filepath.Separator))
	for i, component := range components {
		if component == "" || component == "." || component == ".." {
			_ = unix.Close(fd)
			return -1, errors.New("invalid source directory component")
		}
		next, err := unix.Openat(fd, component, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil {
			_ = unix.Close(fd)
			return -1, err
		}
		_ = unix.Close(fd)
		fd = next
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o022 != 0 {
			_ = unix.Close(fd)
			return -1, errors.New("unsafe source path")
		}
		if i < len(components)-1 && stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) {
			_ = unix.Close(fd)
			return -1, errors.New("untrusted source parent")
		}
	}
	return fd, nil
}

func readStagingSourceAt(dirFD int, name string) ([]byte, error) {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return nil, errors.New("invalid source name")
	}
	var parent unix.Stat_t
	if err := unix.Fstat(dirFD, &parent); err != nil || parent.Mode&unix.S_IFMT != unix.S_IFDIR || parent.Mode&0o022 != 0 {
		return nil, errors.New("unsafe source directory")
	}
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("invalid source")
	}
	defer file.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || !validStagingSourceStat(&stat, parent.Uid) {
		return nil, errors.New("unsafe source")
	}
	data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(data) == 0 || len(data) > 1<<20 {
		return nil, errors.New("invalid source size")
	}
	return data, nil
}

func validStagingSourceStat(stat *unix.Stat_t, owner uint32) bool {
	if stat == nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != owner {
		return false
	}
	mode := stat.Mode & 0o777
	return mode == 0o400 || mode == 0o600
}

func writeStagedSecret(dir, name string, data []byte) error {
	if name == "" || strings.ContainsAny(name, "/\\") {
		return errors.New("invalid staged name")
	}
	path := filepath.Join(dir, name)
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), "")
	if file == nil {
		_ = unix.Close(fd)
		return errors.New("invalid staged file")
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := unix.Fchown(fd, 65532, 65532); err != nil {
		return err
	}
	if err := unix.Fchmod(fd, 0o400); err != nil {
		return err
	}
	return file.Sync()
}

func readRequiredPassword(path string) (string, error) {
	value, err := readSecretFile(path)
	if err != nil || strings.TrimSpace(value) == "" {
		return "", errors.New("lead-gen: файл пароля отсутствует, пуст или имеет небезопасные права")
	}
	return strings.TrimSpace(value), nil
}

func roleSetForTarget(sets []migrate.RoleSet, target targetDefinition) (migrate.RoleSet, bool) {
	for _, spec := range sets {
		if spec.Schema == target.schema {
			// Реестр фиксирует owned relations независимо от параметров вызывающей стороны.
			spec.OwnedRelations = append([]string(nil), target.ownedRelations...)
			return spec, true
		}
	}
	return migrate.RoleSet{}, false
}

func lookupEnvValue(lookupEnv func(string) (string, bool), name string) string {
	value, _ := lookupEnv(name)
	return value
}

func verifyCandidateRoleBinding(manifest CandidateManifest, spec migrate.RoleSet) error {
	target := string(spec.Schema)
	registry, ok := manifest.Roles[target]
	if !ok || !candidateContainsTarget(manifest, target) || spec.OwnerRole != registry.Owner || spec.MigratorRole != registry.Migrator {
		return errors.New("lead-gen: DB role set does not match signed candidate registry")
	}
	key := target
	if target == "integration-platform" {
		key = "vector"
	}
	if registry.Runtime[key] != spec.RuntimeRole {
		return errors.New("lead-gen: DB runtime identity does not match signed candidate registry")
	}
	for _, additional := range spec.AdditionalRuntimes {
		if registry.Runtime["integration-platform"] != additional.Role {
			return errors.New("lead-gen: additional DB runtime identity does not match signed candidate registry")
		}
	}
	return nil
}

func candidateContainsTarget(manifest CandidateManifest, target string) bool {
	_, ok := dbTargets[target]
	_, policyOK := manifest.TargetPolicies[target]
	return manifest.Target == "intake" && ok && policyOK
}

func roleSetsWithJobPasswords(targetID, migratorPassword, runtimePassword, additionalRuntimePassword string) []migrate.RoleSet {
	var requestMigrator, requestRuntime, consoleMigrator, consoleRuntime, vectorMigrator, vectorRuntime, integrationRuntime string
	switch targetID {
	case "request-fulfillment":
		requestMigrator, requestRuntime = migratorPassword, runtimePassword
	case "consoleauth":
		consoleMigrator, consoleRuntime = migratorPassword, runtimePassword
	case "integration-platform":
		vectorMigrator, vectorRuntime, integrationRuntime = migratorPassword, runtimePassword, additionalRuntimePassword
	}
	return migrate.RequestIntakeRoleSets(requestMigrator, requestRuntime, consoleMigrator, consoleRuntime, vectorMigrator, vectorRuntime, integrationRuntime)
}

func roleSetsWithScopedJobPasswords(targetID string, scope migrate.RuntimeRoleScope, migratorPassword, runtimePassword, additionalRuntimePassword string) ([]migrate.RoleSet, error) {
	var requestMigrator, requestRuntime, consoleMigrator, consoleRuntime, vectorMigrator, vectorRuntime, integrationRuntime string
	switch targetID {
	case "request-fulfillment":
		requestMigrator, requestRuntime = migratorPassword, runtimePassword
	case "consoleauth":
		consoleMigrator, consoleRuntime = migratorPassword, runtimePassword
	case "integration-platform":
		vectorMigrator, vectorRuntime, integrationRuntime = migratorPassword, runtimePassword, additionalRuntimePassword
	default:
		return nil, errors.New("lead-gen: неизвестный generation DB target")
	}
	return migrate.RequestIntakeRoleSetsForGeneration(scope, requestMigrator, requestRuntime, consoleMigrator, consoleRuntime, vectorMigrator, vectorRuntime, integrationRuntime)
}

func roleSetsForDBJob(targetID string, scope migrate.RuntimeRoleScope, migratorPassword, runtimePassword, additionalRuntimePassword string) ([]migrate.RoleSet, error) {
	if scope.Environment == "" && scope.Deployment == "" && scope.Generation == "" {
		return roleSetsWithJobPasswords(targetID, migratorPassword, runtimePassword, additionalRuntimePassword), nil
	}
	return roleSetsWithScopedJobPasswords(targetID, scope, migratorPassword, runtimePassword, additionalRuntimePassword)
}

func connectDB(dsn string) (*pgxpool.Pool, func(), error) {
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return nil, nil, errors.New("lead-gen: не удалось создать PostgreSQL pool")
	}
	return pool, pool.Close, nil
}

func verifyRuntimeRole(ctx context.Context, pool *pgxpool.Pool, expected string) error {
	var actual string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&actual); err != nil || actual != expected {
		return errors.New("lead-gen: runtime credential не соответствует ожидаемой роли")
	}
	return nil
}
