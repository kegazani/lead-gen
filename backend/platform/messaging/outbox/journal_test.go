package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestMemoryJournalAppendPendingAndMarkPublished(t *testing.T) {
	journal := NewMemoryJournal()
	ctx := context.Background()

	if err := journal.Append(ctx, Message{ID: "m1", Exchange: "outreach-orchestration", RoutingKey: "approved-action.1", Body: []byte("one")}); err != nil {
		t.Fatalf("Append m1: %v", err)
	}
	if err := journal.Append(ctx, Message{ID: "m2", Exchange: "outreach-orchestration", RoutingKey: "approved-action.1", Body: []byte("two")}); err != nil {
		t.Fatalf("Append m2: %v", err)
	}

	pending, err := journal.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 2 || pending[0].ID != "m1" || pending[1].ID != "m2" {
		t.Fatalf("Pending = %+v, ожидался порядок записи", pending)
	}

	if err := journal.MarkPublished(ctx, "m1"); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}
	pending, err = journal.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "m2" {
		t.Fatalf("Pending после подтверждения = %+v", pending)
	}
}

func TestMemoryJournalAppendIsIdempotentByID(t *testing.T) {
	journal := NewMemoryJournal()
	ctx := context.Background()

	msg := Message{ID: "m1", Body: []byte("one")}
	for i := 0; i < 3; i++ {
		if err := journal.Append(ctx, msg); err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
	}

	pending, err := journal.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("повтор Append создал %d записей, ожидалась одна", len(pending))
	}
}

func TestMemoryJournalPendingLimit(t *testing.T) {
	journal := NewMemoryJournal()
	ctx := context.Background()
	for _, id := range []string{"m1", "m2", "m3"} {
		if err := journal.Append(ctx, Message{ID: id}); err != nil {
			t.Fatalf("Append %s: %v", id, err)
		}
	}

	pending, err := journal.Pending(ctx, 2)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("Pending с limit = %d, ожидалось 2", len(pending))
	}
}

func TestMemoryJournalRejectsEmptyID(t *testing.T) {
	journal := NewMemoryJournal()
	if err := journal.Append(context.Background(), Message{ID: "  "}); err == nil {
		t.Fatal("запись без ID должна быть отвергнута")
	}
}

func TestMemoryJournalMarkUnknownID(t *testing.T) {
	journal := NewMemoryJournal()
	if err := journal.MarkPublished(context.Background(), "нет-такой"); err == nil {
		t.Fatal("подтверждение неизвестной записи должно быть ошибкой")
	}
}

func TestMemoryJournalRespectsCancelledContext(t *testing.T) {
	journal := NewMemoryJournal()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := journal.Append(ctx, Message{ID: "m1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Append отменённого контекста = %v", err)
	}
	if _, err := journal.Pending(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Pending отменённого контекста = %v", err)
	}
	if err := journal.MarkPublished(ctx, "m1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("MarkPublished отменённого контекста = %v", err)
	}
}

func TestMemoryJournalConcurrent(t *testing.T) {
	journal := NewMemoryJournal()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := journal.Append(ctx, Message{ID: "m"}); err != nil {
				t.Errorf("Append: %v", err)
			}
			if _, err := journal.Pending(ctx, 0); err != nil {
				t.Errorf("Pending: %v", err)
			}
			if err := journal.MarkPublished(ctx, "m"); err != nil {
				t.Errorf("MarkPublished: %v", err)
			}
		}()
	}
	wg.Wait()
}
