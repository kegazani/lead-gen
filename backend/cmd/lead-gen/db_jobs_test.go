package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestStagingBundlesAreClosedAndSourceReaderFailsClosed(t *testing.T) {
	if err := verifyConfiguredSecretGeneration("test-generation"); err != nil {
		t.Fatalf("generation ENV alone must not require a secret bundle: %v", err)
	}
	if err := verifyStagedGeneration(""); err != nil {
		t.Fatalf("empty generation is allowed when no staged files are configured: %v", err)
	}
	if err := verifyStagedGeneration("", ""); err != nil {
		t.Fatalf("empty file path is not a configured secret: %v", err)
	}
	if err := verifyStagedGeneration("", "/run/secrets/test-generation/request-dsn"); err == nil {
		t.Fatal("staged DSN path accepted without an expected generation")
	}
	if err := verifyConfiguredSecretGeneration("test-generation", "/run/secrets/test-generation/secret"); err == nil {
		t.Fatal("configured secret path without a staged marker accepted")
	}
	if err := stageSecretBundle("request-migrate", "/tmp/alternate-source", stagedSecretRoot, "generation-test"); err == nil {
		t.Fatal("arbitrary stage source path accepted")
	}
	if err := stageSecretBundle("request-migrate", stagingSourcePath, "/tmp/alternate-volume", "generation-test"); err == nil {
		t.Fatal("arbitrary stage target path accepted")
	}
	if _, ok := stagingBundles["intake"]; !ok {
		t.Fatal("intake bundle missing")
	}
	if _, ok := stagingBundles["arbitrary-path"]; ok {
		t.Fatal("arbitrary bundle unexpectedly allowed")
	}
	if !validSecretGeneration("rotation-20261011_01") || validSecretGeneration("../old") || validSecretGeneration("") {
		t.Fatal("generation validation is not fail-closed")
	}
	for name, stat := range map[string]unix.Stat_t{
		"owner-only":       {Mode: unix.S_IFREG | 0o400, Uid: 501},
		"owner-read-write": {Mode: unix.S_IFREG | 0o600, Uid: 501},
		"wrong-owner":      {Mode: unix.S_IFREG | 0o400, Uid: 502},
		"group-readable":   {Mode: unix.S_IFREG | 0o440, Uid: 501},
		"non-regular":      {Mode: unix.S_IFIFO | 0o400, Uid: 501},
	} {
		valid := validStagingSourceStat(&stat, 501)
		want := name == "owner-only" || name == "owner-read-write"
		if valid != want {
			t.Errorf("validStagingSourceStat(%s)=%v, want %v", name, valid, want)
		}
	}

	dir := t.TempDir()
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "intake-request-runtime-dsn")
	if err := os.WriteFile(path, []byte("test-secret"), 0o400); err != nil {
		t.Fatal(err)
	}
	if data, err := readStagingSource(dir, filepath.Base(path)); err != nil || string(data) != "test-secret" {
		t.Fatalf("valid source rejected: data=%q err=%v", data, err)
	}
	if err := os.Chmod(path, 0o440); err != nil {
		t.Fatal(err)
	}
	if _, err := readStagingSource(dir, filepath.Base(path)); err == nil {
		t.Fatal("group-readable source accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", path); err != nil {
		t.Fatal(err)
	}
	if _, err := readStagingSource(dir, filepath.Base(path)); err == nil {
		t.Fatal("symlink source accepted")
	}
}

func TestStageTrustAnchorPublishesAndIsIdempotent(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded := []byte(base64.RawStdEncoding.EncodeToString(publicKey) + "\n")
	source, volume := t.TempDir(), t.TempDir()
	source, volume = resolvedTestDir(t, source), resolvedTestDir(t, volume)
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "trusted-release.pub"), encoded, 0o400); err != nil {
		t.Fatal(err)
	}
	owner, group := uint32(os.Geteuid()), uint32(os.Getegid())
	if err := stageTrustAnchorForOwner(source, "trusted-release.pub", volume, "release-key-1", false, owner, group); err != nil {
		t.Fatalf("valid trust anchor rejected: %v", err)
	}
	active := filepath.Join(volume, "active")
	defer func() { _ = os.Chmod(active, 0o700) }()
	for name, want := range map[string][]byte{
		"trusted-release.pub": encoded,
		"key-id":              []byte("release-key-1\n"),
		"complete":            []byte("complete\n"),
	} {
		path := filepath.Join(active, name)
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(want) {
			t.Fatalf("published %s: err=%v", name, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if stat.Uid != owner || stat.Gid != group || info.Mode().Perm() != 0o400 {
			t.Fatalf("wrong metadata for %s: uid=%d gid=%d mode=%o", name, stat.Uid, stat.Gid, info.Mode().Perm())
		}
	}
	before, err := os.ReadFile(filepath.Join(active, "trusted-release.pub"))
	if err != nil {
		t.Fatal(err)
	}
	if err := stageTrustAnchorForOwner(source, "trusted-release.pub", volume, "release-key-1", false, owner, group); err != nil {
		t.Fatalf("same key must be idempotent: %v", err)
	}
	if err := stageTrustAnchorForOwner(source, "trusted-release.pub", volume, "release-key-2", false, owner, group); err == nil {
		t.Fatal("same public key with a different key ID accepted")
	}
	otherKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sourceKey := filepath.Join(source, "trusted-release.pub")
	if err := os.Chmod(sourceKey, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceKey, []byte(base64.RawStdEncoding.EncodeToString(otherKey)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sourceKey, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := stageTrustAnchorForOwner(source, "trusted-release.pub", volume, "release-key-1", false, owner, group); err == nil {
		t.Fatal("different public key accepted over existing active bundle")
	}
	after, err := os.ReadFile(filepath.Join(active, "trusted-release.pub"))
	if err != nil || !equalBytes(before, after) {
		t.Fatal("mismatching key ID modified the active trust bundle")
	}
}

func TestStageTrustAnchorRejectsUnsafeInputsAndPreservesActive(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	validKey := []byte(base64.RawStdEncoding.EncodeToString(publicKey) + "\n")
	testCases := []struct {
		name   string
		setup  func(string) error
		remove bool
	}{
		{name: "missing", remove: true},
		{name: "symlink", setup: func(path string) error { return os.Symlink("absent", path) }},
		{name: "nonregular", setup: func(path string) error { return unix.Mkfifo(path, 0o400) }},
		{name: "wrong permissions", setup: func(path string) error { return os.WriteFile(path, validKey, 0o444) }},
		{name: "bad encoding", setup: func(path string) error { return os.WriteFile(path, []byte("not a public key"), 0o400) }},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			source, volume := t.TempDir(), t.TempDir()
			source, volume = resolvedTestDir(t, source), resolvedTestDir(t, volume)
			if err := os.Chmod(source, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(source, "trusted-release.pub")
			if !tc.remove {
				if err := tc.setup(path); err != nil {
					t.Fatal(err)
				}
				if tc.name == "wrong permissions" {
					if err := os.Chmod(path, 0o444); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := stageTrustAnchorForOwner(source, "trusted-release.pub", volume, "release-key-1", false, uint32(os.Geteuid()), uint32(os.Getegid())); err == nil {
				t.Fatal("unsafe trust source accepted")
			}
			if _, err := os.Lstat(filepath.Join(volume, "active")); !os.IsNotExist(err) {
				t.Fatal("invalid source published an active trust bundle")
			}
		})
	}

	// An incomplete active directory is a fail-closed state and must not be
	// repaired or removed by a later valid staging attempt.
	source, volume := t.TempDir(), t.TempDir()
	source, volume = resolvedTestDir(t, source), resolvedTestDir(t, volume)
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "trusted-release.pub"), validKey, 0o400); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(volume, "active")
	if err := os.Mkdir(active, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(active, "sentinel"), []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stageTrustAnchorForOwner(source, "trusted-release.pub", volume, "release-key-1", false, uint32(os.Geteuid()), uint32(os.Getegid())); err == nil {
		t.Fatal("incomplete existing active bundle accepted")
	}
	if got, err := os.ReadFile(filepath.Join(active, "sentinel")); err != nil || string(got) != "unchanged" {
		t.Fatal("existing mismatched active bundle was modified")
	}
}

func resolvedTestDir(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestRuntimeStagedTrustKeyIDMustMatchConfiguredID(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("runtime trust bundle metadata test requires UID/GID 65532 ownership")
	}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"trusted-release.pub": []byte(base64.RawStdEncoding.EncodeToString(publicKey) + "\n"),
		"key-id":              []byte("actual-key\n"),
		"complete":            []byte("complete\n"),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o400); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(filepath.Join(dir, name), 65532, 65532); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(dir, 65532, 65532); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if _, err := readStagedTrustAnchorAt(dir, "configured-key", false); err == nil {
		t.Fatal("runtime accepted a staged key ID different from configured LEADGEN_RELEASE_TRUST_KEY_ID")
	}
	if _, err := readStagedTrustAnchorAt(dir, "actual-key", false); err != nil {
		t.Fatalf("matching staged trust key rejected: %v", err)
	}
}

func TestRuntimeTrustBundleRejectsUnexpectedMetadataAndContents(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("runtime trust bundle metadata test requires UID/GID 65532 ownership")
	}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes := []byte(base64.RawStdEncoding.EncodeToString(publicKey) + "\n")
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, dir string)
	}{
		{"extra file", func(t *testing.T, dir string) {
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "extra"), []byte("x"), 0o400); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory mode", func(t *testing.T, dir string) {
			if err := os.Chmod(dir, 0o550); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory owner", func(t *testing.T, dir string) {
			if err := os.Chown(dir, 0, 65532); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory gid", func(t *testing.T, dir string) {
			if err := os.Chown(dir, 65532, 0); err != nil {
				t.Fatal(err)
			}
		}},
		{"file mode", func(t *testing.T, dir string) {
			if err := os.Chmod(filepath.Join(dir, "complete"), 0o440); err != nil {
				t.Fatal(err)
			}
		}},
		{"file owner", func(t *testing.T, dir string) {
			if err := os.Chown(filepath.Join(dir, "complete"), 0, 65532); err != nil {
				t.Fatal(err)
			}
		}},
		{"file gid", func(t *testing.T, dir string) {
			if err := os.Chown(filepath.Join(dir, "complete"), 65532, 0); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink", func(t *testing.T, dir string) {
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(dir, "complete")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("key-id", filepath.Join(dir, "complete")); err != nil {
				t.Fatal(err)
			}
		}},
		{"tampered marker", func(t *testing.T, dir string) {
			path := filepath.Join(dir, "complete")
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("tampered\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(path, 65532, 65532); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o400); err != nil {
				t.Fatal(err)
			}
		}},
		{"tampered key encoding", func(t *testing.T, dir string) {
			path := filepath.Join(dir, "trusted-release.pub")
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("not-a-key\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(path, 65532, 65532); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, 0o400); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range map[string][]byte{"trusted-release.pub": keyBytes, "key-id": []byte("runtime-key\n"), "complete": []byte("complete\n")} {
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, data, 0o400); err != nil {
					t.Fatal(err)
				}
				if err := os.Chown(path, 65532, 65532); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Chown(dir, 65532, 65532); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, dir)
			if _, err := readStagedTrustAnchorAt(dir, "runtime-key", false); err == nil {
				t.Fatal("unsafe or tampered trust bundle accepted")
			}
		})
	}
}

func TestStageSecretBundlePublishesCompleteGeneration(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("fchown production staging проверяется в disposable root container")
	}
	source := t.TempDir()
	volume := t.TempDir()
	for _, item := range stagingBundles["request-migrate"] {
		if err := os.WriteFile(filepath.Join(source, item.source), []byte("test-secret"), 0o400); err != nil {
			t.Fatal(err)
		}
	}
	if err := stageSecretBundleAt("request-migrate", source, volume, "generation-test", false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"migration-dsn", "generation"} {
		info, err := os.Stat(filepath.Join(volume, "generation-test", name))
		if err != nil {
			t.Fatalf("published %s: %v", name, err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		if stat.Uid != 65532 || stat.Gid != 65532 || info.Mode().Perm() != 0o400 {
			t.Fatalf("wrong staged metadata for %s: uid=%d gid=%d mode=%o", name, stat.Uid, stat.Gid, info.Mode().Perm())
		}
	}
	before, err := os.ReadFile(filepath.Join(volume, "generation-test", "migration-dsn"))
	if err != nil {
		t.Fatal(err)
	}
	if err := stageSecretBundleAt("request-migrate", source, volume, "generation-test", false); err == nil {
		t.Fatal("existing generation overwritten")
	}
	after, err := os.ReadFile(filepath.Join(volume, "generation-test", "migration-dsn"))
	if err != nil || string(after) != string(before) {
		t.Fatal("failed stage modified previously published generation")
	}
}

func TestVerifyStagedBundleRequiresExactAllowlistAndGeneration(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("staged bundle ownership test requires disposable root helper")
	}
	source, volume := t.TempDir(), t.TempDir()
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, item := range stagingBundles["request-migrate"] {
		if err := os.WriteFile(filepath.Join(source, item.source), []byte("synthetic-test-value"), 0o400); err != nil {
			t.Fatal(err)
		}
	}
	if err := stageSecretBundleAt("request-migrate", source, volume, "verify-generation", false); err != nil {
		t.Fatal(err)
	}
	if err := verifyStagedSecretBundle("request-migrate", volume, "verify-generation"); err == nil {
		t.Fatal("production verifier принял альтернативный путь")
	}
	// Root читает реальные staged bytes через descriptor и проверяет metadata
	// runtime-владельца. Production readSecretFile продолжает требовать свой euid.
	readFixture := func(path string) (string, error) {
		fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			return "", err
		}
		file := os.NewFile(uintptr(fd), path)
		defer file.Close()
		info, err := file.Stat()
		if err != nil {
			return "", err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0o400 || stat.Uid != 65532 || stat.Gid != 65532 || stat.Nlink != 1 || info.Size() <= 0 || info.Size() > 1<<20 {
			return "", errors.New("fixture staged-файла имеет неверные метаданные")
		}
		data, err := io.ReadAll(file)
		return string(data), err
	}
	if err := verifyStagedSecretBundleAt("request-migrate", volume, "verify-generation", readFixture); err != nil {
		t.Fatalf("valid bundle rejected: %v", err)
	}
	if err := verifyStagedSecretBundleAt("request-migrate", volume, "stale-generation", readFixture); err == nil {
		t.Fatal("stale generation accepted")
	}
	if err := os.WriteFile(filepath.Join(volume, "verify-generation", "unexpected"), []byte("x"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := verifyStagedSecretBundleAt("request-migrate", volume, "verify-generation", readFixture); err == nil {
		t.Fatal("bundle with an unallowlisted file accepted")
	}
}

// В тестах допускается root-owned sticky system temp, если платформа размещает
// t.TempDir под /tmp. В production этот wrapper не компилируется.
func readSecretFileWithHook(path string, afterOpen func()) (string, error) {
	return readSecretFileFromDescriptor(path, afterOpen, true)
}

func TestDBTargetRegistryIsClosedAndComplete(t *testing.T) {
	want := map[string]string{
		"request-fulfillment":  "request-fulfillment",
		"consoleauth":          "consoleauth",
		"integration-platform": "integration-platform",
	}
	if len(dbTargets) != len(want) {
		t.Fatalf("registry has %d targets; want exactly %d", len(dbTargets), len(want))
	}
	for id, schema := range want {
		target, ok := dbTargets[id]
		if !ok || target.schema.String() != schema || target.runtimeRole == "" || len(target.ownedRelations) == 0 || target.migrationsDir == "" {
			t.Errorf("registry target %q is incomplete: %#v", id, target)
		}
	}
	if _, ok := dbTargets["arbitrary"]; ok {
		t.Fatal("arbitrary target is allowlisted")
	}
}

func TestG15MigrationPolicyPinsAllTenFilesButKeepsReleaseDeferred(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	if len(g15RequestMigrations) != 10 {
		t.Fatalf("G1.5 exact catalog has %d migrations; want 10", len(g15RequestMigrations))
	}
	for i, migration := range g15RequestMigrations {
		if !validSHA256Hex(migration.SHA256) || (i > 0 && g15RequestMigrations[i-1].Name >= migration.Name) {
			t.Fatalf("G1.5 migration catalog is not exact/sorted at index %d: %+v", i, migration)
		}
	}
	if len(deferredG1RequestMigrations) != 7 {
		t.Fatal("G1-original must continue to defer migrations 0005-0011")
	}
	manifest, err := validateG15MigrationManifest(root)
	if err != nil || manifest.PolicyVersion != "g1.5-schema-0010" || len(manifest.Approved) != 10 {
		t.Fatalf("G1.5 exact catalog validation failed: manifest=%+v err=%v", manifest, err)
	}
	if len(manifest.DeferredG1) != 1 || manifest.DeferredG1[0] != "0011_research_execution_mode.sql" {
		t.Fatal("research migration must remain outside approved G1.5")
	}
	if _, err := validateReleaseMigrationManifest(root, "request-fulfillment"); err == nil {
		t.Fatal("G1-original accepted request migrations 0005-0010")
	}
}

func TestG15MigrationPolicyRejectsMissingAndUnknownMigrations(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	source := filepath.Join(root, dbTargets["request-fulfillment"].migrationsDir)
	for _, test := range []struct {
		name   string
		mutate func(string) error
	}{
		{name: "research-tampered", mutate: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "0011_research_execution_mode.sql"), []byte("-- schema: request-fulfillment\nSELECT 1;\n"), 0o600)
		}},
		{name: "missing", mutate: func(dir string) error { return os.Remove(filepath.Join(dir, "0010_verified_runtime_resolution.sql")) }},
		{name: "unknown", mutate: func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "0011_unknown.sql"), []byte("-- schema: request-fulfillment\nSELECT 1;\n"), 0o600)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tempRoot := t.TempDir()
			dir := filepath.Join(tempRoot, dbTargets["request-fulfillment"].migrationsDir)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(source)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
					continue
				}
				data, err := os.ReadFile(filepath.Join(source, entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := test.mutate(dir); err != nil {
				t.Fatal(err)
			}
			if _, err := validateG15MigrationManifest(tempRoot); err == nil {
				t.Fatalf("G1.5 accepted %s migration catalog", test.name)
			}
		})
	}
}

func TestValidateDBJobConfigRejectsUnknownAndMissingCredentials(t *testing.T) {
	env := func(string) (string, bool) { return "", false }
	migrationFile := testSecretFile(t, "migration-dsn")
	base := dbJobFlags{job: "migrate", target: "request-fulfillment", migrationDSNFile: migrationFile}
	if _, _, _, err := validateDBJobConfig(dbJobFlags{job: "migrate", target: "unknown", migrationDSNFile: "/secret/migration"}, env); err == nil {
		t.Fatal("unknown target accepted")
	}
	if _, _, _, err := validateDBJobConfig(dbJobFlags{job: "migrate", target: base.target}, env); err == nil {
		t.Fatal("missing migration credential accepted")
	}
	if _, _, _, err := validateDBJobConfig(dbJobFlags{target: base.target}, env); err == nil {
		t.Fatal("app mode accepted standalone DB target configuration")
	}
	if _, _, _, err := validateDBJobConfig(dbJobFlags{job: "provision", target: base.target, bootstrapDSNFile: testSecretFile(t, "bootstrap-dsn"), migratorPasswordFile: testSecretFile(t, "migrator"), runtimePasswordFile: testSecretFile(t, "runtime")}, env); err == nil {
		t.Fatal("unsigned provision job accepted")
	}
}

func TestSecretFilesRequirePrivateRegularReadableFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("secret-value"), 0o400); err != nil {
		t.Fatal(err)
	}
	value, err := readSecretFileWithHook(path, nil)
	if err != nil || value != "secret-value" {
		t.Fatalf("private secret file: value=%q err=%v", value, err)
	}
	for _, mode := range []uint32{0o000, 0o200, 0o440, 0o444, 0o600, 0o640, 0o644, 0o700} {
		if err := os.Chmod(path, os.FileMode(mode)); err != nil {
			t.Fatal(err)
		}
		if _, err := readSecretFileWithHook(path, nil); err == nil {
			t.Errorf("небезопасный mode %#o принят", mode)
		}
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecretFileWithHook(filepath.Join(filepath.Dir(path), "missing"), nil); err == nil {
		t.Fatal("missing secret accepted")
	}
	if _, err := readSecretFileWithHook(filepath.Dir(path), nil); err == nil {
		t.Fatal("directory accepted as a secret file")
	}
	if err := os.Chmod(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecretFileWithHook(path, nil); err == nil {
		t.Fatal("secret under a group/world-writable parent accepted")
	}
}

func TestSecretFileOwnerMustMatchRuntimeUID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("dummy"), 0o400); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	statForMode := func(mode os.FileMode, owner uint32) unix.Stat_t {
		t.Helper()
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		var info unix.Stat_t
		if err := unix.Fstat(int(file.Fd()), &info); err != nil {
			t.Fatal(err)
		}
		info.Uid = owner
		return info
	}

	for _, mode := range []uint32{0o644, 0o400} {
		info := statForMode(os.FileMode(mode), 0)
		if safeSecretFileStat(&info, 1000) {
			t.Errorf("root-owned mode %#o принят для runtime UID 1000", mode)
		}
	}
	for _, mode := range []uint32{0o440, 0o404, 0o444, 0o640, 0o644} {
		info := statForMode(os.FileMode(mode), 1000)
		if safeSecretFileStat(&info, 1000) {
			t.Errorf("runtime-owned mode %#o с group/other bits принят", mode)
		}
	}
	info := statForMode(0o400, 1000)
	if !safeSecretFileStat(&info, 1000) {
		t.Fatal("runtime-owned 0400 должен быть принят")
	}

	info = statForMode(0o400, uint32(os.Geteuid()))
	wrongUID := uint32(os.Geteuid()) + 1
	if safeSecretFileStat(&info, wrongUID) {
		t.Fatal("файл с несовпадающим owner UID принят")
	}
	if value, err := readSecretFileWithHook(path, nil); err != nil || value != "dummy" {
		t.Fatalf("owner UID + 0400: err=%v", err)
	}
}

// Эти тесты запускаются отдельно внутри собранного app image, чтобы проверить
// фактическую nonroot identity и production readSecretFile.
func TestContainerSecretFileAcceptance(t *testing.T) {
	t.Logf("runtime identity uid=%d gid=%d", os.Geteuid(), os.Getegid())
	path := os.Getenv("LEADGEN_SECRET_ACCEPTANCE_PATH")
	if path == "" {
		t.Skip("container acceptance only")
	}
	_, err := readSecretFile(path)
	wantReject := os.Getenv("LEADGEN_SECRET_ACCEPTANCE_REJECT") == "1"
	if wantReject && err == nil {
		t.Fatal("небезопасный файл принят")
	}
	if !wantReject && err != nil {
		t.Fatalf("защищённый файл не прочитан: %v", err)
	}
}

// Запускается только тестовым бинарником в отдельном временном контейнере root;
// production-путь чтения не получает обхода проверки UID/mode.
func TestContainerPrepareSecretFile(t *testing.T) {
	path := os.Getenv("LEADGEN_SECRET_ACCEPTANCE_PATH")
	if path == "" {
		t.Skip("container acceptance only")
	}
	uid, err := strconv.Atoi(os.Getenv("LEADGEN_SECRET_ACCEPTANCE_UID"))
	if err != nil || uid < 0 {
		t.Fatal("не задан runtime UID для тестового файла")
	}
	if err := os.Chown(filepath.Dir(path), uid, uid); err != nil {
		t.Fatal("не удалось задать owner каталога тестового файла")
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		t.Fatal("не удалось задать mode каталога тестового файла")
	}
	mode, err := strconv.ParseUint(os.Getenv("LEADGEN_SECRET_ACCEPTANCE_MODE"), 8, 32)
	if err != nil {
		t.Fatal("не задан mode тестового файла")
	}
	if os.Getenv("LEADGEN_SECRET_ACCEPTANCE_SYMLINK") == "1" {
		payloadPath := filepath.Join(filepath.Dir(path), "payload")
		_ = os.Remove(path)
		if err := os.WriteFile(payloadPath, []byte("disposable test payload"), 0o400); err != nil {
			t.Fatal("не удалось создать тестовый payload")
		}
		if err := os.Chown(payloadPath, uid, uid); err != nil {
			t.Fatal("не удалось задать owner payload")
		}
		if err := os.Symlink("payload", path); err != nil {
			t.Fatal("не удалось подготовить symlink")
		}
		return
	}
	_ = os.Remove(path)
	if err := os.WriteFile(path, []byte("disposable test payload"), os.FileMode(mode)); err != nil {
		t.Fatal("не удалось создать тестовый secret file")
	}
	if err := os.Chown(path, uid, uid); err != nil {
		t.Fatal("не удалось задать owner тестового файла")
	}
	if err := os.Chmod(path, os.FileMode(mode)); err != nil {
		t.Fatal("не удалось задать mode тестового файла")
	}
}

func TestSecretFileSymlinkIsRejectedWithoutLeakingDetails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "secret")
	const content = "must-not-leak-this-secret"
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	_, err := readSecretFileWithHook(link, nil)
	if err == nil || strings.Contains(err.Error(), content) || strings.Contains(err.Error(), link) {
		t.Fatalf("symlink rejection failed or leaked data/path: %v", err)
	}
}

func TestReplacingSecretPathAfterOpenDoesNotSwitchDescriptor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	oldPath := filepath.Join(dir, "opened-secret")
	const original = "opened-before-replacement"
	const replacement = "replacement-after-open"
	if err := os.WriteFile(path, []byte(original), 0o400); err != nil {
		t.Fatal(err)
	}
	got, err := readSecretFileWithHook(path, func() {
		if err := os.Rename(path, oldPath); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(replacement), 0o400); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil || got != original {
		t.Fatalf("reader switched from opened inode: got=%q err=%v", got, err)
	}
	got, err = readSecretFileWithHook(path, nil)
	if err != nil || got != replacement {
		t.Fatalf("replacement file was not safely readable on next open: got=%q err=%v", got, err)
	}
}

func TestConcurrentSecretPathReplacementDoesNotReadNewTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	decoy := filepath.Join(dir, "decoy")
	replacement := filepath.Join(dir, "replacement")
	backup := filepath.Join(dir, "opened-secret")
	const original = "opened-descriptor-value"
	const substituted = "must-not-be-read-from-replacement"
	if err := os.WriteFile(path, []byte(original), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decoy, []byte(substituted), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(decoy, replacement); err != nil {
		t.Fatal(err)
	}

	got, err := readSecretFileWithHook(path, func() {
		replaced := make(chan error, 1)
		go func() {
			if err := os.Rename(path, backup); err != nil {
				replaced <- err
				return
			}
			replaced <- os.Rename(replacement, path)
		}()
		if err := <-replaced; err != nil {
			t.Errorf("параллельная замена secret path: %v", err)
		}
	})
	if err != nil || got != original || strings.Contains(got, substituted) {
		t.Fatalf("reader прочитал подменённый target: value=%q err=%v", got, err)
	}
	if _, err := readSecretFileWithHook(path, nil); err == nil || strings.Contains(err.Error(), substituted) {
		t.Fatalf("symlink replacement не отклонён безопасно: %v", err)
	}
}

func TestDBJobConfigDoesNotExposeSecretsOrMixCredentialSources(t *testing.T) {
	const secret = "very-secret-dsn-value"
	path := testSecretFile(t, secret)
	env := func(name string) (string, bool) {
		if name == "LEADGEN_MIGRATION_DSN" {
			return secret, true
		}
		return "", false
	}
	flags := dbJobFlags{job: "migrate", target: "request-fulfillment", migrationDSNFile: path}
	_, _, _, err := validateDBJobConfig(flags, env)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("conflicting credential source error leaks value: %v", err)
	}
	_, _, _, err = validateDBJobConfig(dbJobFlags{job: "provision", target: "request-fulfillment", bootstrapDSNFile: path, migratorPasswordFile: path, runtimePasswordFile: path}, func(name string) (string, bool) {
		if name == "LEADGEN_MIGRATION_DSN" {
			return secret, true
		}
		return "", false
	})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("foreign job ENV credential accepted or leaked: %v", err)
	}
	for _, mode := range []dbJobFlags{
		{bootstrapDSNFile: path},
		{migrationDSNFile: path},
	} {
		_, _, _, err := validateDBJobConfig(mode, func(string) (string, bool) { return "", false })
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("application mode accepted admin file or leaked value: %v", err)
		}
	}
}

func TestEachDBJobAcceptsOnlyItsOwnDSNFile(t *testing.T) {
	noEnv := func(string) (string, bool) { return "", false }
	checks := []struct {
		name     string
		flags    dbJobFlags
		accepted bool
	}{
		{"provision-reject-unsigned", dbJobFlags{job: "provision", target: "request-fulfillment", bootstrapDSNFile: testSecretFile(t, "bootstrap"), migratorPasswordFile: testSecretFile(t, "m"), runtimePasswordFile: testSecretFile(t, "r")}, false},
		{"provision-reject-migration", dbJobFlags{job: "provision", target: "request-fulfillment", bootstrapDSNFile: testSecretFile(t, "bootstrap"), migrationDSNFile: testSecretFile(t, "migration"), migratorPasswordFile: testSecretFile(t, "m"), runtimePasswordFile: testSecretFile(t, "r")}, false},
		{"migrate-reject-unsigned", dbJobFlags{job: "migrate", target: "request-fulfillment", migrationDSNFile: testSecretFile(t, "migration")}, false},
		{"migrate-reject-bootstrap", dbJobFlags{job: "migrate", target: "request-fulfillment", migrationDSNFile: testSecretFile(t, "migration"), bootstrapDSNFile: testSecretFile(t, "bootstrap")}, false},
		{"verify-reject-unsigned", dbJobFlags{job: "verify", target: "request-fulfillment", runtimeDSNFile: testSecretFile(t, "runtime")}, false},
		{"verify-reject-migration", dbJobFlags{job: "verify", target: "request-fulfillment", runtimeDSNFile: testSecretFile(t, "runtime"), migrationDSNFile: testSecretFile(t, "migration")}, false},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			_, _, _, err := validateDBJobConfig(check.flags, noEnv)
			if (err == nil) != check.accepted {
				t.Fatalf("accepted=%v err=%v", check.accepted, err)
			}
		})
	}
}

func testSecretFile(t *testing.T, value string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal("user home unavailable for secure fixture")
	}
	dir, err := os.MkdirTemp(home, ".leadgen-secret-test-")
	if err != nil {
		t.Fatal("secure secret fixture directory unavailable")
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte(value), 0o400); err != nil {
		t.Fatal(err)
	}
	return path
}
