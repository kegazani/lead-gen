package command

import (
	"context"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// VerifiedRuntimeResolutionService keeps the trusted writer private. It first
// asks the verifier to establish full-history evidence, then opens one UoW and
// uses only the dedicated verified-resolution capability.
type VerifiedRuntimeResolutionService struct {
	uow      ports.UnitOfWork
	verifier ports.TerminalHistoryVerifier
	clock    ports.Clock
}

func NewVerifiedRuntimeResolutionService(uow ports.UnitOfWork, verifier ports.TerminalHistoryVerifier, clock ports.Clock) *VerifiedRuntimeResolutionService {
	if uow == nil || verifier == nil || clock == nil {
		panic("request-fulfillment: verified runtime resolution требует UoW, verifier и clock")
	}
	return &VerifiedRuntimeResolutionService{uow: uow, verifier: verifier, clock: clock}
}

// VerifyAndAppend verifies the history outside the transaction (no network I/O
// is allowed inside UoW), then writes the evidence using the transaction-scoped
// capability. It never mutates Request or opens admission by itself.
func (s *VerifiedRuntimeResolutionService) VerifyAndAppend(ctx context.Context, resolutionID string, expectedRevision int64, supersedesID string, identity ports.TerminalResolutionIdentity) error {
	if resolutionID == "" || expectedRevision < 0 || identity.WorkflowID == "" || identity.RunID == "" ||
		identity.RequestID == "" || identity.TenantID == "" || identity.AttemptID == "" || identity.Generation < 1 || identity.CommandID == "" {
		return domain.ErrInvalidExecutionResolution
	}
	result, err := s.verifier.VerifyTerminalHistory(ctx, identity)
	if err != nil {
		return err
	}
	intent := result.Intent
	if intent.WorkflowID != identity.WorkflowID || intent.RunID != identity.RunID || intent.RequestID != string(identity.RequestID) ||
		intent.TenantID != string(identity.TenantID) || intent.AttemptID != identity.AttemptID || intent.Generation != identity.Generation || intent.CommandID != identity.CommandID {
		return domain.ErrInvalidExecutionResolution
	}
	outcome, ok := terminalOutcome(result.CloseStatus)
	if !ok {
		return domain.ErrInvalidExecutionResolution
	}
	resolution := domain.ExecutionResolution{
		ResolutionID: resolutionID, ExpectedRevision: expectedRevision, SupersedesID: supersedesID,
		TenantID: identity.TenantID, RequestID: identity.RequestID, Kind: domain.ResolutionRuntimeObservation,
		Generation: identity.Generation, AttemptID: identity.AttemptID, ObservedOutcome: outcome,
		ObservedRunID: identity.RunID, Actor: "temporal-history-verifier", ResolvedAt: s.clock.Now().UTC(),
		EvidenceFingerprint: domain.FingerprintSafeEvidence(result.Evidence),
	}
	verified, err := domain.NewVerifiedRuntimeResolution(resolution, intent)
	if err != nil {
		return err
	}
	return s.uow.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
		return tx.VerifiedRuntimeResolutions().AppendVerifiedRuntimeResolution(txCtx, verified)
	})
}

func terminalOutcome(closeStatus string) (domain.RuntimeResolutionOutcome, bool) {
	switch closeStatus {
	case "FAILED", "TIMED_OUT", "TERMINATED":
		return domain.RuntimeResolutionFailed, true
	case "CANCELED":
		return domain.RuntimeResolutionCanceled, true
	default:
		return "", false
	}
}
