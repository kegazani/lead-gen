package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"lead-gen/backend/platform/migrate"
)

func testReleasePayload() releasePayload {
	now := time.Now().UTC().Truncate(time.Second)
	return releasePayload{
		Version: 1, Purpose: "lead-gen-secret-release", DeploymentID: "deployment-1",
		Environment: "test", Target: "core", DaemonID: "sha256:" + strings.Repeat("d", 64),
		LedgerID: "lead-gen-release-test", Generation: "generation-2", ReleaseNonce: "nonce-2",
		Sequence: 2, BaseActive: "generation-1", BundleFiles: []string{"console-password", "runtime-dsn"},
		ImageDigest: "sha256:" + strings.Repeat("a", 64), IssuedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(time.Minute).Format(time.RFC3339), KeyID: "test-key-1",
	}
}

func TestReleasePayloadUsesDeterministicCanonicalJSONAndEd25519(t *testing.T) {
	payload := testReleasePayload()
	canonical, err := canonicalReleasePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := canonicalReleasePayload(payload)
	if err != nil || !equalBytes(canonical, repeated) {
		t.Fatalf("canonical representation changed: %v", err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privateKey, canonical)
	if !ed25519.Verify(publicKey, canonical, signature) {
		t.Fatal("fresh signature rejected")
	}
	amended := append([]byte(nil), canonical...)
	amended[len(amended)-2] ^= 1
	if ed25519.Verify(publicKey, amended, signature) {
		t.Fatal("tampered canonical payload accepted")
	}
	if got := base64.RawStdEncoding.EncodeToString(signature); got == "" {
		t.Fatal("signature encoding is empty")
	}
}

func TestReleasePayloadRejectsUnsortedAndUnsafeBundleNames(t *testing.T) {
	payload := testReleasePayload()
	payload.BundleFiles = []string{"runtime-dsn", "console-password"}
	if _, err := canonicalReleasePayload(payload); err == nil {
		t.Fatal("unsorted bundle file list accepted")
	}
	payload.BundleFiles = []string{"../private-key"}
	if _, err := canonicalReleasePayload(payload); err == nil {
		t.Fatal("path traversal bundle file accepted")
	}
}

func TestReleaseLedgerTransitionsAndReplayAreStrict(t *testing.T) {
	ledger := releaseLedger{
		Version:          1,
		Identity:         releaseLedgerIdentity{Environment: "test", Deployment: "deployment-1", DaemonID: "daemon", LedgerID: "ledger"},
		ActiveGeneration: "generation-1", Releases: map[string]releaseRecord{},
	}
	record := releaseRecord{Generation: "generation-2", Nonce: "nonce-2", Sequence: 1,
		BaseActive: "generation-1", State: "new", Target: "core", ImageDigest: "sha256:" + strings.Repeat("a", 64),
		Manifest: []string{"runtime-dsn"}}
	if err := applyLedgerTransition(&ledger, "new", "", "", record); err != nil {
		t.Fatal(err)
	}
	record.State = "staging"
	if err := applyLedgerTransition(&ledger, "staging", "", "", record); err != nil {
		t.Fatal(err)
	}
	record.State = "verified"
	record.Proofs = map[string]proofRecord{"core": {KeyID: "key-1", Digest: "sha256:proof"}}
	if err := applyLedgerTransition(&ledger, "verified", "", "", record); err != nil {
		t.Fatal(err)
	}
	if err := applyLedgerTransition(&ledger, "verified", "", "", record); err == nil {
		t.Fatal("duplicate transition accepted")
	}
	consumed := record
	consumed.State = "activation_in_progress"
	consumed.Proofs = map[string]proofRecord{"core": {KeyID: "key-1", Digest: "sha256:proof", Consumed: true}}
	if err := applyLedgerTransition(&ledger, "activation_in_progress", "core", "sha256:proof", consumed); err != nil {
		t.Fatal(err)
	}
	if err := applyLedgerTransition(&ledger, "activation_in_progress", "core", "sha256:proof", consumed); err == nil {
		t.Fatal("activation proof consumed twice")
	}
	activated := consumed
	activated.State = "activated"
	if err := applyLedgerTransition(&ledger, "activated", "", "", activated); err != nil {
		t.Fatal(err)
	}
	if ledger.ActiveGeneration != "generation-2" {
		t.Fatalf("active generation not committed: %q", ledger.ActiveGeneration)
	}
	duplicate := record
	duplicate.Nonce = "nonce-replay"
	duplicate.State = "new"
	if err := applyLedgerTransition(&ledger, "new", "", "", duplicate); err == nil {
		t.Fatal("generation replay accepted")
	}
	wrongBase := record
	wrongBase.Nonce = "nonce-3"
	wrongBase.Generation = "generation-3"
	wrongBase.State = "new"
	wrongBase.BaseActive = "generation-old"
	if err := applyLedgerTransition(&ledger, "new", "", "", wrongBase); err == nil {
		t.Fatal("stale active-generation compare-and-swap accepted")
	}
}

func TestReleasePayloadRejectsUnknownFields(t *testing.T) {
	payload := testReleasePayload()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data[:len(data)-1], []byte(`,"secret_hash":"forbidden"}`)...)
	var decoded releasePayload
	if err := strictJSON(data, &decoded); err == nil {
		t.Fatal("unknown secret/hash field accepted")
	}
}

func TestReleaseAttestationVerificationFailsClosedOnTamperExpiryRevocationAndDaemonMismatch(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	payload := testReleasePayload()
	payload.IssuedAt = now.Format(time.RFC3339)
	payload.ExpiresAt = now.Add(time.Minute).Format(time.RFC3339)
	canonical, err := canonicalReleasePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privateKey, canonical)
	envelope, err := json.Marshal(signedAttestation{Payload: canonical, Signature: base64.RawStdEncoding.EncodeToString(signature)})
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifyReleaseAttestation(envelope, publicKey, payload.KeyID, nil, now)
	if err != nil {
		t.Fatalf("valid attestation rejected: %v", err)
	}
	if err := validateReleaseExpectation(verified, releaseExpectation{
		DeploymentID: payload.DeploymentID, Environment: payload.Environment, Target: payload.Target,
		DaemonID: payload.DaemonID, LedgerID: payload.LedgerID, Generation: payload.Generation,
		ReleaseNonce: payload.ReleaseNonce, Sequence: payload.Sequence, BaseActive: payload.BaseActive,
		ImageDigest: payload.ImageDigest, BundleFiles: payload.BundleFiles,
	}); err != nil {
		t.Fatalf("matching expected identity rejected: %v", err)
	}
	wrongGeneration := releaseExpectation{
		DeploymentID: payload.DeploymentID, Environment: payload.Environment, Target: payload.Target,
		DaemonID: payload.DaemonID, LedgerID: payload.LedgerID, Generation: "stale-generation",
		ReleaseNonce: payload.ReleaseNonce, Sequence: payload.Sequence, BaseActive: payload.BaseActive,
		ImageDigest: payload.ImageDigest, BundleFiles: payload.BundleFiles,
	}
	if err := validateReleaseExpectation(verified, wrongGeneration); err == nil {
		t.Fatal("stale generation accepted by activation verifier")
	}
	wrongDaemon := releaseExpectation{
		DeploymentID: payload.DeploymentID, Environment: payload.Environment, Target: payload.Target,
		DaemonID: "sha256:" + strings.Repeat("e", 64), LedgerID: payload.LedgerID, Generation: payload.Generation,
		ReleaseNonce: payload.ReleaseNonce, Sequence: payload.Sequence, BaseActive: payload.BaseActive,
		ImageDigest: payload.ImageDigest, BundleFiles: payload.BundleFiles,
	}
	if err := validateReleaseExpectation(verified, wrongDaemon); err == nil {
		t.Fatal("daemon identity mismatch accepted")
	}
	if _, err := verifyReleaseAttestation(envelope, publicKey, payload.KeyID, []string{payload.KeyID}, now); err == nil {
		t.Fatal("revoked key accepted")
	}
	if _, err := verifyReleaseAttestation(envelope, publicKey, "another-key", nil, now); err == nil {
		t.Fatal("unknown trust key id accepted")
	}
	if _, err := verifyReleaseAttestation(envelope, publicKey, payload.KeyID, nil, now.Add(2*time.Minute)); err == nil {
		t.Fatal("expired attestation accepted")
	}
	issued := now.Add(-2 * time.Hour)
	expiredPayload := testReleasePayload()
	expiredPayload.IssuedAt = issued.Format(time.RFC3339)
	expiredPayload.ExpiresAt = issued.Add(time.Minute).Format(time.RFC3339)
	expiredCanonical, err := canonicalReleasePayload(expiredPayload)
	if err != nil {
		t.Fatal(err)
	}
	expiredEnvelope, err := json.Marshal(signedAttestation{
		Payload: expiredCanonical, Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(privateKey, expiredCanonical)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyReleaseAttestation(expiredEnvelope, publicKey, expiredPayload.KeyID, nil, now); err == nil {
		t.Fatal("expired attestation started a new activation")
	}
	if _, err := verifyReleaseAttestationMode(expiredEnvelope, publicKey, expiredPayload.KeyID, nil, now, true); err != nil {
		t.Fatalf("expired attestation could not be verified for an already consumed activation proof: %v", err)
	}
	futurePayload := expiredPayload
	futurePayload.IssuedAt = now.Add(time.Hour).Format(time.RFC3339)
	futurePayload.ExpiresAt = now.Add(2 * time.Hour).Format(time.RFC3339)
	futureCanonical, err := canonicalReleasePayload(futurePayload)
	if err != nil {
		t.Fatal(err)
	}
	futureEnvelope, err := json.Marshal(signedAttestation{
		Payload: futureCanonical, Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(privateKey, futureCanonical)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyReleaseAttestationMode(futureEnvelope, publicKey, futurePayload.KeyID, nil, now, true); err == nil {
		t.Fatal("future-issued attestation accepted for activation recovery")
	}
	replacedKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyReleaseAttestation(envelope, replacedKey, payload.KeyID, nil, now); err == nil {
		t.Fatal("attestation accepted after replacing independently pinned trust key")
	}

	var parsed signedAttestation
	if err := json.Unmarshal(envelope, &parsed); err != nil {
		t.Fatal(err)
	}
	payload.DaemonID = "sha256:" + strings.Repeat("f", 64)
	tampered, err := canonicalReleasePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Payload = tampered
	tamperedEnvelope, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyReleaseAttestation(tamperedEnvelope, publicKey, payload.KeyID, nil, now); err == nil {
		t.Fatal("tampered daemon identity accepted")
	}
}

func TestExpiredPreviousActivationManifestRequiresExactActiveLedgerRecord(t *testing.T) {
	manifest := candidateFixture(t)
	issued := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	manifest.IssuedAt = issued.Format(time.RFC3339)
	manifest.ExpiresAt = issued.Add(time.Minute).Format(time.RFC3339)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := signCandidateManifest(manifest, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(signed)
	ledger := releaseLedger{
		Version: 1,
		Identity: releaseLedgerIdentity{
			Environment: manifest.Environment, Deployment: manifest.DeploymentID,
			DaemonID: manifest.DaemonID, LedgerID: manifest.LedgerID,
		},
		Sequence: manifest.Sequence, ActiveGeneration: manifest.Generation,
		Releases: map[string]releaseRecord{manifest.ReleaseNonce: {
			Generation: manifest.Generation, Nonce: manifest.ReleaseNonce, Sequence: manifest.Sequence,
			BaseActive: manifest.BaseActive, State: "activated", Target: manifest.Target,
			ImageDigest: manifest.ImageDigest, CandidateManifestName: "candidate-" + manifest.ReleaseNonce + ".json",
			CandidateManifestSHA256: "sha256:" + hex.EncodeToString(digest[:]),
		}},
	}
	if err := verifyPreviousActivationManifest(signed, manifest.Generation, publicKey, manifest.KeyID, ledger); err != nil {
		t.Fatalf("expired previous active manifest with exact ledger record rejected: %v", err)
	}
	ledger.ActiveGeneration = manifest.BaseActive
	if err := verifyPreviousActivationManifest(signed, manifest.Generation, publicKey, manifest.KeyID, ledger); err == nil {
		t.Fatal("expired previous manifest accepted when ledger active generation differs")
	}
	ledger.ActiveGeneration = manifest.Generation
	ledger.Releases[manifest.ReleaseNonce] = releaseRecord{Generation: manifest.Generation, State: "failed"}
	if err := verifyPreviousActivationManifest(signed, manifest.Generation, publicKey, manifest.KeyID, ledger); err == nil {
		t.Fatal("expired previous manifest accepted without exact activated record")
	}
}

func TestIntakeAttestationRequiresAllGenerationScopedRuntimeRoles(t *testing.T) {
	payload := testReleasePayload()
	payload.Target = "intake"
	payload.RuntimeRoles = map[string]string{}
	for key, scope := range map[string]string{
		"request-fulfillment": "request-fulfillment", "consoleauth": "consoleauth",
		"vector": "integration-platform", "integration-platform": "integration-platform-full-runtime",
	} {
		name, err := migrate.RuntimeRoleName(migrate.RuntimeRoleScope{
			Environment: payload.Environment, Deployment: payload.DeploymentID,
			Target: scope, Generation: payload.Generation,
		})
		if err != nil {
			t.Fatal(err)
		}
		payload.RuntimeRoles[key] = name
	}
	if _, err := canonicalReleasePayload(payload); err != nil {
		t.Fatalf("complete intake roles rejected: %v", err)
	}
	payload.RuntimeRoles["vector"] = payload.RuntimeRoles["request-fulfillment"]
	if _, err := canonicalReleasePayload(payload); err == nil {
		t.Fatal("cross-target generation role substitution accepted")
	}
}

func TestIntakeLedgerCannotReachVerifiedWithoutAllThreeRuntimeChecks(t *testing.T) {
	for _, missing := range []string{"request-fulfillment", "consoleauth", "integration-platform"} {
		t.Run(missing, func(t *testing.T) {
			ledger := releaseLedger{
				Version:          1,
				Identity:         releaseLedgerIdentity{Environment: "test", Deployment: "deployment-1", DaemonID: "daemon", LedgerID: "ledger"},
				ActiveGeneration: "generation-1", Releases: map[string]releaseRecord{},
			}
			record := releaseRecord{Generation: "generation-2", Nonce: "nonce-2", Sequence: 1,
				BaseActive: "generation-1", State: "new", Target: "intake", ImageDigest: "sha256:" + strings.Repeat("a", 64)}
			if err := applyLedgerTransition(&ledger, "new", "", "", record); err != nil {
				t.Fatal(err)
			}
			record.State = "staging"
			if err := applyLedgerTransition(&ledger, "staging", "", "", record); err != nil {
				t.Fatal(err)
			}
			record.CandidateManifestName = "candidate-nonce-2.json"
			record.CandidateManifestSHA256 = "sha256:" + strings.Repeat("b", 64)
			record.DBJobs = map[string]string{
				"candidate-runtime-verify:request-fulfillment":  "succeeded:exit=0",
				"candidate-runtime-verify:consoleauth":          "succeeded:exit=0",
				"candidate-runtime-verify:integration-platform": "succeeded:exit=0",
			}
			delete(record.DBJobs, "candidate-runtime-verify:"+missing)
			record.Proofs = map[string]proofRecord{"intake": {KeyID: "key", Digest: "sha256:" + strings.Repeat("c", 64)}}
			record.State = "verified"
			if err := applyLedgerTransition(&ledger, "verified", "", "", record); err == nil {
				t.Fatalf("verified release accepted with missing %s runtime check", missing)
			}
		})
	}
}

func TestLedgerIPCIsSingleWriterAndRevisionCASPreventsLostUpdates(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() != 65532 {
		t.Skip("holder ownership/peer credentials are exercised by the disposable UID 65532 Linux Docker acceptance test")
	}
	dir, err := os.MkdirTemp("/tmp", "lgipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	lock, err := os.OpenFile(filepath.Join(dir, "release.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	daemonID := "sha256:" + strings.Repeat("a", 64)
	identity := releaseLedgerIdentity{
		Environment: "test", Deployment: "deployment-1", DaemonID: daemonID,
		LedgerID: "lead-gen-release-test-deployment-1-" + strings.Repeat("a", 24),
	}
	owner := releaseOwner{Version: 1, OwnerAttempt: "attempt-1", ReleaseNonce: "nonce-1", Identity: identity, PID: os.Getpid()}
	if err := writeReleaseOwner(dir, owner); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "ledger.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(listener.Addr().String(), 0o660); err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	stop := make(chan struct{})
	serverErr := make(chan error, 1)
	go func() { serverErr <- serveReleaseLedgerUntil(listener, lock, dir, owner, stop) }()
	t.Cleanup(func() {
		close(stop)
		listener.Close()
		<-serverErr
	})

	base := releaseRecord{
		Generation: "generation-2", Nonce: owner.ReleaseNonce, Sequence: 1,
		BaseActive: "generation-1", State: "new", Target: "intake",
		ImageDigest: "sha256:" + strings.Repeat("a", 64),
	}
	if response := sendLedgerIPC(t, listener.Addr().String(), ledgerIPCRequest{
		Identity: identity, OwnerAttempt: owner.OwnerAttempt, ReleaseNonce: owner.ReleaseNonce,
		ExpectedRevision: 0, Action: "new", Record: base,
	}); !response.OK || response.Revision != 1 {
		t.Fatalf("reserve failed: %+v", response)
	}
	staging := base
	staging.State = "staging"
	if response := sendLedgerIPC(t, listener.Addr().String(), ledgerIPCRequest{
		Identity: identity, OwnerAttempt: owner.OwnerAttempt, ReleaseNonce: owner.ReleaseNonce,
		ExpectedRevision: 1, Action: "staging", Record: staging,
	}); !response.OK || response.Revision != 2 {
		t.Fatalf("staging failed: %+v", response)
	}

	verified := staging
	verified.State = "verified"
	verified.Proofs = map[string]proofRecord{"intake": {KeyID: "key-1", Digest: "sha256:proof"}}
	failed := staging
	failed.State = "failed"
	requests := []ledgerIPCRequest{
		{Identity: identity, OwnerAttempt: owner.OwnerAttempt, ReleaseNonce: owner.ReleaseNonce, ExpectedRevision: 2, Action: "verified", Record: verified},
		{Identity: identity, OwnerAttempt: owner.OwnerAttempt, ReleaseNonce: owner.ReleaseNonce, ExpectedRevision: 2, Action: "failed", Record: failed},
	}
	responses := make(chan ledgerIPCResponse, 2)
	for _, request := range requests {
		request := request
		go func() { responses <- sendLedgerIPC(t, listener.Addr().String(), request) }()
	}
	first, second := <-responses, <-responses
	accepted := 0
	for _, response := range []ledgerIPCResponse{first, second} {
		if response.OK && response.Revision == 3 {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("same-revision concurrent mutations must have one winner: %+v %+v", first, second)
	}
	data, err := os.ReadFile(filepath.Join(dir, "ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ledger releaseLedger
	if err := strictJSON(data, &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.Revision != 3 || (ledger.Releases[owner.ReleaseNonce].State != "verified" && ledger.Releases[owner.ReleaseNonce].State != "failed") {
		t.Fatalf("ledger lost or corrupted a concurrent update: %+v", ledger)
	}
	wrongAttempt := requests[0]
	wrongAttempt.ExpectedRevision = 3
	wrongAttempt.OwnerAttempt = "forged-owner-token"
	if response := sendLedgerIPC(t, listener.Addr().String(), wrongAttempt); response.OK {
		t.Fatal("owner token without active holder attempt accepted")
	}
}

func serveReleaseLedgerUntil(listener *net.UnixListener, lock *os.File, dir string, owner releaseOwner, stop <-chan struct{}) error {
	for {
		_ = listener.SetDeadline(time.Now().Add(100 * time.Millisecond))
		connection, err := listener.AcceptUnix()
		if err != nil {
			select {
			case <-stop:
				return nil
			default:
			}
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				continue
			}
			return err
		}
		serveReleaseLedgerConnection(connection, lock, dir, owner)
	}
}

func sendLedgerIPC(t *testing.T, socket string, request ledgerIPCRequest) ledgerIPCResponse {
	t.Helper()
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := connection.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	responseData, err := io.ReadAll(connection)
	if err != nil {
		t.Fatal(err)
	}
	var response ledgerIPCResponse
	if err := strictJSON(responseData, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestReleaseLedgerWriteCommandIsDisabled(t *testing.T) {
	handled, err := releaseHelperMode([]string{"-release-ledger-write", releaseStateDir})
	if !handled || err == nil {
		t.Fatal("direct owner-token ledger writer is still available")
	}
}

func TestReleaseMigrationManifestPinsOnlyApprovedPilotFiles(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	requestManifest, err := validateReleaseMigrationManifest(root, "request-fulfillment")
	if err == nil {
		t.Fatal("request migrations outside the additive G1 manifest unexpectedly accepted")
	}
	if requestManifest.PolicyVersion != "g1-additive-v1" || len(requestManifest.Approved) != 4 || len(requestManifest.DeferredG1) != 7 {
		t.Fatalf("unexpected request G1 migration manifest: %+v", requestManifest)
	}
	for _, target := range []string{"consoleauth", "integration-platform"} {
		manifest, err := validateReleaseMigrationManifest(root, target)
		if err != nil {
			t.Fatalf("approved %s role/schema setup rejected: %v", target, err)
		}
		if manifest.PolicyVersion != "g1-additive-v1" || len(manifest.Approved) == 0 || len(manifest.DeferredG1) != 0 {
			t.Fatalf("unexpected allowlisted %s manifest: %+v", target, manifest)
		}
	}
}

func TestReleaseSigningKeyCheckPrintsOnlyDerivedPublicKey(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyBytes := []byte(base64.RawStdEncoding.EncodeToString(privateKey))
	keyPath := filepath.Join(t.TempDir(), "signing-key")
	if err := os.WriteFile(keyPath, append(privateKeyBytes, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyPath, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LEADGEN_RELEASE_SIGNING_KEY_FILE", keyPath)

	originalStdout := os.Stdout
	readFD, writeFD, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writeFD
	checkErr := runReleaseSigningKeyCheck(nil)
	_ = writeFD.Close()
	os.Stdout = originalStdout
	if checkErr != nil {
		_ = readFD.Close()
		t.Fatal(checkErr)
	}
	output, err := io.ReadAll(readFD)
	_ = readFD.Close()
	if err != nil {
		t.Fatal(err)
	}
	want := base64.RawStdEncoding.EncodeToString(publicKey) + "\n"
	if string(output) != want {
		t.Fatalf("signing check stdout = %q, want derived public key only", output)
	}
	if strings.Contains(string(output), string(privateKeyBytes)) || strings.Contains(string(output), string(privateKey)) {
		t.Fatal("signing check exposed private key bytes")
	}
}

func TestReleaseTrustKeyCheckUsesOnlyStrictStagedTrustReader(t *testing.T) {
	if os.Geteuid() != 0 {
		// Непривилегированный unit suite проверяет helper без подмены euid; запуск
		// CLI subprocess с UID 65532 и RO volume остаётся отдельной Docker acceptance.
		t.Skip("staged trust bundle UID/GID 65532 fixture requires root; Docker CLI acceptance is separate")
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "active")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeUID65532TrustBundle(t, directory, publicKey, "trusted-test-key")

	// Невалидный путь к signing key подтверждает, что trust check не пытается
	// открыть signing private key.
	t.Setenv("LEADGEN_RELEASE_SIGNING_KEY_FILE", filepath.Join(t.TempDir(), "missing-private-key"))
	check := func(keyID string) error {
		return runReleaseTrustKeyCheckAt(
			[]string{filepath.Join(directory, "trusted-release.pub")}, directory, keyID,
			func(path, id string) (ed25519.PublicKey, error) {
				return readStagedTrustAnchorAt(path, id, false)
			},
		)
	}
	if err := check("trusted-test-key"); err != nil {
		t.Fatalf("valid staged trust bundle rejected without signing-key access: %v", err)
	}
	for _, keyID := range []string{"", "wrong-key-id"} {
		err := check(keyID)
		if err == nil {
			t.Fatalf("staged trust check accepted key ID %q", keyID)
		}
		for _, secret := range [][]byte{
			privateKey,
			[]byte(base64.RawStdEncoding.EncodeToString(privateKey)),
			publicKey,
			[]byte(base64.RawStdEncoding.EncodeToString(publicKey)),
		} {
			if strings.Contains(err.Error(), string(secret)) {
				t.Fatalf("trust check error exposed key bytes: %v", err)
			}
		}
	}
	missing := filepath.Join(t.TempDir(), "missing")
	err = runReleaseTrustKeyCheckAt(
		[]string{filepath.Join(missing, "trusted-release.pub")}, missing, "trusted-test-key",
		func(path, id string) (ed25519.PublicKey, error) {
			return readStagedTrustAnchorAt(path, id, false)
		},
	)
	if err == nil {
		t.Fatal("missing staged trust bundle accepted")
	}
	for _, secret := range [][]byte{privateKey, []byte(base64.RawStdEncoding.EncodeToString(privateKey))} {
		if strings.Contains(err.Error(), string(secret)) {
			t.Fatalf("missing bundle error exposed private key bytes: %v", err)
		}
	}
}

func writeUID65532TrustBundle(t *testing.T, directory string, publicKey ed25519.PublicKey, keyID string) {
	t.Helper()
	files := map[string][]byte{
		"trusted-release.pub": []byte(base64.RawStdEncoding.EncodeToString(publicKey) + "\n"),
		"key-id":              []byte(keyID + "\n"),
		"complete":            []byte("complete\n"),
	}
	for name, content := range files {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, content, 0o400); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(path, 65532, 65532); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o400); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chown(directory, 65532, 65532); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
}

type releaseTestFileInfo struct {
	mode os.FileMode
	sys  any
}

func (info releaseTestFileInfo) Name() string       { return "ledger.sock" }
func (info releaseTestFileInfo) Size() int64        { return 0 }
func (info releaseTestFileInfo) Mode() os.FileMode  { return info.mode }
func (info releaseTestFileInfo) ModTime() time.Time { return time.Time{} }
func (info releaseTestFileInfo) IsDir() bool        { return false }
func (info releaseTestFileInfo) Sys() any           { return info.sys }

func TestReleaseIPCRejectsWrongParentAndSocketPermissions(t *testing.T) {
	validDirectory := &unix.Stat_t{Mode: unix.S_IFDIR | 0o700, Uid: 65532, Gid: 65532}
	if !validReleaseStateDirectoryStat(validDirectory) {
		t.Fatal("expected owner-only state directory rejected")
	}
	for _, bad := range []*unix.Stat_t{
		{Mode: unix.S_IFDIR | 0o700, Uid: 65531, Gid: 65532},
		{Mode: unix.S_IFDIR | 0o700, Uid: 65532, Gid: 65531},
		{Mode: unix.S_IFDIR | 0o710, Uid: 65532, Gid: 65532},
		{Mode: unix.S_IFDIR | 0o750, Uid: 65532, Gid: 65532},
	} {
		if validReleaseStateDirectoryStat(bad) {
			t.Fatalf("unsafe parent accepted: %+v", bad)
		}
	}
	validSocket := &syscall.Stat_t{Mode: syscall.S_IFSOCK | 0o660, Uid: 65532, Gid: 65532}
	if !validReleaseSocketStat(validSocket) {
		t.Fatal("expected 0660 holder socket rejected")
	}
	for _, bad := range []*syscall.Stat_t{
		{Mode: syscall.S_IFSOCK | 0o660, Uid: 65531, Gid: 65532},
		{Mode: syscall.S_IFSOCK | 0o660, Uid: 65532, Gid: 65531},
		{Mode: syscall.S_IFSOCK | 0o600, Uid: 65532, Gid: 65532},
		{Mode: syscall.S_IFREG | 0o660, Uid: 65532, Gid: 65532},
	} {
		if validReleaseSocketStat(bad) {
			t.Fatalf("unsafe socket accepted: %+v", bad)
		}
	}
	if err := verifyReleaseSocketInfo(releaseTestFileInfo{
		mode: os.ModeSocket | 0o660, sys: validSocket,
	}); err != nil {
		t.Fatalf("valid socket accepted: %v", err)
	}
	if err := verifyReleaseSocketInfo(releaseTestFileInfo{
		mode: os.ModeSymlink | 0o777, sys: validSocket,
	}); err == nil {
		t.Fatal("symlink socket accepted")
	}

	dir := t.TempDir()
	target := dir + "/target"
	link := dir + "/state-link"
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := verifyReleaseStateDirectoryAt(link); err == nil {
		t.Fatal("symlinked state parent accepted")
	}
}

func TestActivationPublicationResumesDurableBoundaries(t *testing.T) {
	const nonce = "nonce-resume"
	base := []byte("signed-enrolled-base")
	candidate := []byte("signed-candidate-for-same-nonce")
	for _, tc := range []struct {
		name  string
		files map[string][]byte
	}{
		{
			name: "after backup publication",
			files: map[string][]byte{
				"active-candidate.json":                        base,
				"previous-active-candidate-" + nonce + ".json": base,
			},
		},
		{
			name: "after temp write",
			files: map[string][]byte{
				"active-candidate.json":                        base,
				"previous-active-candidate-" + nonce + ".json": base,
				"active-candidate.tmp-" + nonce:                candidate,
			},
		},
		{
			name: "after active rename",
			files: map[string][]byte{
				"active-candidate.json":                        candidate,
				"previous-active-candidate-" + nonce + ".json": base,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range tc.files {
				writeActivationArtifact(t, dir, name, data, 0o400)
			}
			fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(fd)
			verifyBase := func(got []byte) error {
				if !equalBytes(got, base) {
					return errors.New("not the enrolled base")
				}
				return nil
			}
			if err := resumeActiveCandidatePublicationAt(fd, candidate, nonce, verifyBase, uint32(os.Getuid()), uint32(os.Getgid())); err != nil {
				t.Fatalf("recovery rejected: %v", err)
			}
			active, err := os.ReadFile(filepath.Join(dir, "active-candidate.json"))
			if err != nil || !equalBytes(active, candidate) {
				t.Fatalf("candidate not active: %q, %v", active, err)
			}
			backup, err := os.ReadFile(filepath.Join(dir, "previous-active-candidate-"+nonce+".json"))
			if err != nil || !equalBytes(backup, base) {
				t.Fatalf("base backup changed: %q, %v", backup, err)
			}
		})
	}
}

func TestActivationPublicationRejectsMismatchedArtifactsWithoutDeletingThem(t *testing.T) {
	const nonce = "nonce-mismatch"
	base := []byte("signed-enrolled-base")
	candidate := []byte("signed-candidate")
	for _, tc := range []struct {
		name  string
		files map[string][]byte
		mode  string
	}{
		{name: "backup content", files: map[string][]byte{
			"active-candidate.json":                        base,
			"previous-active-candidate-" + nonce + ".json": []byte("other base"),
		}},
		{name: "temp content", files: map[string][]byte{
			"active-candidate.json":                        base,
			"previous-active-candidate-" + nonce + ".json": base,
			"active-candidate.tmp-" + nonce:                []byte("other candidate"),
		}},
		{name: "unsafe temp mode", mode: "temp", files: map[string][]byte{
			"active-candidate.json":                        base,
			"previous-active-candidate-" + nonce + ".json": base,
			"active-candidate.tmp-" + nonce:                candidate,
		}},
		{name: "unexpected active", files: map[string][]byte{
			"active-candidate.json":                        []byte("different active"),
			"previous-active-candidate-" + nonce + ".json": base,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, data := range tc.files {
				mode := os.FileMode(0o400)
				if tc.mode == "temp" && strings.HasPrefix(name, "active-candidate.tmp-") {
					mode = 0o600
				}
				writeActivationArtifact(t, dir, name, data, mode)
			}
			fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(fd)
			verifyBase := func(got []byte) error {
				if !equalBytes(got, base) {
					return errors.New("not the enrolled base")
				}
				return nil
			}
			if err := resumeActiveCandidatePublicationAt(fd, candidate, nonce, verifyBase, uint32(os.Getuid()), uint32(os.Getgid())); err == nil {
				t.Fatal("mismatched or unsafe activation artifact accepted")
			}
			for name, original := range tc.files {
				got, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil || !equalBytes(got, original) {
					t.Fatalf("recovery modified artifact %q: %q, %v", name, got, err)
				}
			}
		})
	}
}

func writeActivationArtifact(t *testing.T, dir, name string, data []byte, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
