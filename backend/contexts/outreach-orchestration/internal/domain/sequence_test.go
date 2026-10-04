package domain_test

import (
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// Набор тестов движка последовательностей: черновики и утверждение, запрет
// автоутверждения, ветвление, приостановка и остановка, ограничения участия и
// журнал.

var seqBase = time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)

func seqChannel(t *testing.T) domain.ActionChannel {
	t.Helper()
	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	return channel
}

func seqStep(t *testing.T, delay time.Duration, transition domain.TransitionCondition) domain.SequenceStep {
	t.Helper()
	step, err := domain.NewSequenceStep(domain.SequenceStepParams{
		Action:     seqChannel(t),
		Material:   domain.MaterialID("material-1"),
		Segment:    domain.SegmentID("segment-1"),
		Delay:      delay,
		Transition: transition,
	})
	if err != nil {
		t.Fatalf("NewSequenceStep: %v", err)
	}
	return step
}

func seqCampaign(t *testing.T) *domain.Campaign {
	t.Helper()
	window, err := domain.NewCampaignWindow(seqBase, seqBase.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("NewCampaignWindow: %v", err)
	}
	campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       "campaign-1",
		TenantID: "tenant-1",
		Name:     "Кампания цепочек",
		Window:   window,
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	return campaign
}

func define(t *testing.T, campaign *domain.Campaign, id domain.SequenceID, steps ...domain.SequenceStep) {
	t.Helper()
	_, err := campaign.DefineSequence(domain.SequenceDefinitionParams{
		ID:       id,
		Campaign: campaign.ID(),
		Tenant:   campaign.TenantID(),
		Name:     "Сценарий " + string(id),
		Steps:    steps,
	})
	if err != nil {
		t.Fatalf("DefineSequence: %v", err)
	}
}

func enroll(t *testing.T, campaign *domain.Campaign, execution domain.ExecutionID, lead domain.LeadID, at time.Time) *domain.SequenceExecution {
	t.Helper()
	executionState, err := campaign.EnrollLead(domain.EnrollmentParams{
		ExecutionID: execution,
		SequenceID:  "sequence-1",
		TenantID:    campaign.TenantID(),
		Lead:        lead,
		Recipient:   domain.RecipientID(string(lead)),
		At:          at,
	})
	if err != nil {
		t.Fatalf("EnrollLead: %v", err)
	}
	return executionState
}

func approveStep(t *testing.T, campaign *domain.Campaign, draftID domain.DraftID, version int, actor domain.ActorID, at time.Time) {
	t.Helper()
	_, _, err := campaign.ApproveDraft(domain.DecisionParams{
		DecisionID:   domain.DecisionID("decision-" + string(draftID)),
		DraftID:      draftID,
		DraftVersion: version,
		Actor:        actor,
		DecidedAt:    at,
	})
	if err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}
}

func TestParseTransitionConditionRejectsOpened(t *testing.T) {
	if _, err := domain.ParseTransitionCondition("opened"); !errors.Is(err, domain.ErrOpenedSignalUnavailable) {
		t.Fatalf("условие по открытию должно быть отвергнуто, получено %v", err)
	}
	condition, err := domain.ParseTransitionCondition("")
	if err != nil || condition != domain.TransitionDefault {
		t.Fatalf("пустое условие должно давать переход по умолчанию, получено %q, %v", condition, err)
	}
}

func TestNewSequenceStepRejectsNegativeDelay(t *testing.T) {
	_, err := domain.NewSequenceStep(domain.SequenceStepParams{
		Action:   seqChannel(t),
		Material: "material-1",
		Segment:  "segment-1",
		Delay:    -time.Second,
	})
	if !errors.Is(err, domain.ErrInvalidStepDelay) {
		t.Fatalf("отрицательная задержка должна быть отвергнута, получено %v", err)
	}
}

func TestAdvanceFormsDraftAndDoesNotSend(t *testing.T) {
	campaign := seqCampaign(t)
	define(t, campaign, "sequence-1", seqStep(t, 0, domain.TransitionDefault))
	enroll(t, campaign, "execution-1", "lead-1", seqBase)

	report, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits())
	if err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	if len(report.Drafted) != 1 {
		t.Fatalf("ожидался один сформированный черновик, получено %d", len(report.Drafted))
	}
	pending := campaign.PendingApproval()
	if len(pending) != 1 {
		t.Fatalf("ожидался один черновик в очереди утверждений, получено %d", len(pending))
	}
	if pending[0].State() != domain.DraftStatePendingApproval {
		t.Fatalf("черновик должен ждать утверждения, состояние %q", pending[0].State())
	}
}

func TestDelayDefersNextStepDraft(t *testing.T) {
	campaign := seqCampaign(t)
	define(t, campaign, "sequence-1",
		seqStep(t, 0, domain.TransitionDefault),
		seqStep(t, 2*time.Hour, domain.TransitionDefault),
	)
	enroll(t, campaign, "execution-1", "lead-1", seqBase)
	if _, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits()); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	approveStep(t, campaign, "execution-1.step-0", 1, "operator-1", seqBase)
	if _, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits()); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}

	// До истечения задержки второй черновик не формируется.
	if report, err := campaign.AdvanceSequences(seqBase.Add(time.Hour), domain.DefaultParticipationLimits()); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	} else if len(report.Drafted) != 0 {
		t.Fatalf("черновик сформирован раньше задержки: %d", len(report.Drafted))
	}
	if report, err := campaign.AdvanceSequences(seqBase.Add(2*time.Hour), domain.DefaultParticipationLimits()); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	} else if len(report.Drafted) != 1 {
		t.Fatalf("по истечении задержки ожидался черновик, получено %d", len(report.Drafted))
	}
}

func TestApprovalDoesNotPropagateAndStallReported(t *testing.T) {
	campaign := seqCampaign(t)
	define(t, campaign, "sequence-1",
		seqStep(t, 0, domain.TransitionDefault),
		seqStep(t, 0, domain.TransitionDefault),
	)
	enroll(t, campaign, "execution-1", "lead-1", seqBase)
	report, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits())
	if err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	approveStep(t, campaign, "execution-1.step-0", 1, "operator-1", seqBase)
	if _, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits()); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	// Второй шаг сформирован, но не утверждён: автоутверждения нет.
	second, ok := campaign.Draft("execution-1.step-1")
	if !ok {
		t.Fatal("ожидался черновик второго шага")
	}
	if second.State() != domain.DraftStatePendingApproval {
		t.Fatalf("второй шаг должен требовать отдельного утверждения, состояние %q", second.State())
	}
	report, err = campaign.AdvanceSequences(seqBase.Add(24*time.Hour), domain.DefaultParticipationLimits())
	if err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	if len(report.Stalled) != 1 {
		t.Fatalf("ожидался простой по шагу, получено %d", len(report.Stalled))
	}
	execution, _ := campaign.Execution("execution-1")
	if execution.StepIndex() != 1 {
		t.Fatalf("цепочка не должна переходить дальше без утверждения, шаг %d", execution.StepIndex())
	}
}

func TestBranchingWaitsForReaction(t *testing.T) {
	campaign := seqCampaign(t)
	define(t, campaign, "sequence-1",
		seqStep(t, 0, domain.TransitionOnTransition),
		seqStep(t, 0, domain.TransitionDefault),
	)
	enroll(t, campaign, "execution-1", "lead-1", seqBase)
	if _, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits()); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	approveStep(t, campaign, "execution-1.step-0", 1, "operator-1", seqBase)

	report, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits())
	if err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	if len(report.Advanced) != 0 || len(report.Held) != 1 {
		t.Fatalf("без реакции шаг не должен продвигать цепочку: advanced=%d held=%d", len(report.Advanced), len(report.Held))
	}

	if err := campaign.RecordReaction(domain.RecordReactionParams{
		ExecutionID: "execution-1",
		Reaction:    domain.ReactionTransitioned,
		Confident:   true,
		At:          seqBase,
	}); err != nil {
		t.Fatalf("RecordReaction: %v", err)
	}
	report, err = campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits())
	if err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	if len(report.Advanced) != 1 {
		t.Fatalf("после реакции цепочка должна продвинуться, advanced=%d", len(report.Advanced))
	}
}

func TestReplySuspendsAndOperatorResumes(t *testing.T) {
	campaign := seqCampaign(t)
	define(t, campaign, "sequence-1",
		seqStep(t, 0, domain.TransitionDefault),
		seqStep(t, 0, domain.TransitionDefault),
	)
	enroll(t, campaign, "execution-1", "lead-1", seqBase)
	if _, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits()); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	if err := campaign.RecordReaction(domain.RecordReactionParams{
		ExecutionID: "execution-1",
		Reaction:    domain.ReactionReplied,
		Confident:   true,
		At:          seqBase,
	}); err != nil {
		t.Fatalf("RecordReaction: %v", err)
	}
	execution, _ := campaign.Execution("execution-1")
	if execution.State() != domain.ExecutionSuspended {
		t.Fatalf("ответ должен приостановить цепочку, состояние %q", execution.State())
	}
	// Приостановленная цепочка не порождает последующие шаги.
	report, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits())
	if err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	if len(report.Drafted) != 0 || len(report.Advanced) != 0 {
		t.Fatalf("приостановленная цепочка не должна продвигаться: drafted=%d advanced=%d", len(report.Drafted), len(report.Advanced))
	}
	// Возобновление только решением оператора.
	if err := campaign.ResumeExecution("execution-1", "operator-1", seqBase); err != nil {
		t.Fatalf("ResumeExecution: %v", err)
	}
	execution, _ = campaign.Execution("execution-1")
	if execution.State() != domain.ExecutionActive {
		t.Fatalf("после решения оператора цепочка должна продолжиться, состояние %q", execution.State())
	}
}

func TestStopOnUnsubscribeIsTerminal(t *testing.T) {
	campaign := seqCampaign(t)
	define(t, campaign, "sequence-1", seqStep(t, 0, domain.TransitionDefault))
	enroll(t, campaign, "execution-1", "lead-1", seqBase)
	if err := campaign.RecordReaction(domain.RecordReactionParams{
		ExecutionID: "execution-1",
		Reaction:    domain.ReactionUnsubscribed,
		Confident:   true,
		At:          seqBase,
	}); err != nil {
		t.Fatalf("RecordReaction: %v", err)
	}
	if err := campaign.ResumeExecution("execution-1", "operator-1", seqBase); !errors.Is(err, domain.ErrResumeNotSuspended) {
		t.Fatalf("остановленную цепочку нельзя возобновить, получено %v", err)
	}
}

func TestUncertainClassificationDoesNotBranch(t *testing.T) {
	campaign := seqCampaign(t)
	define(t, campaign, "sequence-1",
		seqStep(t, 0, domain.TransitionOnReply),
		seqStep(t, 0, domain.TransitionDefault),
	)
	enroll(t, campaign, "execution-1", "lead-1", seqBase)
	if _, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits()); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	approveStep(t, campaign, "execution-1.step-0", 1, "operator-1", seqBase)
	if err := campaign.RecordReaction(domain.RecordReactionParams{
		ExecutionID: "execution-1",
		Reaction:    domain.ReactionReplied,
		Confident:   false,
		At:          seqBase,
		Reason:      "классификация неуверенна",
	}); err != nil {
		t.Fatalf("RecordReaction: %v", err)
	}
	execution, _ := campaign.Execution("execution-1")
	if execution.State() != domain.ExecutionActive {
		t.Fatalf("неуверенная классификация не должна менять состояние, получено %q", execution.State())
	}
	report, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits())
	if err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	if len(report.Advanced) != 0 {
		t.Fatalf("ветка не должна выбираться автоматически: advanced=%d", len(report.Advanced))
	}
}

func TestTouchLimitAcrossSequences(t *testing.T) {
	campaign := seqCampaign(t)
	define(t, campaign, "sequence-1", seqStep(t, 0, domain.TransitionDefault))
	define(t, campaign, "sequence-2", seqStep(t, 0, domain.TransitionDefault))
	limits := domain.ParticipationLimits{MaxTouchesPerLead: 1, TouchPeriod: 24 * time.Hour, MaxPendingDrafts: 100}

	enroll(t, campaign, "execution-1", "lead-1", seqBase)
	if _, err := campaign.AdvanceSequences(seqBase, limits); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	// Вторая цепочка по тому же лиду не должна превышать общий предел.
	execution2, err := campaign.EnrollLead(domain.EnrollmentParams{
		ExecutionID: "execution-2",
		SequenceID:  "sequence-2",
		TenantID:    campaign.TenantID(),
		Lead:        "lead-1",
		Recipient:   "lead-1",
		At:          seqBase,
	})
	if err != nil {
		t.Fatalf("EnrollLead: %v", err)
	}
	report, err := campaign.AdvanceSequences(seqBase, limits)
	if err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	if len(report.Drafted) != 0 {
		t.Fatalf("при достигнутом пределе касаний черновик не формируется, получено %d", len(report.Drafted))
	}
	if execution2.DraftCreated() {
		t.Fatal("второе участие не должно получить черновик при достигнутом пределе")
	}
}

func TestDraftVolumeLimitHaltsFormation(t *testing.T) {
	campaign := seqCampaign(t)
	define(t, campaign, "sequence-1", seqStep(t, 0, domain.TransitionDefault))
	limits := domain.ParticipationLimits{MaxTouchesPerLead: 10, TouchPeriod: 24 * time.Hour, MaxPendingDrafts: 1}

	enroll(t, campaign, "execution-1", "lead-1", seqBase)
	if _, err := campaign.AdvanceSequences(seqBase, limits); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	enroll(t, campaign, "execution-2", "lead-2", seqBase)
	report, err := campaign.AdvanceSequences(seqBase, limits)
	if err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	if !report.Halted {
		t.Fatal("при достигнутом объёме черновиков формирование должно приостановиться")
	}
	if len(report.Drafted) != 0 {
		t.Fatalf("сверх предела черновики не формируются, получено %d", len(report.Drafted))
	}
}

func TestJournalRecordsStepResultAndApprover(t *testing.T) {
	campaign := seqCampaign(t)
	define(t, campaign, "sequence-1",
		seqStep(t, 0, domain.TransitionDefault),
		seqStep(t, 0, domain.TransitionDefault),
	)
	enroll(t, campaign, "execution-1", "lead-1", seqBase)
	if _, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits()); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	approveStep(t, campaign, "execution-1.step-0", 1, "operator-7", seqBase)
	if _, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits()); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}

	journal := campaign.JournalForLead("lead-1")
	var approved *domain.JournalEntry
	for i := range journal {
		if journal[i].Result == domain.OutcomeApproved {
			approved = &journal[i]
		}
	}
	if approved == nil {
		t.Fatal("в журнале нет записи об утверждении шага")
	}
	if approved.Actor != "operator-7" {
		t.Fatalf("в журнале должен быть утвердивший оператор, получено %q", approved.Actor)
	}
	if approved.Step != 0 {
		t.Fatalf("шаг в журнале должен быть 0, получено %d", approved.Step)
	}
}

func TestSequenceSnapshotRoundTrip(t *testing.T) {
	campaign := seqCampaign(t)
	define(t, campaign, "sequence-1",
		seqStep(t, 0, domain.TransitionOnTransition),
		seqStep(t, time.Hour, domain.TransitionDefault),
	)
	enroll(t, campaign, "execution-1", "lead-1", seqBase)
	if _, err := campaign.AdvanceSequences(seqBase, domain.DefaultParticipationLimits()); err != nil {
		t.Fatalf("AdvanceSequences: %v", err)
	}
	approveStep(t, campaign, "execution-1.step-0", 1, "operator-1", seqBase)

	restored, err := domain.RehydrateCampaign(campaign.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateCampaign: %v", err)
	}
	definition, ok := restored.SequenceDefinition("sequence-1")
	if !ok || definition.Len() != 2 {
		t.Fatalf("сценарий не восстановлен: %v", ok)
	}
	execution, ok := restored.Execution("execution-1")
	if !ok {
		t.Fatal("участие не восстановлено")
	}
	if execution.State() != domain.ExecutionActive {
		t.Fatalf("состояние участия должно сохраниться, получено %q", execution.State())
	}
	if len(execution.Journal()) == 0 {
		t.Fatal("журнал участия должен сохраниться")
	}
}
