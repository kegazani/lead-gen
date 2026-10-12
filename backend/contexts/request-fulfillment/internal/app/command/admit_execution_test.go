package command

import (
	"encoding/json"
	"errors"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

func TestReplayCandidateComparesCompleteCommandSemantics(t *testing.T) {
	for _, kind := range []AdmissionKind{AdmissionApprove, AdmissionRerun, AdmissionExecute} {
		t.Run(string(kind), func(t *testing.T) {
			target := 7
			payload, err := commandPayload(4, 2, "offer-1", "snapshot-hash", domain.LeadTarget{State: domain.LeadTargetExplicit, Value: target}, AdmitExecution{Kind: kind, Reason: "reason"})
			if err != nil {
				t.Fatal(err)
			}
			intent, err := domain.NewExecutionIntent("command-1", "request-1", "tenant-1", 1, domain.LeadTarget{State: domain.LeadTargetExplicit, Value: target}, payload)
			if err != nil {
				t.Fatal(err)
			}
			attempt := ports.ExecutionAttempt{Intent: intent, AttemptID: "request-1#1", RequestVersion: 4}
			base := AdmitExecution{LifecycleFields: LifecycleFields{RequestID: "request-1", TenantID: "tenant-1"}, CommandID: "command-1", Kind: kind, Reason: "reason", LeadTarget: &target, RequestVersion: 4, OfferRevision: 2}
			assertReplay := func(name string, candidate AdmitExecution, wantConflict bool) {
				t.Helper()
				got, err := replayCandidate(attempt, candidate)
				if err == nil && !intent.SameCommand(got) {
					err = domain.ErrCommandConflict
				}
				if wantConflict {
					if !errors.Is(err, domain.ErrCommandConflict) {
						t.Fatalf("%s: expected command conflict, got intent=%+v err=%v", name, got, err)
					}
					return
				}
				if err != nil || !intent.SameCommand(got) {
					t.Fatalf("%s: replay mismatch: intent=%+v err=%v", name, got, err)
				}
			}
			assertReplay("идентичный повтор", base, false)
			changed := base
			changed.Reason = "different reason"
			assertReplay("причина", changed, true)
			changedTarget := 8
			changed = base
			changed.LeadTarget = &changedTarget
			assertReplay("цель", changed, true)
			changed = base
			changed.OfferRevision++
			assertReplay("ревизия оффера", changed, true)
			changed = base
			changed.RequestVersion++
			assertReplay("версия заявки", changed, true)
			changed = base
			changed.Kind = AdmissionExecute
			assertReplay("вид команды", changed, kind != AdmissionExecute)

			var decoded map[string]any
			if err := json.Unmarshal(intent.Parameters().Payload, &decoded); err != nil || decoded["offer_snapshot_fingerprint"] != "snapshot-hash" {
				t.Fatalf("canonical command payload missing snapshot reference: %#v err=%v", decoded, err)
			}
			var persisted executionPayload
			if err := json.Unmarshal(intent.Parameters().Payload, &persisted); err != nil {
				t.Fatal(err)
			}
			semantic, err := json.Marshal(persisted.executionPayloadSemantic)
			if err != nil || persisted.CanonicalVersion != executionPayloadVersion || persisted.CanonicalFingerprint != fingerprintPayload(semantic) {
				t.Fatalf("canonical fingerprint/version invalid: version=%d fingerprint=%q err=%v", persisted.CanonicalVersion, persisted.CanonicalFingerprint, err)
			}
		})
	}
}
