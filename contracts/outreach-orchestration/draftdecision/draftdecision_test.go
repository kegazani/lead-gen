package draftdecision

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

func approvedActionFixture() approvedaction.ApprovedAction {
	instant := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	action := approvedaction.NewApprovedAction(approvedaction.Envelope{
		EventID:       "evt-1",
		OccurredAt:    instant,
		TenantID:      "tenant-1",
		CorrelationID: "campaign-1",
		CausationID:   "decision-1",
	})
	action.ApprovedActionID = "company-1@v1"
	action.CampaignID = "campaign-1"
	action.DraftID = "company-1@v1"
	action.DraftVersion = 1
	action.DecisionID = "decision-1"
	action.Channel = approvedaction.ChannelEmailName
	action.RecipientRef = "company-1@v1"
	action.MaterialVersionRef = "company-1@v1"
	action.DecidedAt = instant
	return action
}

func TestDecisionJSONRoundTripApproved(t *testing.T) {
	action := approvedActionFixture()
	original := Decision{Outcome: OutcomeApproved, ApprovedAction: &action}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded Decision
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Outcome != OutcomeApproved {
		t.Fatalf("исход = %q", decoded.Outcome)
	}
	if decoded.ApprovedAction == nil || *decoded.ApprovedAction != action {
		t.Fatalf("утверждённое действие изменено: %+v", decoded.ApprovedAction)
	}
	if err := decoded.ApprovedAction.Validate(); err != nil {
		t.Fatalf("round-trip дал невалидное действие: %v", err)
	}
}

func TestDecisionJSONOmitsActionForNonApprovedOutcomes(t *testing.T) {
	for _, outcome := range []Outcome{OutcomePending, OutcomeRejected} {
		encoded, err := json.Marshal(Decision{Outcome: outcome})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if _, ok := fields["approved_action"]; ok {
			t.Fatalf("исход %q несёт утверждённое действие", outcome)
		}
	}
}

func TestErrorsAreDistinct(t *testing.T) {
	all := []error{ErrNotFound, ErrPending, ErrRejected, ErrUnavailable}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("исходы не различимы: %v и %v", a, b)
			}
		}
	}
}
