package inproc

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"lead-gen/contracts/lead-management/deliverytarget"
)

// stubResolver — реализация владельца для проверки адаптера.
type stubResolver struct {
	calls   int64
	address string
	err     error
	delay   time.Duration
	lastTen string
	lastRef string
}

func (s *stubResolver) ResolveRecipient(ctx context.Context, tenantID, recipientRef string) (deliverytarget.Recipient, error) {
	atomic.AddInt64(&s.calls, 1)
	s.lastTen, s.lastRef = tenantID, recipientRef
	if s.delay > 0 {
		select {
		case <-ctx.Done():
			return deliverytarget.Recipient{}, ctx.Err()
		case <-time.After(s.delay):
		}
	}
	if s.err != nil {
		return deliverytarget.Recipient{}, s.err
	}
	return deliverytarget.Recipient{Address: s.address}, nil
}

func TestBind_RequiresImplementation(t *testing.T) {
	if _, err := Bind(nil); err == nil {
		t.Fatal("Bind без реализации должен отвергаться")
	}
}

func TestAdapter_PassesThrough(t *testing.T) {
	impl := &stubResolver{address: "lead@example.com"}
	adapter, err := Bind(impl)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := adapter.ResolveRecipient(context.Background(), "tenant-1", "ref-1")
	if err != nil {
		t.Fatalf("ResolveRecipient: %v", err)
	}
	if recipient.Address != "lead@example.com" {
		t.Fatalf("адрес: %q", recipient.Address)
	}
	if impl.lastTen != "tenant-1" || impl.lastRef != "ref-1" {
		t.Fatalf("аргументы: tenant=%q ref=%q", impl.lastTen, impl.lastRef)
	}
	if atomic.LoadInt64(&impl.calls) != 1 {
		t.Fatalf("ожидался один вызов, got %d", impl.calls)
	}
}

func TestAdapter_PropagatesRejection(t *testing.T) {
	impl := &stubResolver{err: deliverytarget.ErrRejected}
	adapter, err := Bind(impl)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ResolveRecipient(context.Background(), "tenant-1", "ref-1"); !errors.Is(err, deliverytarget.ErrRejected) {
		t.Fatalf("ожидалась ошибка владельца, got %v", err)
	}
}

func TestAdapter_RejectsEmptyInput(t *testing.T) {
	impl := &stubResolver{address: "lead@example.com"}
	adapter, err := Bind(impl)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range [][2]string{{"", "ref-1"}, {"tenant-1", ""}, {"  ", "  "}} {
		if _, err := adapter.ResolveRecipient(context.Background(), in[0], in[1]); !errors.Is(err, deliverytarget.ErrRejected) {
			t.Fatalf("пустые входы %q/%q должны отвергаться доменным отказом, got %v", in[0], in[1], err)
		}
	}
	if atomic.LoadInt64(&impl.calls) != 0 {
		t.Fatal("пустые входы не должны доходить до владельца")
	}
}

func TestAdapter_RespectsTimeout(t *testing.T) {
	impl := &stubResolver{address: "lead@example.com", delay: time.Second}
	adapter, err := Bind(impl, WithTimeout(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ResolveRecipient(context.Background(), "tenant-1", "ref-1"); err == nil {
		t.Fatal("вызов должен быть прерван по бюджету")
	}
}

func TestAdapter_RespectsCancelledContext(t *testing.T) {
	impl := &stubResolver{address: "lead@example.com"}
	adapter, err := Bind(impl)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.ResolveRecipient(ctx, "tenant-1", "ref-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидалась отмена контекста, got %v", err)
	}
	if atomic.LoadInt64(&impl.calls) != 0 {
		t.Fatal("отменённый вызов не должен доходить до владельца")
	}
}
