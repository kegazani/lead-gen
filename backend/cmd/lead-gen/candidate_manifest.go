package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"lead-gen/backend/platform/migrate"
)

const candidateManifestPurpose = "lead-gen-db-candidate"
const candidateTrustKeyPath = "/run/leadgen-trust/active/trusted-release.pub"

var candidateIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// CandidateManifest — подписанное разрешение на DB candidate, не финальное
// подтверждение успеха. Поля расположены в каноническом порядке сериализации.
// В структуре намеренно нет секретов или digest секретных файлов.
type CandidateManifest struct {
	SchemaVersion      int                              `json:"schemaVersion"`
	Purpose            string                           `json:"purpose"`
	Environment        string                           `json:"environment"`
	DeploymentID       string                           `json:"deploymentId"`
	DaemonID           string                           `json:"daemonId"`
	LedgerID           string                           `json:"ledgerId"`
	Target             string                           `json:"target"`
	Generation         string                           `json:"generation"`
	ReleaseNonce       string                           `json:"releaseNonce"`
	Sequence           uint64                           `json:"sequence"`
	BaseActive         string                           `json:"baseActiveGeneration"`
	Roles              map[string]CandidateRoleSet      `json:"roles"`
	TargetPolicies     map[string]CandidateTargetPolicy `json:"targetPolicies"`
	ImageDigest        string                           `json:"imageDigest"`
	RuntimeImageDigest string                           `json:"runtimeImageDigest"`
	ExecutionProtocol  string                           `json:"executionProtocol"`
	SourceBundle       string                           `json:"sourceBundle"`
	DestinationBundle  string                           `json:"destinationBundle"`
	BundleNames        []string                         `json:"bundleNames"`
	IssuedAt           string                           `json:"issuedAt"`
	ExpiresAt          string                           `json:"expiresAt"`
	KeyID              string                           `json:"keyId"`
	ActivationPending  bool                             `json:"-"`
}

// CandidateTargetPolicy records the exact migration and rollback policy for
// one database member of the indivisible intake release.
type CandidateTargetPolicy struct {
	PolicyVersion string               `json:"policyVersion"`
	Migrations    []CandidateMigration `json:"migrations"`
}

type CandidateRoleSet struct {
	Owner    string            `json:"owner"`
	Migrator string            `json:"migrator"`
	Runtime  map[string]string `json:"runtime"`
}

type CandidateMigration struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

type signedCandidateManifest struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

func canonicalCandidateManifest(m CandidateManifest) ([]byte, error) {
	if m.SchemaVersion != 2 || m.Purpose != candidateManifestPurpose || !candidateIDPattern.MatchString(m.Environment) ||
		!candidateIDPattern.MatchString(m.DeploymentID) || !releaseDigestPattern.MatchString(m.DaemonID) ||
		!candidateIDPattern.MatchString(m.LedgerID) || m.Target != "intake" ||
		!candidateIDPattern.MatchString(m.Generation) || !candidateIDPattern.MatchString(m.ReleaseNonce) ||
		!releaseDigestPattern.MatchString(m.ImageDigest) || !releaseDigestPattern.MatchString(m.RuntimeImageDigest) || !candidateIDPattern.MatchString(m.KeyID) ||
		m.Sequence == 0 || !candidateIDPattern.MatchString(m.BaseActive) || m.ExecutionProtocol != "v1" ||
		!candidateIDPattern.MatchString(m.SourceBundle) || !candidateIDPattern.MatchString(m.DestinationBundle) {
		return nil, errors.New("candidate manifest: schema or required scope fields rejected")
	}
	if len(dbTargets) != 3 || len(m.Roles) != len(dbTargets) || len(m.TargetPolicies) != len(dbTargets) || len(m.BundleNames) == 0 || len(m.BundleNames) > 32 {
		return nil, errors.New("candidate manifest: target or bounded registry rejected")
	}
	for target, definition := range dbTargets {
		roleSet, found := m.Roles[target]
		if !found || roleSet.Owner != "leadgen_"+strings.ReplaceAll(target, "-", "_")+"_owner" || roleSet.Migrator != "leadgen_"+strings.ReplaceAll(target, "-", "_")+"_migrator" {
			return nil, errors.New("candidate manifest: owner/migrator registry rejected")
		}
		scope := migrate.RuntimeRoleScope{Environment: m.Environment, Deployment: m.DeploymentID, Target: target, Generation: m.Generation}
		runtimeKey := target
		expectedRuntimeCount := 1
		if target == "integration-platform" {
			runtimeKey, expectedRuntimeCount = "vector", 2
		}
		wantRole, roleErr := migrate.RuntimeRoleName(scope)
		if roleErr != nil || len(roleSet.Runtime) != expectedRuntimeCount || roleSet.Runtime[runtimeKey] != wantRole || roleSet.Owner == roleSet.Migrator || roleSet.Owner == wantRole || roleSet.Migrator == wantRole {
			return nil, errors.New("candidate manifest: runtime role differs from signed scope")
		}
		if target == "integration-platform" {
			integrationScope := scope
			integrationScope.Target = "integration-platform-full-runtime"
			wantIntegration, roleErr := migrate.RuntimeRoleName(integrationScope)
			if roleErr != nil || roleSet.Runtime["integration-platform"] != wantIntegration {
				return nil, errors.New("candidate manifest: additional runtime role differs from signed scope")
			}
		}
		policy, found := m.TargetPolicies[target]
		if !found || len(policy.Migrations) == 0 || len(policy.Migrations) > 64 {
			return nil, errors.New("candidate manifest: exact policy missing for intake target")
		}
		approvedMigrations := additiveReleaseMigrations[target]
		wantPolicy := "g1-additive-v1"
		if target == "request-fulfillment" {
			approvedMigrations = g15RequestMigrations
			wantPolicy = g15MigrationPolicyVersion
		}
		if policy.PolicyVersion != wantPolicy || len(policy.Migrations) != len(approvedMigrations) {
			return nil, errors.New("candidate manifest: migration policy does not match exact intake allowlist")
		}
		for i, item := range policy.Migrations {
			if item.Name != approvedMigrations[i].Name || item.SHA256 != approvedMigrations[i].SHA256 || !validSHA256Hex(item.SHA256) {
				return nil, errors.New("candidate manifest: migration file/hash is not in exact policy")
			}
		}
		_ = definition
	}
	if !sort.StringsAreSorted(m.BundleNames) || !strictUniqueStrings(m.BundleNames) {
		return nil, errors.New("candidate manifest: bundle names must be sorted and unique")
	}
	for _, name := range m.BundleNames {
		if name == "" || strings.ContainsAny(name, `/\\`) || name == "." || name == ".." {
			return nil, errors.New("candidate manifest: invalid bundle basename")
		}
	}
	issued, issuedErr := time.Parse(time.RFC3339, m.IssuedAt)
	expires, expiresErr := time.Parse(time.RFC3339, m.ExpiresAt)
	if issuedErr != nil || expiresErr != nil || !expires.After(issued) || expires.Sub(issued) > time.Hour {
		return nil, errors.New("candidate manifest: invalid validity window")
	}
	return json.Marshal(m)
}

func validSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func strictUniqueStrings(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i-1] == values[i] {
			return false
		}
	}
	return true
}

func signCandidateManifest(m CandidateManifest, privateKey ed25519.PrivateKey) ([]byte, error) {
	canonical, err := canonicalCandidateManifest(m)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("candidate manifest: signing input rejected")
	}
	sig := ed25519.Sign(privateKey, canonical)
	encoded, err := json.Marshal(signedCandidateManifest{Payload: canonical, Signature: base64.RawStdEncoding.EncodeToString(sig)})
	if err != nil {
		return nil, errors.New("candidate manifest: envelope encoding failed")
	}
	return append(encoded, '\n'), nil
}

func verifyCandidateManifest(data []byte, publicKey ed25519.PublicKey, trustedKeyID string, now time.Time) (CandidateManifest, error) {
	return verifyCandidateManifestMode(data, publicKey, trustedKeyID, now, false)
}

func verifyCandidateManifestMode(data []byte, publicKey ed25519.PublicKey, trustedKeyID string, now time.Time, allowExpired bool) (CandidateManifest, error) {
	var envelope signedCandidateManifest
	if len(data) > 1<<20 || strictJSON(data, &envelope) != nil {
		return CandidateManifest{}, errors.New("candidate manifest: malformed envelope")
	}
	var manifest CandidateManifest
	if strictJSON(envelope.Payload, &manifest) != nil {
		return CandidateManifest{}, errors.New("candidate manifest: schema rejected")
	}
	canonical, err := canonicalCandidateManifest(manifest)
	signature, decodeErr := base64.RawStdEncoding.DecodeString(envelope.Signature)
	if err != nil || !equalBytes(canonical, envelope.Payload) || decodeErr != nil || len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(publicKey, canonical, signature) {
		return CandidateManifest{}, errors.New("candidate manifest: signature or canonical encoding rejected")
	}
	if trustedKeyID == "" || manifest.KeyID != trustedKeyID {
		return CandidateManifest{}, errors.New("candidate manifest: untrusted key id")
	}
	issued, _ := time.Parse(time.RFC3339, manifest.IssuedAt)
	expires, _ := time.Parse(time.RFC3339, manifest.ExpiresAt)
	if (!allowExpired && !expires.After(now)) || !issued.Before(now.Add(time.Minute)) {
		return CandidateManifest{}, errors.New("candidate manifest: expired or not-yet-valid")
	}
	return manifest, nil
}

func readCandidateManifest(path, publicKeyPath, target, keyID string) (CandidateManifest, error) {
	return readCandidateManifestMode(path, publicKeyPath, target, keyID, false)
}

func readActiveCandidateManifest(path, publicKeyPath, target, keyID string) (CandidateManifest, error) {
	return readCandidateManifestMode(path, publicKeyPath, target, keyID, true)
}

func readCandidateManifestMode(path, publicKeyPath, target, keyID string, active bool) (CandidateManifest, error) {
	if path == "" || publicKeyPath != candidateTrustKeyPath {
		return CandidateManifest{}, errors.New("candidate manifest and trust key are required")
	}
	manifestPath := filepath.Clean(path)
	if active && manifestPath != filepath.Join(releaseStateDir, "active-candidate.json") {
		return CandidateManifest{}, errors.New("candidate manifest: active path rejected")
	}
	dataText, err := readSecretFile(path)
	if err != nil {
		return CandidateManifest{}, errors.New("candidate manifest is unavailable")
	}
	data := []byte(dataText)
	publicKey, err := readStagedTrustAnchor(filepath.Dir(publicKeyPath), keyID)
	if err != nil {
		return CandidateManifest{}, errors.New("candidate trust bundle is unavailable or invalid")
	}
	now := time.Now().UTC()
	manifest, err := verifyCandidateManifestMode(data, publicKey, keyID, now, active)
	pathMatches := manifestPath == filepath.Join(releaseStateDir, "candidate-"+manifest.ReleaseNonce+".json")
	if active {
		pathMatches = manifestPath == filepath.Join(releaseStateDir, "active-candidate.json")
	}
	if err != nil || (target != "intake" && target != manifest.Target && func() bool { _, ok := manifest.TargetPolicies[target]; return !ok }()) || !pathMatches {
		return CandidateManifest{}, fmt.Errorf("candidate manifest: signature, trust, or target verification failed")
	}
	if err := verifyCandidateLedgerMode(manifest, data, active); err != nil {
		return CandidateManifest{}, errors.New("candidate manifest: release ledger binding rejected")
	}
	if active {
		ledgerBytes, readErr := os.ReadFile(filepath.Join(releaseStateDir, "ledger.json"))
		var ledger releaseLedger
		if readErr != nil || strictJSON(ledgerBytes, &ledger) != nil {
			return CandidateManifest{}, errors.New("candidate manifest: active ledger unavailable")
		}
		if err := verifyCandidateLedgerRecord(manifest, data, ledger, releaseOwner{}, true); err != nil {
			return CandidateManifest{}, errors.New("candidate manifest: active ledger binding changed")
		}
		expires, _ := time.Parse(time.RFC3339, manifest.ExpiresAt)
		record := ledger.Releases[manifest.ReleaseNonce]
		active := ledger.ActiveGeneration == manifest.Generation && record.State == "activated"
		activationPending := ledger.ActiveGeneration == manifest.BaseActive && record.State == "activation_in_progress"
		if !expires.After(now) && !active && !activationPending {
			return CandidateManifest{}, errors.New("candidate manifest: expired manifest is not bound to active release")
		}
		manifest.ActivationPending = ledger.Releases[manifest.ReleaseNonce].State == "activation_in_progress"
	}
	return manifest, nil
}

func verifyCandidateLedger(manifest CandidateManifest, signedBytes []byte) error {
	return verifyCandidateLedgerMode(manifest, signedBytes, false)
}

func verifyCandidateLedgerMode(manifest CandidateManifest, signedBytes []byte, active bool) error {
	if err := verifyReleaseStateDirectory(releaseStateDir); err != nil {
		return err
	}
	ledgerBytes, err := os.ReadFile(filepath.Join(releaseStateDir, "ledger.json"))
	if err != nil {
		return err
	}
	var ledger releaseLedger
	if strictJSON(ledgerBytes, &ledger) != nil || ledger.Version != 1 ||
		ledger.Identity.Environment != manifest.Environment || ledger.Identity.Deployment != manifest.DeploymentID ||
		ledger.Identity.DaemonID != manifest.DaemonID || ledger.Identity.LedgerID != manifest.LedgerID ||
		(!active && ledger.Sequence != manifest.Sequence) || (active && ledger.Sequence < manifest.Sequence) {
		return errors.New("ledger identity mismatch")
	}
	if active {
		if err := verifyCandidateLedgerRecord(manifest, signedBytes, ledger, releaseOwner{}, true); err != nil {
			return err
		}
		if ledger.Releases[manifest.ReleaseNonce].State == "activation_in_progress" {
			ownerBytes, err := os.ReadFile(filepath.Join(releaseStateDir, "owner.json"))
			if err != nil {
				return errors.New("pending activation holder marker unavailable")
			}
			var owner releaseOwner
			if strictJSON(ownerBytes, &owner) != nil || owner.ReleaseNonce != manifest.ReleaseNonce || owner.Identity != ledger.Identity || owner.PID <= 0 {
				return errors.New("pending activation holder mismatch")
			}
			live, err := readLiveReleaseLedger(owner, ledger.Revision)
			if err != nil {
				return errors.New("pending activation lock-holder unavailable")
			}
			return verifyCandidateLedgerRecord(manifest, signedBytes, live, releaseOwner{}, true)
		}
		return nil
	}
	ownerBytes, err := os.ReadFile(filepath.Join(releaseStateDir, "owner.json"))
	if err != nil {
		return err
	}
	var owner releaseOwner
	if strictJSON(ownerBytes, &owner) != nil {
		return errors.New("release lock owner mismatch")
	}
	if err := verifyCandidateLedgerRecord(manifest, signedBytes, ledger, owner, false); err != nil {
		return err
	}
	liveLedger, err := readLiveReleaseLedger(owner, ledger.Revision)
	if err != nil {
		return err
	}
	return verifyCandidateLedgerRecord(manifest, signedBytes, liveLedger, owner, false)
}

func readLiveReleaseLedger(owner releaseOwner, revision uint64) (releaseLedger, error) {
	socketPath := filepath.Join(releaseStateDir, "ledger.sock")
	if err := verifyReleaseIPCPath(releaseStateDir, socketPath); err != nil {
		return releaseLedger{}, errors.New("live release lock IPC is unavailable")
	}
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return releaseLedger{}, errors.New("live release lock connection failed")
	}
	defer connection.Close()
	if err := verifyReleaseIPCPath(releaseStateDir, socketPath); err != nil {
		return releaseLedger{}, errors.New("live release lock socket changed")
	}
	peerPID, peerUID, peerGID, err := unixPeerCredentials(connection)
	if err != nil || peerPID != owner.PID || peerUID != 65532 || peerGID != 65532 {
		return releaseLedger{}, errors.New("live release lock peer mismatch")
	}
	requestBytes, err := json.Marshal(ledgerIPCRequest{
		Identity: owner.Identity, OwnerAttempt: owner.OwnerAttempt, ReleaseNonce: owner.ReleaseNonce,
		ExpectedRevision: revision, Action: "read",
	})
	if err != nil {
		return releaseLedger{}, errors.New("live release ledger request encoding failed")
	}
	if _, err := connection.Write(requestBytes); err != nil || connection.CloseWrite() != nil {
		return releaseLedger{}, errors.New("live release ledger request failed")
	}
	responseBytes, err := io.ReadAll(io.LimitReader(connection, 1<<20))
	if err != nil {
		return releaseLedger{}, errors.New("live release ledger response failed")
	}
	var response ledgerIPCResponse
	if strictJSON(responseBytes, &response) != nil || !response.OK || response.Ledger == nil || response.Revision != revision {
		return releaseLedger{}, errors.New("live release ledger response rejected")
	}
	return *response.Ledger, nil
}

func verifyCandidateLedgerRecord(manifest CandidateManifest, signedBytes []byte, ledger releaseLedger, owner releaseOwner, active bool) error {
	record, ok := ledger.Releases[manifest.ReleaseNonce]
	digest := sha256.Sum256(signedBytes)
	wantState := "staging"
	activeGenerationMatches := ledger.ActiveGeneration == manifest.BaseActive
	if active {
		wantState = "activated"
		activeGenerationMatches = ledger.ActiveGeneration == manifest.Generation
		if pending, found := ledger.Releases[manifest.ReleaseNonce]; found && pending.State == "activation_in_progress" && ledger.ActiveGeneration == manifest.BaseActive {
			proof, proofExists := pending.Proofs["intake"]
			if !proofExists || !proof.Consumed {
				return errors.New("pending activation is not bound to a consumed proof")
			}
			wantState = "activation_in_progress"
			activeGenerationMatches = true
		}
	}
	if ledger.Version != 1 || (!active && ledger.Sequence != manifest.Sequence) || (active && ledger.Sequence < manifest.Sequence) ||
		ledger.Identity.Environment != manifest.Environment || ledger.Identity.Deployment != manifest.DeploymentID ||
		ledger.Identity.DaemonID != manifest.DaemonID || ledger.Identity.LedgerID != manifest.LedgerID ||
		!ok || record.State != wantState || record.Generation != manifest.Generation || record.Nonce != manifest.ReleaseNonce ||
		record.Sequence != manifest.Sequence || record.BaseActive != manifest.BaseActive || record.Target != manifest.Target ||
		record.ImageDigest != manifest.ImageDigest || !activeGenerationMatches ||
		record.CandidateManifestName != "candidate-"+manifest.ReleaseNonce+".json" ||
		record.CandidateManifestSHA256 != "sha256:"+hex.EncodeToString(digest[:]) {
		return errors.New("ledger candidate binding mismatch")
	}
	if !active && (owner.ReleaseNonce != manifest.ReleaseNonce || owner.Identity != ledger.Identity || owner.PID <= 0) {
		return errors.New("release lock owner mismatch")
	}
	return nil
}
