package idempotency_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/idempotency"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

const (
	testTenant  = domain.TenantID("tenant-1")
	testCommand = domain.CommandID("command-1")
)

func TestStoreReserveReplayAfterComplete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := idempotency.NewStore()

	id, owner, err := store.Reserve(ctx, testTenant, testCommand, domain.CampaignID("campaign-1"))
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if !owner {
		t.Fatal("первый Reserve должен вернуть владение")
	}
	store.Complete(testTenant, testCommand, id)

	replayed, owner, err := store.Reserve(ctx, testTenant, testCommand, domain.CampaignID("campaign-2"))
	if err != nil {
		t.Fatalf("повторный Reserve: %v", err)
	}
	if owner {
		t.Fatal("повторный Reserve не должен получать владение")
	}
	if replayed != id {
		t.Fatalf("повтор вернул кампанию %q, ожидалась %q", replayed, id)
	}
	if got := store.Len(); got != 1 {
		t.Fatalf("ключей %d, ожидался 1", got)
	}
}

func TestStoreReleaseAllowsRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := idempotency.NewStore()

	if _, owner, err := store.Reserve(ctx, testTenant, testCommand, domain.CampaignID("campaign-1")); err != nil || !owner {
		t.Fatalf("Reserve: owner=%v, err=%v", owner, err)
	}
	store.Release(testTenant, testCommand)
	if got := store.Len(); got != 0 {
		t.Fatalf("после Release ключей %d, ожидалось 0", got)
	}

	_, owner, err := store.Reserve(ctx, testTenant, testCommand, domain.CampaignID("campaign-2"))
	if err != nil {
		t.Fatalf("повторный Reserve: %v", err)
	}
	if !owner {
		t.Fatal("освобождённый ключ должен снова резервироваться")
	}
}

func TestStoreWaiterGetsOwnerResult(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := idempotency.NewStore()

	if _, owner, err := store.Reserve(ctx, testTenant, testCommand, domain.CampaignID("campaign-1")); err != nil || !owner {
		t.Fatalf("Reserve: owner=%v, err=%v", owner, err)
	}

	// Ожидающий не получает владение и не выполняет эффект, пока владелец не
	// опубликует результат.
	done := make(chan domain.CampaignID, 1)
	go func() {
		id, owner, err := store.Reserve(ctx, testTenant, testCommand, domain.CampaignID("campaign-2"))
		if err != nil || owner {
			done <- ""
			return
		}
		done <- id
	}()

	store.Complete(testTenant, testCommand, domain.CampaignID("campaign-1"))
	if got := <-done; got != domain.CampaignID("campaign-1") {
		t.Fatalf("ожидающий получил %q, ожидалась campaign-1", got)
	}
}

func TestStoreReserveHonorsContext(t *testing.T) {
	t.Parallel()
	store := idempotency.NewStore()
	if _, owner, err := store.Reserve(context.Background(), testTenant, testCommand, domain.CampaignID("campaign-1")); err != nil || !owner {
		t.Fatalf("Reserve: owner=%v, err=%v", owner, err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := store.Reserve(cancelled, testTenant, testCommand, domain.CampaignID("campaign-2"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("отменённый контекст: получено %v, ожидалась context.Canceled", err)
	}
}

// TestStoreConcurrentReserveSingleOwner воспроизводит гонку двух одинаковых
// команд: владельцем становится ровно один вызов, поэтому эффект выполняется
// только один раз и второй кампании не возникает.
func TestStoreConcurrentReserveSingleOwner(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := idempotency.NewStore()

	type outcome struct {
		id    domain.CampaignID
		owner bool
		err   error
	}
	outcomes := make(chan outcome, 2)
	start := make(chan struct{})

	var wg sync.WaitGroup
	for _, candidate := range []domain.CampaignID{"campaign-a", "campaign-b"} {
		wg.Add(1)
		go func(candidate domain.CampaignID) {
			defer wg.Done()
			<-start
			id, owner, err := store.Reserve(ctx, testTenant, testCommand, candidate)
			if err != nil {
				outcomes <- outcome{err: err}
				return
			}
			if owner {
				// Владелец выполняет эффект и публикует результат.
				store.Complete(testTenant, testCommand, id)
			}
			outcomes <- outcome{id: id, owner: owner}
		}(candidate)
	}
	close(start)
	wg.Wait()
	close(outcomes)

	owners := 0
	var authoritative domain.CampaignID
	for result := range outcomes {
		if result.err != nil {
			t.Fatalf("Reserve: %v", result.err)
		}
		if result.owner {
			owners++
		}
		if authoritative == "" {
			authoritative = result.id
		} else if result.id != authoritative {
			t.Fatalf("участники гонки ссылаются на разные кампании: %q и %q", authoritative, result.id)
		}
	}
	if owners != 1 {
		t.Fatalf("владельцев резервирования %d, ожидался 1", owners)
	}
	if got := store.Len(); got != 1 {
		t.Fatalf("ключей %d, ожидался 1", got)
	}
}
