package command

import (
	"context"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// RecordApproval — вход решения оператора по черновику необратимого шага.
type RecordApproval struct {
	RunID         string
	TenantID      string
	DraftRef      string
	DraftVersion  int
	Approved      bool
	Actor         string
	Reason        string
	CorrelationID string
	CausationID   string
}

// RecordApprovalHandler фиксирует решение оператора: положительное решение
// подтверждает точную версию черновика, отказ держит отправку невыполненной.
type RecordApprovalHandler struct {
	uow       ports.UnitOfWork
	workflows ports.WorkflowRunner
	clock     ports.Clock
	ids       ports.IDGenerator
}

// NewRecordApprovalHandler собирает сценарий решения оператора.
func NewRecordApprovalHandler(uow ports.UnitOfWork, workflows ports.WorkflowRunner, clock ports.Clock, ids ports.IDGenerator) *RecordApprovalHandler {
	return &RecordApprovalHandler{uow: uow, workflows: workflows, clock: clock, ids: ids}
}

// Handle записывает решение в агрегат и подаёт сигнал бегущему исполнению.
func (h *RecordApprovalHandler) Handle(ctx context.Context, in RecordApproval) error {
	tenant := domain.TenantID(in.TenantID)
	now := h.clock.Now()
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		run, err := tx.Runs().FindByID(ctx, domain.RunID(in.RunID), tenant)
		if err != nil {
			return err
		}
		revision := run.Revision()
		hdr := header(h.ids, in.CorrelationID, in.CausationID)
		if in.Approved {
			if err := run.GrantApproval(domain.DraftRef(in.DraftRef), in.DraftVersion, domain.ActorID(in.Actor), now, hdr); err != nil {
				return err
			}
		} else {
			if err := run.RejectApproval(domain.ActorID(in.Actor), in.Reason, now, hdr); err != nil {
				return err
			}
		}
		if err := tx.Runs().Save(ctx, run, revision); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, run.PullDomainEvents()...)
	})
	if err != nil {
		return err
	}
	if h.workflows != nil {
		return h.workflows.SignalApproval(ctx, ports.ApprovalDecisionInput{
			RunID:        in.RunID,
			TenantID:     in.TenantID,
			DraftRef:     in.DraftRef,
			DraftVersion: in.DraftVersion,
			Approved:     in.Approved,
			Actor:        in.Actor,
			Reason:       in.Reason,
		})
	}
	return nil
}
