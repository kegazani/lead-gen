package inproc

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/contracts/outreach-orchestration/approvedaction"
	"lead-gen/contracts/outreach-orchestration/draftdecision"
)

type stubSource struct {
	action approvedaction.ApprovedAction
	err    error
	calls  int
	block  chan struct{}
}

func (s *stubSource) ApprovedAction(ctx context.Context, _ draftdecision.Lookup) (approvedaction.ApprovedAction, error) {
	s.calls++
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return approvedaction.ApprovedAction{}, ctx.Err()
		}
	}
	return s.action, s.err
}

func TestBindRejectsNilImplementation(t *testing.T) {
	if _, err := Bind(nil); err == nil {
		t.Fatal("Bind(nil) не отвергнут")
	}
}

func TestAdapterPassesThroughOwnerResult(t *testing.T) {
	action := approvedaction.ApprovedAction{ApprovedActionID: "act-1"}
	impl := &stubSource{action: action}
	adapter, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	got, err := adapter.ApprovedAction(context.Background(), draftdecision.Lookup{TenantID: "t", DraftRef: "d", DraftVersion: 1})
	if err != nil {
		t.Fatalf("ApprovedAction: %v", err)
	}
	if got != action {
		t.Fatalf("результат владельца изменён: %+v", got)
	}
}

func TestAdapterPassesThroughOutcomes(t *testing.T) {
	impl := &stubSource{err: draftdecision.ErrPending}
	adapter, _ := Bind(impl)
	if _, err := adapter.ApprovedAction(context.Background(), draftdecision.Lookup{}); !errors.Is(err, draftdecision.ErrPending) {
		t.Fatalf("ожидалась ErrPending, получено %v", err)
	}
}

func TestAdapterTimesOutBlockingOwner(t *testing.T) {
	impl := &stubSource{block: make(chan struct{})}
	defer close(impl.block)
	adapter, _ := Bind(impl, WithTimeout(20*time.Millisecond))
	if _, err := adapter.ApprovedAction(context.Background(), draftdecision.Lookup{}); !errors.Is(err, draftdecision.ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
}

func TestAdapterNilIsUnavailable(t *testing.T) {
	var adapter *Adapter
	if _, err := adapter.ApprovedAction(context.Background(), draftdecision.Lookup{}); !errors.Is(err, draftdecision.ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
}
