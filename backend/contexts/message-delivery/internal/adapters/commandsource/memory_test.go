package commandsource

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
)

func testCommand() domain.DeliveryCommand {
	return domain.DeliveryCommand{CommandID: domain.CommandID("cmd-1"), TenantID: domain.TenantID("tenant-1")}
}

func TestMemory_EnqueueNext(t *testing.T) {
	source := New(2)
	if err := source.Enqueue(testCommand()); err != nil {
		t.Fatal(err)
	}
	if source.Len() != 1 {
		t.Fatalf("длина очереди: %d", source.Len())
	}
	command, ok, err := source.Next(context.Background())
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if command.CommandID != "cmd-1" {
		t.Fatalf("команда: %+v", command)
	}
}

func TestMemory_CloseEndsStream(t *testing.T) {
	source := New(1)
	source.Close()
	_, ok, err := source.Next(context.Background())
	if err != nil || ok {
		t.Fatalf("после закрытия: ok=%v err=%v", ok, err)
	}
	if err := source.Enqueue(testCommand()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Enqueue после закрытия: got %v", err)
	}
}

func TestMemory_ContextCancellation(t *testing.T) {
	source := New(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := source.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("отмена контекста: got %v", err)
	}
}

func TestMemory_ContextDeadline(t *testing.T) {
	source := New(1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, _, err := source.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("дедлайн контекста: got %v", err)
	}
}
