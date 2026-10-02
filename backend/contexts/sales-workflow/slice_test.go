package salesworkflow

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	crmunavailable "lead-gen/backend/contexts/sales-workflow/internal/adapters/crm/unavailable"
	persistence "lead-gen/backend/contexts/sales-workflow/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/sales-workflow/internal/app/command"
	"lead-gen/backend/contexts/sales-workflow/internal/app/query"
	"lead-gen/backend/contexts/sales-workflow/internal/domain"
	"lead-gen/backend/contexts/sales-workflow/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

var testNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// fixedClock — детерминированные часы теста.
type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

// seqIDs — детерминированный генератор идентификаторов событий.
type seqIDs struct{ n int }

func (g *seqIDs) NewEventID() domain.EventID {
	g.n++
	return domain.EventID(fmt.Sprintf("evt-%d", g.n))
}

// environment — собранный срез с доступом к журналу и часам.
type environment struct {
	slice   *Slice
	journal *platformoutbox.MemoryJournal
	clock   *fixedClock
	ids     *seqIDs
}

func newEnvironment(t *testing.T, mutate func(*Config)) *environment {
	t.Helper()
	journal := platformoutbox.NewMemoryJournal()
	cfg := Config{
		Journal: journal,
		Clock:   &fixedClock{now: testNow},
		IDs:     &seqIDs{},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	slice, err := Build(cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	clock, _ := cfg.Clock.(*fixedClock)
	ids, _ := cfg.IDs.(*seqIDs)
	return &environment{slice: slice, journal: journal, clock: clock, ids: ids}
}

func (e *environment) createDeal(t *testing.T, id string, revision int64) command.DealResult {
	t.Helper()
	result, err := e.slice.CreateDeal.Handle(context.Background(), command.CreateDeal{
		DealID:           domain.DealID(id),
		TenantID:         domain.TenantID("tenant-1"),
		CompanyRef:       domain.CompanyRef("company-1"),
		ExternalRef:      domain.ExternalDealRef("crm-" + id),
		ExternalRevision: revision,
		Actor:            domain.ActorID("operator-1"),
	})
	if err != nil {
		t.Fatalf("CreateDeal: %v", err)
	}
	return result
}

func (e *environment) changeStage(t *testing.T, id string, to domain.DealStage) command.DealResult {
	t.Helper()
	result, err := e.slice.ChangeDealStage.Handle(context.Background(), command.ChangeDealStage{
		DealID:   domain.DealID(id),
		TenantID: domain.TenantID("tenant-1"),
		Actor:    domain.ActorID("operator-1"),
		To:       to,
	})
	if err != nil {
		t.Fatalf("ChangeDealStage(%s): %v", to, err)
	}
	return result
}

func (e *environment) getDeal(t *testing.T, id string) query.DealView {
	t.Helper()
	view, err := e.slice.GetDeal.Handle(context.Background(), query.GetDeal{
		DealID:   domain.DealID(id),
		TenantID: domain.TenantID("tenant-1"),
	})
	if err != nil {
		t.Fatalf("GetDeal: %v", err)
	}
	return view
}

func TestSliceCreateAndLifecycle(t *testing.T) {
	e := newEnvironment(t, nil)
	result := e.createDeal(t, "deal-1", 1)
	if result.Stage != domain.StageLead || result.Version != 1 {
		t.Fatalf("создание: %+v", result)
	}
	if len(result.Events) != 1 || result.Events[0].Name() != domain.EventDealStateChanged {
		t.Fatalf("события создания: %+v", result.Events)
	}

	e.changeStage(t, "deal-1", domain.StageQualified)
	e.changeStage(t, "deal-1", domain.StageProposal)

	view := e.getDeal(t, "deal-1")
	if view.Stage != domain.StageProposal || view.Version != 3 {
		t.Fatalf("состояние сделки: %+v", view)
	}

	syncView, err := e.slice.CRMSyncStatus.Handle(context.Background(), query.CRMSyncStatus{
		DealID:   domain.DealID("deal-1"),
		TenantID: domain.TenantID("tenant-1"),
	})
	if err != nil {
		t.Fatalf("CRMSyncStatus: %v", err)
	}
	if syncView.Status != domain.SyncSynced || syncView.ExternalRevision != 1 {
		t.Fatalf("состояние синхронизации: %+v", syncView)
	}
}

func TestSliceIngestAppliesStaleAndConflict(t *testing.T) {
	e := newEnvironment(t, nil)
	e.createDeal(t, "deal-1", 1)
	e.changeStage(t, "deal-1", domain.StageQualified)
	e.changeStage(t, "deal-1", domain.StageProposal)

	// Следующая ревизия применяется: этап двигается.
	applied := e.ingest(t, command.IngestCRMEvent{
		DealID:           "deal-1",
		TenantID:         "tenant-1",
		ExternalRef:      "crm-deal-1",
		ExternalRevision: 2,
		Stage:            domain.StageNegotiation,
	})
	if !applied.Applied || applied.Stage != domain.StageNegotiation {
		t.Fatalf("применимое событие: %+v", applied)
	}

	// Пропуск ревизий — конфликт: локальное состояние не перезаписано.
	conflict := e.ingest(t, command.IngestCRMEvent{
		DealID:           "deal-1",
		TenantID:         "tenant-1",
		ExternalRef:      "crm-deal-1",
		ExternalRevision: 4,
		Stage:            domain.StageLost,
	})
	if conflict.Applied || conflict.SyncStatus != domain.SyncConflict {
		t.Fatalf("конфликт: %+v", conflict)
	}
	if len(conflict.Events) != 1 || conflict.Events[0].Name() != domain.EventCRMSyncConflict {
		t.Fatalf("события конфликта: %+v", conflict.Events)
	}
	if view := e.getDeal(t, "deal-1"); view.Stage != domain.StageNegotiation {
		t.Fatalf("этап перезаписан конфликтом: %+v", view)
	}

	// Следующая за применённой ревизия снимает конфликт.
	recovered := e.ingest(t, command.IngestCRMEvent{
		DealID:           "deal-1",
		TenantID:         "tenant-1",
		ExternalRef:      "crm-deal-1",
		ExternalRevision: 3,
		Stage:            domain.StageWon,
	})
	if !recovered.Applied || recovered.SyncStatus != domain.SyncSynced || recovered.Stage != domain.StageWon {
		t.Fatalf("восстановление: %+v", recovered)
	}
}

func TestSliceIngestConfirmsResult(t *testing.T) {
	e := newEnvironment(t, nil)
	e.createDeal(t, "deal-1", 1)
	e.changeStage(t, "deal-1", domain.StageQualified)
	e.changeStage(t, "deal-1", domain.StageProposal)

	result := e.ingest(t, command.IngestCRMEvent{
		DealID:           "deal-1",
		TenantID:         "tenant-1",
		ExternalRef:      "crm-deal-1",
		ExternalRevision: 2,
		Stage:            domain.StageWon,
		ResultConfirmed:  true,
		ActionRef:        "action-1",
		EvidenceRef:      "evidence-1",
	})
	if !result.Applied {
		t.Fatalf("событие не применено: %+v", result)
	}
	if !containsEvent(result.Events, domain.EventResultAttributed) {
		t.Fatalf("нет result-attributed: %+v", result.Events)
	}
	if view := e.getDeal(t, "deal-1"); !view.ResultConfirmed || view.Stage != domain.StageWon {
		t.Fatalf("состояние после атрибуции: %+v", view)
	}
}

func TestSliceAttributeResultIdempotent(t *testing.T) {
	e := newEnvironment(t, nil)
	e.createDeal(t, "deal-1", 1)
	e.changeStage(t, "deal-1", domain.StageQualified)
	e.changeStage(t, "deal-1", domain.StageProposal)

	has := func() bool {
		for _, name := range collectEvents(e.journal) {
			if name == domain.EventResultAttributed {
				return true
			}
		}
		return false
	}

	first, err := e.slice.AttributeResult.Handle(context.Background(), command.AttributeResult{
		DealID:      "deal-1",
		TenantID:    "tenant-1",
		Actor:       "operator-1",
		ActionRef:   "action-1",
		EvidenceRef: "evidence-1",
		ExternalRef: "crm-deal-1",
	})
	if err != nil {
		t.Fatalf("AttributeResult: %v", err)
	}
	if !has() {
		t.Fatal("первая атрибуция не породила событие")
	}
	versionAfter := first.Version

	// Идемпотентный повтор.
	second, err := e.slice.AttributeResult.Handle(context.Background(), command.AttributeResult{
		DealID:      "deal-1",
		TenantID:    "tenant-1",
		Actor:       "operator-1",
		ActionRef:   "action-1",
		EvidenceRef: "evidence-1",
		ExternalRef: "crm-deal-1",
	})
	if err != nil {
		t.Fatalf("повтор AttributeResult: %v", err)
	}
	if second.Version != versionAfter {
		t.Fatalf("повтор изменил версию: %d → %d", versionAfter, second.Version)
	}

	// Другая внешняя ссылка отвергается.
	if _, err := e.slice.AttributeResult.Handle(context.Background(), command.AttributeResult{
		DealID:      "deal-1",
		TenantID:    "tenant-1",
		Actor:       "operator-1",
		ActionRef:   "action-2",
		EvidenceRef: "evidence-2",
		ExternalRef: "crm-other",
	}); !errors.Is(err, domain.ErrAlreadyAttributed) {
		t.Fatalf("другая ссылка: %v, ожидалась ErrAlreadyAttributed", err)
	}
}

func TestSliceTenantIsolation(t *testing.T) {
	e := newEnvironment(t, nil)
	e.createDeal(t, "deal-1", 1)

	_, err := e.slice.GetDeal.Handle(context.Background(), query.GetDeal{
		DealID:   "deal-1",
		TenantID: "tenant-other",
	})
	if !errors.Is(err, query.ErrTenantMismatch) {
		t.Fatalf("чтение чужого тенанта: %v, ожидалась ErrTenantMismatch", err)
	}
}

func TestSliceCRMUnavailable(t *testing.T) {
	e := newEnvironment(t, func(cfg *Config) {
		cfg.CRM = crmunavailable.New()
	})
	_, err := e.slice.CreateDeal.Handle(context.Background(), command.CreateDeal{
		DealID:           "deal-1",
		TenantID:         "tenant-1",
		CompanyRef:       "company-1",
		ExternalRef:      "crm-deal-1",
		ExternalRevision: 1,
		Actor:            "operator-1",
	})
	if !errors.Is(err, ports.ErrCRMUnavailable) {
		t.Fatalf("недоступная CRM: %v, ожидалась ErrCRMUnavailable", err)
	}
}

func TestSyncRepositoryMissing(t *testing.T) {
	repo := persistence.New()
	if _, err := repo.FindByDeal(context.Background(), "deal-1"); !errors.Is(err, ports.ErrCRMSyncNotFound) {
		t.Fatalf("отсутствие синхронизации: %v", err)
	}
}

// ingest — вспомогательный вызов приёма события CRM.
func (e *environment) ingest(t *testing.T, cmd command.IngestCRMEvent) command.IngestCRMEventResult {
	t.Helper()
	result, err := e.slice.IngestCRMEvent.Handle(context.Background(), cmd)
	if err != nil {
		t.Fatalf("IngestCRMEvent: %v", err)
	}
	return result
}

func containsEvent(events []domain.DomainEvent, name domain.EventName) bool {
	for _, event := range events {
		if event.Name() == name {
			return true
		}
	}
	return false
}

// collectEvents читает имена событий, записанных в журнал outbox.
func collectEvents(journal *platformoutbox.MemoryJournal) []domain.EventName {
	pending, err := journal.Pending(context.Background(), 0)
	if err != nil {
		return nil
	}
	names := make([]domain.EventName, 0, len(pending))
	for _, message := range pending {
		names = append(names, domain.EventName(message.Headers["event_name"]))
	}
	return names
}
