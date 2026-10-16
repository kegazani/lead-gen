package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// Два воркера читают одну версию до начала записи: только победивший CAS
// получает право на внешнее исполнение выбранным способом.
func TestResearchAndLegacyApprovalShareOnePostgresCAS(t *testing.T) {
	p := testPool(t)
	truncateTables(t)
	ctx := context.Background()
	request, err := domain.NewRequest(domain.NewRequestParams{ID: "request-mode-race", TenantID: "tenant-mode-race", OfferID: "offer", Content: "Автоматизация", Target: "Генподрядчики", Actor: "human", At: time.Now(), EventID: "created"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = request.StartValidation(domain.TransitionInput{Actor: "operator", At: time.Now(), EventID: "validating"}); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewRepository(p)
	if err = repo.Save(ctx, request, 0); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, mode := range []string{domain.ExecutionModeLegacy, domain.ExecutionModeResearch} {
		wg.Add(1)
		go func(mode string) {
			defer wg.Done()
			results <- postgres.NewUnitOfWork(p).WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
				loaded, e := tx.Requests().FindByID(txCtx, request.ID())
				if e != nil {
					return e
				}
				expected := loaded.Version()
				ready <- struct{}{}
				<-release
				var selection *domain.ResearchSelection
				if mode == domain.ExecutionModeResearch {
					selection = &domain.ResearchSelection{Quantity: 11, Unit: "company", OfferRevision: 1, Markets: []domain.ResearchMarket{{Country: "RU", Languages: []string{"ru"}}}}
				}
				events, e := loaded.ValidateForExecution(domain.TransitionInput{Actor: "operator", At: time.Now(), EventID: domain.EventID("approval-" + mode)}, mode, selection)
				if e != nil {
					return e
				}
				if e = tx.Requests().Save(txCtx, loaded, expected); e != nil {
					return e
				}
				return tx.Outbox().Append(txCtx, events...)
			})
		}(mode)
	}
	<-ready
	<-ready
	close(release)
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ports.ErrVersionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("успехов=%d конфликтов=%d", successes, conflicts)
	}
	restored, err := repo.FindByID(ctx, request.ID())
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = p.QueryRow(ctx, `SELECT count(*) FROM "request-fulfillment".outbox`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("событий одобрения=%d", count)
	}
	if restored.ExecutionMode() == domain.ExecutionModeResearch {
		if _, err = p.Exec(ctx, `UPDATE "request-fulfillment".request SET snapshot=snapshot-'ExecutionMode' WHERE id=$1`, string(request.ID())); err == nil {
			t.Fatal("старый writer стёр research mode")
		}
	}
}
