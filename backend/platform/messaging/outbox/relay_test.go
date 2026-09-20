package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// recordedPublish — зафиксированный вызов публикации.
type recordedPublish struct {
	exchange   string
	routingKey string
	body       string
	eventID    string
}

// fakePublisher — Publisher для тестов без внешней сети. failFirst задаёт число
// первых попыток, которые завершаются ошибкой (эмуляция временной недоступности
// брокера).
type fakePublisher struct {
	mu        sync.Mutex
	attempts  int
	failFirst int
	recorded  []recordedPublish
}

func (p *fakePublisher) Publish(_ context.Context, exchange, routingKey string, body []byte, headers map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts++
	if p.attempts <= p.failFirst {
		return errors.New("брокер недоступен")
	}
	p.recorded = append(p.recorded, recordedPublish{
		exchange:   exchange,
		routingKey: routingKey,
		body:       string(body),
		eventID:    headers["event_id"],
	})
	return nil
}

func (p *fakePublisher) snapshot() []recordedPublish {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]recordedPublish, len(p.recorded))
	copy(out, p.recorded)
	return out
}

func TestRelayPublishesPendingAfterCommit(t *testing.T) {
	journal := NewMemoryJournal()
	pub := &fakePublisher{}
	ctx := context.Background()

	// Записи в журнале — это уже зафиксированные изменения: relay публикует их и
	// только их.
	if err := journal.Append(ctx, Message{ID: "m1", Exchange: "outreach-orchestration", RoutingKey: "approved-action.1", Body: []byte("one"), Headers: map[string]string{"event_id": "e1"}}); err != nil {
		t.Fatalf("Append m1: %v", err)
	}
	if err := journal.Append(ctx, Message{ID: "m2", Exchange: "outreach-orchestration", RoutingKey: "approved-action.1", Body: []byte("two"), Headers: map[string]string{"event_id": "e2"}}); err != nil {
		t.Fatalf("Append m2: %v", err)
	}

	cancel, done := runRelay(t, NewRelay(journal, pub, time.Millisecond))
	waitFor(t, func() bool { return len(pub.snapshot()) == 2 }, "relay не опубликовал обе записи")
	cancel()
	waitRelayStop(t, done)

	recorded := pub.snapshot()
	if recorded[0].eventID != "e1" || recorded[1].eventID != "e2" {
		t.Fatalf("порядок публикации нарушен: %+v", recorded)
	}
	if recorded[0].exchange != "outreach-orchestration" || recorded[0].routingKey != "approved-action.1" {
		t.Fatalf("адрес публикации = %+v", recorded[0])
	}

	pending, err := journal.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("после публикации осталось %d неподтверждённых записей", len(pending))
	}
}

func TestRelaySurvivesRestartWithoutDuplicates(t *testing.T) {
	journal := NewMemoryJournal()
	pub := &fakePublisher{}
	ctx := context.Background()

	if err := journal.Append(ctx, Message{ID: "m1", Body: []byte("one"), Headers: map[string]string{"event_id": "e1"}}); err != nil {
		t.Fatalf("Append m1: %v", err)
	}

	cancel, done := runRelay(t, NewRelay(journal, pub, time.Millisecond))
	waitFor(t, func() bool { return len(pub.snapshot()) == 1 }, "первый запуск не опубликовал запись")
	cancel()
	waitRelayStop(t, done)

	// Новое сообщение появилось уже после остановки relay.
	if err := journal.Append(ctx, Message{ID: "m2", Body: []byte("two"), Headers: map[string]string{"event_id": "e2"}}); err != nil {
		t.Fatalf("Append m2: %v", err)
	}

	cancel, done = runRelay(t, NewRelay(journal, pub, time.Millisecond))
	waitFor(t, func() bool { return len(pub.snapshot()) == 2 }, "повторный запуск не опубликовал новую запись")
	cancel()
	waitRelayStop(t, done)

	// Идемпотентность по ID: подтверждённая запись не публикуется повторно.
	counts := map[string]int{}
	for _, r := range pub.snapshot() {
		counts[r.eventID]++
	}
	if counts["e1"] != 1 || counts["e2"] != 1 {
		t.Fatalf("публикации по ID: %+v, ожидалось по одной", counts)
	}
}

func TestRelayRearributesAfterTransientPublishError(t *testing.T) {
	journal := NewMemoryJournal()
	ctx := context.Background()

	if err := journal.Append(ctx, Message{ID: "m1", Body: []byte("one"), Headers: map[string]string{"event_id": "e1"}}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Первая попытка публикации падает: запись остаётся в журнале и уходит на
	// следующем проходе, а не теряется.
	pub := &fakePublisher{failFirst: 1}
	cancel, done := runRelay(t, NewRelay(journal, pub, time.Millisecond))
	waitFor(t, func() bool { return len(pub.snapshot()) == 1 }, "relay не повторил публикацию после временного отказа")
	cancel()
	waitRelayStop(t, done)

	pending, err := journal.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("запись не подтверждена после успешного повтора: %+v", pending)
	}
}

func TestRelayKeepsMessagePendingWhileBrokerDown(t *testing.T) {
	journal := NewMemoryJournal()
	ctx := context.Background()

	if err := journal.Append(ctx, Message{ID: "m1", Body: []byte("one"), Headers: map[string]string{"event_id": "e1"}}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	pub := &fakePublisher{failFirst: 1 << 20}
	cancel, done := runRelay(t, NewRelay(journal, pub, time.Millisecond))
	time.Sleep(20 * time.Millisecond)
	cancel()
	waitRelayStop(t, done)

	if got := len(pub.snapshot()); got != 0 {
		t.Fatalf("публикаций = %d, ожидалось 0 при недоступном брокере", got)
	}
	pending, err := journal.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "m1" {
		t.Fatalf("запись потеряна при отказе брокера: %+v", pending)
	}
}

// runRelay запускает Run в отдельной горутине: метод блокирующий.
func runRelay(t *testing.T, relay *Relay) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- relay.Run(ctx) }()
	return cancel, done
}

// waitRelayStop дожидается штатного завершения Run после отмены контекста.
func waitRelayStop(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run не завершился после отмены контекста")
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
