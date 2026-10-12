package firecrawl

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"lead-gen/contracts/integration-platform/budget"
)

func TestTaskBudgetReserveAndSettle(t *testing.T) {
	b, err := NewTaskBudget(10)
	if err != nil {
		t.Fatalf("NewTaskBudget: %v", err)
	}

	if err := b.Reserve(6); err != nil {
		t.Fatalf("Reserve(6): %v", err)
	}
	if err := b.Reserve(4); err != nil {
		t.Fatalf("Reserve(4): %v", err)
	}
	if err := b.Reserve(1); err == nil {
		t.Fatal("Reserve(1) поверх лимита должен отказать")
	}

	snap := b.Snapshot()
	if snap.ReservedCredits != 10 || snap.Available != 0 {
		t.Fatalf("снимок после резервов: reserved=%d available=%d, want 10/0", snap.ReservedCredits, snap.Available)
	}

	// Фактический расход меньше резерва: 6 зарезервировано, потрачено 4.
	b.Settle(6, 4)
	snap = b.Snapshot()
	if snap.ConfirmedSpentCredits != 4 {
		t.Fatalf("confirmed = %d, want 4", snap.ConfirmedSpentCredits)
	}
	if snap.ReservedCredits != 4 {
		t.Fatalf("reserved = %d, want 4 (остался резерв 4)", snap.ReservedCredits)
	}
	if snap.Available != 2 {
		t.Fatalf("available = %d, want 2", snap.Available)
	}
}

// TestTaskBudgetParallelReserve проверяет приёмку 1: два параллельных воркера не
// могут зарезервировать суммарно больше лимита.
func TestTaskBudgetParallelReserve(t *testing.T) {
	const max = 20
	b, err := NewTaskBudget(max)
	if err != nil {
		t.Fatalf("NewTaskBudget: %v", err)
	}

	const workers = 8
	const perWorker = 4 // 8 * 4 = 32 попыток резерва по 4 кредита

	var wg sync.WaitGroup
	reserved := make(chan int, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := 0
			for j := 0; j < perWorker; j++ {
				if b.Reserve(4) == nil {
					got += 4
				}
			}
			reserved <- got
		}()
	}
	wg.Wait()
	close(reserved)

	total := 0
	for n := range reserved {
		total += n
	}
	if total > max {
		t.Fatalf("параллельные воркеры зарезервировали %d, что превышает лимит %d", total, max)
	}
	if total != max {
		t.Fatalf("суммарный резерв = %d, want %d (весь бюджет должен быть распределён)", total, max)
	}
}

func TestTaskBudgetStop(t *testing.T) {
	b, _ := NewTaskBudget(5)
	b.Stop()
	if err := b.Reserve(1); err == nil {
		t.Fatal("Reserve после Stop должен отказать")
	}
	if got := b.Snapshot().Status; got != BudgetStopped {
		t.Fatalf("status = %q, want %q", got, BudgetStopped)
	}
}

// TestTaskBudgetZeroIsUnlimited проверяет семантику «бюджет не задан»: 0 —
// допустимый лимит, лимит не применяется, но фактический расход измеряется.
func TestTaskBudgetZeroIsUnlimited(t *testing.T) {
	b, err := NewTaskBudget(0)
	if err != nil {
		t.Fatalf("NewTaskBudget(0): %v", err)
	}
	if snap := b.Snapshot(); snap.MaxCredits != 0 {
		t.Fatalf("max_credits = %d, want 0 (бюджет не задан)", snap.MaxCredits)
	}

	// Безлимитный бюджет не отклоняет большие резервы.
	if err := b.Reserve(1000); err != nil {
		t.Fatalf("Reserve(1000) без лимита: %v", err)
	}
	if err := b.Reserve(1000); err != nil {
		t.Fatalf("Reserve(1000) повторно: %v", err)
	}
	// Available неиспользуемо при безлимитном бюджете.
	if snap := b.Snapshot(); snap.Available != 0 || snap.ReservedCredits != 2000 {
		t.Fatalf("снимок: available=%d reserved=%d, want 0/2000", snap.Available, snap.ReservedCredits)
	}

	// Фактический расход фиксируется, неиспользованный резерв освобождается.
	b.Settle(1000, 700)
	snap := b.Snapshot()
	if snap.ConfirmedSpentCredits != 700 {
		t.Fatalf("confirmed = %d, want 700", snap.ConfirmedSpentCredits)
	}
	if snap.ReservedCredits != 1000 {
		t.Fatalf("reserved = %d, want 1000", snap.ReservedCredits)
	}
	if snap.Status != BudgetActive {
		t.Fatalf("статус = %q, want active (без лимита не исчерпается)", snap.Status)
	}
	b.Settle(1000, 1000)
	if snap := b.Snapshot(); snap.ConfirmedSpentCredits != 1700 || snap.Status != BudgetActive {
		t.Fatalf("снимок: spent=%d status=%q, want 1700/active", snap.ConfirmedSpentCredits, snap.Status)
	}
}

// TestTaskBudgetNotSetStillStop проверяет, что безлимитный бюджет всё равно
// останавливается при Stop и после него резерв запрещён.
func TestTaskBudgetNotSetStillStop(t *testing.T) {
	b, _ := NewTaskBudget(0)
	b.Stop()
	if err := b.Reserve(1); err == nil {
		t.Fatal("Reserve после Stop должен отказать")
	}
	if got := b.Snapshot().Status; got != BudgetStopped {
		t.Fatalf("status = %q, want %q", got, BudgetStopped)
	}
}

func TestTaskBudgetInvalid(t *testing.T) {
	if _, err := NewTaskBudget(-1); err == nil {
		t.Fatal("NewTaskBudget(-1) должен отказать")
	}
}

func TestRegistryGetOrCreate(t *testing.T) {
	r, err := NewRegistry(RegistryConfig{MaxEntries: 10, FinishedTTL: time.Minute})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	b1, err := r.GetOrCreate("task-1", 10)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	b2, err := r.GetOrCreate("task-1", 99)
	if err != nil {
		t.Fatalf("повторный GetOrCreate: %v", err)
	}
	if b1 != b2 {
		t.Fatal("повторный GetOrCreate должен вернуть тот же бюджет")
	}
	if got := b2.Snapshot().MaxCredits; got != 10 {
		t.Fatalf("лимит существующего бюджета изменился на %d, want 10", got)
	}
	if _, ok := r.Get("task-missing"); ok {
		t.Fatal("Get неизвестной задачи должен вернуть ok=false")
	}
}

func TestRegistryDefaultsAreFiniteAndInvalidLimitsRejected(t *testing.T) {
	registry, err := NewRegistry(RegistryConfig{})
	if err != nil {
		t.Fatalf("NewRegistry defaults: %v", err)
	}
	if registry.maxEntries <= 0 || registry.finishedTTL <= 0 {
		t.Fatalf("registry defaults must be finite and positive: capacity=%d ttl=%s", registry.maxEntries, registry.finishedTTL)
	}
	if _, err := NewRegistry(RegistryConfig{MaxEntries: -1}); err == nil {
		t.Fatal("negative capacity must fail configuration validation")
	}
	if _, err := NewRegistry(RegistryConfig{FinishedTTL: -time.Second}); err == nil {
		t.Fatal("negative retention must fail configuration validation")
	}
}

func TestRegistryFinishReplayCapacityAndTTL(t *testing.T) {
	clock := newFakeClock()
	registry, err := NewRegistry(RegistryConfig{MaxEntries: 1, FinishedTTL: time.Minute, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	first, err := registry.GetOrCreate("same-id", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Reserve(3); err != nil {
		t.Fatal(err)
	}
	if err := first.Settle(3, 2); err != nil {
		t.Fatal(err)
	}
	if err := registry.Finish(context.Background(), "same-id"); err != nil {
		t.Fatalf("Finish settled scope: %v", err)
	}
	replay, err := registry.GetOrCreate("same-id", 99)
	if err != nil || replay != first {
		t.Fatalf("same task ID должен replay-ить удерживаемый scope: task=%p original=%p err=%v", replay, first, err)
	}
	if _, err := registry.GetOrCreate("other-id", 10); !errors.Is(err, budget.ErrRegistryCapacity) {
		t.Fatalf("ёмкость должна отказать до eviction неистёкшего terminal scope: %v", err)
	}
	clock.Advance(time.Minute)
	second, err := registry.GetOrCreate("other-id", 10)
	if err != nil || second == nil {
		t.Fatalf("после TTL истёкший settled scope должен evict-иться: task=%v err=%v", second, err)
	}
	status := registry.Status()
	if status.Entries != 1 || status.Capacity != 1 || status.CapacityRejections != 1 {
		t.Fatalf("registry pressure status = %+v", status)
	}
}

func TestRegistryUnknownReservationPinsCapacityUntilExactOperationEvidence(t *testing.T) {
	clock := newFakeClock()
	registry, err := NewRegistry(RegistryConfig{MaxEntries: 1, FinishedTTL: time.Second, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	task, err := registry.GetOrCreate("unknown", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Reserve(4); err != nil {
		t.Fatal(err)
	}
	if err := task.MarkUnknown(4); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Hour) // unknown outcome не освобождается TTL.
	if err := registry.Finish(context.Background(), "unknown"); !errors.Is(err, budget.ErrUnknownOutcomeUnreconciled) {
		t.Fatalf("unknown без exact operation evidence остаётся pinned: %v", err)
	}
	if _, err := registry.GetOrCreate("new", 1); !errors.Is(err, budget.ErrRegistryCapacity) {
		t.Fatalf("unknown scope не должен вытесняться по TTL/capacity: %v", err)
	}
	status := registry.Status()
	if status.UnknownReservations != 1 || status.ActiveScopes != 1 || status.CapacityRejections != 1 {
		t.Fatalf("pinned unknown status = %+v", status)
	}

	if err := registry.Finish(context.Background(), "unknown"); !errors.Is(err, budget.ErrUnknownOutcomeUnreconciled) {
		t.Fatalf("повторный Finish без operation evidence должен оставить unknown: %v", err)
	}
	if snapshot := task.Snapshot(); snapshot.ReservedCredits != 4 || snapshot.UnknownCredits != 4 || snapshot.ConfirmedSpentCredits != 0 {
		t.Fatalf("aggregate balance не должен освобождать unknown reserve: %+v", snapshot)
	}
	if status := registry.Status(); status.FinishedScopes != 0 || status.UnknownReservations != 1 {
		t.Fatalf("unknown scope ошибочно стал terminal: %+v", status)
	}
}

func TestRegistryUnknownEntriesFillCapacityAndAggregateBalanceCannotReleaseReservations(t *testing.T) {
	clock := newFakeClock()
	registry, err := NewRegistry(RegistryConfig{MaxEntries: 2, FinishedTTL: time.Second, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"unknown-a", "unknown-b"} {
		task, err := registry.GetOrCreate(id, 5)
		if err != nil {
			t.Fatalf("GetOrCreate(%s): %v", id, err)
		}
		if err := task.Reserve(2); err != nil {
			t.Fatal(err)
		}
		if err := task.MarkUnknown(2); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(24 * time.Hour)
	for _, id := range []string{"unknown-a", "unknown-b"} {
		if err := registry.Finish(context.Background(), id); !errors.Is(err, budget.ErrUnknownOutcomeUnreconciled) {
			t.Errorf("Finish(%s) without operation evidence = %v, want ErrUnknownOutcomeUnreconciled", id, err)
		}
	}
	for _, id := range []string{"paid-request-1", "paid-request-2"} {
		if _, err := registry.GetOrCreate(id, 1); !errors.Is(err, budget.ErrRegistryCapacity) {
			t.Errorf("GetOrCreate(%s) at full unknown capacity = %v, want ErrRegistryCapacity", id, err)
		}
	}
	if status := registry.Status(); status.Entries != 2 || status.UnknownReservations != 2 || status.CapacityRejections != 2 {
		t.Fatalf("unknown capacity pressure = %+v", status)
	}

	// Даже aggregate decrease не несёт attribution к конкретной операции и потому
	// не разрешает ни один unknown reservation.
	clock.Advance(time.Second)
	for _, id := range []string{"unknown-a", "unknown-b"} {
		if err := registry.Finish(context.Background(), id); !errors.Is(err, budget.ErrUnknownOutcomeUnreconciled) {
			t.Errorf("Finish(%s) должен сохранить unknown без exact evidence: %v", id, err)
		}
	}
	if _, err := registry.GetOrCreate("still-blocked", 1); !errors.Is(err, budget.ErrRegistryCapacity) {
		t.Fatalf("unknown записи должны продолжать занимать capacity: %v", err)
	}
}

func TestRegistryFinishWaitsForInflightAndPreventsFurtherReserve(t *testing.T) {
	clock := newFakeClock()
	registry, err := NewRegistry(RegistryConfig{MaxEntries: 1, FinishedTTL: time.Minute, Clock: clock})
	if err != nil {
		t.Fatal(err)
	}
	task, err := registry.GetOrCreate("inflight", 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := task.Reserve(2); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- registry.Finish(context.Background(), "inflight") }()
	deadline := time.Now().Add(time.Second)
	for task.CanStart() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if task.CanStart() {
		t.Fatal("Finish должен блокировать новые затратные действия сразу")
	}
	select {
	case err := <-finished:
		t.Fatalf("Finish завершился до settlement: %v", err)
	default:
	}
	if err := task.Settle(2, 1); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Finish после settlement: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Finish не проснулся после settlement")
	}
	if _, err := registry.GetOrCreate("inflight", 100); err != nil {
		t.Fatalf("replay того же terminal ID должен вернуть существующий scope: %v", err)
	}
}

func ExampleTaskBudget() {
	b, _ := NewTaskBudget(20)
	_ = b.Reserve(10)
	b.Settle(10, 7)
	fmt.Printf("available=%d", b.Snapshot().Available)
	// Output: available=13
}
