package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
)

// RefreshAudience — команда регулярного пересчёта: повторно применить политику
// сегментации ко всем сохранённым профилям и зафиксировать снимок изменившихся
// сегментов. Обслуживает роль schedule контекста.
type RefreshAudience struct {
	// TenantID ограничивает область пересчёта. Пустое значение означает все
	// тенанты: роль schedule обслуживает сервис целиком.
	TenantID      domain.TenantID
	Actor         domain.ActorID
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// RefreshAudienceResult — итог прохода: сколько сегментов пересмотрено, сколько
// изменило состав и сколько снимков зафиксировано.
type RefreshAudienceResult struct {
	Segments           int
	MembershipsChanged int
	SnapshotsFixed     int
}

// RefreshAudienceHandler — use case регулярного пересчёта и фиксации.
type RefreshAudienceHandler struct {
	segments ports.SegmentRepository
	uow      ports.UnitOfWork
	clock    ports.Clock
	ids      ports.IDGenerator
}

// NewRefreshAudienceHandler собирает use case из порта чтения сегментов, порта
// транзакции, часов и генератора идентификаторов.
func NewRefreshAudienceHandler(segments ports.SegmentRepository, uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *RefreshAudienceHandler {
	return &RefreshAudienceHandler{segments: segments, uow: uow, clock: clock, ids: ids}
}

// Refresh выполняет один проход пересчёта. Сегменты обрабатываются по одному:
// пересчёт и фиксация снимка каждого сегмента идут в отдельной транзакции, поэтому
// сбой одного сегмента не откатывает остальные.
func (h *RefreshAudienceHandler) Refresh(ctx context.Context, cmd RefreshAudience) (RefreshAudienceResult, error) {
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}

	segments, err := h.list(ctx, cmd.TenantID)
	if err != nil {
		return RefreshAudienceResult{}, err
	}

	var result RefreshAudienceResult
	for _, summary := range segments {
		changed, fixed, err := h.refreshSegment(ctx, summary.ID(), at, cmd)
		if err != nil {
			return result, err
		}
		result.Segments++
		if changed {
			result.MembershipsChanged++
		}
		if fixed {
			result.SnapshotsFixed++
		}
	}
	return result, nil
}

// list возвращает область пересчёта: сегменты тенанта или все сегменты.
func (h *RefreshAudienceHandler) list(ctx context.Context, tenant domain.TenantID) ([]*domain.Segment, error) {
	if tenant == "" {
		return h.segments.List(ctx)
	}
	return h.segments.ListByTenant(ctx, tenant)
}

// refreshSegment пересчитывает один сегмент и фиксирует снимок, если состав
// изменился или снимка ещё нет. Возвращает, изменился ли состав и зафиксирован ли
// снимок.
func (h *RefreshAudienceHandler) refreshSegment(ctx context.Context, id domain.SegmentID, at time.Time, cmd RefreshAudience) (bool, bool, error) {
	var changed, fixed bool
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		segment, err := tx.Segments().FindByID(ctx, id)
		if err != nil {
			return err
		}
		expectedVersion := segment.Version()

		for _, profile := range segment.LeadProfiles() {
			result, err := segment.ScoreLead(profile, domain.Provenance{
				EventID:       h.ids.NewEventID(),
				At:            at,
				Actor:         cmd.Actor,
				CorrelationID: cmd.CorrelationID,
				CausationID:   cmd.CausationID,
			})
			if err != nil {
				return err
			}
			if result.MembershipChanged {
				changed = true
			}
		}
		if changed {
			if err := tx.Segments().Save(ctx, segment, expectedVersion); err != nil {
				return err
			}
			if err := tx.Outbox().Append(ctx, segment.DomainEvents()...); err != nil {
				return err
			}
			segment.PullDomainEvents()
		}

		existing, err := tx.Snapshots().ListBySegment(ctx, segment.ID())
		if err != nil {
			return err
		}
		if !changed && len(existing) > 0 {
			return nil
		}
		snapshot, err := domain.FixAudienceSnapshot(segment, domain.NewAudienceSnapshotParams{
			ID:       h.ids.NewSnapshotID(),
			TenantID: segment.TenantID(),
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
		if err := tx.Outbox().Append(ctx, snapshot.DomainEvents()...); err != nil {
			return err
		}
		snapshot.PullDomainEvents()
		fixed = true
		return nil
	})
	if err != nil {
		return false, false, err
	}
	return changed, fixed, nil
}
