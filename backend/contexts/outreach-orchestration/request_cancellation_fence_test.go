package outreachorchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	inmemory "lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

type cancellationFenceAccess struct{}

func (cancellationFenceAccess) CanAct(context.Context, domain.ActorID, domain.TenantID) (bool, error) {
	return true, nil
}

type cancellationFenceClock struct{}

func (cancellationFenceClock) Now() time.Time { return time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC) }

func cancellationFenceHandoff() command.SubmitHandoff {
	return command.SubmitHandoff{RequestID: "req-1", TenantID: "tenant-1", Items: []command.SubmitHandoffItem{
		{LeadID: "lead-a", RecipientRef: "recipient-a", MaterialRef: "material-a"},
		{LeadID: "lead-b", RecipientRef: "recipient-b", MaterialRef: "material-b"},
	}}
}

func TestRequestCancellationBeforeHandoffPersistsTombstoneAndRejectsLateCommand(t *testing.T) {
	ctx := context.Background()
	uow := inmemory.NewUnitOfWork()
	cancel := command.NewCancelRequestHandler(uow)
	cmd := command.CancelRequest{TenantID: "tenant-1", RequestID: "req-1"}
	if err := cancel.Handle(ctx, cmd); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := cancel.Handle(ctx, cmd); err != nil {
		t.Fatalf("повтор Cancel: %v", err)
	}

	handoff := command.NewSubmitHandoffHandler(cancellationFenceAccess{}, cancellationFenceClock{}, uow)
	if _, err := handoff.Handle(ctx, cancellationFenceHandoff()); !errors.Is(err, command.ErrHandoffRejected) {
		t.Fatalf("поздний handoff должен быть отвергнут, ошибка=%v", err)
	}
	if _, err := uow.Repository().FindByID(ctx, domain.CampaignID("handoff-req-1")); err == nil {
		t.Fatal("кампания не должна создаваться после отмены")
	}
}

func TestRequestCancellationAfterHandoffBlocksSingleAndBatchApproval(t *testing.T) {
	ctx := context.Background()
	uow := inmemory.NewUnitOfWork()
	access, clock := cancellationFenceAccess{}, cancellationFenceClock{}
	if _, err := command.NewSubmitHandoffHandler(access, clock, uow).Handle(ctx, cancellationFenceHandoff()); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	if err := command.NewCancelRequestHandler(uow).Handle(ctx, command.CancelRequest{TenantID: "tenant-1", RequestID: "req-1"}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	campaign, err := uow.Repository().FindByID(ctx, domain.CampaignID("handoff-req-1"))
	if err != nil {
		t.Fatalf("кампания должна сохраниться для чтения: %v", err)
	}
	if len(campaign.Drafts()) != 2 {
		t.Fatalf("отмена не должна удалять черновики: %d", len(campaign.Drafts()))
	}
	draft := campaign.Drafts()[0]
	approve := command.NewApproveDraftHandler(access, clock, uow)
	_, err = approve.Handle(ctx, command.ApproveDraft{CampaignID: campaign.ID(), TenantID: campaign.TenantID(), Actor: "operator-1", DraftID: draft.ID(), DecisionID: "cancelled-decision", ExpectedVersion: draft.Version()})
	if !errors.Is(err, command.ErrRequestCancelled) {
		t.Fatalf("одиночное утверждение должно быть fenced: %v", err)
	}

	batch := command.NewApproveBatchHandler(access, clock, uow, func(context.Context, domain.RiskFactsRequest) ([]domain.RiskReason, error) { return nil, nil })
	_, err = batch.Handle(ctx, command.ApproveBatch{CampaignID: campaign.ID(), TenantID: campaign.TenantID(), Actor: "operator-1", Digest: "valid-nonempty", Composition: []command.BatchApproveItem{{DraftID: draft.ID(), Version: draft.Version()}}})
	if !errors.Is(err, command.ErrRequestCancelled) {
		t.Fatalf("пакетное утверждение должно быть fenced: %v", err)
	}
}

type cancellationRecorder struct {
	activeTransaction *bool
	calls             []approvedaction.CampaignCancellation
	err               error
}

func (r *cancellationRecorder) CancelCampaign(_ context.Context, cancellation approvedaction.CampaignCancellation) error {
	if r.activeTransaction != nil && *r.activeTransaction {
		return errors.New("Canceller вызван внутри owner transaction")
	}
	r.calls = append(r.calls, cancellation)
	return r.err
}

type transactionTrackingUnitOfWork struct {
	ports.UnitOfWork
	active bool
}

func (u *transactionTrackingUnitOfWork) WithinTransaction(ctx context.Context, fn func(ports.Transaction) error) error {
	u.active = true
	defer func() { u.active = false }()
	return u.UnitOfWork.WithinTransaction(ctx, fn)
}

func TestRequestCancellationRetryRepeatsCampaignPropagationAfterCommit(t *testing.T) {
	ctx := context.Background()
	base := inmemory.NewUnitOfWork()
	uow := &transactionTrackingUnitOfWork{UnitOfWork: base}
	canceller := &cancellationRecorder{activeTransaction: &uow.active}
	handler := command.NewCancelRequestHandler(uow, canceller)
	cancel := command.CancelRequest{TenantID: " tenant-1 ", RequestID: " req-1 "}

	if err := handler.Handle(ctx, cancel); err != nil {
		t.Fatalf("первая отмена: %v", err)
	}
	if err := handler.Handle(ctx, cancel); err != nil {
		t.Fatalf("повтор отмены: %v", err)
	}
	if len(canceller.calls) != 2 {
		t.Fatalf("повтор должен повторно вызвать downstream, вызовов=%d", len(canceller.calls))
	}
	want := approvedaction.CampaignCancellation{TenantID: "tenant-1", CampaignID: "handoff-req-1"}
	if canceller.calls[0] != want || canceller.calls[1] != want {
		t.Fatalf("downstream cancellation не совпадает: %+v", canceller.calls)
	}
}

func TestRequestCancellationUnavailableKeepsTombstoneAndCanRetry(t *testing.T) {
	ctx := context.Background()
	uow := inmemory.NewUnitOfWork()
	canceller := &cancellationRecorder{err: errors.New("delivery owner offline")}
	handler := command.NewCancelRequestHandler(uow, canceller)
	cancel := command.CancelRequest{TenantID: "tenant-1", RequestID: "req-1"}

	if err := handler.Handle(ctx, cancel); !errors.Is(err, command.ErrCancellationPending) {
		t.Fatalf("недоступность downstream должна быть явной pending-ошибкой, err=%v", err)
	}
	if _, err := command.NewSubmitHandoffHandler(cancellationFenceAccess{}, cancellationFenceClock{}, uow).Handle(ctx, cancellationFenceHandoff()); !errors.Is(err, command.ErrHandoffRejected) {
		t.Fatalf("owner tombstone должен сохраниться после downstream failure и блокировать handoff: %v", err)
	}

	canceller.err = nil
	if err := handler.Handle(ctx, cancel); err != nil {
		t.Fatalf("повтор отмены после восстановления downstream: %v", err)
	}
	if len(canceller.calls) != 2 {
		t.Fatalf("повтор должен заново вызвать downstream, вызовов=%d", len(canceller.calls))
	}
}

var _ ports.Clock = cancellationFenceClock{}
