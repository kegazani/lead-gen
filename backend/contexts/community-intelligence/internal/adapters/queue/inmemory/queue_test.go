package inmemory

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

func TestQueueEnqueueDequeue(t *testing.T) {
	queue := NewBuffered(1)
	ctx := context.Background()
	cmd := ports.ReadCommand{CommunityID: "c-1", TenantID: "t-1"}
	if err := queue.Enqueue(ctx, cmd); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	got, ok := queue.Dequeue(ctx)
	if !ok {
		t.Fatal("Dequeue вернул false при непустой очереди")
	}
	if got.CommunityID != "c-1" {
		t.Fatalf("команда: %+v", got)
	}
}

func TestQueueDequeueStopsOnCancel(t *testing.T) {
	queue := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := queue.Dequeue(ctx); ok {
		t.Fatal("Dequeue должен вернуть false при отменённом контексте")
	}
}

func TestQueueEnqueueStopsOnCancel(t *testing.T) {
	queue := New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := queue.Enqueue(ctx, ports.ReadCommand{}); err == nil {
		t.Fatal("Enqueue должен вернуть ошибку при отменённом контексте")
	}
}
