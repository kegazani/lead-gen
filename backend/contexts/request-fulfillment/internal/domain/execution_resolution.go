package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type ExecutionResolutionKind string

const (
	ResolutionTarget             ExecutionResolutionKind = "target"
	ResolutionRuntimeObservation ExecutionResolutionKind = "runtime"
)

type RuntimeResolutionOutcome string

const (
	RuntimeResolutionCompleted RuntimeResolutionOutcome = "completed"
	RuntimeResolutionFailed    RuntimeResolutionOutcome = "failed"
	RuntimeResolutionCanceled  RuntimeResolutionOutcome = "canceled"
)

var ErrInvalidExecutionResolution = errors.New("domain: некорректное разрешение исполнения")

// ExecutionResolution — неизменяемая аудиторская запись. Raw evidence не
// сохраняется: хранится только SHA-256 fingerprint доказательства.
type ExecutionResolution struct {
	ResolutionID        string
	Revision            int64
	ExpectedRevision    int64
	SupersedesID        string
	TenantID            TenantID
	RequestID           RequestID
	Kind                ExecutionResolutionKind
	Generation          int64
	AttemptID           string
	Target              LeadTarget
	ObservedOutcome     RuntimeResolutionOutcome
	ObservedRunID       string
	Actor               string
	ResolvedAt          time.Time
	EvidenceFingerprint string
}

// TerminalIntentEvidence mirrors the immutable, PII-free workflow marker. It is
// stored with a verified resolution so the original terminal cause survives a
// restart. A checksum is an integrity check only; it is not proof that the
// marker came from Temporal. Construction is restricted to the application
// verifier path, and the writer itself is only available inside a UoW.
type TerminalIntentEvidence struct {
	Version        int
	WorkflowID     string
	RunID          string
	RequestID      string
	TenantID       string
	AttemptID      string
	Generation     int64
	CommandID      string
	ReasonCode     string
	OriginalReason string
	Checksum       string
}

// VerifiedRuntimeResolution is opaque to storage callers. The constructor
// validates the complete payload binding and checksum; only a verified
// application result should be passed to it. There is intentionally no
// exported Verified flag or setter.
type VerifiedRuntimeResolution struct {
	resolution ExecutionResolution
	evidence   TerminalIntentEvidence
}

func NewVerifiedRuntimeResolution(resolution ExecutionResolution, evidence TerminalIntentEvidence) (VerifiedRuntimeResolution, error) {
	evidenceBytes, _ := json.Marshal(evidence)
	if resolution.Kind != ResolutionRuntimeObservation || resolution.Validate() != nil ||
		resolution.ObservedOutcome == RuntimeResolutionCompleted || evidence.Version != 1 ||
		evidence.WorkflowID == "" || evidence.RunID == "" || evidence.RequestID == "" || evidence.TenantID == "" ||
		evidence.AttemptID == "" || evidence.Generation < 1 || evidence.CommandID == "" ||
		evidence.OriginalReason == "" || !validTerminalReasonCode(evidence.ReasonCode) ||
		evidence.TenantID != resolution.TenantID.String() || evidence.RequestID != resolution.RequestID.String() ||
		evidence.Generation != resolution.Generation || evidence.AttemptID != resolution.AttemptID ||
		evidence.RunID != resolution.ObservedRunID || evidence.WorkflowID != "fulfillment-"+evidence.RequestID ||
		evidence.Checksum == "" || evidence.Checksum != terminalIntentChecksum(evidence) ||
		resolution.EvidenceFingerprint != FingerprintSafeEvidence(evidenceBytes) {
		return VerifiedRuntimeResolution{}, fmt.Errorf("%w: проверенный terminal payload не совпадает с runtime resolution", ErrInvalidExecutionResolution)
	}
	return VerifiedRuntimeResolution{resolution: resolution, evidence: evidence}, nil
}

func (r VerifiedRuntimeResolution) Resolution() ExecutionResolution  { return r.resolution }
func (r VerifiedRuntimeResolution) Evidence() TerminalIntentEvidence { return r.evidence }

// WithRevision returns the same verified evidence with adapter-assigned CAS
// revision metadata. It cannot change any proof-bound identity or payload.
func (r VerifiedRuntimeResolution) WithRevision(revision int64, supersedesID string) VerifiedRuntimeResolution {
	r.resolution.Revision = revision
	r.resolution.SupersedesID = supersedesID
	return r
}

func terminalIntentChecksum(evidence TerminalIntentEvidence) string {
	evidence.Checksum = ""
	encoded, _ := json.Marshal(evidence)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func validTerminalReasonCode(code string) bool {
	switch code {
	case "search_failed", "draft_failed", "handoff_failed", "transition_rejected", "workflow_timed_out":
		return true
	default:
		return false
	}
}

func (r ExecutionResolution) Validate() error {
	if strings.TrimSpace(r.ResolutionID) == "" || r.Revision != 0 || r.ExpectedRevision < 0 || r.TenantID.validate() != nil || r.RequestID.validate() != nil ||
		strings.TrimSpace(r.Actor) == "" || r.ResolvedAt.IsZero() || !validEvidenceFingerprint(r.EvidenceFingerprint) {
		return fmt.Errorf("%w: обязательные audit-поля", ErrInvalidExecutionResolution)
	}
	switch r.Kind {
	case ResolutionTarget:
		if r.Generation != 0 || r.AttemptID != "" || r.ObservedRunID != "" || r.ObservedOutcome != "" || r.Target.State != LeadTargetExplicit {
			return fmt.Errorf("%w: target resolution должна ссылаться на legacy request и задавать explicit target", ErrInvalidExecutionResolution)
		}
		return r.Target.Validate()
	case ResolutionRuntimeObservation:
		if r.Generation < 1 || strings.TrimSpace(r.AttemptID) == "" || strings.TrimSpace(r.ObservedRunID) == "" || r.Target.State != "" ||
			(r.ObservedOutcome != RuntimeResolutionCompleted && r.ObservedOutcome != RuntimeResolutionFailed && r.ObservedOutcome != RuntimeResolutionCanceled) {
			return fmt.Errorf("%w: runtime observation требует attempt, generation, наблюдавшийся RunID и исход", ErrInvalidExecutionResolution)
		}
		return nil
	default:
		return fmt.Errorf("%w: неизвестный тип %q", ErrInvalidExecutionResolution, r.Kind)
	}
}

func validEvidenceFingerprint(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

// FingerprintSafeEvidence hashes bytes already canonicalized by the trusted
// application boundary. Callers must pass only non-PII evidence content or
// explicitly safe content-hash metadata; raw bytes are never persisted.
func FingerprintSafeEvidence(canonicalEvidence []byte) string {
	canonical := CanonicalizeEvidenceBytes(canonicalEvidence)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// CanonicalizeEvidenceBytes defines the v1 byte representation: a fixed domain
// separator followed by an unsigned big-endian byte length and the exact safe
// evidence bytes. Callers must select/sanitize evidence at the trusted
// application boundary; this encoding performs no PII storage or redaction.
func CanonicalizeEvidenceBytes(evidence []byte) []byte {
	canonical := make([]byte, len("request-fulfillment-evidence-v1\x00")+8+len(evidence))
	copy(canonical, "request-fulfillment-evidence-v1\x00")
	binary.BigEndian.PutUint64(canonical[len("request-fulfillment-evidence-v1\x00"):], uint64(len(evidence)))
	copy(canonical[len("request-fulfillment-evidence-v1\x00")+8:], evidence)
	return canonical
}
