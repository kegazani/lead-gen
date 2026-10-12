package workflowadapter

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/workflow"
)

func TestEditorialExecutionGeneratesAtTargetAndCompletesWithoutHandoff(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).Return(searchResult{Found: 2, LeadIDs: []string{"lead-a", "lead-b"}}, nil).Once()
	env.OnActivity(ActivityDraftEmails, mock.Anything, mock.MatchedBy(func(in draftInput) bool { return in.Editorial && len(in.LeadIDs) == 2 })).Return(draftResult{Drafted: 2, LeadIDs: []string{"lead-a", "lead-b"}}, nil).Once()

	env.ExecuteWorkflow(Fulfillment, Params{
		RequestID: "req-editorial", TenantID: "tenant-1", LeadTarget: 2,
		ExecutionPurpose: "editorial", Policy: ExecutionPolicy{ApprovalTTL: time.Minute, ExecutionTimeout: 3 * time.Hour},
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatal(err)
	}
	if status.Phase != PhaseDraftsGenerated || status.Drafted != 2 || status.Handed != 0 {
		t.Fatalf("status = %+v", status)
	}
	env.AssertNotCalled(t, "ExecuteActivity", ActivityHandoffDrafts)
	env.AssertNotCalled(t, "ExecuteActivity", ActivityFulfillRequest)
	env.AssertNotCalled(t, "ExecuteActivity", ActivityCancelRequest)
	env.AssertExpectations(t)
}

func TestLegacyEditorialExecutionKeepsHandoffPath(t *testing.T) {
	env := newEnv(t)
	env.OnGetVersion(changeEditorialDraftCompletion, workflow.DefaultVersion, workflow.Version(1)).
		Return(func(string, workflow.Version, workflow.Version) workflow.Version { return workflow.DefaultVersion })
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).Return(searchResult{Found: 2, LeadIDs: []string{"lead-a", "lead-b"}}, nil).Once()
	env.OnActivity(ActivityDraftEmails, mock.Anything, mock.Anything).Return(draftResult{Drafted: 2, LeadIDs: []string{"lead-a", "lead-b"}}, nil).Once()
	env.OnActivity(ActivityHandoffDrafts, mock.Anything, mock.Anything).Return(handoffOutcome{Handed: 2, Expected: 2}, nil).Once()

	env.ExecuteWorkflow(Fulfillment, Params{
		RequestID: "req-editorial-legacy", TenantID: "tenant-1", LeadTarget: 2,
		ExecutionPurpose: "editorial", Policy: ExecutionPolicy{ApprovalTTL: time.Minute, ExecutionTimeout: 3 * time.Hour},
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatal(err)
	}
	if status.Phase != PhasePreparedForReview || status.Drafted != 2 || status.Handed != 2 {
		t.Fatalf("legacy status = %+v", status)
	}
	env.AssertExpectations(t)
}

func TestEditorialExecutionRejectsEmptySearchAndMissingGenerator(t *testing.T) {
	t.Run("нет лидов", func(t *testing.T) {
		env := newEnv(t)
		env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).Return(searchResult{}, nil).Times(maxTopUpRounds)
		env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-editorial-empty", TenantID: "tenant-1", LeadTarget: 1, ExecutionPurpose: "editorial"})
		if env.GetWorkflowError() == nil {
			t.Fatal("пустой editorial crawl не должен завершаться успехом")
		}
		env.AssertNotCalled(t, "ExecuteActivity", ActivityDraftEmails)
	})
	t.Run("нет генератора", func(t *testing.T) {
		activities := &Activities{}
		_, err := activities.DraftEmails(t.Context(), draftInput{RequestID: "req", TenantID: "t", LeadIDs: []string{"lead"}, Editorial: true})
		if err == nil {
			t.Fatal("editorial без LLM-контракта должен завершиться ошибкой")
		}
	})
}
