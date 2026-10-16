package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/idgen"
	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/researchbridge"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/agent-orchestration/researchmission"
	"lead-gen/contracts/agent-orchestration/researchplan"
)

type researchReconcileClock struct{}

func (researchReconcileClock) Now() time.Time { return time.Now().UTC() }

type researchReconcileMission struct {
	researchmission.Port
	snapshot researchmission.Snapshot
}

func (m researchReconcileMission) FindByIntent(context.Context, researchmission.Scope, string) ([]researchmission.Snapshot, error) {
	return []researchmission.Snapshot{m.snapshot}, nil
}
func TestResearchReconcileConcurrentPostgresRestart(t *testing.T) {
	p := testPool(t)
	truncateTables(t)
	ctx := context.Background()
	repo := postgres.NewRepository(p)
	clock := researchReconcileClock{}
	ids := idgen.NewRandom()
	uow := postgres.NewUnitOfWork(p)
	request, err := domain.NewRequest(domain.NewRequestParams{ID: "research-reconcile", TenantID: "tenant-reconcile", OfferID: "offer", Content: "Автоматизация", Target: "Производители", Actor: "human", At: clock.Now(), EventID: "created", ExecutionPurpose: "editorial"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = request.StartValidation(domain.TransitionInput{Actor: "human", At: clock.Now(), EventID: "validating"}); err != nil {
		t.Fatal(err)
	}
	selection := domain.ResearchSelection{Quantity: 3, Unit: "company", OfferRevision: 1, Markets: []domain.ResearchMarket{{Country: "RU", Languages: []string{"ru"}}}}
	if _, err = request.ValidateForExecution(domain.TransitionInput{Actor: "human", At: clock.Now(), EventID: "approved"}, domain.ExecutionModeResearch, &selection); err != nil {
		t.Fatal(err)
	}
	request.PullDomainEvents()
	if err = repo.Save(ctx, request, 0); err != nil {
		t.Fatal(err)
	}
	now := clock.Now()
	mission := researchReconcileMission{snapshot: researchmission.Snapshot{Scope: researchmission.Scope{TenantID: "tenant-reconcile", PurposeID: "editorial"}, IntentID: string(request.ID()), MissionID: "mission-reconcile", Revision: 7, CriteriaVersion: 1, Goal: researchplan.Goal{Quantity: 3, Unit: "company"}, Markets: []researchplan.MarketSelection{{Country: "RU", Languages: []string{"ru"}}}, State: "completed", Outcome: "goal-reached", Counters: researchmission.Counters{Saved: 4, Accepted: 3}, CompletedAt: &now}}
	makeWorker := func() *researchbridge.Reconciler {
		return researchbridge.NewReconciler(repo, mission, command.NewStartProcessingHandler(uow, clock, ids), command.NewFulfillRequestHandler(uow, clock, ids), command.NewFailRequestHandler(uow, clock, ids), command.NewCancelRequestHandler(uow, clock, ids))
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- makeWorker().Scan(ctx) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil && !errors.Is(err, ports.ErrVersionConflict) && !errors.Is(err, domain.ErrTerminalState) {
			t.Fatal(err)
		}
	}
	// Новый экземпляр после проигранного CAS завершает проход без повторных событий.
	if err = makeWorker().Scan(ctx); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.FindByID(ctx, request.ID())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status() != domain.StatusFulfilled {
		t.Fatal(loaded.Status())
	}
	var count int
	if err = p.QueryRow(ctx, `SELECT count(*) FROM "request-fulfillment".outbox`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("ожидались только processing и fulfilled, событий=%d", count)
	}
}
