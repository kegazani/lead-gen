package inmemory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

func TestExecutionResolutionsAreAppendOnlyAndTenantScoped(t *testing.T) {
	repo := inmemory.New()
	request := newRequest(t)
	if err := repo.Save(context.Background(), request, 0); err != nil {
		t.Fatal(err)
	}
	resolution := domain.ExecutionResolution{
		ResolutionID: "resolution-target-1", TenantID: request.TenantID(), RequestID: request.ID(),
		Kind: domain.ResolutionTarget, Target: domain.LeadTarget{State: domain.LeadTargetExplicit, Value: 0},
		Actor: "operator-7", ResolvedAt: time.Date(2026, 2, 3, 4, 5, 6, 123456789, time.UTC),
		EvidenceFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := repo.AppendResolution(context.Background(), resolution); err != nil {
		t.Fatal(err)
	}
	ownerState, err := repo.ReadOwnerState(context.Background(), request.TenantID(), request.ID())
	if err != nil || ownerState.HasAttempt || ownerState.Runtime != ports.AttemptUnknown || ownerState.Target != resolution.Target {
		t.Fatalf("target resolution изменила runtime gate или выдумала attempt: state=%+v err=%v", ownerState, err)
	}
	if err := repo.AppendResolution(context.Background(), resolution); err != nil {
		t.Fatalf("идемпотентный повтор: %v", err)
	}
	changed := resolution
	changed.Target.Value = 8
	if err := repo.AppendResolution(context.Background(), changed); !errors.Is(err, ports.ErrExecutionResolutionConflict) {
		t.Fatalf("конфликт ID с другой целью = %v", err)
	}
	rows, err := repo.ListResolutions(context.Background(), request.TenantID(), request.ID())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Target.State != domain.LeadTargetExplicit || rows[0].Target.Value != 0 {
		t.Fatalf("resolution rows = %+v", rows)
	}
	if _, err := repo.ListResolutions(context.Background(), "tenant-other", request.ID()); !errors.Is(err, ports.ErrRequestNotFound) {
		t.Fatalf("cross-tenant read = %v", err)
	}
	if err := repo.AppendResolution(context.Background(), domain.ExecutionResolution{
		ResolutionID: "resolution-target-incomplete", TenantID: request.TenantID(), RequestID: request.ID(),
		Kind: domain.ResolutionTarget, Target: domain.LeadTarget{State: domain.LeadTargetExplicit},
	}); !errors.Is(err, domain.ErrInvalidExecutionResolution) {
		t.Fatalf("неполный audit принят: %v", err)
	}
}

func TestResolutionRevisionCASAndTimestampIndependentCurrent(t *testing.T) {
	repo := inmemory.New()
	request := newRequest(t)
	if err := repo.Save(context.Background(), request, 0); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	first := domain.ExecutionResolution{ResolutionID: "revision-one", TenantID: request.TenantID(), RequestID: request.ID(), Kind: domain.ResolutionTarget, Target: domain.LeadTarget{State: domain.LeadTargetExplicit, Value: 1}, Actor: "operator", ResolvedAt: when, EvidenceFingerprint: domain.FingerprintSafeEvidence([]byte("safe evidence"))}
	if err := repo.AppendResolution(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ResolutionID = "revision-two"
	second.Target.Value = 2
	second.ExpectedRevision = 1
	second.SupersedesID = first.ResolutionID
	if err := repo.AppendResolution(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	stale := second
	stale.ResolutionID = "stale-revision"
	stale.ExpectedRevision = 0
	stale.SupersedesID = ""
	if err := repo.AppendResolution(context.Background(), stale); !errors.Is(err, ports.ErrExecutionResolutionConflict) {
		t.Fatalf("stale CAS=%v", err)
	}
	state, err := repo.ReadOwnerState(context.Background(), request.TenantID(), request.ID())
	if err != nil || state.Target.Value != 2 {
		t.Fatalf("current resolution depends on timestamp: state=%+v err=%v", state, err)
	}
	rows, err := repo.ListResolutions(context.Background(), request.TenantID(), request.ID())
	if err != nil || len(rows) != 2 || rows[0].Revision != 1 || rows[1].Revision != 2 {
		t.Fatalf("revision chain=%+v err=%v", rows, err)
	}
}

func TestEvidenceFingerprintRequiresCanonicalLowercaseSHA256(t *testing.T) {
	if got := domain.FingerprintSafeEvidence([]byte("canonical-safe-bytes")); len(got) != 64 || got != domain.FingerprintSafeEvidence([]byte("canonical-safe-bytes")) {
		t.Fatalf("fingerprint=%q", got)
	}
	if err := (domain.ExecutionResolution{ResolutionID: "bad-upper", TenantID: "tenant", RequestID: "request", Kind: domain.ResolutionTarget, Target: domain.LeadTarget{State: domain.LeadTargetExplicit}, Actor: "operator", ResolvedAt: time.Now(), EvidenceFingerprint: "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"}).Validate(); !errors.Is(err, domain.ErrInvalidExecutionResolution) {
		t.Fatalf("uppercase SHA-256 accepted: %v", err)
	}
}

func TestRuntimeObservationCannotAssertVerifiedTerminal(t *testing.T) {
	repo := inmemory.New()
	request := newRequest(t)
	if err := repo.Save(context.Background(), request, 0); err != nil {
		t.Fatal(err)
	}
	intent, err := domain.NewExecutionIntent("command-runtime", request.ID(), request.TenantID(), 1,
		domain.LeadTarget{State: domain.LeadTargetUnspecified}, []byte(`{"revision":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveIntent(context.Background(), intent, 0); err != nil {
		t.Fatal(err)
	}
	lease, err := repo.ClaimNext(context.Background(), "dispatcher", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.BindRun(context.Background(), lease, "temporal-run-verified"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetState(context.Background(), lease, ports.AttemptTerminal); err != nil {
		t.Fatal(err)
	}
	withoutRuntimeEvidence, err := repo.ReadOwnerState(context.Background(), request.TenantID(), request.ID())
	if err != nil || withoutRuntimeEvidence.Runtime != ports.AttemptUnknown {
		t.Fatalf("attempt terminal без runtime resolution ошибочно снял gate: state=%+v err=%v", withoutRuntimeEvidence, err)
	}
	verifiedAttempt, err := repo.FindByCommand(context.Background(), request.TenantID(), intent.CommandID())
	if err != nil {
		t.Fatal(err)
	}
	runtime := domain.ExecutionResolution{
		ResolutionID: "resolution-runtime-1", TenantID: request.TenantID(), RequestID: request.ID(),
		Kind: domain.ResolutionRuntimeObservation, Generation: 1, AttemptID: verifiedAttempt.AttemptID,
		ObservedOutcome: domain.RuntimeResolutionFailed, ObservedRunID: "temporal-run-verified",
		Actor: "reconciler", ResolvedAt: time.Date(2026, 2, 3, 4, 5, 7, 0, time.UTC),
		EvidenceFingerprint: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	}
	wrongRun := runtime
	wrongRun.ObservedRunID = "unverified-run"
	if err := repo.AppendResolution(context.Background(), wrongRun); !errors.Is(err, ports.ErrExecutionFenceConflict) {
		t.Fatalf("runtime resolution с неподтверждённым RunID принята: %v", err)
	}
	if err := repo.AppendResolution(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	rows, err := repo.ListResolutions(context.Background(), request.TenantID(), request.ID())
	if err != nil || len(rows) != 1 || rows[0].Kind != domain.ResolutionRuntimeObservation || rows[0].ObservedRunID != "temporal-run-verified" {
		t.Fatalf("runtime resolution не хранится отдельным свидетельством: rows=%+v err=%v", rows, err)
	}
	if _, err := repo.ClaimNext(context.Background(), "dispatcher-again", time.Minute); !errors.Is(err, ports.ErrExecutionLeaseUnavailable) {
		t.Fatalf("runtime resolution сама сняла execution gate: %v", err)
	}
	ownerState, err := repo.ReadOwnerState(context.Background(), request.TenantID(), request.ID())
	if err != nil || ownerState.Runtime != ports.AttemptUnknown {
		t.Fatalf("непроверенное runtime observation сняло gate: state=%+v err=%v", ownerState, err)
	}
}
