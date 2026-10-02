package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// ChangeAuthority — команда изменения условий действующего основания обработки:
// срока хранения и окна действия.
type ChangeAuthority struct {
	BasisID       domain.BasisID
	TenantID      domain.TenantID
	RetentionDays int
	ValidFrom     time.Time
	ValidUntil    time.Time
	Actor         domain.ActorID
	Reason        string
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// ChangeAuthorityHandler — use case изменения основания.
type ChangeAuthorityHandler struct {
	repo  ports.ProcessingBasisRepository
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewChangeAuthorityHandler собирает use case.
func NewChangeAuthorityHandler(repo ports.ProcessingBasisRepository, clock ports.Clock, ids ports.IDGenerator) *ChangeAuthorityHandler {
	return &ChangeAuthorityHandler{repo: repo, clock: clock, ids: ids}
}

// Handle загружает основание, применяет изменение и сохраняет агрегат с
// оптимистичной блокировкой. Отозванное основание не меняется.
func (h *ChangeAuthorityHandler) Handle(ctx context.Context, cmd ChangeAuthority) (BasisResult, error) {
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	retention, err := domain.NewRetentionPeriod(cmd.RetentionDays)
	if err != nil {
		return BasisResult{}, err
	}
	validFrom := cmd.ValidFrom
	if validFrom.IsZero() {
		validFrom = at
	}
	validUntil := cmd.ValidUntil
	if validUntil.IsZero() {
		validUntil = retention.Until(validFrom)
	}

	basis, expected, err := loadForChange(ctx, h.repo, cmd.BasisID, cmd.TenantID)
	if err != nil {
		return BasisResult{}, err
	}
	events, err := basis.ChangeAuthority(domain.ChangeAuthorityParams{
		Retention:     retention,
		ValidFrom:     validFrom,
		ValidUntil:    validUntil,
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
