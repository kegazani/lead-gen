package inbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lead-gen/backend/platform/transport/amqp"
)

func TestMemoryDeduperSeenThenRecord(t *testing.T) {
	deduper := NewMemoryDeduper()
	ctx := context.Background()

	seen, err := deduper.Seen(ctx, "evt-1")
	if err != nil {
		t.Fatalf("Seen: %v", err)
	}
	if seen {
		t.Fatal("новый event_id не должен считаться виденным")
	}

	if err := deduper.Record(ctx, "evt-1"); err != nil {
		t.Fatalf("Record: %v", err)
	}

	seen, err = deduper.Seen(ctx, "evt-1")
	if err != nil {
		t.Fatalf("Seen: %v", err)
	}
	if !seen {
		t.Fatal("записанный event_id должен считаться виденным")
	}
}

func TestMemoryDeduperRepeatRecordIsIdempotent(t *testing.T) {
	deduper := NewMemoryDeduper()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := deduper.Record(ctx, "evt-1"); err != nil {
			t.Fatalf("Record #%d: %v", i, err)
		}
	}
}

func TestMemoryDeduperRejectsEmptyEventID(t *testing.T) {
	deduper := NewMemoryDeduper()
	if _, err := deduper.Seen(context.Background(), "  "); err == nil {
		t.Fatal("пустой event_id при Seen должен быть ошибкой")
	}
	if err := deduper.Record(context.Background(), ""); err == nil {
		t.Fatal("пустой event_id при Record должен быть ошибкой")
	}
}

func TestMemoryDeduperRespectsCancelledContext(t *testing.T) {
	deduper := NewMemoryDeduper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := deduper.Seen(ctx, "evt-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Seen отменённого контекста = %v", err)
	}
	if err := deduper.Record(ctx, "evt-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Record отменённого контекста = %v", err)
	}
}

func TestMemoryDeduperConcurrent(t *testing.T) {
	deduper := NewMemoryDeduper()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := deduper.Seen(ctx, "evt-1"); err != nil {
				t.Errorf("Seen: %v", err)
			}
			if err := deduper.Record(ctx, "evt-1"); err != nil {
				t.Errorf("Record: %v", err)
			}
		}()
	}
	wg.Wait()

	seen, err := deduper.Seen(ctx, "evt-1")
	if err != nil || !seen {
		t.Fatalf("Seen = %v, err = %v", seen, err)
	}
}

// TestDedupPreventsSecondEffect проверяет главный инвариант at-least-once:
// повторная доставка сообщения не создаёт второго эффекта, потому что потребитель
// дедуплицирует по event_id.
func TestDedupPreventsSecondEffect(t *testing.T) {
	broker := amqp.NewInMemoryBroker(3)
	deduper := NewMemoryDeduper()
	ctx := context.Background()
	const queue = "message-delivery.approved-action"

	if err := broker.Publish(ctx, "outreach-orchestration", "approved-action.1", []byte("payload"), map[string]string{"event_id": "e1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var calls, effects int32
	handler := func(ctx context.Context, d amqp.Delivery) error {
		calls := atomic.AddInt32(&calls, 1)
		seen, err := deduper.Seen(ctx, d.EventID)
		if err != nil {
			return err
		}
		if seen {
			// Повтор: эффект не выполняется, сообщение подтверждается.
			return nil
		}
		atomic.AddInt32(&effects, 1)
		if err := deduper.Record(ctx, d.EventID); err != nil {
			return err
		}
		if calls == 1 {
			// Сбой после выполненного эффекта: брокер доставит повторно.
			return errors.New("сбой после эффекта")
		}
		return nil
	}

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- broker.Consume(runCtx, queue, handler) }()

	waitFor(t, func() bool { return atomic.LoadInt32(&calls) >= 2 }, "сообщение не доставлено повторно")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Consume: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Consume не завершился после отмены контекста")
	}

	if got := atomic.LoadInt32(&effects); got != 1 {
		t.Fatalf("эффектов = %d, ожидался один: повтор не должен создавать второй эффект", got)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("доставок = %d, ожидалось 2", got)
	}
	if dlq := broker.DLQ(queue); len(dlq) != 0 {
		t.Fatalf("DLQ = %+v, ожидалась пустая", dlq)
	}
}

// waitFor ждёт выполнения условия, чтобы не зависеть от точного момента события.
func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(msg)
}
