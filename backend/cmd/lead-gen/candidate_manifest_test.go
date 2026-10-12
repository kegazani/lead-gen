package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/platform/migrate"
)

func testSignedCandidateFiles(t *testing.T) (manifestPath, publicKeyPath string, lookup func(string) (string, bool)) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signCandidateManifest(candidateFixture(t), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	manifestPath = filepath.Join(dir, "candidate.json")
	publicKeyPath = filepath.Join(dir, "trusted.pub")
	if err := os.WriteFile(manifestPath, signed, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicKeyPath, publicKey, 0o400); err != nil {
		t.Fatal(err)
	}
	return manifestPath, publicKeyPath, func(name string) (string, bool) {
		if name == "LEADGEN_RELEASE_TRUST_KEY_ID" {
			return "test-key", true
		}
		return "", false
	}
}

func candidateFixture(t *testing.T) CandidateManifest {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	const env, deployment, generation = "test", "deployment-1", "generation-2"
	roles := make(map[string]CandidateRoleSet, len(dbTargets))
	for dbTarget := range dbTargets {
		runtimeTarget := dbTarget
		runtimeName, err := migrate.RuntimeRoleName(migrate.RuntimeRoleScope{Environment: env, Deployment: deployment, Target: runtimeTarget, Generation: generation})
		if dbTarget == "integration-platform" {
			runtimeTarget = "integration-platform-full-runtime"
		}
		if dbTarget == "integration-platform" {
			vectorRole, err := migrate.RuntimeRoleName(migrate.RuntimeRoleScope{Environment: env, Deployment: deployment, Target: dbTarget, Generation: generation})
			if err != nil {
				t.Fatal(err)
			}
			runtimeName = vectorRole
		}
		if err != nil {
			t.Fatal(err)
		}
		runtime := map[string]string{dbTarget: runtimeName}
		if dbTarget == "integration-platform" {
			additionalRole, err := migrate.RuntimeRoleName(migrate.RuntimeRoleScope{Environment: env, Deployment: deployment, Target: runtimeTarget, Generation: generation})
			if err != nil {
				t.Fatal(err)
			}
			runtime = map[string]string{"vector": runtimeName, "integration-platform": additionalRole}
		}
		roles[dbTarget] = CandidateRoleSet{Owner: "leadgen_" + strings.ReplaceAll(dbTarget, "-", "_") + "_owner", Migrator: "leadgen_" + strings.ReplaceAll(dbTarget, "-", "_") + "_migrator", Runtime: runtime}
	}
	policies := make(map[string]CandidateTargetPolicy, len(dbTargets))
	for target, approved := range additiveReleaseMigrations {
		version := "g1-additive-v1"
		if target == "request-fulfillment" {
			approved = g15RequestMigrations
			version = g15MigrationPolicyVersion
		}
		migrations := make([]CandidateMigration, 0, len(approved))
		for _, item := range approved {
			migrations = append(migrations, CandidateMigration{Name: item.Name, SHA256: item.SHA256})
		}
		policies[target] = CandidateTargetPolicy{PolicyVersion: version, Migrations: migrations}
	}
	return CandidateManifest{
		SchemaVersion: 2, Purpose: candidateManifestPurpose, Environment: env, DeploymentID: deployment,
		DaemonID: "sha256:" + strings.Repeat("a", 64), LedgerID: "ledger-test", Target: "intake",
		Generation: generation, ReleaseNonce: "nonce-2", Sequence: 2, BaseActive: "generation-1",
		Roles: roles, TargetPolicies: policies,
		ImageDigest: "sha256:" + strings.Repeat("b", 64), RuntimeImageDigest: "sha256:" + strings.Repeat("c", 64),
		ExecutionProtocol: "v1", SourceBundle: "request-provision", DestinationBundle: "request-migrate",
		BundleNames: []string{"consoleauth-migrate", "consoleauth-provision", "intake", "integration-platform-migrate", "integration-platform-provision", "request-migrate", "request-provision"},
		IssuedAt:    now.Format(time.RFC3339), ExpiresAt: now.Add(5 * time.Minute).Format(time.RFC3339), KeyID: "test-key",
	}
}

func TestCandidateManifestSignatureIsCanonicalAndStrict(t *testing.T) {
	manifest := candidateFixture(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signCandidateManifest(manifest, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifyCandidateManifest(signed, publicKey, "test-key", time.Now().UTC())
	if err != nil || verified.Generation != manifest.Generation {
		t.Fatalf("valid manifest rejected: generation=%q err=%v", verified.Generation, err)
	}

	var envelope signedCandidateManifest
	if err := json.Unmarshal(signed, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Payload = []byte(strings.Replace(string(envelope.Payload), `"target":"intake"`, `"target":"consoleauth"`, 1))
	tampered, _ := json.Marshal(envelope)
	if _, err := verifyCandidateManifest(tampered, publicKey, "test-key", time.Now().UTC()); err == nil {
		t.Fatal("tampered candidate manifest accepted")
	}
	if _, err := verifyCandidateManifest(signed, publicKey, "other-key", time.Now().UTC()); err == nil {
		t.Fatal("untrusted key id accepted")
	}
	replacedKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyCandidateManifest(signed, replacedKey, "test-key", time.Now().UTC()); err == nil {
		t.Fatal("candidate accepted after replacing pinned public key")
	}
	if _, err := verifyCandidateManifest([]byte(`{"payload":{},"signature":""}`), publicKey, "test-key", time.Now().UTC()); err == nil {
		t.Fatal("unsigned candidate manifest accepted")
	}
	expired := manifest
	expired.IssuedAt = time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	expired.ExpiresAt = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	expiredSigned, err := signCandidateManifest(expired, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyCandidateManifest(expiredSigned, publicKey, "test-key", time.Now().UTC()); err == nil {
		t.Fatal("expired candidate manifest accepted")
	}
	scope := migrate.RuntimeRoleScope{Environment: manifest.Environment, Deployment: manifest.DeploymentID,
		Target: "request-fulfillment", Generation: manifest.Generation}
	sets, err := roleSetsWithScopedJobPasswords("request-fulfillment", scope, "", "", "")
	if err != nil || !verifyCandidateRoleSets(verified, sets) {
		t.Fatalf("verified manifest did not derive its signed generation RoleSet: err=%v", err)
	}
	sets[0].RuntimeRole = "unsigned-static-role"
	if verifyCandidateRoleSets(verified, sets) {
		t.Fatal("runtime role set mismatch was accepted")
	}
}

func TestCandidateManifestRejectsNonCanonicalOrUnboundedFields(t *testing.T) {
	manifest := candidateFixture(t)
	manifest.BundleNames = []string{"../runtime-password"}
	if _, err := canonicalCandidateManifest(manifest); err == nil {
		t.Fatal("path-like bundle name accepted")
	}
	manifest = candidateFixture(t)
	manifest.ExecutionProtocol = "v2"
	if _, err := canonicalCandidateManifest(manifest); err == nil {
		t.Fatal("v2 activated before bridge gate")
	}
	manifest = candidateFixture(t)
	policy := manifest.TargetPolicies["request-fulfillment"]
	policy.Migrations[0].SHA256 = strings.Repeat("0", 64)
	manifest.TargetPolicies["request-fulfillment"] = policy
	if _, err := canonicalCandidateManifest(manifest); err == nil {
		t.Fatal("migration hash differing from exact policy accepted")
	}
	manifest = candidateFixture(t)
	policy = manifest.TargetPolicies["request-fulfillment"]
	policy.PolicyVersion = "g1-5-v1"
	manifest.TargetPolicies["request-fulfillment"] = policy
	if _, err := canonicalCandidateManifest(manifest); err == nil {
		t.Fatal("obsolete G1.5 policy version accepted")
	}
}

func TestCandidateManifestRequiresCompleteThreeTargetIntakePolicy(t *testing.T) {
	manifest := candidateFixture(t)
	if len(manifest.Roles) != 3 || len(manifest.TargetPolicies) != 3 {
		t.Fatalf("signed intake candidate must bind exactly three database targets: roles=%d policies=%d", len(manifest.Roles), len(manifest.TargetPolicies))
	}
	for _, target := range []string{"request-fulfillment", "consoleauth", "integration-platform"} {
		if _, ok := manifest.Roles[target]; !ok {
			t.Fatalf("candidate role registry omits %s", target)
		}
		if _, ok := manifest.TargetPolicies[target]; !ok {
			t.Fatalf("candidate migration policy omits %s", target)
		}
	}
	delete(manifest.TargetPolicies, "consoleauth")
	if _, err := canonicalCandidateManifest(manifest); err == nil {
		t.Fatal("partial two-target candidate manifest accepted")
	}
	manifest = candidateFixture(t)
	manifest.TargetPolicies["integration-platform"].Migrations[0].SHA256 = strings.Repeat("0", 64)
	if _, err := canonicalCandidateManifest(manifest); err == nil {
		t.Fatal("tampered vector-only migration policy accepted")
	}
}

func TestCandidateManifestLedgerBindingRejectsScopeReplayAndWrongHolder(t *testing.T) {
	manifest := candidateFixture(t)
	signed := []byte("signed-candidate-envelope")
	digest := sha256.Sum256(signed)
	identity := releaseLedgerIdentity{Environment: manifest.Environment, Deployment: manifest.DeploymentID,
		DaemonID: manifest.DaemonID, LedgerID: manifest.LedgerID}
	ledger := releaseLedger{
		Version: 1, Identity: identity, Sequence: manifest.Sequence, ActiveGeneration: manifest.BaseActive,
		Releases: map[string]releaseRecord{manifest.ReleaseNonce: {
			Generation: manifest.Generation, Nonce: manifest.ReleaseNonce, Sequence: manifest.Sequence,
			BaseActive: manifest.BaseActive, State: "staging", Target: manifest.Target, ImageDigest: manifest.ImageDigest,
			CandidateManifestName:   "candidate-" + manifest.ReleaseNonce + ".json",
			CandidateManifestSHA256: "sha256:" + hex.EncodeToString(digest[:]),
		}},
	}
	owner := releaseOwner{ReleaseNonce: manifest.ReleaseNonce, Identity: identity, PID: 123}
	if err := verifyCandidateLedgerRecord(manifest, signed, ledger, owner, false); err != nil {
		t.Fatalf("matching ledger/holder rejected: %v", err)
	}
	wrong := ledger
	wrong.Sequence++
	if err := verifyCandidateLedgerRecord(manifest, signed, wrong, owner, false); err == nil {
		t.Fatal("different ledger sequence accepted")
	}
	if err := verifyCandidateLedgerRecord(manifest, signed, ledger, releaseOwner{Identity: identity, PID: 123}, false); err == nil {
		t.Fatal("wrong holder nonce accepted")
	}
	if err := verifyCandidateLedgerRecord(manifest, append(signed, 'x'), ledger, owner, false); err == nil {
		t.Fatal("manifest replay with changed envelope bytes accepted")
	}
}

func TestActiveCandidateLedgerBindingUsesActivatedGenerationAndAllowsLaterFailedSequence(t *testing.T) {
	manifest := candidateFixture(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signCandidateManifest(manifest, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(signed)
	identity := releaseLedgerIdentity{
		Environment: manifest.Environment, Deployment: manifest.DeploymentID,
		DaemonID: manifest.DaemonID, LedgerID: manifest.LedgerID,
	}
	ledger := releaseLedger{
		Version: 1, Identity: identity, Revision: 9,
		Sequence:         manifest.Sequence + 1, // более поздняя незавершённая попытка не меняет active generation
		ActiveGeneration: manifest.Generation,
		Releases: map[string]releaseRecord{manifest.ReleaseNonce: {
			Generation: manifest.Generation, Nonce: manifest.ReleaseNonce,
			Sequence: manifest.Sequence, BaseActive: manifest.BaseActive,
			State: "activated", Target: "intake", ImageDigest: manifest.ImageDigest,
			CandidateManifestName:   "candidate-" + manifest.ReleaseNonce + ".json",
			CandidateManifestSHA256: "sha256:" + hex.EncodeToString(digest[:]),
		}},
	}
	if _, err := verifyCandidateManifest(signed, publicKey, manifest.KeyID, time.Now().UTC()); err != nil {
		t.Fatalf("valid signed active manifest rejected: %v", err)
	}
	if err := verifyCandidateLedgerRecord(manifest, signed, ledger, releaseOwner{}, true); err != nil {
		t.Fatalf("active ledger binding rejected: %v", err)
	}

	for name, mutate := range map[string]func(*releaseLedger){
		"inactive generation": func(value *releaseLedger) { value.ActiveGeneration = manifest.BaseActive },
		"unactivated record": func(value *releaseLedger) {
			record := value.Releases[manifest.ReleaseNonce]
			record.State = "verified"
			value.Releases[manifest.ReleaseNonce] = record
		},
		"replayed sequence": func(value *releaseLedger) { value.Sequence = manifest.Sequence - 1 },
		"different generation": func(value *releaseLedger) {
			value.ActiveGeneration = "another-generation"
		},
	} {
		t.Run(name, func(t *testing.T) {
			invalid := ledger
			invalid.Releases = make(map[string]releaseRecord, len(ledger.Releases))
			for key, value := range ledger.Releases {
				invalid.Releases[key] = value
			}
			mutate(&invalid)
			if err := verifyCandidateLedgerRecord(manifest, signed, invalid, releaseOwner{}, true); err == nil {
				t.Fatal("invalid active ledger authorized manifest")
			}
		})
	}
}

func TestExpiredManifestIsAcceptedOnlyForLedgerBoundActiveRelease(t *testing.T) {
	manifest := candidateFixture(t)
	manifest.IssuedAt = time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	manifest.ExpiresAt = time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signCandidateManifest(manifest, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyCandidateManifest(signed, publicKey, manifest.KeyID, time.Now().UTC()); err == nil {
		t.Fatal("expired candidate accepted before activation")
	}
	verified, err := verifyCandidateManifestMode(signed, publicKey, manifest.KeyID, time.Now().UTC(), true)
	if err != nil {
		t.Fatalf("validly signed expired active manifest rejected before ledger binding: %v", err)
	}
	digest := sha256.Sum256(signed)
	identity := releaseLedgerIdentity{Environment: manifest.Environment, Deployment: manifest.DeploymentID,
		DaemonID: manifest.DaemonID, LedgerID: manifest.LedgerID}
	activeLedger := releaseLedger{
		Version: 1, Identity: identity, Sequence: manifest.Sequence + 1, ActiveGeneration: manifest.Generation,
		Releases: map[string]releaseRecord{manifest.ReleaseNonce: {
			Generation: manifest.Generation, Nonce: manifest.ReleaseNonce, Sequence: manifest.Sequence,
			BaseActive: manifest.BaseActive, State: "activated", Target: manifest.Target, ImageDigest: manifest.ImageDigest,
			CandidateManifestName:   "candidate-" + manifest.ReleaseNonce + ".json",
			CandidateManifestSHA256: "sha256:" + hex.EncodeToString(digest[:]),
		}},
	}
	if err := verifyCandidateLedgerRecord(verified, signed, activeLedger, releaseOwner{}, true); err != nil {
		t.Fatalf("active ledger binding did not authorize expired restart: %v", err)
	}
	pendingLedger := activeLedger
	pendingLedger.ActiveGeneration = manifest.BaseActive
	pendingRecord := pendingLedger.Releases[manifest.ReleaseNonce]
	pendingRecord.State = "activation_in_progress"
	pendingRecord.Proofs = map[string]proofRecord{"intake": {Consumed: true}}
	pendingLedger.Releases[manifest.ReleaseNonce] = pendingRecord
	if err := verifyCandidateLedgerRecord(verified, signed, pendingLedger, releaseOwner{}, true); err != nil {
		t.Fatalf("one-time consumed activation-in-progress ledger did not authorize expired recovery: %v", err)
	}
	pendingRecord.Proofs["intake"] = proofRecord{Consumed: false}
	pendingLedger.Releases[manifest.ReleaseNonce] = pendingRecord
	if err := verifyCandidateLedgerRecord(verified, signed, pendingLedger, releaseOwner{}, true); err == nil {
		t.Fatal("activation-in-progress ledger without a consumed proof accepted expired manifest")
	}
	inactiveLedger := activeLedger
	inactiveLedger.ActiveGeneration = manifest.BaseActive
	if err := verifyCandidateLedgerRecord(verified, signed, inactiveLedger, releaseOwner{}, true); err == nil {
		t.Fatal("expired manifest with inactive ledger generation accepted")
	}
	changedRecordLedger := activeLedger
	changedRecordLedger.Releases = map[string]releaseRecord{}
	if err := verifyCandidateLedgerRecord(verified, signed, changedRecordLedger, releaseOwner{}, true); err == nil {
		t.Fatal("expired manifest without its exact active ledger record accepted")
	}
	futureIssued := manifest
	futureIssued.IssuedAt = time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339)
	futureIssued.ExpiresAt = time.Now().UTC().Add(3 * time.Minute).Format(time.RFC3339)
	futureSigned, err := signCandidateManifest(futureIssued, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyCandidateManifestMode(futureSigned, publicKey, manifest.KeyID, time.Now().UTC(), true); err == nil {
		t.Fatal("future-issued active manifest accepted despite expiry exemption")
	}
	if _, err := verifyCandidateManifestMode(signed, publicKey, "other-key", time.Now().UTC(), true); err == nil {
		t.Fatal("expired active manifest bypassed trust key scope")
	}
}
