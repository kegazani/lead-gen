package idempotency

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestMemoryStoreMarksSeen(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	seen, err := store.Seen(ctx, "approved-action|tenant-1|draft-1|3")
	if err != nil {
		t.Fatalf("Seen: %v", err)
	}
	if seen {
		t.Fatal("новый ключ не должен считаться виденным")
	}

	if err := store.Record(ctx, "approved-action|tenant-1|draft-1|3"); err != nil {
		t.Fatalf("Record: %v", err)
	}

	seen, err = store.Seen(ctx, "approved-action|tenant-1|draft-1|3")
	if err != nil {
		t.Fatalf("Seen: %v", err)
	}
	if !seen {
		t.Fatal("записанный ключ должен считаться виденным")
	}
}

func TestMemoryStoreRepeatRecordIsIdempotent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := store.Record(ctx, "k"); err != nil {
			t.Fatalf("Record #%d: %v", i, err)
		}
	}
	if err := store.Record(ctx, "k"); err != nil {
		t.Fatalf("повторная запись: %v", err)
	}
}

func TestMemoryStoreRespectsCancelledContext(t *testing.T) {
	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.Seen(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Seen отменённого контекста = %v", err)
	}
	if err := store.Record(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Record отменённого контекста = %v", err)
	}
}

func TestMemoryStoreConcurrent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "key"
			if _, err := store.Seen(ctx, key); err != nil {
				t.Errorf("Seen: %v", err)
			}
			if err := store.Record(ctx, key); err != nil {
				t.Errorf("Record: %v", err)
			}
		}(i)
	}
	wg.Wait()

	seen, err := store.Seen(ctx, "key")
	if err != nil || !seen {
		t.Fatalf("Seen = %v, err = %v", seen, err)
	}
}
