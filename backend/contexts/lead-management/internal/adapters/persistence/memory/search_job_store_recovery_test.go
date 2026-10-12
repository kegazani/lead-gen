package memory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/platform/jobs"
)

func TestSearchJobStoreRecoveryLeaseAndResultMerge(t *testing.T) {
	ctx := context.Background()
	store := memory.NewSearchJobStore()
	if err := store.Create(ctx, jobs.Job{ID: "recover", Payload: []byte("query")}); err != nil {
		t.Fatal(err)
	}
	oldAttempt, err := store.TakeQueued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordResult(ctx, "recover", []string{"a.example", "b.example"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReconcileRunning(ctx, time.Now().Add(time.Second), 4, "", ""); err != nil {
		t.Fatal(err)
	}
	newAttempt, err := store.TakeQueued(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RenewLease(ctx, "recover", oldAttempt.Attempts); !errors.Is(err, jobs.ErrTransitionRejected) {
		t.Fatalf("heartbeat старой попытки = %v, ожидался отказ", err)
	}
	if err := store.RecordResult(ctx, "recover", []string{"b.example", "c.example"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetSearchJob(ctx, "recover")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.example", "b.example", "c.example"}
	if len(got.ResultRef) != len(want) {
		t.Fatalf("result refs = %v, ожидались %v", got.ResultRef, want)
	}
	for i := range want {
		if got.ResultRef[i] != want[i] {
			t.Fatalf("result refs = %v, ожидались %v", got.ResultRef, want)
		}
	}
	if err := store.UpdateOwnedStatus(ctx, "recover", oldAttempt.Attempts, jobs.StatusDone, "", ""); !errors.Is(err, jobs.ErrTransitionRejected) {
		t.Fatalf("старая попытка завершила актуальную: %v", err)
	}
	if err := store.UpdateOwnedStatus(ctx, "recover", newAttempt.Attempts, jobs.StatusDone, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.RenewLease(ctx, "recover", newAttempt.Attempts); !errors.Is(err, jobs.ErrTransitionRejected) {
		t.Fatalf("terminal lease обновлён: %v", err)
	}
}
