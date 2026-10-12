package blocks_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/agent-orchestration/internal/adapters/blocks"
	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// approvalStub — тестовый двойник порта подачи черновика на утверждение.
type approvalStub struct {
	err   error
	got   ports.ApprovalSubmission
	calls int
}

func (s *approvalStub) SubmitForApproval(_ context.Context, in ports.ApprovalSubmission) error {
	s.calls++
	s.got = in
	return s.err
}

// approvalInput — вход шага ожидания утверждения: артефакт — ссылка на материал.
func approvalInput() ports.StepInput {
	return ports.StepInput{
		RunID: "run-1", StepID: "await-approval", TenantID: "tenant-1",
		MissionID: "mission-1", CompanyID: "company-1", ArtifactRef: "company-1@v2",
	}
}

func TestAwaitApprovalExecutorSubmitsAndWaits(t *testing.T) {
	contract := &approvalStub{}
	result, err := blocks.NewAwaitApprovalExecutor(contract).Execute(context.Background(), approvalInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsSuccess() || !result.AwaitApproval {
		t.Fatalf("исход = %q ожидание = %v", result.Outcome.Kind(), result.AwaitApproval)
	}
	if string(result.DraftRef) != "company-1@v2" || result.DraftVersion != 2 {
		t.Fatalf("черновик = %q v%d", result.DraftRef, result.DraftVersion)
	}
	if contract.got.MaterialRef != "company-1@v2" || contract.got.DraftVersion != 2 {
		t.Fatalf("подача = %+v", contract.got)
	}
}

func TestAwaitApprovalExecutorOwnerOutcomes(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantKind domain.BlockOutcomeKind
	}{
		{"недоступность", ports.ErrApprovalSubmitUnavailable, domain.OutcomeUnavailable},
		{"отказ владельца", ports.ErrApprovalSubmitRejected, domain.OutcomeFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := blocks.NewAwaitApprovalExecutor(&approvalStub{err: tc.err}).Execute(context.Background(), approvalInput())
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.Outcome.Kind() != tc.wantKind {
				t.Fatalf("исход = %q, ожидался %q", result.Outcome.Kind(), tc.wantKind)
			}
		})
	}
}

func TestAwaitApprovalExecutorWithoutDraft(t *testing.T) {
	contract := &approvalStub{}
	in := approvalInput()
	in.ArtifactRef = " "
	result, err := blocks.NewAwaitApprovalExecutor(contract).Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsUnavailable() || contract.calls != 0 {
		t.Fatalf("исход = %q, вызовов = %d", result.Outcome.Kind(), contract.calls)
	}
}

func TestAwaitApprovalExecutorWithoutContract(t *testing.T) {
	result, err := blocks.NewAwaitApprovalExecutor(nil).Execute(context.Background(), approvalInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsUnavailable() {
		t.Fatalf("исход = %q, ожидалась недоступность", result.Outcome.Kind())
	}
}

// publisherStub — тестовый двойник издателя approved-action.
type publisherStub struct {
	err       error
	published []approvedaction.ApprovedAction
}

func (p *publisherStub) Publish(_ context.Context, action approvedaction.ApprovedAction) error {
	if p.err != nil {
		return p.err
	}
	p.published = append(p.published, action)
	return nil
}

// approvedActionStub — тестовый двойник источника утверждённого действия.
type approvedActionStub struct {
	action approvedaction.ApprovedAction
	err    error
	got    ports.ApprovedActionLookup
}

func (s *approvedActionStub) ApprovedAction(_ context.Context, in ports.ApprovedActionLookup) (approvedaction.ApprovedAction, error) {
	s.got = in
	return s.action, s.err
}

func TestSendExecutorPublishesApprovedAction(t *testing.T) {
	publisher := &publisherStub{}
	source := &approvedActionStub{action: approvedaction.ApprovedAction{ApprovedActionID: "act-1"}}
	result, err := blocks.NewSendExecutor(publisher, source).Execute(context.Background(), approvalInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsSuccess() {
		t.Fatalf("исход = %q", result.Outcome.Kind())
	}
	if string(result.DraftRef) != "company-1@v2" || result.DraftVersion != 2 {
		t.Fatalf("черновик = %q v%d: шаг отправки должен возвращать версию решения", result.DraftRef, result.DraftVersion)
	}
	if source.got.DraftRef != "company-1@v2" || len(publisher.published) != 1 {
		t.Fatalf("запрос = %+v опубликовано = %d", source.got, len(publisher.published))
	}
}

func TestSendExecutorNotSanctioned(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"решение не принято", ports.ErrApprovedActionPending},
		{"оператор отклонил", ports.ErrApprovedActionRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			publisher := &publisherStub{}
			result, err := blocks.NewSendExecutor(publisher, &approvedActionStub{err: tc.err}).Execute(context.Background(), approvalInput())
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if result.Outcome.Kind() != domain.OutcomeFailed {
				t.Fatalf("исход = %q, ожидался отказ", result.Outcome.Kind())
			}
			if len(publisher.published) != 0 {
				t.Fatal("отправка без решения оператора недопустима")
			}
		})
	}
}

func TestSendExecutorSourceUnavailable(t *testing.T) {
	result, err := blocks.NewSendExecutor(&publisherStub{}, &approvedActionStub{err: ports.ErrApprovedActionUnavailable}).Execute(context.Background(), approvalInput())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Outcome.IsUnavailable() {
		t.Fatalf("исход = %q, ожидалась недоступность", result.Outcome.Kind())
	}
}

func TestSendExecutorWithoutContracts(t *testing.T) {
	cases := []struct {
		name      string
		publisher approvedaction.Publisher
		source    ports.ApprovedActionSource
	}{
		{"нет издателя", nil, &approvedActionStub{}},
		{"нет источника", &publisherStub{}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := blocks.NewSendExecutor(tc.publisher, tc.source).Execute(context.Background(), approvalInput())
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if !result.Outcome.IsUnavailable() {
				t.Fatalf("исход = %q, ожидалась недоступность", result.Outcome.Kind())
			}
		})
	}
}

func TestSendExecutorTransportErrorPropagates(t *testing.T) {
	transport := errors.New("сбой транспорта")
	publisher := &publisherStub{err: transport}
	if _, err := blocks.NewSendExecutor(publisher, &approvedActionStub{}).Execute(context.Background(), approvalInput()); !errors.Is(err, transport) {
		t.Fatalf("ошибка = %v, ожидалась транспортная", err)
	}
}
