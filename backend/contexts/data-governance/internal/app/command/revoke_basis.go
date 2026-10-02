package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// RevokeBasis — команда отзыва основания обработки. Причина обязательна
// (auditability).
type RevokeBasis struct {
	BasisID       domain.BasisID
	TenantID      domain.TenantID
	Actor         domain.ActorID
	Reason        string
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// RevokeBasisHandler — use case отзыва основания.
type RevokeBasisHandler struct {
	repo  ports.ProcessingBasisRepository
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewRevokeBasisHandler собирает use case.
func NewRevokeBasisHandler(repo ports.ProcessingBasisRepository, clock ports.Clock, ids ports.IDGenerator) *RevokeBasisHandler {
	return &RevokeBasisHandler{repo: repo, clock: clock, ids: ids}
}

// Handle загружает основание, отзывает его и сохраняет агрегат с оптимистичной
// блокировкой.
func (h *RevokeBasisHandler) Handle(ctx context.Context, cmd RevokeBasis) (BasisResult, error) {
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	basis, expected, err := loadForChange(ctx, h.repo, cmd.BasisID, cmd.TenantID)
	if err != nil {
		return BasisResult{}, err
	}
	events, err := basis.Revoke(domain.RevokeParams{
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
