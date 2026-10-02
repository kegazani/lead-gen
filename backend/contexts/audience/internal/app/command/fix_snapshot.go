package command

import (
	"context"
	"strings"
	"time"

	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
)

// FixAudienceSnapshot — команда фиксации снимка аудитории: зафиксировать текущий
// состав сегмента в неизменяемый снимок, на который ссылается кампания.
type FixAudienceSnapshot struct {
	// SnapshotID — идентификатор снимка. Пустое значение означает, что
	// идентификатор назначает прикладной слой генератором.
	SnapshotID    domain.SnapshotID
	SegmentID     domain.SegmentID
	TenantID      domain.TenantID
	Actor         domain.ActorID
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// FixAudienceSnapshotResult — результат фиксации снимка.
type FixAudienceSnapshotResult struct {
	SnapshotID  domain.SnapshotID
	SegmentID   domain.SegmentID
	MemberCount int
	Version     int64
	Events      []domain.DomainEvent
}

// FixAudienceSnapshotHandler — use case фиксации снимка аудитории.
type FixAudienceSnapshotHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewFixAudienceSnapshotHandler собирает use case из порта транзакции, часов и
// генератора идентификаторов.
func NewFixAudienceSnapshotHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *FixAudienceSnapshotHandler {
	return &FixAudienceSnapshotHandler{uow: uow, clock: clock, ids: ids}
}

// Handle фиксирует снимок сегмента и сохраняет его вместе с событием
// audience-snapshot-created в одной транзакции.
func (h *FixAudienceSnapshotHandler) Handle(ctx context.Context, cmd FixAudienceSnapshot) (FixAudienceSnapshotResult, error) {
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	snapshotID := cmd.SnapshotID
	if strings.TrimSpace(string(snapshotID)) == "" {
		snapshotID = h.ids.NewSnapshotID()
	}

	var result FixAudienceSnapshotResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		segment, err := tx.Segments().FindByID(ctx, cmd.SegmentID)
		if err != nil {
			return err
		}
		if segment.TenantID() != cmd.TenantID {
			return ErrTenantMismatch
		}
		snapshot, err := domain.FixAudienceSnapshot(segment, domain.NewAudienceSnapshotParams{
			ID:       snapshotID,
			TenantID: cmd.TenantID,
			Provenance: domain.Provenance{
				EventID:       h.ids.NewEventID(),
				At:            at,
				Actor:         cmd.Actor,
				CorrelationID: cmd.CorrelationID,
				CausationID:   cmd.CausationID,
			},
		})
		if err != nil {
			return err
		}
		if err := tx.Snapshots().Save(ctx, snapshot); err != nil {
			return err
		}
		events := snapshot.DomainEvents()
		if err := tx.Outbox().Append(ctx, events...); err != nil {
			return err
		}
		snapshot.PullDomainEvents()

		result = FixAudienceSnapshotResult{
			SnapshotID:  snapshot.ID(),
			SegmentID:   snapshot.SegmentID(),
			MemberCount: snapshot.MemberCount(),
			Version:     snapshot.Version(),
			Events:      events,
		}
		return nil
	})
	if err != nil {
		return FixAudienceSnapshotResult{}, err
	}
	return result, nil
}
