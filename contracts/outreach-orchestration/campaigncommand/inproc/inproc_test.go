package inproc

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/contracts/outreach-orchestration/campaigncommand"
)

func validSubmission() campaigncommand.Submission {
	return campaigncommand.Submission{
		TenantID:     "tenant-1",
		RunID:        "run-1",
		CompanyID:    "company-1",
		DraftRef:     "company-1@v1",
		DraftVersion: 1,
		MaterialRef:  "company-1@v1",
	}
}

// stubPort — управляемая реализация владельца.
type stubPort struct {
	result campaigncommand.Result
	err    error
	calls  int
	block  chan struct{}
}

func (s *stubPort) SubmitForApproval(ctx context.Context, _ campaigncommand.Submission) (campaigncommand.Result, error) {
	s.calls++
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return campaigncommand.Result{}, ctx.Err()
		}
	}
	return s.result, s.err
}

func TestBindRejectsNilImplementation(t *testing.T) {
	if _, err := Bind(nil); err == nil {
		t.Fatal("Bind(nil) не отвергнут")
	}
}

func TestAdapterPassesThroughOwnerResult(t *testing.T) {
	impl := &stubPort{result: campaigncommand.Result{CampaignID: "campaign-1", DraftRef: "company-1@v1", DraftVersion: 1, Repeated: true}}
	adapter, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	result, err := adapter.SubmitForApproval(context.Background(), validSubmission())
	if err != nil {
		t.Fatalf("SubmitForApproval: %v", err)
	}
	if result != impl.result {
		t.Fatalf("результат владельца изменён: %+v", result)
	}
}

func TestAdapterRejectsInvalidCommandWithoutCallingOwner(t *testing.T) {
	impl := &stubPort{}
	adapter, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if _, err := adapter.SubmitForApproval(context.Background(), campaigncommand.Submission{}); !errors.Is(err, campaigncommand.ErrRejected) {
		t.Fatalf("ожидалась ErrRejected, получено %v", err)
	}
	if impl.calls != 0 {
		t.Fatalf("невалидная команда дошла до владельца: вызовов %d", impl.calls)
	}
}

func TestAdapterTimesOutBlockingOwner(t *testing.T) {
	impl := &stubPort{block: make(chan struct{})}
	defer close(impl.block)
	adapter, err := Bind(impl, WithTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if _, err := adapter.SubmitForApproval(context.Background(), validSubmission()); !errors.Is(err, campaigncommand.ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
}

func TestAdapterNilIsUnavailable(t *testing.T) {
	var adapter *Adapter
	if _, err := adapter.SubmitForApproval(context.Background(), validSubmission()); !errors.Is(err, campaigncommand.ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
}
