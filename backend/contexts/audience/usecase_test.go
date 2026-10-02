package audience_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	outboxinmemory "lead-gen/backend/contexts/audience/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/audience/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/audience/internal/app/command"
	"lead-gen/backend/contexts/audience/internal/app/query"
	"lead-gen/backend/contexts/audience/internal/domain"
	"lead-gen/backend/contexts/audience/internal/ports"
	"lead-gen/backend/contexts/audience/internal/ports/contracts"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

// fixedClock — детерминированные часы теста.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// seqIDs — детерминированный генератор идентификаторов.
type seqIDs struct{ n int }

func (g *seqIDs) NewEventID() domain.EventID {
	g.n++
	return domain.EventID(fmt.Sprintf("evt-%d", g.n))
}

func (g *seqIDs) NewSnapshotID() domain.SnapshotID {
	g.n++
	return domain.SnapshotID(fmt.Sprintf("snap-%d", g.n))
}

// harness — собранные адаптеры и use cases контекста.
type harness struct {
	segments  *persistence.SegmentRepository
	snapshots *persistence.SnapshotRepository
	journal   *platformoutbox.MemoryJournal
	create    *command.CreateSegmentHandler
	score     *command.ScoreLeadHandler
	fix       *command.FixAudienceSnapshotHandler
	refresh   *command.RefreshAudienceHandler
	get       *query.GetAudienceSnapshotHandler
}

func newHarness() *harness {
	segments := persistence.NewSegmentRepository()
	snapshots := persistence.NewSnapshotRepository()
	journal := platformoutbox.NewMemoryJournal()
	uow := outboxinmemory.NewUnitOfWorkWith(segments, snapshots, journal)
	clock := fixedClock{at: epoch}
	ids := &seqIDs{}
	return &harness{
		segments:  segments,
		snapshots: snapshots,
		journal:   journal,
		create:    command.NewCreateSegmentHandler(uow),
		score:     command.NewScoreLeadHandler(uow, clock, ids),
		fix:       command.NewFixAudienceSnapshotHandler(uow, clock, ids),
		refresh:   command.NewRefreshAudienceHandler(segments, uow, clock, ids),
		get:       query.NewGetAudienceSnapshotHandler(snapshots),
	}
}

// createSegment создаёт сегмент с порогом 0.5 на модели v1.
func (h *harness) createSegment(t *testing.T) {
	t.Helper()
	if _, err := h.create.Handle(context.Background(), command.CreateSegment{
		SegmentID:    "segment-1",
		TenantID:     "tenant-1",
		Name:         "Тёплые лиды",
		ModelVersion: "v1",
		MinScore:     0.5,
	}); err != nil {
		t.Fatalf("создание сегмента: %v", err)
	}
}

// leadProfile собирает профиль лида с силой сигнала fit.
func leadProfile(leadID string, fit float64) contracts.LeadProfile {
	return contracts.LeadProfile{
		LeadID:     leadID,
		TenantID:   "tenant-1",
		ObservedAt: epoch,
		Signals:    []contracts.SignalValue{{Name: "fit", Strength: fit}},
	}
}

// TestCreateScoreFixAndRead — вертикальный срез: создать сегмент, оценить лида,
// зафиксировать снимок и прочитать его.
func TestCreateScoreFixAndRead(t *testing.T) {
	h := newHarness()
	h.createSegment(t)

	scored, err := h.score.Handle(context.Background(), command.ScoreLead{
		SegmentID: "segment-1",
		TenantID:  "tenant-1",
		Profile:   leadProfile("lead-1", 0.9),
		Actor:     "actor-1",
	})
	if err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	if !scored.Eligible || !scored.MembershipChanged {
		t.Fatalf("лид с оценкой 0.9 должен войти в сегмент: %+v", scored)
	}
	if len(scored.Events) != 1 {
		t.Fatalf("событий скоринга = %d, ожидалось 1", len(scored.Events))
	}

	fixed, err := h.fix.Handle(context.Background(), command.FixAudienceSnapshot{
		SegmentID: "segment-1",
		TenantID:  "tenant-1",
		Actor:     "actor-1",
	})
	if err != nil {
		t.Fatalf("фиксация снимка: %v", err)
	}
	if fixed.MemberCount != 1 {
		t.Fatalf("размер снимка = %d, ожидался 1", fixed.MemberCount)
	}

	view, err := h.get.Handle(context.Background(), query.GetAudienceSnapshot{
		SnapshotID: fixed.SnapshotID,
		TenantID:   "tenant-1",
	})
	if err != nil {
		t.Fatalf("чтение снимка: %v", err)
	}
	if view.MemberCount != 1 || view.SegmentID != "segment-1" {
		t.Fatalf("представление снимка: %+v", view)
	}

	// События обоих use cases попали в outbox.
	pending, err := h.journal.Pending(context.Background(), 0)
	if err != nil {
		t.Fatalf("журнал outbox: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("записей outbox = %d, ожидалось 2 (membership-changed, snapshot-created)", len(pending))
	}
}

// TestScoreLeadTenantMismatch — профиль чужого тенанта отвергается.
func TestScoreLeadTenantMismatch(t *testing.T) {
	h := newHarness()
	h.createSegment(t)

	_, err := h.score.Handle(context.Background(), command.ScoreLead{
		SegmentID: "segment-1",
		TenantID:  "tenant-2",
		Profile:   leadProfile("lead-1", 0.9),
		Actor:     "actor-1",
	})
	if !errors.Is(err, command.ErrTenantMismatch) {
		t.Fatalf("ошибка = %v, ожидалась ErrTenantMismatch", err)
	}
}

// TestScoreUnknownSegment — оценка лида в отсутствующем сегменте не создаёт его.
func TestScoreUnknownSegment(t *testing.T) {
	h := newHarness()
	_, err := h.score.Handle(context.Background(), command.ScoreLead{
		SegmentID: "segment-missing",
		TenantID:  "tenant-1",
		Profile:   leadProfile("lead-1", 0.9),
		Actor:     "actor-1",
	})
	if !errors.Is(err, ports.ErrSegmentNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrSegmentNotFound", err)
	}
}

// TestReadSnapshotTenantScoped — снимок другого тенанта неотличим от отсутствующего.
func TestReadSnapshotTenantScoped(t *testing.T) {
	h := newHarness()
	h.createSegment(t)
	if _, err := h.score.Handle(context.Background(), command.ScoreLead{
		SegmentID: "segment-1", TenantID: "tenant-1", Profile: leadProfile("lead-1", 0.9), Actor: "actor-1",
	}); err != nil {
		t.Fatalf("скоринг: %v", err)
	}
	fixed, err := h.fix.Handle(context.Background(), command.FixAudienceSnapshot{
		SegmentID: "segment-1", TenantID: "tenant-1", Actor: "actor-1",
	})
	if err != nil {
		t.Fatalf("фиксация снимка: %v", err)
	}
	_, err = h.get.Handle(context.Background(), query.GetAudienceSnapshot{
		SnapshotID: fixed.SnapshotID, TenantID: "tenant-2",
	})
	if !errors.Is(err, ports.ErrSnapshotNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrSnapshotNotFound", err)
	}
}

// TestRefreshFixesSnapshotOnce — регулярный пересчёт фиксирует снимок сегмента без
// снимка и не повторяет фиксацию, когда состав не менялся.
func TestRefreshFixesSnapshotOnce(t *testing.T) {
	h := newHarness()
	h.createSegment(t)
	if _, err := h.score.Handle(context.Background(), command.ScoreLead{
		SegmentID: "segment-1", TenantID: "tenant-1", Profile: leadProfile("lead-1", 0.9), Actor: "actor-1",
	}); err != nil {
		t.Fatalf("скоринг: %v", err)
	}

	first, err := h.refresh.Refresh(context.Background(), command.RefreshAudience{Actor: "system"})
	if err != nil {
		t.Fatalf("пересчёт: %v", err)
	}
	if first.Segments != 1 || first.SnapshotsFixed != 1 || first.MembershipsChanged != 0 {
		t.Fatalf("первый проход: %+v, ожидались 1 сегмент, 1 снимок, 0 изменений", first)
	}

	second, err := h.refresh.Refresh(context.Background(), command.RefreshAudience{Actor: "system"})
	if err != nil {
		t.Fatalf("пересчёт: %v", err)
	}
	if second.SnapshotsFixed != 0 {
		t.Fatalf("повторный проход фиксировал снимок без изменений: %+v", second)
	}
}
