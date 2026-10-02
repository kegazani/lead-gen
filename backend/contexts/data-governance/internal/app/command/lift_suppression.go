package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// LiftSuppression — команда снятия записи стоп-листа.
type LiftSuppression struct {
	BasisID       domain.BasisID
	TenantID      domain.TenantID
	SuppressionID domain.SuppressionID
	Actor         domain.ActorID
	Reason        string
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// LiftSuppressionHandler — use case снятия записи стоп-листа.
type LiftSuppressionHandler struct {
	repo  ports.ProcessingBasisRepository
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewLiftSuppressionHandler собирает use case.
func NewLiftSuppressionHandler(repo ports.ProcessingBasisRepository, clock ports.Clock, ids ports.IDGenerator) *LiftSuppressionHandler {
	return &LiftSuppressionHandler{repo: repo, clock: clock, ids: ids}
}

// Handle снимает активную запись стоп-листа. Запись не удаляется: она становится
// неактивной и сохраняет историю решения.
func (h *LiftSuppressionHandler) Handle(ctx context.Context, cmd LiftSuppression) (BasisResult, error) {
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	basis, expected, err := loadForChange(ctx, h.repo, cmd.BasisID, cmd.TenantID)
	if err != nil {
		return BasisResult{}, err
	}
	events, err := basis.LiftSuppression(domain.LiftParams{
		EntryID:       cmd.SuppressionID,
		Actor:         cmd.Actor,
		Reason:        cmd.Reason,
		At:            at,
		EventID:       h.ids.NewEventID(),
		CorrelationID: cmd.CorrelationID,
		CausationID:   cmd.CausationID,
	})
	if err != nil {
		return BasisResult{}, err
	}
	if err := h.repo.Save(ctx, basis, expected); err != nil {
		return BasisResult{}, err
	}
	return result(basis, events), nil
}
