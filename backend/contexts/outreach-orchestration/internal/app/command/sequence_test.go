package command

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

func sequenceStep(t *testing.T, transition domain.TransitionCondition) domain.SequenceStep {
	t.Helper()
	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	step, err := domain.NewSequenceStep(domain.SequenceStepParams{
		Action:     channel,
		Material:   "material-1",
		Segment:    "segment-1",
		Transition: transition,
	})
	if err != nil {
		t.Fatalf("NewSequenceStep: %v", err)
	}
	return step
}

func (k *testKit) defineSequence(t *testing.T, steps ...domain.SequenceStep) DefineSequenceResult {
	t.Helper()
	result, err := NewDefineSequenceHandler(k.access, k.uow).Handle(context.Background(), DefineSequence{
		CampaignID: "campaign-1",
		TenantID:   "tenant-1",
		Actor:      "operator-1",
		SequenceID: "sequence-1",
		Name:       "Сценарий среза",
		Steps:      steps,
	})
	if err != nil {
		t.Fatalf("DefineSequence.Handle: %v", err)
	}
	return result
}

func TestDefineSequenceIsIdempotent(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	savesBefore := kit.repo.saves

	first := kit.defineSequence(t, sequenceStep(t, domain.TransitionDefault))
	if !first.Created || first.Steps != 1 {
		t.Fatalf("ожидалось создание сценария из одного шага, получено %+v", first)
	}
	second := kit.defineSequence(t, sequenceStep(t, domain.TransitionDefault))
	if second.Created {
		t.Fatal("повтор команды не должен создавать второй сценарий")
	}
	if kit.repo.saves != savesBefore+1 {
		t.Fatalf("повтор не должен сохранять агрегат, число сохранений %d", kit.repo.saves)
	}
}

func TestEnrollLeadFormsFirstDraftAndDoesNotApproveIt(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.defineSequence(t, sequenceStep(t, domain.TransitionDefault))

	result, err := NewEnrollLeadHandler(kit.access, kit.clock, kit.uow, domain.DefaultParticipationLimits()).Handle(
		context.Background(),
		EnrollLead{
			CampaignID:  "campaign-1",
			TenantID:    "tenant-1",
			Actor:       "operator-1",
			SequenceID:  "sequence-1",
			ExecutionID: "execution-1",
			Lead:        "lead-1",
			Recipient:   "lead-1",
		},
	)
	if err != nil {
		t.Fatalf("EnrollLead.Handle: %v", err)
	}
	if !result.Created || result.DraftID != "execution-1.step-0" {
		t.Fatalf("ожидался первый черновик шага, получено %+v", result)
	}

	campaign := kit.campaign(t)
	pending := campaign.PendingApproval()
	if len(pending) != 1 || pending[0].State() != domain.DraftStatePendingApproval {
		t.Fatalf("черновик должен ждать утверждения, получено %d", len(pending))
	}
	if campaign.PendingSequenceDrafts() != 1 {
		t.Fatalf("объём неутверждённых черновиков цепочек должен быть 1, получено %d", campaign.PendingSequenceDrafts())
	}
}

func TestRecordReplyThenResume(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.defineSequence(t, sequenceStep(t, domain.TransitionDefault))
	if _, err := NewEnrollLeadHandler(kit.access, kit.clock, kit.uow, domain.DefaultParticipationLimits()).Handle(
		context.Background(),
		EnrollLead{
			CampaignID:  "campaign-1",
			TenantID:    "tenant-1",
			Actor:       "operator-1",
			SequenceID:  "sequence-1",
			ExecutionID: "execution-1",
			Lead:        "lead-1",
			Recipient:   "lead-1",
		},
	); err != nil {
		t.Fatalf("EnrollLead.Handle: %v", err)
	}

	if _, err := NewRecordOutcomeHandler(kit.access, kit.clock, kit.uow).Handle(context.Background(), RecordOutcome{
		CampaignID:  "campaign-1",
		TenantID:    "tenant-1",
		Actor:       "operator-1",
		ExecutionID: "execution-1",
		Reaction:    domain.ReactionReplied,
		Confident:   true,
	}); err != nil {
		t.Fatalf("RecordOutcome.Handle: %v", err)
	}

	campaign := kit.campaign(t)
	execution, _ := campaign.Execution("execution-1")
	if execution.State() != domain.ExecutionSuspended {
		t.Fatalf("ответ должен приостановить цепочку, состояние %q", execution.State())
	}

	if _, err := NewResumeExecutionHandler(kit.access, kit.clock, kit.uow).Handle(context.Background(), ResumeExecution{
		CampaignID:  "campaign-1",
		TenantID:    "tenant-1",
		Actor:       "operator-1",
		ExecutionID: "execution-1",
	}); err != nil {
		t.Fatalf("ResumeExecution.Handle: %v", err)
	}
	campaign = kit.campaign(t)
	execution, _ = campaign.Execution("execution-1")
	if execution.State() != domain.ExecutionActive {
		t.Fatalf("после решения оператора цепочка должна продолжиться, состояние %q", execution.State())
	}
}

func TestRemoveLeadStopsSequence(t *testing.T) {
	kit := newTestKit(t)
	kit.mustCreate(t)
	kit.defineSequence(t, sequenceStep(t, domain.TransitionDefault))
	if _, err := NewEnrollLeadHandler(kit.access, kit.clock, kit.uow, domain.DefaultParticipationLimits()).Handle(
		context.Background(),
		EnrollLead{
			CampaignID:  "campaign-1",
			TenantID:    "tenant-1",
			Actor:       "operator-1",
			SequenceID:  "sequence-1",
			ExecutionID: "execution-1",
			Lead:        "lead-1",
			Recipient:   "lead-1",
		},
	); err != nil {
		t.Fatalf("EnrollLead.Handle: %v", err)
	}

	result, err := NewRemoveLeadHandler(kit.access, kit.clock, kit.uow).Handle(context.Background(), RemoveLead{
		CampaignID:  "campaign-1",
		TenantID:    "tenant-1",
		Actor:       "operator-1",
		ExecutionID: "execution-1",
		Reason:      "лид попросил прекратить",
	})
	if err != nil {
		t.Fatalf("RemoveLead.Handle: %v", err)
	}
	if !result.Removed || result.State != domain.ExecutionStopped {
		t.Fatalf("удаление должно остановить участие, получено %+v", result)
	}
	campaign := kit.campaign(t)
	journal := campaign.JournalForLead("lead-1")
	found := false
	for _, entry := range journal {
		if entry.Result == domain.OutcomeRemoved && entry.Reason == "лид попросил прекратить" {
			found = true
		}
	}
	if !found {
		t.Fatal("причина удаления должна быть зафиксирована в журнале")
	}
}
