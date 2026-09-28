package memory_test

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/contracts/integration-platform/llm"
)

// TestSearchJobStoreSetInterpretationRoundTrip проверяет, что интерпретация
// сохраняется отдельным полем задания в in-memory адаптере и не меняет статус.
func TestSearchJobStoreSetInterpretationRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := memory.NewSearchJobStore()
	job, err := store.CreateSearchJob(ctx, "агентства b2b", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}

	interp := &llm.Interpretation{
		TargetLead:  "B2B-агентство",
		NeedSignals: []string{"ищет лидогенерацию"},
	}
	if err := store.SetInterpretation(ctx, job.ID, interp); err != nil {
		t.Fatalf("SetInterpretation: %v", err)
	}

	list, err := store.ListSearchJobs(ctx, 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	got := list[0]
	if got.Interpretation == nil {
		t.Fatal("интерпретация не сохранена")
	}
	if got.Interpretation.TargetLead != "B2B-агентство" || len(got.Interpretation.NeedSignals) != 1 {
		t.Fatalf("интерпретация = %+v", got.Interpretation)
	}
	if got.Status != job.Status {
		t.Fatalf("статус изменился: %v → %v", job.Status, got.Status)
	}
}
