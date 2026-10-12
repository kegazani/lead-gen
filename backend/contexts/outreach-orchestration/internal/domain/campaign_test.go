package domain

import (
	"errors"
	"testing"
	"time"
)

func testMoment() time.Time {
	return time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
}

func newCampaign(t *testing.T) *Campaign {
	t.Helper()
	c, err := CreateCampaign(CreateCampaignParams{
		ID:       CampaignID("campaign-1"),
		TenantID: TenantID("tenant-1"),
		Name:     "Первый срез утверждения",
		Window:   mustWindow(t),
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	return c
}

func decisionParamsFor(draftID DraftID, version int, reason string) DecisionParams {
	return DecisionParams{
		DecisionID:   DecisionID("decision-" + string(draftID)),
		DraftID:      draftID,
		DraftVersion: version,
		Actor:        ActorID("operator-1"),
		Reason:       reason,
		DecidedAt:    testDecisionTime(),
	}
}

func eventOfType[T DomainEvent](events []DomainEvent) (T, bool) {
	for _, e := range events {
		if typed, ok := e.(T); ok {
			return typed, true
		}
	}
	var zero T
	return zero, false
}

func countEvents[T DomainEvent](events []DomainEvent) int {
	n := 0
	for _, e := range events {
		if _, ok := e.(T); ok {
			n++
		}
	}
	return n
}

func TestCreateCampaignValidatesParams(t *testing.T) {
	valid := CreateCampaignParams{
		ID:       CampaignID("campaign-1"),
		TenantID: TenantID("tenant-1"),
		Name:     "Кампания",
		Window:   mustWindow(t),
	}

	campaign, err := CreateCampaign(valid)
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	if campaign.State() != CampaignStateDraft {
		t.Fatalf("состояние %q, ожидалось %q", campaign.State(), CampaignStateDraft)
	}
	if len(campaign.Drafts()) != 0 || len(campaign.Decisions()) != 0 {
		t.Fatal("новая кампания должна быть пустой")
	}

	cases := map[string]func(*CreateCampaignParams){
		"без идентификатора": func(p *CreateCampaignParams) { p.ID = "" },
		"без тенанта":        func(p *CreateCampaignParams) { p.TenantID = "" },
		"без названия":       func(p *CreateCampaignParams) { p.Name = "  " },
		"без окна":           func(p *CreateCampaignParams) { p.Window = CampaignWindow{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			params := valid
			mutate(&params)
			if _, err := CreateCampaign(params); err == nil {
				t.Fatal("ожидалась доменная ошибка")
			}
		})
	}
}

func TestDraftActionPutsDraftIntoPendingApproval(t *testing.T) {
	campaign := newCampaign(t)

	draft, events, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment())
	if err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	if draft.State() != DraftStatePendingApproval {
		t.Fatalf("состояние черновика %q, ожидалось %q", draft.State(), DraftStatePendingApproval)
	}
	if draft.Version() != 1 {
		t.Fatalf("версия черновика %d, ожидалась 1", draft.Version())
	}
	if campaign.State() != CampaignStateActive {
		t.Fatalf("состояние кампании %q, ожидалось %q", campaign.State(), CampaignStateActive)
	}

	changed, ok := eventOfType[CampaignStateChanged](events)
	if !ok {
		t.Fatal("постановка черновика должна порождать campaign-state-changed")
	}
	if changed.From != CampaignStateDraft || changed.To != CampaignStateActive {
		t.Fatalf("переход %q → %q", changed.From, changed.To)
	}
	if len(campaign.PendingApproval()) != 1 {
		t.Fatal("черновик должен попадать в очередь утверждения")
	}

	// Второй черновик не меняет состояние кампании и не порождает события.
	_, moreEvents, err := campaign.DraftAction(DraftID("draft-2"), testSpec(t), testMoment())
	if err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	if len(moreEvents) != 0 {
		t.Fatalf("состояние кампании не менялось, событий быть не должно: %v", moreEvents)
	}

	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); !errors.Is(err, ErrDuplicateDraft) {
		t.Fatalf("ожидалась ErrDuplicateDraft, получено %v", err)
	}
	if _, _, err := campaign.DraftAction("", testSpec(t), testMoment()); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("ожидалась ErrEmptyID, получено %v", err)
	}
}

func TestI1NoExecutionWithoutPositiveDecision(t *testing.T) {
	campaign := newCampaign(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}

	err := campaign.ExecuteDraft(DraftID("draft-1"), 1)
	if !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("ожидалась ErrApprovalRequired, получено %v", err)
	}

	draft, _ := campaign.Draft(DraftID("draft-1"))
	if draft.State() != DraftStatePendingApproval {
		t.Fatalf("черновик изменился без решения: %q", draft.State())
	}
	if len(campaign.Decisions()) != 0 {
		t.Fatal("решений быть не должно")
	}

	// Отклонение также не открывает исполнение.
	if _, _, err := campaign.RejectDraft(decisionParamsFor(DraftID("draft-1"), 1, "материал не согласован")); err != nil {
		t.Fatalf("RejectDraft: %v", err)
	}
	if err := campaign.ExecuteDraft(DraftID("draft-1"), 1); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("отклонённый черновик не исполняется, получено %v", err)
	}
}

func TestApproveEmitsActionApprovedWithExactVersion(t *testing.T) {
	campaign := newCampaign(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}

	decision, events, err := campaign.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 1, ""))
	if err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}
	if decision.DraftVersion() != 1 || !decision.IsApproval() {
		t.Fatalf("решение не привязано к точной версии: %+v", decision)
	}

	approved, ok := eventOfType[ActionApproved](events)
	if !ok {
		t.Fatal("утверждение должно порождать action-approved")
	}
	if approved.DraftVersion != 1 {
		t.Fatalf("событие несёт версию %d, ожидалась 1", approved.DraftVersion)
	}
	if approved.DraftID != DraftID("draft-1") ||
		approved.CampaignID != CampaignID("campaign-1") ||
		approved.TenantID != TenantID("tenant-1") {
		t.Fatalf("событие потеряло ссылки: %+v", approved)
	}
	if !approved.Channel.Equals(mustChannel(t, ActionChannelEmailName)) {
		t.Fatalf("канал события %q", approved.Channel)
	}
	if approved.DecisionID != decision.ID() || approved.OccurredAt().IsZero() {
		t.Fatalf("событие не связано с решением: %+v", approved)
	}
	if approved.Material != MaterialID("material-1") {
		t.Fatalf("событие не несёт материал черновика: %+v", approved)
	}

	pulled := campaign.PullDomainEvents()
	if _, ok := eventOfType[ActionApproved](pulled); !ok {
		t.Fatal("событие должно вычитываться агрегатом")
	}
	if len(campaign.PullDomainEvents()) != 0 {
		t.Fatal("повторное вычитывание должно быть пустым")
	}
	if len(campaign.DecisionsForDraft(DraftID("draft-1"))) != 1 {
		t.Fatal("журнал должен содержать одно решение")
	}
}

func TestI2DecisionOnStaleVersionIsRejected(t *testing.T) {
	campaign := newCampaign(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	if _, _, err := campaign.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 1, "")); err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}

	revised := testSpec(t)
	revised.Channel = mustChannel(t, ActionChannelCallName)
	if _, err := campaign.ReviseDraft(DraftID("draft-1"), 1, revised); err != nil {
		t.Fatalf("ReviseDraft: %v", err)
	}

	draft, _ := campaign.Draft(DraftID("draft-1"))
	if draft.Version() != 2 || draft.State() != DraftStatePendingApproval {
		t.Fatalf("после правки черновик %q v%d", draft.State(), draft.Version())
	}
	decisionsBefore := len(campaign.Decisions())

	// Решение по устаревшей версии отвергается и ничего не меняет.
	_, events, err := campaign.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 1, ""))
	if !errors.Is(err, ErrDraftVersionMismatch) {
		t.Fatalf("ожидалась ErrDraftVersionMismatch, получено %v", err)
	}
	if len(events) != 0 || len(campaign.Decisions()) != decisionsBefore {
		t.Fatal("устаревшее решение не должно менять состояние и журнал")
	}
	if draft.State() != DraftStatePendingApproval {
		t.Fatalf("черновик изменился по устаревшему решению: %q", draft.State())
	}

	// Прежнее утверждение не разрешает новую версию (I1 + I2).
	if err := campaign.ExecuteDraft(DraftID("draft-1"), 2); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("изменённый черновик требует нового решения, получено %v", err)
	}

	// Новое решение по актуальной версии проходит и несёт версию 2.
	_, events, err = campaign.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 2, ""))
	if err != nil {
		t.Fatalf("ApproveDraft по версии 2: %v", err)
	}
	approved, ok := eventOfType[ActionApproved](events)
	if !ok || approved.DraftVersion != 2 {
		t.Fatalf("ожидалось action-approved версии 2, получено %+v", approved)
	}
	if err := campaign.ExecuteDraft(DraftID("draft-1"), 2); err != nil {
		t.Fatalf("утверждённая версия должна исполняться: %v", err)
	}
	if draft.State() != DraftStateExecuted {
		t.Fatalf("состояние после исполнения %q", draft.State())
	}
}

func TestRepeatedDecisionIsIdempotent(t *testing.T) {
	campaign := newCampaign(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}

	first, _, err := campaign.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 1, ""))
	if err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}
	campaign.PullDomainEvents()

	second, events, err := campaign.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 1, ""))
	if err != nil {
		t.Fatalf("повторное решение не должно быть ошибкой: %v", err)
	}
	if second.ID() != first.ID() {
		t.Fatalf("повтор создал второе решение: %s и %s", first.ID(), second.ID())
	}
	if len(events) != 0 {
		t.Fatal("повтор не должен порождать новое событие")
	}
	if len(campaign.Decisions()) != 1 {
		t.Fatalf("записей решения %d, ожидалась 1", len(campaign.Decisions()))
	}
	if len(campaign.PullDomainEvents()) != 0 {
		t.Fatal("неподтверждённых событий быть не должно")
	}
}

func TestConflictingOutcomeForSameVersionIsRejected(t *testing.T) {
	campaign := newCampaign(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	if _, _, err := campaign.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 1, "")); err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}

	_, _, err := campaign.RejectDraft(decisionParamsFor(DraftID("draft-1"), 1, "передумали"))
	if !errors.Is(err, ErrDecisionConflict) {
		t.Fatalf("ожидалась ErrDecisionConflict, получено %v", err)
	}
	draft, _ := campaign.Draft(DraftID("draft-1"))
	if draft.State() != DraftStateApproved || len(campaign.Decisions()) != 1 {
		t.Fatal("конфликтующее решение не должно менять состояние")
	}
}

// Повтор с другим автором не идемпотентен: второе решение не попадает в
// журнал, поэтому его нельзя молча проглотить (spec.md, «Аудит решений»).
func TestRepeatedDecisionWithDifferentActorIsConflict(t *testing.T) {
	campaign := newCampaign(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	first := decisionParamsFor(DraftID("draft-1"), 1, "")
	if _, _, err := campaign.ApproveDraft(first); err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}
	campaign.PullDomainEvents()

	other := first
	other.Actor = ActorID("operator-2")
	_, events, err := campaign.ApproveDraft(other)
	if !errors.Is(err, ErrDecisionConflict) {
		t.Fatalf("ожидалась ErrDecisionConflict, получено %v", err)
	}
	if len(events) != 0 || len(campaign.Decisions()) != 1 {
		t.Fatal("конфликт не должен менять журнал или эмитить события")
	}
}

// Повтор с другой причиной — тоже другое решение и конфликт.
func TestRepeatedDecisionWithDifferentReasonIsConflict(t *testing.T) {
	campaign := newCampaign(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	first := decisionParamsFor(DraftID("draft-1"), 1, "")
	if _, _, err := campaign.ApproveDraft(first); err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}

	changed := first
	changed.Reason = "решение по итогам обсуждения"
	if _, _, err := campaign.ApproveDraft(changed); !errors.Is(err, ErrDecisionConflict) {
		t.Fatalf("ожидалась ErrDecisionConflict, получено %v", err)
	}
	if len(campaign.Decisions()) != 1 {
		t.Fatalf("журнал решений %d, ожидалась 1 запись", len(campaign.Decisions()))
	}
}

func TestRejectReturnsDraftForRevision(t *testing.T) {
	campaign := newCampaign(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}

	if _, _, err := campaign.RejectDraft(decisionParamsFor(DraftID("draft-1"), 1, "   ")); !errors.Is(err, ErrDecisionReasonRequired) {
		t.Fatalf("отклонение без причины: ожидалась ErrDecisionReasonRequired, получено %v", err)
	}

	_, events, err := campaign.RejectDraft(decisionParamsFor(DraftID("draft-1"), 1, "нужна другая формулировка"))
	if err != nil {
		t.Fatalf("RejectDraft: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("отклонение не порождает action-approved: %v", events)
	}
	draft, _ := campaign.Draft(DraftID("draft-1"))
	if draft.State() != DraftStateRejected {
		t.Fatalf("состояние %q, ожидалось %q", draft.State(), DraftStateRejected)
	}

	journal := campaign.DecisionsForDraft(DraftID("draft-1"))
	if len(journal) != 1 {
		t.Fatalf("журнал решений: %d", len(journal))
	}
	if journal[0].Actor() != ActorID("operator-1") || journal[0].Reason() == "" || journal[0].DecidedAt().IsZero() {
		t.Fatalf("журнал потерял автора, причину или время: %+v", journal[0])
	}

	// Доработка возвращает черновик на утверждение и требует нового решения.
	if _, err := campaign.ReviseDraft(DraftID("draft-1"), 1, testSpec(t)); err != nil {
		t.Fatalf("ReviseDraft: %v", err)
	}
	if err := campaign.ExecuteDraft(DraftID("draft-1"), 2); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("доработанный черновик требует нового решения, получено %v", err)
	}
	if _, _, err := campaign.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 2, "")); err != nil {
		t.Fatalf("ApproveDraft после доработки: %v", err)
	}
	if err := campaign.ExecuteDraft(DraftID("draft-1"), 2); err != nil {
		t.Fatalf("ExecuteDraft: %v", err)
	}
}

func TestExecuteAndFailTransitions(t *testing.T) {
	campaign := newCampaign(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	if err := campaign.FailDraft(DraftID("draft-1"), 1, "ошибка"); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("сбой без решения недопустим, получено %v", err)
	}

	if _, _, err := campaign.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 1, "")); err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}
	if err := campaign.FailDraft(DraftID("draft-1"), 1, "  "); !errors.Is(err, ErrDecisionReasonRequired) {
		t.Fatalf("ожидалась ErrDecisionReasonRequired, получено %v", err)
	}
	if err := campaign.ExecuteDraft(DraftID("draft-1"), 2); !errors.Is(err, ErrDraftVersionMismatch) {
		t.Fatalf("ожидалась ErrDraftVersionMismatch, получено %v", err)
	}
	if err := campaign.ExecuteDraft(DraftID("draft-1"), 1); err != nil {
		t.Fatalf("ExecuteDraft: %v", err)
	}
	draft, _ := campaign.Draft(DraftID("draft-1"))
	if draft.State() != DraftStateExecuted {
		t.Fatalf("состояние %q", draft.State())
	}
	// Повторное исполнение и сбой после исполнения запрещены.
	if err := campaign.ExecuteDraft(DraftID("draft-1"), 1); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("ожидалась ErrApprovalRequired, получено %v", err)
	}
	if err := campaign.FailDraft(DraftID("draft-1"), 1, "поздно"); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("ожидалась ErrApprovalRequired, получено %v", err)
	}
	if draft.State() != DraftStateExecuted {
		t.Fatalf("состояние изменилось: %q", draft.State())
	}

	// Неизвестный черновик отвергается до любых проверок версии.
	if err := campaign.ExecuteDraft(DraftID("missing"), 1); !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("ожидалась ErrDraftNotFound, получено %v", err)
	}
}

func TestExpireDraftDoesNotApprove(t *testing.T) {
	campaign := newCampaign(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	if err := campaign.ExpireDraft(DraftID("draft-1"), 1); err != nil {
		t.Fatalf("ExpireDraft: %v", err)
	}
	draft, _ := campaign.Draft(DraftID("draft-1"))
	if draft.State() != DraftStateExpired {
		t.Fatalf("состояние %q, ожидалось %q", draft.State(), DraftStateExpired)
	}
	if err := campaign.ExecuteDraft(DraftID("draft-1"), 1); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("просроченный черновик не исполняется, получено %v", err)
	}
	if err := campaign.ExpireDraft(DraftID("draft-1"), 2); !errors.Is(err, ErrDraftVersionMismatch) {
		t.Fatalf("ожидалась ErrDraftVersionMismatch, получено %v", err)
	}
}

func TestI3CompletedCampaignIsNotResumed(t *testing.T) {
	campaign := newCampaign(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}

	events, err := campaign.Complete(testMoment())
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	changed, ok := eventOfType[CampaignStateChanged](events)
	if !ok || changed.To != CampaignStateCompleted {
		t.Fatalf("ожидался campaign-state-changed в completed, получено %+v", changed)
	}

	if _, err := campaign.Complete(testMoment()); !errors.Is(err, ErrCampaignCompleted) {
		t.Fatalf("повторное завершение: ожидалась ErrCampaignCompleted, получено %v", err)
	}
	if _, _, err := campaign.DraftAction(DraftID("draft-2"), testSpec(t), testMoment()); !errors.Is(err, ErrCampaignCompleted) {
		t.Fatalf("ожидалась ErrCampaignCompleted, получено %v", err)
	}
	if _, _, err := campaign.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 1, "")); !errors.Is(err, ErrCampaignCompleted) {
		t.Fatalf("ожидалась ErrCampaignCompleted, получено %v", err)
	}
	if _, _, err := campaign.RejectDraft(decisionParamsFor(DraftID("draft-1"), 1, "нет")); !errors.Is(err, ErrCampaignCompleted) {
		t.Fatalf("ожидалась ErrCampaignCompleted, получено %v", err)
	}
	if _, err := campaign.ReviseDraft(DraftID("draft-1"), 1, testSpec(t)); !errors.Is(err, ErrCampaignCompleted) {
		t.Fatalf("ожидалась ErrCampaignCompleted, получено %v", err)
	}
	if err := campaign.ExecuteDraft(DraftID("draft-1"), 1); !errors.Is(err, ErrCampaignCompleted) {
		t.Fatalf("ожидалась ErrCampaignCompleted, получено %v", err)
	}
	if err := campaign.ExpireDraft(DraftID("draft-1"), 1); !errors.Is(err, ErrCampaignCompleted) {
		t.Fatalf("ожидалась ErrCampaignCompleted, получено %v", err)
	}
}

func TestCampaignProducesEventsUntilPulled(t *testing.T) {
	campaign := newCampaign(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	if _, _, err := campaign.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 1, "")); err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}
	if _, err := campaign.Complete(testMoment()); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	events := campaign.DomainEvents()
	if countEvents[CampaignStateChanged](events) != 2 {
		t.Fatalf("ожидались две смены состояния, получено %d", countEvents[CampaignStateChanged](events))
	}
	if countEvents[ActionApproved](events) != 1 {
		t.Fatalf("ожидался один action-approved, получено %d", countEvents[ActionApproved](events))
	}
	if got := campaign.PullDomainEvents(); len(got) != 3 {
		t.Fatalf("вычитано событий %d, ожидалось 3", len(got))
	}
	if len(campaign.DomainEvents()) != 0 {
		t.Fatal("после вычитывания буфер должен быть пуст")
	}
}
