package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// runReleaseActivationGate — apply-time Tofu gate. Он повторно проверяет
// подпись, независимый trust anchor, подписанный candidate, holder IPC и ledger,
// затем атомарно потребляет proof до того, как Tofu заменит intake runtime.
func runReleaseActivationGate(args []string) error {
	if len(args) != 3 || filepath.Clean(args[0]) != releaseStateDir ||
		filepath.Clean(args[1]) != candidateTrustKeyPath || filepath.Clean(args[2]) != "/run/leadgen-attestation/attestation.json" || os.Geteuid() != 65532 {
		return errors.New("release activation gate: fixed mount paths and runtime UID required")
	}
	if err := verifyReleaseStateDirectory(releaseStateDir); err != nil {
		return errors.New("release activation gate: release-state mount rejected")
	}
	attestation, err := readActivationArtifact(args[2])
	if err != nil {
		return errors.New("release activation gate: sanitized attestation unavailable")
	}
	keyID := os.Getenv("LEADGEN_RELEASE_TRUST_KEY_ID")
	if keyID == "" {
		return errors.New("release activation gate: trusted key id is required")
	}
	publicKey, err := readStagedTrustAnchor(trustRuntimeRoot, keyID)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return errors.New("release activation gate: independent staged trust key unavailable or mismatched")
	}
	now := time.Now().UTC()
	payload, err := verifyReleaseAttestationMode(attestation, publicKey, keyID,
		strings.Split(os.Getenv("LEADGEN_RELEASE_REVOKED_KEY_IDS"), ","), now, true)
	if err != nil || payload.Target != "intake" {
		return errors.New("release activation gate: signed intake attestation rejected")
	}
	ownerBytes, err := os.ReadFile(filepath.Join(releaseStateDir, "owner.json"))
	if err != nil {
		return errors.New("release activation gate: active remote lock holder unavailable")
	}
	var owner releaseOwner
	if strictJSON(ownerBytes, &owner) != nil || owner.ReleaseNonce != payload.ReleaseNonce || owner.Identity.Environment != payload.Environment || owner.Identity.Deployment != payload.DeploymentID || owner.Identity.DaemonID != payload.DaemonID || owner.Identity.LedgerID != payload.LedgerID || owner.PID <= 0 {
		return errors.New("release activation gate: holder identity differs from signed proof")
	}
	ledgerBytes, err := os.ReadFile(filepath.Join(releaseStateDir, "ledger.json"))
	if err != nil {
		return errors.New("release activation gate: durable ledger unavailable")
	}
	var diskLedger releaseLedger
	if strictJSON(ledgerBytes, &diskLedger) != nil {
		return errors.New("release activation gate: durable ledger malformed")
	}
	ledger, err := readLiveReleaseLedger(owner, diskLedger.Revision)
	if err != nil {
		return errors.New("release activation gate: remote ledger IPC unavailable")
	}
	record, ok := ledger.Releases[payload.ReleaseNonce]
	if !ok || ledger.Identity != owner.Identity || (record.State != "verified" && record.State != "activation_in_progress") || record.Target != "intake" ||
		record.Generation != payload.Generation || record.BaseActive != payload.BaseActive ||
		record.Sequence != payload.Sequence || ledger.Sequence != payload.Sequence || ledger.ActiveGeneration != payload.BaseActive ||
		record.ImageDigest != payload.ImageDigest || record.CandidateManifestName != "candidate-"+payload.ReleaseNonce+".json" {
		return errors.New("release activation gate: ledger state or generation does not authorize intake activation")
	}
	for _, target := range []string{"request-fulfillment", "consoleauth", "integration-platform"} {
		if !strings.HasPrefix(record.DBJobs["candidate-runtime-verify:"+target], "succeeded:exit=0") {
			return errors.New("release activation gate: all three candidate DSNs must pass verification")
		}
	}
	manifestPath := filepath.Join(releaseStateDir, record.CandidateManifestName)
	manifestText, err := readSecretFile(manifestPath)
	manifestBytes := []byte(manifestText)
	if err != nil || "sha256:"+digestHex(manifestBytes) != record.CandidateManifestSHA256 {
		return errors.New("release activation gate: signed candidate artifact differs from ledger")
	}
	manifest, err := verifyCandidateManifestMode(manifestBytes, publicKey, keyID, now, record.State == "activation_in_progress")
	if err != nil || manifest.Target != "intake" || manifest.Generation != payload.Generation ||
		manifest.RuntimeImageDigest != payload.ImageDigest || manifest.ReleaseNonce != payload.ReleaseNonce ||
		manifest.Sequence != payload.Sequence || manifest.BaseActive != payload.BaseActive ||
		manifest.Environment != payload.Environment || manifest.DeploymentID != payload.DeploymentID || manifest.DaemonID != payload.DaemonID || manifest.LedgerID != payload.LedgerID {
		return errors.New("release activation gate: signed candidate scope/image mismatch")
	}
	if err := validateReleaseExpectation(payload, releaseExpectation{
		DeploymentID: manifest.DeploymentID, Environment: manifest.Environment, Target: "intake",
		DaemonID: manifest.DaemonID, LedgerID: manifest.LedgerID, Generation: manifest.Generation,
		ReleaseNonce: manifest.ReleaseNonce, Sequence: manifest.Sequence, BaseActive: manifest.BaseActive,
		ImageDigest: manifest.ImageDigest, CandidateManifestSHA256: record.CandidateManifestSHA256,
		BundleFiles: payload.BundleFiles,
	}); err != nil {
		return errors.New("release activation gate: attestation/candidate relationship rejected")
	}
	if err := verifyIntakeAttestedRoles(payload, manifest); err != nil {
		return errors.New("release activation gate: attested roles do not match candidate")
	}
	// Порядок соответствует канонической сериализации payload.
	if !equalStringSlices(payload.BundleFiles, []string{"console-password", "consoleauth-runtime-dsn", "generation", "request-runtime-dsn", "vector-runtime-dsn"}) {
		return errors.New("release activation gate: signed intake bundle file set differs")
	}
	proofDigest := digestHex(payloadCanonicalBytes(attestation))
	proof, ok := record.Proofs["intake"]
	if !ok || proof.KeyID != payload.KeyID || proof.Digest != "sha256:"+proofDigest || proof.ExpiresAt != payload.ExpiresAt || proof.IssuedAt != payload.IssuedAt || proof.Consumed != (record.State == "activation_in_progress") {
		return errors.New("release activation gate: proof absent, stale or inconsistent with activation ledger")
	}
	proofExpires, parseErr := time.Parse(time.RFC3339, proof.ExpiresAt)
	if parseErr != nil || (!proofExpires.After(now) && record.State != "activation_in_progress") {
		return errors.New("release activation gate: expired proof cannot start a new activation")
	}
	if record.State == "verified" {
		consumed := record
		consumed.State = "activation_in_progress"
		consumed.Proofs = cloneProofs(record.Proofs)
		proof.Consumed = true
		consumed.Proofs["intake"] = proof
		request := ledgerIPCRequest{
			Identity: owner.Identity, OwnerAttempt: owner.OwnerAttempt, ReleaseNonce: owner.ReleaseNonce,
			ExpectedRevision: ledger.Revision, Action: "activation_in_progress", ProofTarget: "intake",
			ProofDigest: proof.Digest, Record: consumed,
		}
		if _, err := sendReleaseLedgerIPC(request); err != nil {
			return errors.New("release activation gate: one-time ledger proof consumption failed")
		}
	}
	if err := resumeActiveCandidatePublication(releaseStateDir, manifestBytes, payload.ReleaseNonce, payload.BaseActive, publicKey, keyID, ledger); err != nil {
		return errors.New("release activation gate: signed active-manifest publication failed")
	}
	return nil
}

func resumeActiveCandidatePublication(stateDir string, candidate []byte, nonce, baseActive string, publicKey ed25519.PublicKey, keyID string, ledger releaseLedger) error {
	if !safeReleaseID.MatchString(nonce) {
		return errors.New("invalid release nonce")
	}
	directory, err := unix.Open(stateDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer unix.Close(directory)
	if len(candidate) == 0 || len(candidate) > 1<<20 {
		return errors.New("candidate size rejected")
	}
	verifyBase := func(data []byte) error {
		return verifyPreviousActivationManifest(data, baseActive, publicKey, keyID, ledger)
	}
	return resumeActiveCandidatePublicationAt(directory, candidate, nonce, verifyBase, 65532, 65532)
}

func resumeActiveCandidatePublicationAt(directory int, candidate []byte, nonce string, verifyBase func([]byte) error, owner, group uint32) error {
	if !safeReleaseID.MatchString(nonce) || len(candidate) == 0 || len(candidate) > 1<<20 || verifyBase == nil {
		return errors.New("activation publication metadata rejected")
	}
	backupName := "previous-active-candidate-" + nonce + ".json"
	active, activeErr := readOwnedReleaseArtifactAt(directory, "active-candidate.json", owner, group)
	backup, backupErr := readOwnedReleaseArtifactAt(directory, backupName, owner, group)
	tempName := "active-candidate.tmp-" + nonce
	temporary, tempErr := readOwnedReleaseArtifactAt(directory, tempName, owner, group)
	if tempErr == nil && !equalBytes(temporary, candidate) {
		return errors.New("orphan active candidate temp differs from signed candidate")
	}
	if tempErr != nil && !errors.Is(tempErr, unix.ENOENT) {
		return tempErr
	}
	if backupErr == nil {
		if err := verifyBase(backup); err != nil {
			return errors.New("activation backup does not match enrolled base manifest")
		}
	} else if !errors.Is(backupErr, unix.ENOENT) {
		return backupErr
	}
	if activeErr != nil && !errors.Is(activeErr, unix.ENOENT) {
		return activeErr
	}
	if backupErr == nil {
		if err := unix.Fsync(directory); err != nil {
			return errors.New("verified activation backup durability could not be confirmed")
		}
	}
	if activeErr == nil && equalBytes(active, candidate) {
		if backupErr != nil {
			return errors.New("published candidate has no verified base backup")
		}
		return nil
	}
	if activeErr != nil || verifyBase(active) != nil {
		return errors.New("active candidate is neither enrolled base nor exact candidate")
	}
	if backupErr == nil {
		if !equalBytes(backup, active) {
			return errors.New("existing activation backup differs from active base")
		}
	} else if err := createOwnedReleaseArtifactAt(directory, backupName, active, owner, group); err != nil {
		return err
	}
	if err := publishActiveCandidateAt(directory, candidate, nonce, owner, group); err != nil {
		return err
	}
	return nil
}

func verifyPreviousActivationManifest(data []byte, generation string, publicKey ed25519.PublicKey, keyID string, ledger releaseLedger) error {
	// The caller has already proved that the durable active ledger still names
	// generation as the base of this in-progress activation. Its signed manifest
	// may therefore be older than the short candidate TTL.
	manifest, err := verifyCandidateManifestMode(data, publicKey, keyID, time.Now().UTC(), true)
	if err != nil || manifest.Target != "intake" || manifest.Generation != generation || manifest.ExecutionProtocol != "v1" || manifest.TargetPolicies["request-fulfillment"].PolicyVersion != g15MigrationPolicyVersion {
		return errors.New("previous active manifest signature or scope rejected")
	}
	if ledger.ActiveGeneration != generation || verifyCandidateLedgerRecord(manifest, data, ledger, releaseOwner{}, true) != nil {
		return errors.New("previous active manifest is not bound to the active release ledger")
	}
	return nil
}

func readOwnedReleaseArtifactAt(directory int, name string, owner, group uint32) ([]byte, error) {
	fd, err := unix.Openat(directory, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("release artifact unavailable")
	}
	defer file.Close()
	var info unix.Stat_t
	if unix.Fstat(fd, &info) != nil || !safeSecretFileStat(&info, owner) || info.Gid != group || info.Size <= 0 || info.Size > 1<<20 {
		return nil, errors.New("release artifact type/owner/mode/size rejected")
	}
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(data) != int(info.Size) || len(data) > 1<<20 {
		return nil, errors.New("release artifact read rejected")
	}
	return data, nil
}

func createOwnedReleaseArtifactAt(directory int, name string, data []byte, owner, group uint32) error {
	fd, err := unix.Openat(directory, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o400)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return errors.New("release artifact unavailable")
	}
	if _, err = file.Write(data); err == nil {
		err = unix.Fchown(fd, int(owner), int(group))
	}
	if err == nil {
		err = unix.Fchmod(fd, 0o400)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = unix.Fsync(directory)
	}
	if err != nil {
		return errors.New("release artifact durable publication failed")
	}
	return nil
}

func publishActiveCandidateAt(directory int, data []byte, nonce string, owner, group uint32) error {
	if !safeReleaseID.MatchString(nonce) || len(data) == 0 || len(data) > 1<<20 {
		return errors.New("active candidate publication metadata rejected")
	}
	temporary := "active-candidate.tmp-" + nonce
	if existing, err := readOwnedReleaseArtifactAt(directory, temporary, owner, group); err == nil {
		if !equalBytes(existing, data) {
			return errors.New("orphan active candidate temp differs from signed candidate")
		}
	} else if errors.Is(err, unix.ENOENT) {
		if err := createOwnedReleaseArtifactAt(directory, temporary, data, owner, group); err != nil {
			return err
		}
	} else {
		return err
	}
	if err := unix.Renameat(directory, temporary, directory, "active-candidate.json"); err != nil {
		return err
	}
	return unix.Fsync(directory)
}

func readActivationArtifact(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "release-attestation")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("attestation open failed")
	}
	defer file.Close()
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Mode&0o022 != 0 || info.Size <= 0 || info.Size > 1<<20 {
		return nil, errors.New("attestation file type/mode/size rejected")
	}
	return io.ReadAll(io.LimitReader(file, 1<<20))
}

func payloadCanonicalBytes(data []byte) []byte {
	var envelope signedAttestation
	if strictJSON(data, &envelope) != nil {
		return nil
	}
	return envelope.Payload
}

func digestHex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func cloneProofs(input map[string]proofRecord) map[string]proofRecord {
	result := make(map[string]proofRecord, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func sendReleaseLedgerIPC(request ledgerIPCRequest) (uint64, error) {
	socketPath := filepath.Join(releaseStateDir, "ledger.sock")
	if err := verifyReleaseIPCPath(releaseStateDir, socketPath); err != nil {
		return 0, err
	}
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return 0, err
	}
	defer connection.Close()
	if err := verifyReleaseIPCPath(releaseStateDir, socketPath); err != nil {
		return 0, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return 0, err
	}
	if _, err := connection.Write(encoded); err != nil || connection.CloseWrite() != nil {
		return 0, errors.New("ledger IPC write failed")
	}
	responseBytes, err := io.ReadAll(io.LimitReader(connection, 1<<20))
	if err != nil {
		return 0, err
	}
	var response ledgerIPCResponse
	if strictJSON(responseBytes, &response) != nil || !response.OK || response.Revision != request.ExpectedRevision+1 {
		return 0, errors.New("ledger IPC CAS rejected")
	}
	return response.Revision, nil
}

func verifyIntakeAttestedRoles(payload releasePayload, manifest CandidateManifest) error {
	for _, item := range []struct{ key, scope string }{
		{"request-fulfillment", "request-fulfillment"}, {"consoleauth", "consoleauth"},
		{"vector", "integration-platform"}, {"integration-platform", "integration-platform-full-runtime"},
	} {
		want := ""
		if item.key == "vector" || item.key == "integration-platform" {
			want = manifest.Roles["integration-platform"].Runtime[item.key]
		} else {
			want = manifest.Roles[item.scope].Runtime[item.scope]
		}
		if want == "" || payload.RuntimeRoles[item.key] != want {
			return errors.New("attested role binding mismatch")
		}
	}
	return nil
}
