package inproc

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"lead-gen/contracts/message-delivery/deliverycommand"
)

func validCommand() deliverycommand.Command {
	return deliverycommand.Command{
		CommandID:        "cmd-1",
		TenantID:         "tenant-1",
		ApprovedActionID: "act-1",
		RecipientRef:     "ref-1",
		DeliveryAddress:  "lead@example.com",
		SenderDomain:     "mail.example.com",
		Channel:          deliverycommand.ChannelEmailName,
		CorrelationID:    "act-1",
	}
}

type stubPort struct {
	calls int64
	err   error
	delay time.Duration
}

func (s *stubPort) Deliver(ctx context.Context, _ deliverycommand.Command) (deliverycommand.Result, error) {
	atomic.AddInt64(&s.calls, 1)
	if s.delay > 0 {
		select {
		case <-ctx.Done():
			return deliverycommand.Result{}, ctx.Err()
		case <-time.After(s.delay):
		}
	}
	if s.err != nil {
		return deliverycommand.Result{}, s.err
	}
	return deliverycommand.Result{MessageID: "msg-1", Status: deliverycommand.StatusDelivered, AttemptNumber: 1, Recorded: true}, nil
}

func TestAdapter_PassesThrough(t *testing.T) {
	impl := &stubPort{}
	adapter, err := Bind(impl)
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Deliver(context.Background(), validCommand())
	if err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if result.MessageID != "msg-1" || result.Status != deliverycommand.StatusDelivered {
		t.Fatalf("результат: %+v", result)
	}
	if atomic.LoadInt64(&impl.calls) != 1 {
		t.Fatalf("ожидался один вызов, got %d", impl.calls)
	}
}

func TestAdapter_DeduplicatesByKey(t *testing.T) {
	impl := &stubPort{}
	adapter, err := Bind(impl)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	command := validCommand()
	if _, err := adapter.Deliver(ctx, command); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Deliver(ctx, command); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&impl.calls); got != 1 {
		t.Fatalf("повтор с тем же ключом не должен доходить до владельца, got %d вызовов", got)
	}
}

func TestAdapter_RejectsInvalidCommand(t *testing.T) {
	impl := &stubPort{}
	adapter, err := Bind(impl)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Deliver(context.Background(), deliverycommand.Command{}); err == nil {
		t.Fatal("невалидная команда должна отвергаться адаптером")
	}
	if atomic.LoadInt64(&impl.calls) != 0 {
		t.Fatal("невалидная команда не должна доходить до владельца")
	}
}

func TestAdapter_PropagatesError(t *testing.T) {
	impl := &stubPort{err: deliverycommand.ErrRejected}
	adapter, err := Bind(impl)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Deliver(context.Background(), validCommand()); !errors.Is(err, deliverycommand.ErrRejected) {
		t.Fatalf("ожидалась ошибка владельца, got %v", err)
	}
}

func TestAdapter_RespectsTimeout(t *testing.T) {
	impl := &stubPort{delay: time.Second}
	adapter, err := Bind(impl, WithTimeout(10*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Deliver(context.Background(), validCommand()); err == nil {
		t.Fatal("вызов должен быть прерван по бюджету")
	}
}

func TestBind_RequiresImplementation(t *testing.T) {
	if _, err := Bind(nil); err == nil {
		t.Fatal("Bind без реализации должен отвергаться")
	}
}
