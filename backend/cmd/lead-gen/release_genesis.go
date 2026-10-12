package main

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"lead-gen/backend/platform/migrate"
)

const (
	observedG1Purpose     = "lead-gen-observed-legacy-g1"
	initialReleaseKind    = "initial-g1-to-g15"
	initialBaselineKind   = "observed-legacy-g1"
	initialCandidateV3    = 3
	genesisLedgerVersion  = 2
	maxGenesisValidity    = 15 * time.Minute
	maxIPCRequestPayload  = 1 << 20
	releaseControlKeyPath = "/run/leadgen-release-control/active/control.key"
)

const (
	releaseControlSocketName = "ledger-control.sock"
	releaseReadSocketName    = "ledger-read.sock"
)

// ObservedG1EnrollmentV1 — signed inventory of the actual legacy deployment.
// It contains identities and catalog facts only; credentials and DSNs have no
// fields in this wire type.
type ObservedG1EnrollmentV1 struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	Purpose         string              `json:"purpose"`
	Kind            string              `json:"kind"`
	Environment     string              `json:"environment"`
	DeploymentID    string              `json:"deploymentId"`
	DockerEndpoint  string              `json:"dockerEndpoint"`
	DockerEngineID  string              `json:"dockerEngineId"`
	DaemonID        string              `json:"daemonId"`
	LedgerID        string              `json:"ledgerId"`
	ReleaseNonce    string              `json:"releaseNonce"`
	Generation      string              `json:"generation"`
	PlanSHA256      string              `json:"planSha256"`
	ComposeProject  string              `json:"composeProject"`
	Intake          ObservedContainer   `json:"intake"`
	OtherContainers []ObservedContainer `json:"otherContainers"`
	GreenDatabase   ObservedGreenDB     `json:"greenDatabase"`
	Schemas         []ObservedSchema    `json:"schemas"`
	ObservedAt      string              `json:"observedAt"`
	ExpiresAt       string              `json:"expiresAt"`
	KeyID           string              `json:"keyId"`
}

type ObservedContainer struct {
	Service     string   `json:"service"`
	ContainerID string   `json:"containerId"`
	ImageDigest string   `json:"imageDigest"`
	Networks    []string `json:"networks"`
	Mounts      []string `json:"mounts"`
}

type ObservedGreenDB struct {
	ContainerID   string `json:"containerId"`
	ImageDigest   string `json:"imageDigest"`
	VolumeName    string `json:"volumeName"`
	DatabaseName  string `json:"databaseName"`
	CatalogLogin  string `json:"catalogLogin"`
	PostgresMajor int    `json:"postgresMajor"`
	SystemID      string `json:"systemId"`
}

type ObservedSchema struct {
	Name       string   `json:"name"`
	Versions   []string `json:"versions"`
	OwnerRoles []string `json:"ownerRoles"`
	LoginRoles []string `json:"loginRoles"`
	ACLRef     string   `json:"aclRef"`
}

type signedEnrollment struct {
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

// InitialCandidateManifestV3 является отдельным wire type. Он намеренно не
// приводится к CandidateManifest v2: отсутствие BaseActive не маскируется
// выдуманным поколением G1.
type InitialCandidateManifestV3 struct {
	SchemaVersion int                              `json:"schemaVersion"`
	Purpose       string                           `json:"purpose"`
	ReleaseKind   string                           `json:"releaseKind"`
	Environment   string                           `json:"environment"`
	DeploymentID  string                           `json:"deploymentId"`
	DaemonID      string                           `json:"daemonId"`
	LedgerID      string                           `json:"ledgerId"`
	Target        string                           `json:"target"`
	Generation    string                           `json:"generation"`
	ReleaseNonce  string                           `json:"releaseNonce"`
	Sequence      uint64                           `json:"sequence"`
	Base          InitialG1Baseline                `json:"base"`
	PlanSHA256    string                           `json:"planSha256"`
	EnrollmentSHA string                           `json:"enrollmentSha256"`
	BackupSHA     string                           `json:"backupEvidenceSha256"`
	ImageDigest   string                           `json:"imageDigest"`
	RuntimeImage  string                           `json:"runtimeImageDigest"`
	Execution     string                           `json:"executionProtocol"`
	Roles         map[string]CandidateRoleSet      `json:"roles"`
	Policies      map[string]CandidateTargetPolicy `json:"targetPolicies"`
	IssuedAt      string                           `json:"issuedAt"`
	ExpiresAt     string                           `json:"expiresAt"`
	KeyID         string                           `json:"keyId"`
}

type InitialG1Baseline struct {
	Kind             string `json:"kind"`
	EnrollmentSHA256 string `json:"enrollmentSha256"`
}

type genesisLedger struct {
	Version  int                             `json:"version"`
	Identity releaseLedgerIdentity           `json:"identity"`
	Revision uint64                          `json:"revision"`
	Sequence uint64                          `json:"sequence"`
	Genesis  *genesisRecord                  `json:"genesis"`
	Releases map[string]genesisReleaseRecord `json:"releases"`
}

type genesisRecord struct {
	Kind          string `json:"kind"`
	EnrollmentSHA string `json:"enrollmentSha256"`
	ReleaseNonce  string `json:"releaseNonce"`
	CreatedAt     string `json:"createdAt"`
}

type genesisReleaseRecord struct {
	Generation         string            `json:"generation"`
	Nonce              string            `json:"nonce"`
	Sequence           uint64            `json:"sequence"`
	ReleaseKind        string            `json:"releaseKind"`
	Base               InitialG1Baseline `json:"base"`
	State              string            `json:"state"`
	DBMutationStarted  bool              `json:"db_mutation_started"`
	WriteEnableStarted bool              `json:"write_enable_started"`
}

func canonicalObservedG1Enrollment(e ObservedG1EnrollmentV1) ([]byte, error) {
	if e.SchemaVersion != 1 || e.Purpose != observedG1Purpose || e.Kind != initialBaselineKind ||
		!candidateIDPattern.MatchString(e.Environment) || !candidateIDPattern.MatchString(e.DeploymentID) ||
		!validLocalDockerEndpoint(e.DockerEndpoint) || !candidateIDPattern.MatchString(e.DockerEngineID) ||
		!releaseDigestPattern.MatchString(e.DaemonID) || !candidateIDPattern.MatchString(e.LedgerID) ||
		!candidateIDPattern.MatchString(e.ReleaseNonce) || !candidateIDPattern.MatchString(e.Generation) ||
		!validSHA256Hex(e.PlanSHA256) || !candidateIDPattern.MatchString(e.ComposeProject) ||
		!candidateIDPattern.MatchString(e.KeyID) || len(e.OtherContainers) == 0 || len(e.Schemas) == 0 {
		return nil, errors.New("G1 enrollment: required observed scope missing")
	}
	if err := validateObservedContainer(e.Intake, "intake"); err != nil {
		return nil, err
	}
	for i, c := range e.OtherContainers {
		if c.Service == "intake" || (i > 0 && e.OtherContainers[i-1].Service >= c.Service) {
			return nil, errors.New("G1 enrollment: other containers must be sorted and unique")
		}
		if err := validateObservedContainer(c, c.Service); err != nil {
			return nil, err
		}
	}
	db := e.GreenDatabase
	if db.ContainerID == "" || db.ImageDigest == "" || db.VolumeName == "" || db.DatabaseName == "" ||
		db.CatalogLogin == "" || db.PostgresMajor != 17 || db.SystemID == "" {
		return nil, errors.New("G1 enrollment: observed green database identity incomplete")
	}
	for i, schema := range e.Schemas {
		if schema.Name == "" || (i > 0 && e.Schemas[i-1].Name >= schema.Name) || len(schema.Versions) == 0 ||
			!strictlySorted(schema.Versions) || !strictlySorted(schema.OwnerRoles) || !strictlySorted(schema.LoginRoles) || !validSHA256Hex(schema.ACLRef) {
			return nil, errors.New("G1 enrollment: exact sorted schema/role inventory required")
		}
	}
	observed, err := time.Parse(time.RFC3339, e.ObservedAt)
	expires, expErr := time.Parse(time.RFC3339, e.ExpiresAt)
	if err != nil || expErr != nil || observed.After(time.Now().UTC().Add(time.Minute)) ||
		!expires.After(observed) || expires.Sub(observed) > maxGenesisValidity {
		return nil, errors.New("G1 enrollment: observation validity rejected")
	}
	return json.Marshal(e)
}

func validLocalDockerEndpoint(endpoint string) bool {
	if !strings.HasPrefix(endpoint, "unix:///") || strings.ContainsAny(endpoint, "?#") || strings.Contains(endpoint, "..") {
		return false
	}
	return len(endpoint) <= 256
}

func validateObservedContainer(c ObservedContainer, service string) error {
	if c.Service != service || !candidateIDPattern.MatchString(c.ContainerID) || !releaseDigestPattern.MatchString(c.ImageDigest) ||
		len(c.Networks) == 0 || !strictlySorted(c.Networks) || !strictlySorted(c.Mounts) {
		return errors.New("G1 enrollment: exact container/image/mount/network facts required")
	}
	for _, mount := range c.Mounts {
		if mount == "" || strings.Contains(mount, "password=") || strings.Contains(mount, "dsn=") {
			return errors.New("G1 enrollment: credential-bearing mount fact rejected")
		}
	}
	return nil
}

func strictlySorted(values []string) bool {
	for i, value := range values {
		if value == "" || (i > 0 && values[i-1] >= value) {
			return false
		}
	}
	return true
}

func signObservedG1Enrollment(e ObservedG1EnrollmentV1, key ed25519.PrivateKey) ([]byte, error) {
	canonical, err := canonicalObservedG1Enrollment(e)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("G1 enrollment: signing input rejected")
	}
	encoded, err := json.Marshal(signedEnrollment{Payload: canonical, Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, canonical))})
	if err != nil {
		return nil, errors.New("G1 enrollment: envelope encoding failed")
	}
	return append(encoded, '\n'), nil
}

func verifyObservedG1Enrollment(data []byte, key ed25519.PublicKey, keyID string, now time.Time, expected ObservedG1EnrollmentV1) (ObservedG1EnrollmentV1, error) {
	var envelope signedEnrollment
	var enrollment ObservedG1EnrollmentV1
	if len(data) > maxIPCRequestPayload || strictJSON(data, &envelope) != nil || strictJSON(envelope.Payload, &enrollment) != nil {
		return enrollment, errors.New("G1 enrollment: malformed signed snapshot")
	}
	canonical, err := canonicalObservedG1Enrollment(enrollment)
	sig, decodeErr := base64.RawStdEncoding.DecodeString(envelope.Signature)
	expires, _ := time.Parse(time.RFC3339, enrollment.ExpiresAt)
	if err != nil || decodeErr != nil || !equalBytes(canonical, envelope.Payload) || len(key) != ed25519.PublicKeySize ||
		!ed25519.Verify(key, canonical, sig) || enrollment.KeyID != keyID || !expires.After(now) ||
		enrollment.Environment != expected.Environment || enrollment.DeploymentID != expected.DeploymentID ||
		enrollment.DockerEndpoint != expected.DockerEndpoint || enrollment.DockerEngineID != expected.DockerEngineID ||
		enrollment.DaemonID != expected.DaemonID || enrollment.LedgerID != expected.LedgerID ||
		enrollment.ReleaseNonce != expected.ReleaseNonce || enrollment.Generation != expected.Generation ||
		enrollment.PlanSHA256 != expected.PlanSHA256 || enrollment.ComposeProject != expected.ComposeProject {
		return ObservedG1EnrollmentV1{}, errors.New("G1 enrollment: signature, freshness or expected deployment scope rejected")
	}
	return enrollment, nil
}

func initialEnrollmentDigest(signed []byte) string {
	digest := sha256.Sum256(signed)
	return hex.EncodeToString(digest[:])
}

func canonicalInitialCandidateV3(m InitialCandidateManifestV3) ([]byte, error) {
	if m.SchemaVersion != initialCandidateV3 || m.Purpose != candidateManifestPurpose || m.ReleaseKind != initialReleaseKind ||
		!candidateIDPattern.MatchString(m.Environment) || !candidateIDPattern.MatchString(m.DeploymentID) ||
		!releaseDigestPattern.MatchString(m.DaemonID) || !candidateIDPattern.MatchString(m.LedgerID) || m.Target != "intake" ||
		!candidateIDPattern.MatchString(m.Generation) || !candidateIDPattern.MatchString(m.ReleaseNonce) || m.Sequence != 1 ||
		m.Base.Kind != initialBaselineKind || !validSHA256Hex(m.Base.EnrollmentSHA256) || m.Base.EnrollmentSHA256 != m.EnrollmentSHA ||
		!validSHA256Hex(m.PlanSHA256) || !validSHA256Hex(m.EnrollmentSHA) || !validSHA256Hex(m.BackupSHA) ||
		!releaseDigestPattern.MatchString(m.ImageDigest) || !releaseDigestPattern.MatchString(m.RuntimeImage) || m.Execution != "v1" ||
		!candidateIDPattern.MatchString(m.KeyID) || len(m.Roles) != len(dbTargets) || len(m.Policies) != len(dbTargets) {
		return nil, errors.New("initial candidate v3: schema, release kind, typed G1 baseline or scope rejected")
	}
	// Initial candidate policy is exact and independent of the ordinary v2 path.
	for target := range dbTargets {
		roles, ok := m.Roles[target]
		policy, policyOK := m.Policies[target]
		if !ok || !policyOK || roles.Owner == "" || roles.Migrator == "" || len(roles.Runtime) == 0 {
			return nil, errors.New("initial candidate v3: complete three-target role/policy registry required")
		}
		roleStem := "leadgen_" + strings.ReplaceAll(target, "-", "_")
		if roles.Owner != roleStem+"_owner" || roles.Migrator != roleStem+"_migrator" || roles.Owner == roles.Migrator {
			return nil, errors.New("initial candidate v3: owner/migrator identity is not canonical")
		}
		runtimeTargets := map[string]string{target: target}
		if target == "integration-platform" {
			runtimeTargets = map[string]string{"vector": "integration-platform", "integration-platform": "integration-platform-full-runtime"}
		}
		if len(roles.Runtime) != len(runtimeTargets) {
			return nil, errors.New("initial candidate v3: runtime role set is incomplete")
		}
		for roleKey, runtimeTarget := range runtimeTargets {
			want, roleErr := migrate.RuntimeRoleName(migrate.RuntimeRoleScope{Environment: m.Environment, Deployment: m.DeploymentID, Target: runtimeTarget, Generation: m.Generation})
			if roleErr != nil || roles.Runtime[roleKey] != want {
				return nil, errors.New("initial candidate v3: runtime role differs from signed scope")
			}
		}
		approved := additiveReleaseMigrations[target]
		wantPolicy := "g1-additive-v1"
		if target == "request-fulfillment" {
			approved, wantPolicy = g15RequestMigrations, g15MigrationPolicyVersion
		}
		if policy.PolicyVersion != wantPolicy || len(policy.Migrations) != len(approved) {
			return nil, errors.New("initial candidate v3: migration policy differs from pinned allowlist")
		}
		for i, item := range approved {
			if policy.Migrations[i].Name != item.Name || policy.Migrations[i].SHA256 != item.SHA256 {
				return nil, errors.New("initial candidate v3: migration hash differs from exact policy")
			}
		}
	}
	issued, err := time.Parse(time.RFC3339, m.IssuedAt)
	expires, expErr := time.Parse(time.RFC3339, m.ExpiresAt)
	if err != nil || expErr != nil || !expires.After(issued) || expires.Sub(issued) > time.Hour {
		return nil, errors.New("initial candidate v3: validity window rejected")
	}
	return json.Marshal(m)
}

func signInitialCandidateV3(m InitialCandidateManifestV3, key ed25519.PrivateKey) ([]byte, error) {
	canonical, err := canonicalInitialCandidateV3(m)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("initial candidate v3: signing input rejected")
	}
	data, err := json.Marshal(signedCandidateManifest{Payload: canonical, Signature: base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, canonical))})
	if err != nil {
		return nil, errors.New("initial candidate v3: encoding failed")
	}
	return append(data, '\n'), nil
}

func verifyInitialCandidateV3(data []byte, key ed25519.PublicKey, keyID string, now time.Time) (InitialCandidateManifestV3, error) {
	var envelope signedCandidateManifest
	var manifest InitialCandidateManifestV3
	if len(data) > maxIPCRequestPayload || strictJSON(data, &envelope) != nil || strictJSON(envelope.Payload, &manifest) != nil {
		return manifest, errors.New("initial candidate v3: malformed envelope")
	}
	canonical, err := canonicalInitialCandidateV3(manifest)
	sig, decodeErr := base64.RawStdEncoding.DecodeString(envelope.Signature)
	expires, _ := time.Parse(time.RFC3339, manifest.ExpiresAt)
	if err != nil || decodeErr != nil || !equalBytes(canonical, envelope.Payload) || len(key) != ed25519.PublicKeySize ||
		!ed25519.Verify(key, canonical, sig) || manifest.KeyID != keyID || !expires.After(now) {
		return InitialCandidateManifestV3{}, errors.New("initial candidate v3: signature, trust or expiry rejected")
	}
	return manifest, nil
}

// bootstrapGenesis создает baseline и первую release запись только в пустом v2
// ledger. В частности, G1 никогда не становится signed active manifest.
func bootstrapGenesis(current *genesisLedger, identity releaseLedgerIdentity, enrollment ObservedG1EnrollmentV1, signedEnrollmentBytes []byte, publicKey ed25519.PublicKey, keyID, nonce, generation string, now time.Time) (genesisLedger, error) {
	if current != nil || len(signedEnrollmentBytes) == 0 || !safeReleaseID.MatchString(nonce) || !safeReleaseID.MatchString(generation) ||
		enrollment.Kind != initialBaselineKind || enrollment.ReleaseNonce != nonce || enrollment.Generation != generation ||
		enrollment.Environment != identity.Environment || enrollment.DeploymentID != identity.Deployment || enrollment.DaemonID != identity.DaemonID ||
		enrollment.LedgerID != identity.LedgerID {
		return genesisLedger{}, errors.New("release genesis: nonempty ledger or enrollment scope mismatch")
	}
	if _, err := canonicalObservedG1Enrollment(enrollment); err != nil {
		return genesisLedger{}, err
	}
	verified, err := verifyObservedG1Enrollment(signedEnrollmentBytes, publicKey, keyID, now, enrollment)
	if err != nil {
		return genesisLedger{}, errors.New("release genesis: signed observed G1 enrollment rejected")
	}
	verifiedBytes, _ := canonicalObservedG1Enrollment(verified)
	enrollmentBytes, _ := canonicalObservedG1Enrollment(enrollment)
	if !equalBytes(verifiedBytes, enrollmentBytes) {
		return genesisLedger{}, errors.New("release genesis: supplied inventory differs from signed enrollment")
	}
	digest := initialEnrollmentDigest(signedEnrollmentBytes)
	baseline := InitialG1Baseline{Kind: initialBaselineKind, EnrollmentSHA256: digest}
	record := genesisReleaseRecord{Generation: generation, Nonce: nonce, Sequence: 1, ReleaseKind: initialReleaseKind, Base: baseline, State: "new"}
	return genesisLedger{Version: genesisLedgerVersion, Identity: identity, Revision: 1, Sequence: 1,
		Genesis:  &genesisRecord{Kind: initialBaselineKind, EnrollmentSHA: digest, ReleaseNonce: nonce, CreatedAt: now.UTC().Format(time.RFC3339)},
		Releases: map[string]genesisReleaseRecord{nonce: record}}, nil
}

func validateGenesisLedger(l genesisLedger) error {
	if l.Version != genesisLedgerVersion || l.Genesis == nil || l.Genesis.Kind != initialBaselineKind ||
		!validSHA256Hex(l.Genesis.EnrollmentSHA) || l.Genesis.ReleaseNonce == "" || l.Releases == nil ||
		l.Sequence == 0 || l.Revision == 0 {
		return errors.New("release genesis: typed v2 genesis record required")
	}
	first, ok := l.Releases[l.Genesis.ReleaseNonce]
	if !ok || first.ReleaseKind != initialReleaseKind || first.Base.Kind != initialBaselineKind ||
		first.Base.EnrollmentSHA256 != l.Genesis.EnrollmentSHA || first.Sequence != 1 || first.Nonce != l.Genesis.ReleaseNonce {
		return errors.New("release genesis: initial release is not bound to the unique G1 enrollment")
	}
	return nil
}

func validateGenesisMonotonic(old, next genesisReleaseRecord) error {
	if old.Nonce != next.Nonce || old.Generation != next.Generation || old.ReleaseKind != initialReleaseKind || next.ReleaseKind != old.ReleaseKind ||
		old.Base != next.Base || (old.DBMutationStarted && !next.DBMutationStarted) || (old.WriteEnableStarted && !next.WriteEnableStarted) {
		return errors.New("release genesis: identity changed or monotonic marker cleared")
	}
	return nil
}

// HMAC control protocol: canonical JSON covers scope, revision, action, payload,
// challenge and holder epoch. Used challenges live for one holder epoch.
type releaseControlRequest struct {
	Identity         releaseLedgerIdentity `json:"identity"`
	OwnerAttempt     string                `json:"ownerAttempt"`
	ReleaseNonce     string                `json:"releaseNonce"`
	Epoch            string                `json:"epoch"`
	ExpectedRevision uint64                `json:"expectedRevision"`
	Action           string                `json:"action"`
	Challenge        string                `json:"challenge"`
	Payload          json.RawMessage       `json:"payload"`
	MAC              string                `json:"mac"`
}

type controlScope struct {
	Identity     releaseLedgerIdentity `json:"identity"`
	OwnerAttempt string                `json:"ownerAttempt"`
	ReleaseNonce string                `json:"releaseNonce"`
	Epoch        string                `json:"epoch"`
}

// releaseReadRequest не содержит mutation action, arbitrary payload или
// credential. Read endpoint принимает только свежий challenge и точную scope.
type releaseReadRequest struct {
	Identity        releaseLedgerIdentity `json:"identity"`
	OwnerAttempt    string                `json:"ownerAttempt"`
	ReleaseNonce    string                `json:"releaseNonce"`
	Epoch           string                `json:"epoch"`
	Challenge       string                `json:"challenge"`
	MinimumRevision uint64                `json:"minimumRevision"`
}

func validateReadRequest(req releaseReadRequest, scope controlScope, currentRevision uint64) error {
	if req.Identity != scope.Identity || req.OwnerAttempt != scope.OwnerAttempt || req.ReleaseNonce != scope.ReleaseNonce ||
		req.Epoch != scope.Epoch || !validChallenge(req.Challenge) || req.MinimumRevision > currentRevision {
		return errors.New("release read: exact scope, fresh challenge or revision rejected")
	}
	return nil
}

func controlMAC(key []byte, request releaseControlRequest) ([]byte, error) {
	if len(key) != 32 || !validChallenge(request.Challenge) || len(request.Payload) > maxIPCRequestPayload ||
		request.Action == "" || request.OwnerAttempt == "" || request.ReleaseNonce == "" || request.Epoch == "" ||
		!validLedgerIdentity(request.Identity) {
		return nil, errors.New("release control: invalid scoped request")
	}
	request.MAC = ""
	canonical, err := json.Marshal(request)
	if err != nil {
		return nil, errors.New("release control: canonical encoding failed")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(canonical)
	return mac.Sum(nil), nil
}

func signControlRequest(key []byte, request *releaseControlRequest) error {
	if request == nil {
		return errors.New("release control: request missing")
	}
	mac, err := controlMAC(key, *request)
	if err != nil {
		return err
	}
	request.MAC = base64.RawStdEncoding.EncodeToString(mac)
	return nil
}

type challengeRegistry struct {
	mu   sync.Mutex
	used map[string]struct{}
}

func newChallengeRegistry() *challengeRegistry {
	return &challengeRegistry{used: make(map[string]struct{})}
}

func (r *challengeRegistry) verifyAndConsume(key []byte, req releaseControlRequest, scope controlScope, revision uint64, lockHeld bool) error {
	if r == nil || !lockHeld || req.Identity != scope.Identity || req.OwnerAttempt != scope.OwnerAttempt ||
		req.ReleaseNonce != scope.ReleaseNonce || req.Epoch != scope.Epoch || req.ExpectedRevision != revision {
		return errors.New("release control: holder lock or exact scope/revision rejected")
	}
	provided, err := base64.RawStdEncoding.DecodeString(req.MAC)
	expected, macErr := controlMAC(key, req)
	if err != nil || macErr != nil || !hmac.Equal(provided, expected) {
		return errors.New("release control: capability MAC rejected")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.used[req.Challenge]; exists {
		return errors.New("release control: challenge replay rejected")
	}
	r.used[req.Challenge] = struct{}{}
	return nil
}

func validChallenge(challenge string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(challenge)
	return err == nil && len(decoded) == 32
}

func newReleaseChallenge() (string, error) {
	challenge := make([]byte, 32)
	if _, err := rand.Read(challenge); err != nil {
		return "", errors.New("release IPC: fresh challenge unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(challenge), nil
}

type signedLedgerSnapshot struct {
	Scope     controlScope    `json:"scope"`
	Challenge string          `json:"challenge"`
	Revision  uint64          `json:"revision"`
	IssuedAt  string          `json:"issuedAt"`
	ExpiresAt string          `json:"expiresAt"`
	Ledger    json.RawMessage `json:"ledger"`
	Signature string          `json:"signature"`
}

func signReadSnapshot(scope controlScope, challenge string, revision uint64, ledger json.RawMessage, expiry time.Time, key ed25519.PrivateKey) (signedLedgerSnapshot, error) {
	if !validChallenge(challenge) || revision == 0 || len(ledger) == 0 || len(ledger) > maxIPCRequestPayload ||
		len(key) != ed25519.PrivateKeySize || scope.Epoch == "" || !validLedgerIdentity(scope.Identity) {
		return signedLedgerSnapshot{}, errors.New("release read: snapshot scope rejected")
	}
	snapshot := signedLedgerSnapshot{Scope: scope, Challenge: challenge, Revision: revision,
		IssuedAt: time.Now().UTC().Format(time.RFC3339Nano), ExpiresAt: expiry.UTC().Format(time.RFC3339Nano), Ledger: ledger}
	canonical, err := canonicalReadSnapshot(snapshot)
	if err != nil {
		return signedLedgerSnapshot{}, err
	}
	snapshot.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key, canonical))
	return snapshot, nil
}

func canonicalReadSnapshot(s signedLedgerSnapshot) ([]byte, error) {
	if !validChallenge(s.Challenge) || s.Revision == 0 || s.Scope.Epoch == "" || len(s.Ledger) == 0 {
		return nil, errors.New("release read: incomplete signed snapshot")
	}
	s.Signature = ""
	return json.Marshal(s)
}

func verifyReadSnapshot(s signedLedgerSnapshot, key ed25519.PublicKey, expectedScope controlScope, challenge string, minRevision uint64, now time.Time) ([]byte, error) {
	canonical, err := canonicalReadSnapshot(s)
	sig, sigErr := base64.RawStdEncoding.DecodeString(s.Signature)
	expires, expErr := time.Parse(time.RFC3339Nano, s.ExpiresAt)
	issued, issuedErr := time.Parse(time.RFC3339Nano, s.IssuedAt)
	if err != nil || sigErr != nil || expErr != nil || issuedErr != nil || len(key) != ed25519.PublicKeySize ||
		!ed25519.Verify(key, canonical, sig) || s.Scope != expectedScope || s.Challenge != challenge ||
		!validChallenge(challenge) || s.Revision < minRevision || !expires.After(now) || issued.After(now.Add(time.Minute)) ||
		expires.Sub(issued) > time.Minute {
		return nil, errors.New("release read: signature, challenge, epoch, revision or freshness rejected")
	}
	return append([]byte(nil), s.Ledger...), nil
}

func genesisLedgerDigest(l genesisLedger) (string, error) {
	if err := validateGenesisLedger(l); err != nil {
		return "", err
	}
	data, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}

// Hook fail-closed: typed протокол пока не подключён к внешнему runner/state writer.
func runInitialGenesisHelper(args []string) error {
	if len(args) != 0 {
		return errors.New("release initial G1 bootstrap: unexpected arguments")
	}
	return errors.New("release initial G1 bootstrap: typed holder integration is not enabled")
}

func sortedSchemaNames(schemas []ObservedSchema) {
	sort.Slice(schemas, func(i, j int) bool { return schemas[i].Name < schemas[j].Name })
}
