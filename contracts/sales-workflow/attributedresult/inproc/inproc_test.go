package inproc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"lead-gen/contracts/sales-workflow/attributedresult"
	"lead-gen/contracts/sales-workflow/envelope"
)

func valid() attributedresult.AttributedResult {
	result := attributedresult.NewAttributedResult(envelope.Envelope{
		EventID:       "evt-1",
		OccurredAt:    time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
	})
	result.DealID = "deal-1"
	result.ActionRef = "action-1"
	result.EvidenceRef = "evidence-1"
	result.ExternalRef = "crm-1"
	result.ConfirmedAt = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	return result
}

// recorder — реализация владельца: считает вызовы.
type recorder struct {
	mu    sync.Mutex
	calls int
}

func (r *recorder) Publish(context.Context, attributedresult.AttributedResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestBindRequiresImplementation(t *testing.T) {
	if _, err := Bind(nil); err == nil {
		t.Fatal("Bind с nil-реализацией вернул nil-ошибку")
	}
}

func TestPublishDelegatesAndRejectsInvalid(t *testing.T) {
	impl := &recorder{}
	publisher, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := publisher.Publish(context.Background(), valid()); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if impl.count() != 1 {
		t.Fatalf("реализация вызвана %d раз", impl.count())
	}
	if err := publisher.Publish(context.Background(), attributedresult.AttributedResult{}); err == nil {
		t.Fatal("пустой контракт не отвергнут")
	}
	if impl.count() != 1 {
		t.Fatalf("невалидный контракт дошёл до владельца: %d", impl.count())
	}
}

func TestPublishIdempotentByKey(t *testing.T) {
	impl := &recorder{}
	publisher, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	first := valid()
	if err := publisher.Publish(context.Background(), first); err != nil {
		t.Fatalf("первый Publish: %v", err)
	}
	repeat := first
	repeat.EventID = "evt-2"
	if err := publisher.Publish(context.Background(), repeat); err != nil {
		t.Fatalf("повторный Publish: %v", err)
	}
	if impl.count() != 1 {
		t.Fatalf("идемпотентность нарушена: вызовов %d", impl.count())
	}
}

func TestPublishRespectsCancelledContext(t *testing.T) {
	publisher, err := Bind(&recorder{})
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := publisher.Publish(ctx, valid()); !errors.Is(err, context.Canceled) {
		t.Fatalf("отменённый контекст: %v", err)
	}
}
