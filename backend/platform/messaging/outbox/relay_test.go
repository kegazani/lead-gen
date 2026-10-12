package outbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/observability/prommetrics"
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
	failure   error
	recorded  []recordedPublish
}

func (p *fakePublisher) Publish(_ context.Context, exchange, routingKey string, body []byte, headers map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts++
	if p.attempts <= p.failFirst {
		if p.failure != nil {
			return p.failure
		}
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

func TestRelayLogsOnlySafeFailureCategory(t *testing.T) {
	secret := "sentinel-secret alice@example.com 550e8400-e29b-41d4-a716-446655440000"
	newJournal := func() *MemoryJournal {
		journal := NewMemoryJournal()
		if err := journal.Append(context.Background(), Message{ID: "event-1", Body: []byte("body"), Headers: map[string]string{"correlation_id": "corr-1"}}); err != nil {
			t.Fatal(err)
		}
		return journal
	}
	cases := []struct {
		name     string
		journal  func() Journal
		pub      *fakePublisher
		category string
	}{
		{name: "journal_read", journal: func() Journal { return failingPendingJournal{err: fmt.Errorf("wrapped: %w", errors.New(secret))} }, pub: &fakePublisher{}, category: "journal_read"},
		{name: "publish", journal: func() Journal { return newJournal() }, pub: &fakePublisher{failFirst: 1, failure: fmt.Errorf("wrapped: %w", errors.New(secret))}, category: "publish"},
		{name: "mark_published", journal: func() Journal {
			return failingMarkJournal{MemoryJournal: newJournal(), err: fmt.Errorf("wrapped: %w", errors.New(secret))}
		}, pub: &fakePublisher{}, category: "mark_published"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			relay := NewRelay(tc.journal(), tc.pub, time.Second).WithLogger(observ.NewLoggerWithWriter(&output))
			relay.publish(context.Background())
			logged := output.String()
			for _, unsafe := range []string{"sentinel-secret", "alice@example.com", "550e8400-e29b-41d4-a716-446655440000", "wrapped:"} {
				if strings.Contains(logged, unsafe) {
					t.Fatalf("исходная ошибка попала в JSON-лог (%q): %s", unsafe, logged)
				}
			}
			if !strings.Contains(logged, `"failure_category":"`+tc.category+`"`) {
				t.Fatalf("лог не содержит безопасную категорию %q: %s", tc.category, logged)
			}
		})
	}
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

// TestRelayLogsAndCountsPublishFailure проверяет, что ошибка публикации не
// проглатывается: она логируется с корреляцией записи, увеличивает счётчик
// отказов, запись остаётся неподтверждённой, а цикл продолжает проходы
// (design.md, Решения 4 и 5).
func TestRelayLogsAndCountsPublishFailure(t *testing.T) {
	journal := NewMemoryJournal()
	ctx := context.Background()

	if err := journal.Append(ctx, Message{ID: "m1", Body: []byte("one"), Headers: map[string]string{"event_id": "e1", "correlation_id": "corr-1"}}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Брокер устойчиво недоступен: каждый проход публикации падает.
	pub := &fakePublisher{failFirst: 1 << 20}
	var buf bytes.Buffer
	metrics := observ.NewMetrics().(*observ.MemoryMetrics)
	relay := NewRelay(journal, pub, time.Millisecond).
		WithLogger(observ.NewLoggerWithWriter(&buf)).
		WithMetrics(metrics)

	cancel, done := runRelay(t, relay)
	time.Sleep(20 * time.Millisecond)
	cancel()
	waitRelayStop(t, done)

	// Запись не подтверждена и остаётся для повтора.
	pending, err := journal.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "m1" {
		t.Fatalf("запись потеряна при отказе брокера: %+v", pending)
	}

	// Счётчик отказов вырос.
	if got := metrics.Counter(outboxPublishFailuresTotal, nil); got == 0 {
		t.Fatalf("счётчик отказов публикации не вырос")
	}

	// Ошибка залогирована с корреляцией записи.
	if !strings.Contains(buf.String(), "публикация записи не удалась") {
		t.Fatalf("лог не несёт ошибку публикации: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "corr-1") {
		t.Fatalf("лог не несёт correlation_id записи: %s", buf.String())
	}
}

// TestRelayPublishFailureAppearsInExposition проверяет сквозной путь счётчика
// отказов публикации через реальный адаптер Prometheus: при устойчивом отказе
// Publish счётчик outbox_publish_failures_total растёт и виден в /metrics, а
// запись остаётся неподтверждённой (design.md, Решения 2 и 5).
func TestRelayPublishFailureAppearsInExposition(t *testing.T) {
	journal := NewMemoryJournal()
	ctx := context.Background()

	if err := journal.Append(ctx, Message{ID: "m1", Body: []byte("one"), Headers: map[string]string{"event_id": "e1", "correlation_id": "corr-1"}}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	pub := &fakePublisher{failFirst: 1 << 20}
	adapter := prommetrics.New()
	relay := NewRelay(journal, pub, time.Millisecond).WithMetrics(adapter)

	cancel, done := runRelay(t, relay)
	waitFor(t, func() bool {
		rec := httptest.NewRecorder()
		adapter.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		return strings.Contains(rec.Body.String(), "outbox_publish_failures_total")
	}, "счётчик отказов публикации не появился в /metrics")
	cancel()
	waitRelayStop(t, done)

	// Запись остаётся неподтверждённой для повтора (at-least-once).
	pending, err := journal.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != "m1" {
		t.Fatalf("запись потеряна при отказе брокера: %+v", pending)
	}
}

// failingPendingJournal — Journal, у которого чтение очереди всегда падает.
// Эмулирует отказ хранилища, а не брокера.
type failingPendingJournal struct{ err error }

func (f failingPendingJournal) Append(context.Context, Message) error           { return nil }
func (f failingPendingJournal) Pending(context.Context, int) ([]Message, error) { return nil, f.err }
func (f failingPendingJournal) MarkPublished(context.Context, string) error     { return nil }

// failingMarkJournal — Journal, у которого подтверждение записи всегда падает;
// чтение очереди делегирует MemoryJournal. Эмулирует отказ при MarkPublished.
type failingMarkJournal struct {
	*MemoryJournal
	err error
}

func (f failingMarkJournal) MarkPublished(context.Context, string) error { return f.err }

// TestRelayCountsOnlyPublishFailures проверяет, что счётчик отказов публикации
// учитывает только отказ publisher.Publish, а не ошибки чтения журнала или
// подтверждения записи (design.md, Решение 5): чужие ошибки лишь логируются.
func TestRelayCountsOnlyPublishFailures(t *testing.T) {
	t.Run("ошибка чтения журнала", func(t *testing.T) {
		metrics := observ.NewMetrics().(*observ.MemoryMetrics)
		var buf bytes.Buffer
		relay := NewRelay(failingPendingJournal{err: errors.New("журнал недоступен")}, &fakePublisher{}, time.Millisecond).
			WithLogger(observ.NewLoggerWithWriter(&buf)).
			WithMetrics(metrics)

		relay.publish(context.Background())

		if got := metrics.Counter(outboxPublishFailuresTotal, nil); got != 0 {
			t.Fatalf("ошибка чтения журнала увеличила счётчик отказов публикации: %d", got)
		}
		if !strings.Contains(buf.String(), "чтение журнала не удалось") {
			t.Fatalf("ошибка чтения журнала не залогирована: %s", buf.String())
		}
	})

	t.Run("ошибка подтверждения записи", func(t *testing.T) {
		journal := NewMemoryJournal()
		if err := journal.Append(context.Background(), Message{ID: "m1", Body: []byte("one"), Headers: map[string]string{"event_id": "e1"}}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		metrics := observ.NewMetrics().(*observ.MemoryMetrics)
		var buf bytes.Buffer
		relay := NewRelay(failingMarkJournal{MemoryJournal: journal, err: errors.New("подтверждение недоступно")}, &fakePublisher{}, time.Millisecond).
			WithLogger(observ.NewLoggerWithWriter(&buf)).
			WithMetrics(metrics)

		relay.publish(context.Background())

		if got := metrics.Counter(outboxPublishFailuresTotal, nil); got != 0 {
			t.Fatalf("ошибка подтверждения увеличила счётчик отказов публикации: %d", got)
		}
		if !strings.Contains(buf.String(), "подтверждение записи не удалось") {
			t.Fatalf("ошибка подтверждения не залогирована: %s", buf.String())
		}
	})
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
