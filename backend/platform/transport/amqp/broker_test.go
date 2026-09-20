package amqp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// queueForContract — имя очереди по конвенции <downstream>.<contract>.
const queueForContract = "message-delivery.approved-action"

func TestInMemoryBrokerDurableUntilAck(t *testing.T) {
	broker := NewInMemoryBroker(3)
	ctx := context.Background()

	if err := broker.Publish(ctx, "outreach-orchestration", "approved-action.1", []byte("payload"), map[string]string{"event_id": "e1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Публикация до потребителя не теряется: durable-очередь держит сообщение до ack.
	if got := broker.Pending(queueForContract); got != 1 {
		t.Fatalf("Pending до потребителя = %d, ожидалось 1", got)
	}

	received := make(chan Delivery, 1)
	cancel, done := startConsumer(t, broker, queueForContract, func(_ context.Context, d Delivery) error {
		received <- d
		return nil
	})
	defer stopConsumer(t, cancel, done)

	d := waitDelivery(t, received)
	if string(d.Body) != "payload" || d.EventID != "e1" || d.Redelivered {
		t.Fatalf("доставка = %+v", d)
	}
	waitFor(t, func() bool { return broker.Pending(queueForContract) == 0 }, "ack не снял сообщение с очереди")
	if dlq := broker.DLQ(queueForContract); len(dlq) != 0 {
		t.Fatalf("DLQ = %+v, ожидалась пустая", dlq)
	}
}

func TestInMemoryBrokerRetriesWithinAttemptsThenAcks(t *testing.T) {
	broker := NewInMemoryBroker(3)
	ctx := context.Background()

	if err := broker.Publish(ctx, "outreach-orchestration", "approved-action.1", []byte("payload"), map[string]string{"event_id": "e2"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var attempts int32
	var mu sync.Mutex
	var deliveries []Delivery
	cancel, done := startConsumer(t, broker, queueForContract, func(_ context.Context, d Delivery) error {
		mu.Lock()
		deliveries = append(deliveries, d)
		mu.Unlock()
		if atomic.AddInt32(&attempts, 1) < 3 {
			return errors.New("сбой обработки")
		}
		return nil
	})
	defer stopConsumer(t, cancel, done)

	waitFor(t, func() bool { return atomic.LoadInt32(&attempts) == 3 }, "сообщение не доставлено трижды в пределах попыток")
	waitFor(t, func() bool { return broker.Pending(queueForContract) == 0 }, "сообщение не подтверждено после успеха")

	if dlq := broker.DLQ(queueForContract); len(dlq) != 0 {
		t.Fatalf("DLQ = %+v, ожидалась пустая: попытка успешна до исчерпания", dlq)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deliveries) != 3 {
		t.Fatalf("доставок = %d, ожидалось 3", len(deliveries))
	}
	if deliveries[0].Redelivered {
		t.Fatal("первая доставка не должна быть помечена повтором")
	}
	if !deliveries[1].Redelivered || !deliveries[2].Redelivered {
		t.Fatal("повторы должны быть помечены Redelivered")
	}
}

func TestInMemoryBrokerMovesToDLQAfterAttemptsExhausted(t *testing.T) {
	broker := NewInMemoryBroker(2)
	ctx := context.Background()

	if err := broker.Publish(ctx, "outreach-orchestration", "approved-action.1", []byte("poison"), map[string]string{"event_id": "e3"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var attempts int32
	cancel, done := startConsumer(t, broker, queueForContract, func(_ context.Context, _ Delivery) error {
		atomic.AddInt32(&attempts, 1)
		return errors.New("постоянный сбой")
	})
	defer stopConsumer(t, cancel, done)

	waitFor(t, func() bool { return len(broker.DLQ(queueForContract)) == 1 }, "сообщение не ушло в DLQ после исчерпания попыток")
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("доставок = %d, ожидалось 2 (maxAttempts)", got)
	}
	if got := broker.Pending(queueForContract); got != 0 {
		t.Fatalf("Pending после DLQ = %d, ожидалось 0", got)
	}

	dlq := broker.DLQ(queueForContract)
	if string(dlq[0].Body) != "poison" || dlq[0].EventID != "e3" || !dlq[0].Redelivered {
		t.Fatalf("запись DLQ = %+v", dlq[0])
	}

	// После исчерпания попыток сообщение больше не доставляется.
	time.Sleep(30 * time.Millisecond)
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("после DLQ доставок = %d, ожидалось 2", got)
	}
}

func TestInMemoryBrokerSingleConsumerPerQueue(t *testing.T) {
	broker := NewInMemoryBroker(1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- broker.Consume(ctx, queueForContract, func(_ context.Context, _ Delivery) error { return nil })
	}()
	// Даём первому потребителю зарегистрироваться.
	waitFor(t, func() bool {
		broker.mu.Lock()
		defer broker.mu.Unlock()
		return broker.queues[queueForContract] != nil && broker.queues[queueForContract].running
	}, "потребитель не зарегистрировался")

	if err := broker.Consume(context.Background(), queueForContract, func(_ context.Context, _ Delivery) error { return nil }); err == nil {
		t.Fatal("второй потребитель одной очереди должен быть отвергнут")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Consume: %v", err)
	}
}

func TestInMemoryBrokerPublishRoutesByContract(t *testing.T) {
	broker := NewInMemoryBroker(3)
	ctx := context.Background()

	if err := broker.Publish(ctx, "outreach-orchestration", "approved-action.1", []byte("a"), nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	// Чужой контракт в очередь этого потребителя не попадает.
	if err := broker.Publish(ctx, "outreach-orchestration", "campaign-planned.2", []byte("b"), nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if got := broker.Pending(queueForContract); got != 1 {
		t.Fatalf("Pending = %d, ожидалось 1 (маршрут только по своему контракту)", got)
	}
}

// startConsumer запускает Consume в отдельной горутине: метод блокирующий.
func startConsumer(t *testing.T, broker *InMemoryBroker, queue string, handler Handler) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- broker.Consume(ctx, queue, handler) }()
	return cancel, done
}

// stopConsumer отменяет контекст и дожидается штатного завершения потребителя.
func stopConsumer(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Consume: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Consume не завершился после отмены контекста")
	}
}

// waitDelivery ждёт доставку из канала с ограничением по времени.
func waitDelivery(t *testing.T, ch <-chan Delivery) Delivery {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(2 * time.Second):
		t.Fatal("сообщение не доставлено")
		return Delivery{}
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
