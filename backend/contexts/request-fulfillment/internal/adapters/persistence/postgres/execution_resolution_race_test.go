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

func TestExecutionResolutionConcurrentSameID(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	truncateTables(t)
	request, err := domain.NewRequest(domain.NewRequestParams{
		ID: "req-resolution-race", OfferID: "offer-resolution-race", TenantID: "tenant-resolution-race",
		Title:   "Resolution race",
		Content: "offer", Actor: "test", At: time.Now().UTC(), EventID: "event-resolution-race",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewRepository(pool).Save(ctx, request, 0); err != nil {
		t.Fatal(err)
	}
	base := domain.ExecutionResolution{
		ResolutionID: "resolution-race-id", TenantID: request.TenantID(), RequestID: request.ID(),
		Kind: domain.ResolutionTarget, Target: domain.LeadTarget{State: domain.LeadTargetExplicit, Value: 0},
		Actor: "operator", ResolvedAt: time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
		EvidenceFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	repo := postgres.NewExecutionResolutionRepository(pool)
	runTogether := func(a, b domain.ExecutionResolution) []error {
		t.Helper()
		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, resolution := range []domain.ExecutionResolution{a, b} {
			wg.Add(1)
			go func(i int, resolution domain.ExecutionResolution) {
				defer wg.Done()
				<-start
				errs[i] = repo.AppendResolution(ctx, resolution)
			}(i, resolution)
		}
		close(start)
		wg.Wait()
		return errs
	}
	for i, err := range runTogether(base, base) {
		if err != nil {
			t.Fatalf("одинаковый конкурентный вызов %d: %v", i, err)
		}
	}
	rows, err := repo.ListResolutions(ctx, request.TenantID(), request.ID())
	if err != nil || len(rows) != 1 {
		t.Fatalf("после одинаковой гонки rows=%+v err=%v; ожидалась одна строка", rows, err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM "request-fulfillment".execution_resolution_revision WHERE resolution_id=$1`, base.ResolutionID); err == nil {
		t.Fatal("append-only строка удалена перед проверкой конфликтной гонки")
	}
	// Используем новый ключ: два разных payload должны атомарно принять только
	// один вариант, а проигравший обязан получить явный конфликт.
	base.ResolutionID = "resolution-race-conflict"
	base.ExpectedRevision = 1
	base.SupersedesID = rows[0].ResolutionID
	changed := base
	changed.Target.Value = 8
	errs := runTogether(base, changed)
	accepted, conflicts := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ports.ErrExecutionResolutionConflict):
			conflicts++
		default:
			t.Errorf("неожиданный результат конфликтной гонки: %v", err)
		}
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatalf("конфликтная гонка: accepted=%d conflicts=%d errors=%v", accepted, conflicts, errs)
	}
	rows, err = repo.ListResolutions(ctx, request.TenantID(), request.ID())
	if err != nil || len(rows) != 2 {
		t.Fatalf("после конфликтной гонки rows=%+v err=%v; ожидались две разные resolution", rows, err)
	}
}

func TestExecutionResolutionPostgresTenantCrossAccess(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	truncateTables(t)
	request, err := domain.NewRequest(domain.NewRequestParams{
		ID: "req-resolution-tenant", OfferID: "offer-resolution-tenant", TenantID: "tenant-resolution-owner",
		Title:   "Resolution tenant",
		Content: "offer", Actor: "test", At: time.Now().UTC(), EventID: "event-resolution-tenant",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.NewRepository(pool).Save(ctx, request, 0); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewExecutionResolutionRepository(pool)
	resolution := domain.ExecutionResolution{
		ResolutionID: "resolution-cross-tenant", TenantID: request.TenantID(), RequestID: request.ID(),
		Kind: domain.ResolutionTarget, Target: domain.LeadTarget{State: domain.LeadTargetExplicit, Value: 0},
		Actor: "operator", ResolvedAt: time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
		EvidenceFingerprint: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	if err := repo.AppendResolution(ctx, resolution); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ListResolutions(ctx, "tenant-resolution-other", request.ID()); err != ports.ErrRequestNotFound {
		t.Fatalf("cross-tenant list error=%v; ожидался ErrRequestNotFound", err)
	}
	foreign := resolution
	foreign.ResolutionID = "resolution-cross-tenant-write"
	foreign.TenantID = "tenant-resolution-other"
	if err := repo.AppendResolution(ctx, foreign); err == nil {
		t.Fatal("cross-tenant resolution insert принят")
	}
	rows, err := repo.ListResolutions(ctx, request.TenantID(), request.ID())
	if err != nil || len(rows) != 1 {
		t.Fatalf("после cross-tenant попытки rows=%+v err=%v", rows, err)
	}
}
