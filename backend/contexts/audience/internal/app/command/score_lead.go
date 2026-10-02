package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
	"lead-gen/backend/contexts/audience/internal/ports/contracts"
)

// ScoreLead — команда скоринга лида и сегментации: оценить профиль лида по модели
// сегмента и пересчитать его принадлежность сегменту.
type ScoreLead struct {
	SegmentID     domain.SegmentID
	TenantID      domain.TenantID
	Profile       contracts.LeadProfile
	Actor         domain.ActorID
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// ScoreLeadResult — результат скоринга лида.
type ScoreLeadResult struct {
	SegmentID         domain.SegmentID
	LeadID            domain.LeadID
	Value             float64
	Eligible          bool
	MembershipChanged bool
	MemberCount       int
	Version           int
	Events            []domain.DomainEvent
}

// ScoreLeadHandler — use case скоринга и сегментации.
type ScoreLeadHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewScoreLeadHandler собирает use case из порта транзакции, часов и генератора
// идентификаторов событий.
func NewScoreLeadHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *ScoreLeadHandler {
	return &ScoreLeadHandler{uow: uow, clock: clock, ids: ids}
}

// Handle оценивает лид и фиксирует изменение состава сегмента вместе с его
// событием в одной транзакции. Оценка воспроизводима по версии модели сегмента.
func (h *ScoreLeadHandler) Handle(ctx context.Context, cmd ScoreLead) (ScoreLeadResult, error) {
	if cmd.Profile.TenantID != string(cmd.TenantID) {
		return ScoreLeadResult{}, ErrTenantMismatch
	}
	profile, err := cmd.Profile.Domain()
	if err != nil {
		return ScoreLeadResult{}, err
	}
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}

	var result ScoreLeadResult
	err = h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		segment, err := tx.Segments().FindByID(ctx, cmd.SegmentID)
		if err != nil {
			return err
		}
		if segment.TenantID() != cmd.TenantID {
			return ErrTenantMismatch
		}
		expectedVersion := segment.Version()

		scored, err := segment.ScoreLead(profile, domain.Provenance{
			EventID:       h.ids.NewEventID(),
			At:            at,
			Actor:         cmd.Actor,
			CorrelationID: cmd.CorrelationID,
			CausationID:   cmd.CausationID,
		})
		if err != nil {
			return err
		}
		if err := tx.Segments().Save(ctx, segment, expectedVersion); err != nil {
			return err
		}
		events := segment.DomainEvents()
		if err := tx.Outbox().Append(ctx, events...); err != nil {
			return err
		}
		segment.PullDomainEvents()

		result = ScoreLeadResult{
			SegmentID:         segment.ID(),
			LeadID:            scored.LeadID,
			Value:             scored.Value.Float64(),
			Eligible:          scored.Eligible,
			MembershipChanged: scored.MembershipChanged,
			MemberCount:       scored.MemberCount,
			Version:           scored.Version,
			Events:            events,
		}
		return nil
	})
	if err != nil {
		return ScoreLeadResult{}, err
	}
	return result, nil
}
