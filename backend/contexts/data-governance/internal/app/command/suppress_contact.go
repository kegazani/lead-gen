package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// SuppressContact — команда внесения контакта в стоп-лист.
type SuppressContact struct {
	BasisID       domain.BasisID
	TenantID      domain.TenantID
	SuppressionID domain.SuppressionID
	Contact       domain.ContactRef
	Reason        string
	Actor         domain.ActorID
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// SuppressContactHandler — use case внесения контакта в стоп-лист.
type SuppressContactHandler struct {
	repo  ports.ProcessingBasisRepository
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewSuppressContactHandler собирает use case.
func NewSuppressContactHandler(repo ports.ProcessingBasisRepository, clock ports.Clock, ids ports.IDGenerator) *SuppressContactHandler {
	return &SuppressContactHandler{repo: repo, clock: clock, ids: ids}
}

// Handle вносит контакт в стоп-лист основания. Повторное внесение контакта с
// активной записью отвергается доменом.
func (h *SuppressContactHandler) Handle(ctx context.Context, cmd SuppressContact) (BasisResult, error) {
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	basis, expected, err := loadForChange(ctx, h.repo, cmd.BasisID, cmd.TenantID)
	if err != nil {
		return BasisResult{}, err
	}
	events, err := basis.SuppressContact(domain.SuppressEntryParams{
		ID:            cmd.SuppressionID,
		Contact:       cmd.Contact,
		Reason:        cmd.Reason,
		Actor:         cmd.Actor,
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
