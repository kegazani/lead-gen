package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// CreateBasis — команда создания основания обработки.
type CreateBasis struct {
	BasisID       domain.BasisID
	TenantID      domain.TenantID
	Purpose       string
	Category      domain.DataCategory
	RetentionDays int
	// ValidFrom — начало окна действия; пусто — момент команды.
	ValidFrom time.Time
	// ValidUntil — конец окна действия; пусто — срок хранения от ValidFrom.
	ValidUntil    time.Time
	Actor         domain.ActorID
	Reason        string
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// CreateBasisHandler — use case создания основания обработки.
type CreateBasisHandler struct {
	repo  ports.ProcessingBasisRepository
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewCreateBasisHandler собирает use case из порта репозитория, часов и
// генератора идентификаторов событий.
func NewCreateBasisHandler(repo ports.ProcessingBasisRepository, clock ports.Clock, ids ports.IDGenerator) *CreateBasisHandler {
	return &CreateBasisHandler{repo: repo, clock: clock, ids: ids}
}

// Handle создаёт основание и сохраняет его. Полномочие сразу действительно для
// указанной цели, категории, тенанта и периода (инвариант I1).
func (h *CreateBasisHandler) Handle(ctx context.Context, cmd CreateBasis) (BasisResult, error) {
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}

	purpose, err := domain.NewProcessingPurpose(cmd.Purpose, cmd.Category)
	if err != nil {
		return BasisResult{}, err
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

	basis, err := domain.NewProcessingBasis(domain.NewBasisParams{
		ID:            cmd.BasisID,
		TenantID:      cmd.TenantID,
		Purpose:       purpose,
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
	if err := h.repo.Save(ctx, basis, 0); err != nil {
		return BasisResult{}, err
	}
	return result(basis, basis.DomainEvents()), nil
}
