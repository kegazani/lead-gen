package requestfulfillment_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

type admissionClock struct{}

func (admissionClock) Now() time.Time { return time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC) }

type admissionIDs struct {
	mu sync.Mutex
	n  int
}

func (s *admissionIDs) NewEventID() domain.EventID {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	return domain.EventID(fmt.Sprintf("admission-event-%d", s.n))
}

func TestAdmissionReplayBeforeLifecycleAndConflict(t *testing.T) {
	uow, repo, journal, handler := newAdmissionHarness(t)
	createValidatingRequest(t, uow)
	target := 7
	confidence := 0.75
	base := command.AdmitExecution{
		LifecycleFields: admissionFields(), CommandID: "cmd-one", Kind: command.AdmissionApprove, LeadTarget: &target,
		Confidence: &confidence, FormParameters: json.RawMessage(`{"limit":10,"mode":"strict"}`), Reason: "approved",
	}
	first, err := handler.Handle(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if first.Generation != 1 || first.AttemptID == "" || first.WorkflowID == string(first.RequestID) || first.State != ports.AttemptQueued {
		t.Fatalf("identity/state admission invalid: %+v", first)
	}
	replay, err := handler.Handle(context.Background(), base)
	if err != nil || !replay.Replay || replay.AttemptID != first.AttemptID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	// Для трёх ingress-ов сохранённая команда обязана менять результат replay,
	// если изменилось хотя бы одно семантическое поле. Kind rerun/execute здесь
	// проверяется на уже принятом CommandID и не проходит повторный lifecycle gate.
	for name, mutate := range map[string]func(*command.AdmitExecution){
		"target":                       func(c *command.AdmitExecution) { changed := 8; c.LeadTarget = &changed },
		"reason":                       func(c *command.AdmitExecution) { c.Reason = "different reason" },
		"request version":              func(c *command.AdmitExecution) { c.RequestVersion = 999 },
		"offer revision":               func(c *command.AdmitExecution) { c.OfferRevision = 2 },
		"confidence":                   func(c *command.AdmitExecution) { changed := 0.5; c.Confidence = &changed },
		"form parameters":              func(c *command.AdmitExecution) { c.FormParameters = json.RawMessage(`{"limit":11,"mode":"strict"}`) },
		"command kind approve/rerun":   func(c *command.AdmitExecution) { c.Kind = command.AdmissionRerun },
		"command kind approve/execute": func(c *command.AdmitExecution) { c.Kind = command.AdmissionExecute },
		"actor":                        func(c *command.AdmitExecution) { c.Actor = "other-operator" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if _, err := handler.Handle(context.Background(), candidate); !errors.Is(err, ports.ErrExecutionCommandConflict) {
				t.Fatalf("изменённый payload не дал conflict: %v", err)
			}
		})
	}
	// Порядок ключей JSON не меняет семантику; fingerprint и версия остаются в
	// неизменяемом сохранённом payload.
	equivalent := base
	equivalent.FormParameters = json.RawMessage(`{"mode":"strict","limit":10}`)
	if result, err := handler.Handle(context.Background(), equivalent); err != nil || !result.Replay {
		t.Fatalf("JSON object key ordering must remain canonical: result=%+v err=%v", result, err)
	}
	stored, err := repo.FindByCommand(context.Background(), "tenant-1", "cmd-one")
	if err != nil || stored.Intent.Parameters().LeadTarget != (domain.LeadTarget{State: domain.LeadTargetExplicit, Value: 7}) {
		t.Fatalf("stored intent changed: %+v err=%v", stored, err)
	}
	messages, err := journal.Pending(context.Background(), 0)
	if err != nil || len(messages) != 2 {
		t.Fatalf("outbox messages=%d err=%v; ожидается request lifecycle + intent", len(messages), err)
	}
	var storedPayload map[string]any
	if err := json.Unmarshal(stored.Intent.Parameters().Payload, &storedPayload); err != nil {
		t.Fatal(err)
	}
	if storedPayload["canonical_version"] != float64(1) || storedPayload["canonical_fingerprint"] == "" || storedPayload["offer_snapshot_fingerprint"] == "" {
		t.Fatalf("payload не содержит закреплённую canonical version/fingerprint/offer fingerprint: %s", stored.Intent.Parameters().Payload)
	}
}

func TestAdmissionRejectsNewCommandDuringQueuedActiveOrUnknownWithoutWrites(t *testing.T) {
	for _, state := range []ports.AttemptState{ports.AttemptQueued, ports.AttemptUnknown, ports.AttemptClaimed} {
		t.Run(string(state), func(t *testing.T) {
			uow, repo, journal, handler := newAdmissionHarness(t)
			createValidatingRequest(t, uow)
			first, err := handler.Handle(context.Background(), command.AdmitExecution{
				LifecycleFields: admissionFields(), CommandID: "cmd-first", Kind: command.AdmissionApprove,
			})
			if err != nil {
				t.Fatal(err)
			}
			if state == ports.AttemptUnknown || state == ports.AttemptClaimed {
				lease, claimErr := repo.ClaimNext(context.Background(), "dispatcher-test", time.Minute)
				if claimErr != nil {
					t.Fatal(claimErr)
				}
				if state == ports.AttemptUnknown {
					if claimErr = repo.SetState(context.Background(), lease, ports.AttemptUnknown); claimErr != nil {
						t.Fatal(claimErr)
					}
				}
			}
			before, err := repo.FindByID(context.Background(), "request-admission")
			if err != nil {
				t.Fatal(err)
			}
			beforeVersion, beforeStatus := before.Version(), before.Status()
			beforeMessages, _ := journal.Pending(context.Background(), 0)
			if _, err := handler.Handle(context.Background(), command.AdmitExecution{
				LifecycleFields: admissionFields(), CommandID: "cmd-new", Kind: command.AdmissionApprove,
			}); !errors.Is(err, ports.ErrExecutionAdmissionBlocked) {
				t.Fatalf("new admission error=%v", err)
			}
			after, err := repo.FindByID(context.Background(), "request-admission")
			if err != nil || after.Version() != beforeVersion || after.Status() != beforeStatus {
				t.Fatalf("aggregate changed: before=%d/%s after=%d/%s err=%v", beforeVersion, beforeStatus, after.Version(), after.Status(), err)
			}
			if _, err := repo.FindByCommand(context.Background(), "tenant-1", "cmd-new"); !errors.Is(err, ports.ErrExecutionAttemptNotFound) {
				t.Fatalf("rejected intent exists: %v", err)
			}
			afterMessages, _ := journal.Pending(context.Background(), 0)
			if len(afterMessages) != len(beforeMessages) {
				t.Fatalf("rejected admission wrote outbox: before=%d after=%d", len(beforeMessages), len(afterMessages))
			}
			if first.Generation != 1 {
				t.Fatalf("initial generation=%d", first.Generation)
			}
		})
	}
}

func TestAdmissionBlocksUnknownLegacyEvenAfterTargetResolution(t *testing.T) {
	uow, repo, journal, handler := newAdmissionHarness(t)
	ids := &admissionIDs{}
	clock := admissionClock{}
	created, err := command.NewSubmitRequestHandler(uow, clock, ids).Handle(context.Background(), command.SubmitRequest{
		RequestID: "request-admission", OfferID: "offer-admission", TenantID: "tenant-1", Content: "offer", Actor: "client",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := repo.FindByID(context.Background(), created.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := request.Snapshot()
	snapshot.ExecutionHistoryKnown = false
	legacy, err := domain.RehydrateRequest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), legacy, request.Version()); err != nil {
		t.Fatal(err)
	}
	if _, err := command.NewStartValidationHandler(uow, clock, ids).Handle(context.Background(), command.StartValidation{
		LifecycleFields: admissionFields(),
	}); err != nil {
		t.Fatal(err)
	}
	resolution := domain.ExecutionResolution{
		ResolutionID: "legacy-target-resolution", TenantID: "tenant-1", RequestID: "request-admission",
		Kind: domain.ResolutionTarget, Target: domain.LeadTarget{State: domain.LeadTargetExplicit, Value: 13},
		Actor: "operator", ResolvedAt: clock.Now(), EvidenceFingerprint: domain.FingerprintSafeEvidence([]byte("target evidence")),
	}
	if err := repo.AppendResolution(context.Background(), resolution); err != nil {
		t.Fatal(err)
	}
	before, _ := repo.FindByID(context.Background(), "request-admission")
	beforeMessages, _ := journal.Pending(context.Background(), 0)
	if _, err := handler.Handle(context.Background(), command.AdmitExecution{
		LifecycleFields: admissionFields(), CommandID: "legacy-new-command", Kind: command.AdmissionApprove,
	}); !errors.Is(err, ports.ErrExecutionAdmissionBlocked) {
		t.Fatalf("legacy admission=%v, ожидался блок", err)
	}
	after, _ := repo.FindByID(context.Background(), "request-admission")
	afterMessages, _ := journal.Pending(context.Background(), 0)
	if after.Version() != before.Version() || after.Status() != before.Status() || len(afterMessages) != len(beforeMessages) {
		t.Fatalf("legacy rejection wrote aggregate/outbox: before=%d/%s/%d after=%d/%s/%d", before.Version(), before.Status(), len(beforeMessages), after.Version(), after.Status(), len(afterMessages))
	}
	if _, err := repo.FindByCommand(context.Background(), "tenant-1", "legacy-new-command"); !errors.Is(err, ports.ErrExecutionAttemptNotFound) {
		t.Fatalf("legacy rejection stored intent: %v", err)
	}
}

func TestConcurrentAdmissionSameAndDifferentCommandID(t *testing.T) {
	for _, same := range []bool{true, false} {
		t.Run(fmt.Sprintf("same=%t", same), func(t *testing.T) {
			uow, repo, _, handler := newAdmissionHarness(t)
			createValidatingRequest(t, uow)
			start := make(chan struct{})
			results := make(chan command.AdmissionResult, 2)
			errs := make(chan error, 2)
			for i := 0; i < 2; i++ {
				go func(i int) {
					<-start
					id := domain.CommandID("cmd-race-a")
					if i == 1 && !same {
						id = "cmd-race-b"
					}
					result, err := handler.Handle(context.Background(), command.AdmitExecution{
						LifecycleFields: admissionFields(), CommandID: id, Kind: command.AdmissionApprove,
					})
					results <- result
					errs <- err
				}(i)
			}
			close(start)
			r1, e1 := <-results, <-errs
			r2, e2 := <-results, <-errs
			if same {
				if e1 != nil || e2 != nil || r1.AttemptID != r2.AttemptID {
					t.Fatalf("same CommandID results=%+v/%+v errors=%v/%v", r1, r2, e1, e2)
				}
			} else if (e1 == nil) == (e2 == nil) {
				t.Fatalf("different IDs must admit exactly one: errors=%v/%v", e1, e2)
			}
			owner, err := repo.ReadOwnerState(context.Background(), "tenant-1", "request-admission")
			if err != nil || !owner.HasAttempt || owner.Attempt.Intent.Generation() != 1 {
				t.Fatalf("owner state=%+v err=%v", owner, err)
			}
		})
	}
}

func newAdmissionHarness(t *testing.T) (*inmemory.UnitOfWork, *persistence.Repository, *inmemory.TransactionalJournal, *command.AdmitExecutionHandler) {
	t.Helper()
	repo := persistence.New()
	journal := inmemory.NewTransactionalJournal()
	uow := inmemory.NewUnitOfWorkWith(repo, journal)
	ids := &admissionIDs{}
	clock := admissionClock{}
	return uow, repo, journal, command.NewAdmitExecutionHandler(uow, clock, ids)
}

func createValidatingRequest(t *testing.T, uow ports.UnitOfWork) {
	t.Helper()
	ids := &admissionIDs{}
	clock := admissionClock{}
	if _, err := command.NewSubmitRequestHandlerWithExecutionHistoryKnown(uow, clock, ids, true).Handle(context.Background(), command.SubmitRequest{
		RequestID: "request-admission", OfferID: "offer-admission", TenantID: "tenant-1", Content: "offer", Actor: "client",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := command.NewStartValidationHandler(uow, clock, ids).Handle(context.Background(), command.StartValidation{
		LifecycleFields: admissionFields(),
	}); err != nil {
		t.Fatal(err)
	}
}

func admissionFields() command.LifecycleFields {
	return command.LifecycleFields{RequestID: "request-admission", TenantID: "tenant-1", Actor: "operator"}
}
