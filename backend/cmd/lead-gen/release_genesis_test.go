package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/platform/migrate"
)

func testObservedG1Enrollment(t *testing.T) ObservedG1EnrollmentV1 {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	hash := strings.Repeat("a", 64)
	return ObservedG1EnrollmentV1{
		SchemaVersion: 1, Purpose: observedG1Purpose, Kind: initialBaselineKind,
		Environment: "local", DeploymentID: "deployment-1", DockerEndpoint: "unix:///var/run/docker.sock",
		DockerEngineID: "engine-1", DaemonID: "sha256:" + hash, LedgerID: "ledger-1",
		ReleaseNonce: "nonce-genesis", Generation: "generation-g15", PlanSHA256: hash,
		ComposeProject: "leadgen", Intake: ObservedContainer{Service: "intake", ContainerID: "container-intake",
			ImageDigest: "sha256:" + strings.Repeat("b", 64), Networks: []string{"backend"}, Mounts: []string{"/run/secrets:ro", "/var/run/app:rw"}},
		OtherContainers: []ObservedContainer{{Service: "demand", ContainerID: "container-demand",
			ImageDigest: "sha256:" + strings.Repeat("c", 64), Networks: []string{"backend"}, Mounts: []string{"/data:rw"}}},
		GreenDatabase: ObservedGreenDB{ContainerID: "postgres-green", ImageDigest: "sha256:" + strings.Repeat("d", 64),
			VolumeName: "green-data-observed", DatabaseName: "leadgen", CatalogLogin: "leadgen_admin", PostgresMajor: 17, SystemID: "7420000000000000001"},
		Schemas: []ObservedSchema{{Name: "request-fulfillment", Versions: []string{"0001", "0002", "0003"},
			OwnerRoles: []string{"request_owner"}, LoginRoles: []string{"leadgen_admin"}, ACLRef: hash}},
		ObservedAt: now.Format(time.RFC3339), ExpiresAt: now.Add(10 * time.Minute).Format(time.RFC3339), KeyID: "release-key",
	}
}

func testControlIdentity() releaseLedgerIdentity {
	daemonID := "sha256:" + strings.Repeat("a", 64)
	return releaseLedgerIdentity{Environment: "local", Deployment: "deployment-1", DaemonID: daemonID,
		LedgerID: "lead-gen-release-local-deployment-1-" + strings.Repeat("a", 24)}
}

func TestObservedG1EnrollmentRequiresValidScopedSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enrollment := testObservedG1Enrollment(t)
	if _, err := canonicalObservedG1Enrollment(enrollment); err != nil {
		t.Fatalf("enrollment fixture invalid: %v", err)
	}
	signed, err := signObservedG1Enrollment(enrollment, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifyObservedG1Enrollment(signed, publicKey, enrollment.KeyID, time.Now().UTC(), enrollment)
	if err != nil || verified.GreenDatabase.CatalogLogin != "leadgen_admin" {
		t.Fatalf("valid observed enrollment rejected: err=%v", err)
	}
	wrongScope := enrollment
	wrongScope.DockerEngineID = "other-engine"
	if _, err := verifyObservedG1Enrollment(signed, publicKey, enrollment.KeyID, time.Now().UTC(), wrongScope); err == nil {
		t.Fatal("enrollment accepted under another Docker engine scope")
	}
	unsigned, _ := json.Marshal(enrollment)
	if _, err := verifyObservedG1Enrollment(unsigned, publicKey, enrollment.KeyID, time.Now().UTC(), enrollment); err == nil {
		t.Fatal("unsigned observed G1 inventory accepted")
	}
	stale := enrollment
	stale.ObservedAt = time.Now().UTC().Add(-20 * time.Minute).Format(time.RFC3339)
	stale.ExpiresAt = time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	staleSigned, err := signObservedG1Enrollment(stale, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyObservedG1Enrollment(staleSigned, publicKey, enrollment.KeyID, time.Now().UTC(), stale); err == nil {
		t.Fatal("stale observed G1 inventory accepted")
	}
}

func TestGenesisIsSignedOneTimeAndNeverCreatesG1ActiveManifest(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	enrollment := testObservedG1Enrollment(t)
	signed, err := signObservedG1Enrollment(enrollment, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	identity := releaseLedgerIdentity{Environment: enrollment.Environment, Deployment: enrollment.DeploymentID,
		DaemonID: enrollment.DaemonID, LedgerID: enrollment.LedgerID}
	now := time.Now().UTC()
	ledger, err := bootstrapGenesis(nil, identity, enrollment, signed, publicKey, enrollment.KeyID, enrollment.ReleaseNonce, enrollment.Generation, now)
	if err != nil {
		t.Fatalf("genesis failed: %v", err)
	}
	if err := validateGenesisLedger(ledger); err != nil {
		t.Fatalf("typed genesis ledger rejected: %v", err)
	}
	if ledger.Genesis.EnrollmentSHA != initialEnrollmentDigest(signed) || ledger.Releases[enrollment.ReleaseNonce].Base.Kind != initialBaselineKind {
		t.Fatal("genesis did not bind the exact signed G1 observation")
	}
	if _, err := bootstrapGenesis(&ledger, identity, enrollment, signed, publicKey, enrollment.KeyID, enrollment.ReleaseNonce, enrollment.Generation, now); err == nil {
		t.Fatal("second genesis accepted")
	}
	if _, err := bootstrapGenesis(nil, identity, enrollment, signed, publicKey, enrollment.KeyID, "other-nonce", enrollment.Generation, now); err == nil {
		t.Fatal("replayed/mismatched enrollment nonce accepted")
	}
	badSignature := append([]byte(nil), signed...)
	badSignature[len(badSignature)-3] ^= 1
	if _, err := bootstrapGenesis(nil, identity, enrollment, badSignature, publicKey, enrollment.KeyID, enrollment.ReleaseNonce, enrollment.Generation, now); err == nil {
		t.Fatal("forged enrollment accepted for genesis")
	}
	if _, err := verifyCandidateManifest(signed, publicKey, enrollment.KeyID, now); err == nil {
		t.Fatal("G1 enrollment proof was treated as an active CandidateManifest v2")
	}
	if _, err := genesisLedgerDigest(ledger); err != nil {
		t.Fatal(err)
	}
}

func TestInitialCandidateV3IsSeparateFromOrdinaryV2AndRequiresTypedG1Refs(t *testing.T) {
	manifest := initialCandidateFixture(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	signed, err := signInitialCandidateV3(manifest, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifyInitialCandidateV3(signed, publicKey, manifest.KeyID, time.Now().UTC())
	if err != nil || verified.ReleaseKind != initialReleaseKind {
		t.Fatalf("signed initial v3 rejected: %v", err)
	}
	if _, err := verifyCandidateManifest(signed, publicKey, manifest.KeyID, time.Now().UTC()); err == nil {
		t.Fatal("initial v3 was accepted by ordinary strict CandidateManifest v2 verifier")
	}
	manifest.Base.Kind = "active-g1-manifest"
	if _, err := canonicalInitialCandidateV3(manifest); err == nil {
		t.Fatal("fake signed active G1 manifest accepted as initial baseline")
	}
	manifest = initialCandidateFixture(t)
	manifest.EnrollmentSHA = strings.Repeat("f", 64)
	if _, err := canonicalInitialCandidateV3(manifest); err == nil {
		t.Fatal("candidate whose baseline digest differs from enrollment ref accepted")
	}
}

func initialCandidateFixture(t *testing.T) InitialCandidateManifestV3 {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	const env, deployment, generation = "local", "deployment-1", "generation-g15"
	roles := map[string]CandidateRoleSet{}
	policies := map[string]CandidateTargetPolicy{}
	for target := range dbTargets {
		role, err := migrate.RuntimeRoleName(migrate.RuntimeRoleScope{Environment: env, Deployment: deployment, Target: target, Generation: generation})
		if err != nil {
			t.Fatal(err)
		}
		runtime := map[string]string{target: role}
		if target == "integration-platform" {
			full, err := migrate.RuntimeRoleName(migrate.RuntimeRoleScope{Environment: env, Deployment: deployment, Target: "integration-platform-full-runtime", Generation: generation})
			if err != nil {
				t.Fatal(err)
			}
			runtime = map[string]string{"vector": role, "integration-platform": full}
		}
		roles[target] = CandidateRoleSet{Owner: "leadgen_" + strings.ReplaceAll(target, "-", "_") + "_owner",
			Migrator: "leadgen_" + strings.ReplaceAll(target, "-", "_") + "_migrator", Runtime: runtime}
		approved, version := additiveReleaseMigrations[target], "g1-additive-v1"
		if target == "request-fulfillment" {
			approved, version = g15RequestMigrations, g15MigrationPolicyVersion
		}
		migrations := make([]CandidateMigration, 0, len(approved))
		for _, item := range approved {
			migrations = append(migrations, CandidateMigration{Name: item.Name, SHA256: item.SHA256})
		}
		policies[target] = CandidateTargetPolicy{PolicyVersion: version, Migrations: migrations}
	}
	return InitialCandidateManifestV3{SchemaVersion: initialCandidateV3, Purpose: candidateManifestPurpose,
		ReleaseKind: initialReleaseKind, Environment: env, DeploymentID: deployment,
		DaemonID: "sha256:" + strings.Repeat("a", 64), LedgerID: "ledger-1", Target: "intake",
		Generation: generation, ReleaseNonce: "nonce-genesis", Sequence: 1,
		Base:       InitialG1Baseline{Kind: initialBaselineKind, EnrollmentSHA256: strings.Repeat("b", 64)},
		PlanSHA256: strings.Repeat("c", 64), EnrollmentSHA: strings.Repeat("b", 64), BackupSHA: strings.Repeat("d", 64),
		ImageDigest: "sha256:" + strings.Repeat("e", 64), RuntimeImage: "sha256:" + strings.Repeat("f", 64),
		Execution: "v1", Roles: roles, Policies: policies, IssuedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(5 * time.Minute).Format(time.RFC3339), KeyID: "release-key"}
}

func TestReleaseControlChallengeIsConsumedAtomicallyUnderConcurrency(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	scope := controlScope{Identity: testControlIdentity(), OwnerAttempt: "attempt-1", ReleaseNonce: "nonce-1", Epoch: "epoch-1"}
	challenge, _ := newReleaseChallenge()
	req := releaseControlRequest{Identity: scope.Identity, OwnerAttempt: scope.OwnerAttempt, ReleaseNonce: scope.ReleaseNonce,
		Epoch: scope.Epoch, ExpectedRevision: 4, Action: "db-mutation-begin", Challenge: challenge, Payload: json.RawMessage(`{"target":"request-fulfillment"}`)}
	if err := signControlRequest(key, &req); err != nil {
		t.Fatal(err)
	}
	registry := newChallengeRegistry()
	const callers = 16
	results := make(chan bool, callers)
	for range callers {
		go func() { results <- registry.verifyAndConsume(key, req, scope, 4, true) == nil }()
	}
	accepted := 0
	for range callers {
		if <-results {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("one-time challenge accepted %d times", accepted)
	}
}

func FuzzControlMACRejectsMutatedCanonicalRequest(f *testing.F) {
	f.Add("db-mutation-begin", `{"target":"request-fulfillment"}`)
	f.Fuzz(func(t *testing.T, action, payload string) {
		if len(action) > 64 || len(payload) > 1024 {
			t.Skip()
		}
		key := []byte(strings.Repeat("f", 32))
		scope := controlScope{Identity: testControlIdentity(), OwnerAttempt: "attempt-fuzz", ReleaseNonce: "nonce-fuzz", Epoch: "epoch-fuzz"}
		challenge, err := newReleaseChallenge()
		if err != nil {
			t.Fatal(err)
		}
		req := releaseControlRequest{Identity: scope.Identity, OwnerAttempt: scope.OwnerAttempt, ReleaseNonce: scope.ReleaseNonce,
			Epoch: scope.Epoch, ExpectedRevision: 3, Action: action, Challenge: challenge, Payload: json.RawMessage(payload)}
		if err := signControlRequest(key, &req); err != nil {
			return
		}
		if action != "" {
			req.Action += "-changed"
		} else {
			req.Action = "x"
		}
		if err := newChallengeRegistry().verifyAndConsume(key, req, scope, 3, true); err == nil {
			t.Fatal("mutated control request passed HMAC verification")
		}
	})
}

func TestGenesisMarkersAreMonotonic(t *testing.T) {
	old := genesisReleaseRecord{Generation: "g15", Nonce: "n1", ReleaseKind: initialReleaseKind,
		Base: InitialG1Baseline{Kind: initialBaselineKind, EnrollmentSHA256: strings.Repeat("a", 64)}, DBMutationStarted: true}
	next := old
	if err := validateGenesisMonotonic(old, next); err != nil {
		t.Fatal(err)
	}
	next.DBMutationStarted = false
	if err := validateGenesisMonotonic(old, next); err == nil {
		t.Fatal("db_mutation_started cleared")
	}
	next = old
	next.WriteEnableStarted = false
	old.WriteEnableStarted = true
	if err := validateGenesisMonotonic(old, next); err == nil {
		t.Fatal("write_enable_started cleared")
	}
}

func TestReleaseControlCapabilityBindsScopeRevisionActionPayloadAndChallenge(t *testing.T) {
	identity := testControlIdentity()
	scope := controlScope{Identity: identity, OwnerAttempt: "attempt-1", ReleaseNonce: "nonce-1", Epoch: "epoch-1"}
	key := []byte(strings.Repeat("k", 32))
	challenge, err := newReleaseChallenge()
	if err != nil {
		t.Fatal(err)
	}
	request := releaseControlRequest{Identity: identity, OwnerAttempt: scope.OwnerAttempt, ReleaseNonce: scope.ReleaseNonce,
		Epoch: scope.Epoch, ExpectedRevision: 7, Action: "db-mutation-begin", Challenge: challenge, Payload: json.RawMessage(`{"target":"request-fulfillment"}`)}
	if err := signControlRequest(key, &request); err != nil {
		t.Fatal(err)
	}
	registry := newChallengeRegistry()
	if err := registry.verifyAndConsume(key, request, scope, 7, true); err != nil {
		t.Fatalf("valid cross-namespace control capability refused: %v", err)
	}
	if err := registry.verifyAndConsume(key, request, scope, 7, true); err == nil {
		t.Fatal("replayed control challenge accepted")
	}
	for name, mutate := range map[string]func(*releaseControlRequest){
		"action":  func(r *releaseControlRequest) { r.Action = "write-enable" },
		"payload": func(r *releaseControlRequest) { r.Payload = json.RawMessage(`{"target":"consoleauth"}`) },
		"scope":   func(r *releaseControlRequest) { r.ReleaseNonce = "other-nonce" },
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := newReleaseChallenge()
			r := request
			r.Challenge, r.MAC = c, ""
			if err := signControlRequest(key, &r); err != nil {
				t.Fatal(err)
			}
			mutate(&r)
			if err := newChallengeRegistry().verifyAndConsume(key, r, scope, 7, true); err == nil {
				t.Fatal("modified request scope/action/payload accepted")
			}
		})
	}
	newChallenge, _ := newReleaseChallenge()
	wrongRevision := request
	wrongRevision.Challenge, wrongRevision.MAC = newChallenge, ""
	if err := signControlRequest(key, &wrongRevision); err != nil {
		t.Fatal(err)
	}
	if err := newChallengeRegistry().verifyAndConsume(key, wrongRevision, scope, 8, true); err == nil {
		t.Fatal("stale expected revision accepted")
	}
	if err := newChallengeRegistry().verifyAndConsume(nil, request, scope, 7, true); err == nil {
		t.Fatal("same UID caller without capability key accepted")
	}
	newChallenge, _ = newReleaseChallenge()
	lockLost := request
	lockLost.Challenge, lockLost.MAC = newChallenge, ""
	_ = signControlRequest(key, &lockLost)
	if err := newChallengeRegistry().verifyAndConsume(key, lockLost, scope, 7, false); err == nil {
		t.Fatal("holder that lost flock accepted control mutation")
	}
}

func TestSignedReadSnapshotBindsChallengeEpochAndRevision(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	scope := controlScope{Identity: testControlIdentity(), OwnerAttempt: "attempt-1", ReleaseNonce: "nonce-1", Epoch: "epoch-1"}
	challenge, _ := newReleaseChallenge()
	readRequest := releaseReadRequest{Identity: scope.Identity, OwnerAttempt: scope.OwnerAttempt,
		ReleaseNonce: scope.ReleaseNonce, Epoch: scope.Epoch, Challenge: challenge, MinimumRevision: 8}
	if err := validateReadRequest(readRequest, scope, 9); err != nil {
		t.Fatalf("typed read-only request rejected: %v", err)
	}
	if err := validateReadRequest(readRequest, scope, 7); err == nil {
		t.Fatal("read request accepted when requested revision is ahead of holder")
	}
	ledger := json.RawMessage(`{"revision":9,"active_generation":"g15"}`)
	now := time.Now().UTC()
	snapshot, err := signReadSnapshot(scope, challenge, 9, ledger, now.Add(30*time.Second), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := verifyReadSnapshot(snapshot, publicKey, scope, challenge, 9, now)
	if err != nil || !equalBytes(got, ledger) {
		t.Fatalf("signed snapshot rejected: %v", err)
	}
	otherChallenge, _ := newReleaseChallenge()
	if _, err := verifyReadSnapshot(snapshot, publicKey, scope, otherChallenge, 9, now); err == nil {
		t.Fatal("stale read challenge accepted")
	}
	wrongEpoch := scope
	wrongEpoch.Epoch = "old-epoch"
	if _, err := verifyReadSnapshot(snapshot, publicKey, wrongEpoch, challenge, 9, now); err == nil {
		t.Fatal("stale holder epoch accepted")
	}
	if _, err := verifyReadSnapshot(snapshot, publicKey, scope, challenge, 10, now); err == nil {
		t.Fatal("older ledger revision accepted")
	}
	bad := snapshot
	bad.Ledger = json.RawMessage(`{"revision":10}`)
	if _, err := verifyReadSnapshot(bad, publicKey, scope, challenge, 9, now); err == nil {
		t.Fatal("tampered live ledger snapshot accepted")
	}
}

func TestNewInitialReleaseCLIHooksFailClosedAndDoNotLeakCapability(t *testing.T) {
	for _, args := range [][]string{{"-release-sign-g1-enrollment"}, {"-release-sign-backup-evidence"}, {"-release-bootstrap-g1"}} {
		handled, err := releaseHelperMode(args)
		if !handled || err == nil || strings.Contains(err.Error(), "capability") {
			t.Fatalf("new initial G1 CLI hook is not fail-closed: handled=%v err=%v", handled, err)
		}
	}
}

func TestReleaseStateMutationRequiresHolderAuthority(t *testing.T) {
	// Без удостоверенного lock-holder callback даже корректно подписанный запрос
	// не получает право записи candidate/active artifact.
	key := []byte(strings.Repeat("k", 32))
	scope := controlScope{Identity: testControlIdentity(), OwnerAttempt: "attempt-1", ReleaseNonce: "nonce-1", Epoch: "epoch-1"}
	challenge, _ := newReleaseChallenge()
	req := releaseControlRequest{Identity: scope.Identity, OwnerAttempt: scope.OwnerAttempt, ReleaseNonce: scope.ReleaseNonce,
		Epoch: scope.Epoch, ExpectedRevision: 1, Action: "publish-candidate", Challenge: challenge, Payload: json.RawMessage(`{"digest":"sha256:abc"}`)}
	_ = signControlRequest(key, &req)
	if err := newChallengeRegistry().verifyAndConsume(key, req, scope, 1, false); err == nil {
		t.Fatal("candidate/active writer accepted without live holder lock")
	}
}

func TestEnrollmentDigestIsStableAndUsesSignedEnvelopeBytes(t *testing.T) {
	data := []byte(`{"payload":"observed"}`)
	want := sha256.Sum256(data)
	if got := initialEnrollmentDigest(data); got != hex.EncodeToString(want[:]) {
		t.Fatalf("unexpected signed enrollment digest %s", got)
	}
}
