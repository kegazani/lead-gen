package domain

import (
	"errors"
	"testing"
	"time"
)

var testAt = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func mustExternal(t *testing.T, ref string, revision int64) ExternalVersion {
	t.Helper()
	v, err := NewExternalVersion(ExternalDealRef(ref), revision)
	if err != nil {
		t.Fatalf("NewExternalVersion: %v", err)
	}
	return v
}

func newTestDeal(t *testing.T) *Deal {
	t.Helper()
	deal, err := NewDeal(NewDealParams{
		ID:         DealID("deal-1"),
		TenantID:   TenantID("tenant-1"),
		CompanyRef: CompanyRef("company-1"),
		External:   mustExternal(t, "crm-1", 1),
		Actor:      ActorID("operator-1"),
		At:         testAt,
		EventID:    EventID("evt-create"),
	})
	if err != nil {
		t.Fatalf("NewDeal: %v", err)
	}
	return deal
}

func action(eventID string) ActionInput {
	return ActionInput{
		Actor:   ActorID("operator-1"),
		At:      testAt,
		EventID: EventID(eventID),
	}
}

func TestNewDealStartsAtLeadAndEmitsStateChanged(t *testing.T) {
	deal := newTestDeal(t)
	if deal.Stage() != StageLead {
		t.Fatalf("этап %q, ожидался %q", deal.Stage(), StageLead)
	}
	events := deal.DomainEvents()
	if len(events) != 1 || events[0].Name() != EventDealStateChanged {
		t.Fatalf("события создания: %+v", events)
	}
	changed, ok := events[0].(DealStateChanged)
	if !ok || changed.From != "" || changed.To != StageLead {
		t.Fatalf("событие deal-state-changed: %+v", events[0])
	}
	if deal.Version() != 1 {
		t.Fatalf("версия %d, ожидалась 1", deal.Version())
	}
}

func TestDealLifecycleFollowsWorkflow(t *testing.T) {
	deal := newTestDeal(t)
	deal.PullDomainEvents()

	for _, to := range []DealStage{StageQualified, StageProposal, StageNegotiation, StageWon} {
		events, err := deal.ChangeStage(action("evt-"+string(to)), to)
		if err != nil {
			t.Fatalf("переход в %s: %v", to, err)
		}
		if len(events) != 1 || events[0].Name() != EventDealStateChanged {
			t.Fatalf("переход в %s не породил deal-state-changed: %+v", to, events)
		}
	}
	if deal.Stage() != StageWon {
		t.Fatalf("этап %q, ожидался won", deal.Stage())
	}
	if _, err := deal.ChangeStage(action("evt-after"), StageLost); !errors.Is(err, ErrTerminalDeal) {
		t.Fatalf("переход из терминального этапа: %v, ожидалась ErrTerminalDeal", err)
	}
}

func TestDealRejectsInvalidStageTransition(t *testing.T) {
	deal := newTestDeal(t)
	if _, err := deal.ChangeStage(action("evt-skip"), StageWon); !errors.Is(err, ErrInvalidStageTransition) {
		t.Fatalf("lead → won: %v, ожидалась ErrInvalidStageTransition", err)
	}
	if _, err := deal.ChangeStage(action("evt-unknown"), DealStage("nonsense")); !errors.Is(err, ErrUnknownDealStage) {
		t.Fatalf("неизвестный этап: %v, ожидалась ErrUnknownDealStage", err)
	}
}

func TestAttributionConfirmedAndIdempotent(t *testing.T) {
	deal := newTestDeal(t)
	advanceTo(t, deal, StageProposal)
	deal.PullDomainEvents()

	proof, err := NewAttributionProof(ActionRef("action-1"), EvidenceRef("evidence-1"), ExternalDealRef("crm-1"), testAt)
	if err != nil {
		t.Fatalf("NewAttributionProof: %v", err)
	}
	events, err := deal.ConfirmAttribution(action("evt-attr-1"), proof)
	if err != nil {
		t.Fatalf("ConfirmAttribution: %v", err)
	}
	if len(events) != 1 || events[0].Name() != EventResultAttributed {
		t.Fatalf("события атрибуции: %+v", events)
	}
	if !deal.ResultConfirmed() {
		t.Fatal("результат должен быть подтверждён")
	}
	versionAfterFirst := deal.Version()

	// Повтор с той же внешней ссылкой идемпотентен: события и версии нет.
	events, err = deal.ConfirmAttribution(action("evt-attr-2"), proof)
	if err != nil {
		t.Fatalf("повторная атрибуция: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("повторная атрибуция породила события: %+v", events)
	}
	if deal.Version() != versionAfterFirst {
		t.Fatalf("версия изменилась на идемпотентном повторе: %d → %d", versionAfterFirst, deal.Version())
	}

	// Другая внешняя ссылка отвергается.
	other, err := NewAttributionProof(ActionRef("action-2"), EvidenceRef("evidence-2"), ExternalDealRef("crm-2"), testAt)
	if err != nil {
		t.Fatalf("NewAttributionProof: %v", err)
	}
	if _, err := deal.ConfirmAttribution(action("evt-attr-3"), other); !errors.Is(err, ErrAlreadyAttributed) {
		t.Fatalf("другая ссылка: %v, ожидалась ErrAlreadyAttributed", err)
	}
}

func TestAttributionRequiresCompleteProofAndAllowedStage(t *testing.T) {
	deal := newTestDeal(t)

	// На этапе lead атрибуция недопустима.
	proof, err := NewAttributionProof(ActionRef("action-1"), EvidenceRef("evidence-1"), ExternalDealRef("crm-1"), testAt)
	if err != nil {
		t.Fatalf("NewAttributionProof: %v", err)
	}
	if _, err := deal.ConfirmAttribution(action("evt-attr"), proof); !errors.Is(err, ErrAttributionNotAllowed) {
		t.Fatalf("атрибуция на lead: %v, ожидалась ErrAttributionNotAllowed", err)
	}

	// Неполное доказательство отвергается.
	if _, err := NewAttributionProof(ActionRef(""), EvidenceRef("evidence-1"), ExternalDealRef("crm-1"), testAt); !errors.Is(err, ErrInsufficientProof) {
		t.Fatalf("пустой action_ref: %v, ожидалась ErrInsufficientProof", err)
	}
	if _, err := NewAttributionProof(ActionRef("action-1"), EvidenceRef("evidence-1"), ExternalDealRef("crm-1"), time.Time{}); !errors.Is(err, ErrInsufficientProof) {
		t.Fatalf("без времени: %v, ожидалась ErrInsufficientProof", err)
	}
}

func TestApplyExternalRequiresNewerVersion(t *testing.T) {
	deal := newTestDeal(t)
	advanceTo(t, deal, StageProposal)
	deal.PullDomainEvents()

	// Внешняя версия продолжает применённую: этап обновляется.
	events, err := deal.ApplyExternal(action("evt-ext"), mustExternal(t, "crm-1", 2), StageNegotiation)
	if err != nil {
		t.Fatalf("ApplyExternal: %v", err)
	}
	if deal.Stage() != StageNegotiation || len(events) != 1 {
		t.Fatalf("этап %q, события %+v", deal.Stage(), events)
	}

	// Устаревшая версия отвергается агрегатом.
	if _, err := deal.ApplyExternal(action("evt-ext-old"), mustExternal(t, "crm-1", 2), StageWon); !errors.Is(err, ErrInvalidExternalVersion) {
		t.Fatalf("устаревшая версия: %v, ожидалась ErrInvalidExternalVersion", err)
	}
}

// advanceTo выполняет последовательные допустимые переходы до целевого этапа.
func advanceTo(t *testing.T, deal *Deal, target DealStage) {
	t.Helper()
	for _, stage := range DealStages() {
		if deal.Stage() == target {
			return
		}
		if deal.Stage().CanTransitionTo(stage) {
			if _, err := deal.ChangeStage(action("evt-"+string(stage)), stage); err != nil {
				t.Fatalf("переход в %s: %v", stage, err)
			}
		}
	}
	if deal.Stage() != target {
		t.Fatalf("не удалось достичь этапа %s, текущий %s", target, deal.Stage())
	}
}
