package firecrawl

import (
	"fmt"
	"sync"
	"testing"
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
	r := NewRegistry()
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

func ExampleTaskBudget() {
	b, _ := NewTaskBudget(20)
	_ = b.Reserve(10)
	b.Settle(10, 7)
	fmt.Printf("available=%d", b.Snapshot().Available)
	// Output: available=13
}
