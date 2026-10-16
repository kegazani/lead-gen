package inmemory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/idgen"
	outbox "lead-gen/backend/contexts/request-fulfillment/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/researchbridge"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/agent-orchestration/researchmission"
	"lead-gen/contracts/agent-orchestration/researchplan"
)

type reconcileClock struct{}

func (reconcileClock) Now() time.Time { return time.Now().UTC() }

type reconcileMissions struct {
	researchmission.Port
	snapshots           []researchmission.Snapshot
	err                 error
	calledInTransaction bool
}

func (m *reconcileMissions) FindByIntent(ctx context.Context, scope researchmission.Scope, intent string) ([]researchmission.Snapshot, error) {
	m.calledInTransaction = ports.HasTransactionScope(ctx)
	return m.snapshots, m.err
}
func TestReconcileResearchRecoversOwnerStatusWithoutApprovingOrDuplicatingEvents(t *testing.T) {
	for _, tc := range []struct {
		name, state, outcome, reason string
		accepted                     int
		want                         domain.RequestStatus
	}{
		{"goal", "completed", "goal-reached", "goal-reached", 3, domain.StatusFulfilled},
		{"partial", "completed", "partial", "firecrawl-budget-exhausted", 1, domain.StatusFailed},
		{"cancel", "cancelled", "", "human-cancelled", 0, domain.StatusCancelled},
		{"failure", "failed", "partial", "owner-persistence-failed", 0, domain.StatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			repo := persistence.New()
			journal := outbox.NewTransactionalJournal()
			uow := outbox.NewUnitOfWorkWith(repo, journal)
			clock := reconcileClock{}
			ids := idgen.NewRandom()
			req, err := domain.NewRequest(domain.NewRequestParams{ID: "recovery", TenantID: "tenant", OfferID: "offer", Content: "Автоматизация", Target: "Производители", Actor: "human", At: clock.Now(), EventID: "created", ExecutionPurpose: "editorial"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = req.StartValidation(domain.TransitionInput{Actor: "human", At: clock.Now(), EventID: "validating"}); err != nil {
				t.Fatal(err)
			}
			selection := domain.ResearchSelection{Quantity: 3, Unit: "company", OfferRevision: 1, Markets: []domain.ResearchMarket{{Country: "RU", Languages: []string{"ru"}}}}
			if _, err = req.ValidateForExecution(domain.TransitionInput{Actor: "human", At: clock.Now(), EventID: "approved"}, domain.ExecutionModeResearch, &selection); err != nil {
				t.Fatal(err)
			}
			req.PullDomainEvents()
			if err = repo.Save(ctx, req, 0); err != nil {
				t.Fatal(err)
			}
			now := clock.Now()
			snapshot := researchmission.Snapshot{Scope: researchmission.Scope{TenantID: "tenant", PurposeID: "editorial"}, IntentID: "recovery", MissionID: "mission", Revision: 1, CriteriaVersion: 1, Goal: researchplan.Goal{Quantity: 3, Unit: "company"}, State: "running"}
			missions := &reconcileMissions{snapshots: []researchmission.Snapshot{snapshot}}
			reconciler := researchbridge.NewReconciler(repo, missions, command.NewStartProcessingHandler(uow, clock, ids), command.NewFulfillRequestHandler(uow, clock, ids), command.NewFailRequestHandler(uow, clock, ids), command.NewCancelRequestHandler(uow, clock, ids))
			// Временная ошибка не меняет owner-state; следующий процесс восстанавливается.
			missions.err = errors.New("read unavailable")
			if reconciler.Scan(ctx) == nil {
				t.Fatal("ошибка чтения скрыта")
			}
			loaded, _ := repo.FindByID(ctx, req.ID())
			if loaded.Status() != domain.StatusValidated {
				t.Fatal("нет доказательства запуска")
			}
			missions.err = nil
			if err = reconciler.Scan(ctx); err != nil {
				t.Fatal(err)
			}
			loaded, _ = repo.FindByID(ctx, req.ID())
			if loaded.Status() != domain.StatusProcessing {
				t.Fatal(loaded.Status())
			}
			snapshot.Revision = 2
			snapshot.State = tc.state
			snapshot.Outcome = tc.outcome
			snapshot.StopReason = tc.reason
			snapshot.Counters.Accepted = tc.accepted
			snapshot.CompletedAt = &now
			missions.snapshots = []researchmission.Snapshot{snapshot}
			if err = reconciler.Scan(ctx); err != nil {
				t.Fatal(err)
			}
			loaded, _ = repo.FindByID(ctx, req.ID())
			if loaded.Status() != tc.want {
				t.Fatalf("status=%s", loaded.Status())
			}
			version := loaded.Version()
			if err = reconciler.Observe(ctx, snapshot); err != nil {
				t.Fatal(err)
			}
			loaded, _ = repo.FindByID(ctx, req.ID())
			if loaded.Version() != version {
				t.Fatal("повтор создал переход")
			}
			if missions.calledInTransaction {
				t.Fatal("чужой владелец прочитан внутри transaction")
			}
			snapshot.Scope.TenantID = "another"
			if !errors.Is(reconciler.Observe(ctx, snapshot), researchbridge.ErrDenied) {
				t.Fatal("чужой tenant принят")
			}
			if tc.want == domain.StatusFailed {
				code, reason, ok := loaded.FailureReason()
				if !ok || code == "" || reason == "" {
					t.Fatal("частичный результат скрыт")
				}
			}
		})
	}
}
