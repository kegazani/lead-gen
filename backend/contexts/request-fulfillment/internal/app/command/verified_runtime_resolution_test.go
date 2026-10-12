package command

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

type staticTerminalVerifier struct {
	result ports.TerminalHistoryVerification
}

func (v staticTerminalVerifier) VerifyTerminalHistory(_ context.Context, id ports.TerminalResolutionIdentity) (ports.TerminalHistoryVerification, error) {
	if v.result.Intent.WorkflowID != id.WorkflowID || v.result.Intent.RunID != id.RunID {
		return ports.TerminalHistoryVerification{}, domain.ErrInvalidExecutionResolution
	}
	return v.result, nil
}

type fixedResolutionClock struct{ now time.Time }

func (c fixedResolutionClock) Now() time.Time { return c.now }

// These local ports deliberately model only the boundary exercised here. They
// keep an application-service test independent of infrastructure adapters.
type verifiedResolutionTestUOW struct {
	tx verifiedResolutionTestTransaction
}

func (u verifiedResolutionTestUOW) WithinTransaction(ctx context.Context, fn func(context.Context, ports.Transaction) error) error {
	return fn(ports.WithTransactionScope(ctx), u.tx)
}

type verifiedResolutionTestTransaction struct{ writer *verifiedResolutionTestWriter }

func (verifiedResolutionTestTransaction) Requests() ports.RequestRepository { return nil }
func (verifiedResolutionTestTransaction) Outbox() ports.Outbox              { return nil }
func (verifiedResolutionTestTransaction) ExecutionAttempts() ports.ExecutionAttemptRepository {
	return nil
}
func (verifiedResolutionTestTransaction) ExecutionResolutions() ports.ExecutionResolutionRepository {
	return nil
}
func (tx verifiedResolutionTestTransaction) VerifiedRuntimeResolutions() ports.VerifiedRuntimeResolutionWriter {
	return tx.writer
}

type verifiedResolutionTestWriter struct {
	currentRevision int64
	currentID       string
	terminal        bool
	appended        map[string]domain.VerifiedRuntimeResolution
	observations    []domain.ExecutionResolution
}

func newVerifiedResolutionTestWriter() *verifiedResolutionTestWriter {
	return &verifiedResolutionTestWriter{appended: make(map[string]domain.VerifiedRuntimeResolution)}
}

func (w *verifiedResolutionTestWriter) AppendVerifiedRuntimeResolution(_ context.Context, resolution domain.VerifiedRuntimeResolution) error {
	value := resolution.Resolution()
	if previous, ok := w.appended[value.ResolutionID]; ok {
		previousValue := previous.Resolution()
		previousValue.Revision = value.Revision
		if previousValue != value || previous.Evidence() != resolution.Evidence() {
			return ports.ErrExecutionResolutionConflict
		}
		return nil
	}
	if value.ExpectedRevision != w.currentRevision || value.SupersedesID != w.currentID {
		return ports.ErrExecutionResolutionConflict
	}
	w.currentRevision++
	w.currentID = value.ResolutionID
	w.terminal = true
	w.appended[value.ResolutionID] = resolution.WithRevision(w.currentRevision, value.SupersedesID)
	return nil
}

func (w *verifiedResolutionTestWriter) appendUnverifiedObservation(observation domain.ExecutionResolution) {
	// An ordinary observation is not accepted by the verified writer capability
	// and cannot alter its terminal/admission state.
	w.observations = append(w.observations, observation)
}

func TestVerifiedRuntimeResolutionOnlyOpensGateThroughApplicationVerifier(t *testing.T) {
	ctx := context.Background()
	writer := newVerifiedResolutionTestWriter()
	requestID := domain.RequestID("request-verified")
	tenantID := domain.TenantID("tenant-verified")
	attemptID := "attempt-verified"
	const commandID = "command-verified"
	writer.appendUnverifiedObservation(domain.ExecutionResolution{
		ResolutionID: "untrusted-terminal-claim", TenantID: tenantID, RequestID: requestID,
		Kind: domain.ResolutionRuntimeObservation, Generation: 1, AttemptID: attemptID,
		ObservedOutcome: domain.RuntimeResolutionFailed, ObservedRunID: "temporal-run-verified",
		Actor: "untrusted-input-says-verifier", ResolvedAt: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		EvidenceFingerprint: domain.FingerprintSafeEvidence([]byte("fabricated")),
	})
	if writer.terminal || len(writer.observations) != 1 {
		t.Fatalf("unverified observation changed terminal gate: terminal=%v observations=%d", writer.terminal, len(writer.observations))
	}

	evidence := domain.TerminalIntentEvidence{
		Version: 1, WorkflowID: "fulfillment-" + string(requestID), RunID: "temporal-run-verified",
		RequestID: string(requestID), TenantID: string(tenantID), AttemptID: attemptID,
		Generation: 1, CommandID: commandID, ReasonCode: "search_failed",
		OriginalReason: "исполнение прервано: поиск лидов завершился ошибкой",
	}
	evidence.Checksum = terminalEvidenceChecksum(t, evidence)
	payload, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	verifier := staticTerminalVerifier{result: ports.TerminalHistoryVerification{
		Intent: evidence, CloseStatus: "FAILED", Evidence: payload,
	}}
	service := NewVerifiedRuntimeResolutionService(
		verifiedResolutionTestUOW{tx: verifiedResolutionTestTransaction{writer: writer}},
		verifier,
		fixedResolutionClock{now: time.Date(2026, 4, 1, 1, 0, 0, 0, time.UTC)},
	)
	id := ports.TerminalResolutionIdentity{
		WorkflowID: evidence.WorkflowID, RunID: evidence.RunID, RequestID: requestID, TenantID: tenantID,
		AttemptID: attemptID, Generation: evidence.Generation, CommandID: evidence.CommandID,
	}
	if err := service.VerifyAndAppend(ctx, "verified-resolution-1", 0, "", id); err != nil {
		t.Fatalf("trusted application verifier did not append: %v", err)
	}
	if err := service.VerifyAndAppend(ctx, "verified-resolution-1", 0, "", id); err != nil {
		t.Fatalf("идентичный повтор verified resolution не идемпотентен: %v", err)
	}
	if !writer.terminal || writer.currentRevision != 1 {
		t.Fatalf("verified resolution did not establish terminal state: terminal=%v revision=%d", writer.terminal, writer.currentRevision)
	}
	if err := service.VerifyAndAppend(ctx, "stale-resolution", 0, "", id); !errors.Is(err, ports.ErrExecutionResolutionConflict) {
		t.Fatalf("stale expected revision must conflict, got %v", err)
	}
	foreign := id
	foreign.TenantID = "foreign-tenant"
	if err := service.VerifyAndAppend(ctx, "foreign-tenant-resolution", 1, "verified-resolution-1", foreign); err == nil {
		t.Fatal("foreign tenant history identity must not be written")
	}
	if writer.currentRevision != 1 || writer.currentID != "verified-resolution-1" || !writer.terminal {
		t.Fatalf("stale or foreign resolution changed current state: %+v", writer)
	}
}

func TestUnverifiedObservationCannotBeConstructedAsVerifiedResolution(t *testing.T) {
	observation := domain.ExecutionResolution{
		ResolutionID: "untrusted-terminal-claim", TenantID: "tenant-verified", RequestID: "request-verified",
		Kind: domain.ResolutionRuntimeObservation, Generation: 1, AttemptID: "attempt-verified",
		ObservedOutcome: domain.RuntimeResolutionFailed, ObservedRunID: "temporal-run-verified",
		Actor: "untrusted-input-says-verifier", ResolvedAt: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		EvidenceFingerprint: domain.FingerprintSafeEvidence([]byte("fabricated")),
	}
	if _, err := domain.NewVerifiedRuntimeResolution(observation, domain.TerminalIntentEvidence{}); !errors.Is(err, domain.ErrInvalidExecutionResolution) {
		t.Fatalf("unverified observation unexpectedly became verified: %v", err)
	}
}

func terminalEvidenceChecksum(t *testing.T, evidence domain.TerminalIntentEvidence) string {
	t.Helper()
	evidence.Checksum = ""
	encoded, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
