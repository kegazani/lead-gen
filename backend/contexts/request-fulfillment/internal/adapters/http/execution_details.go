package httpapi

import (
	"fmt"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// Детали строятся из наблюдаемого workflow и собственного хранилища писем.
// Возраст heartbeat означает связь с исполнителем, а не число обойдённых сайтов.
type executionDetailsView struct {
	SearchRound, SearchRoundLimit, FromPool, Crawled, SavedDrafts int
	SourcesKnown, DraftsKnown                                     bool
	Elapsed, StepLimit, Explanation, SearchIssue                  string
	Steps                                                         []executionStepView
}

type executionStepView struct {
	Label, State, RunningFor, HeartbeatAge, RetryIn, Failure string
	Attempt                                                  int32
}

func executionDetails(status ports.WorkflowStatus, savedDrafts int, draftsKnown bool, now time.Time) executionDetailsView {
	view := executionDetailsView{
		SearchRound: status.SearchRound, SearchRoundLimit: status.SearchRoundLimit,
		SourcesKnown: status.SourcesKnown, FromPool: status.FromPool, Crawled: status.Crawled,
		SavedDrafts: savedDrafts, DraftsKnown: draftsKnown,
	}
	messages := consoleMessages()
	switch status.PartialReason {
	case "search_unavailable":
		view.SearchIssue = messages.Text("request.details.issue.search")
	case "crawl_unavailable":
		view.SearchIssue = messages.Text("request.details.issue.crawl")
	case "extract_cost_unknown":
		view.SearchIssue = messages.Text("request.details.issue.extract")
	case "deadline_exceeded":
		view.SearchIssue = messages.Text("request.details.issue.deadline")
	case "budget_capacity":
		view.SearchIssue = messages.Text("request.details.issue.budget")
	case "cancelled":
		view.SearchIssue = messages.Text("request.details.issue.cancelled")
	}

	switch status.Phase {
	case "searching":
		view.Explanation = messages.Text("request.details.searching")
	case "drafting":
		view.Explanation = messages.Text("request.details.drafting")
	case "starting-processing":
		view.Explanation = messages.Text("request.details.starting")
	}
	if !status.StartedAt.IsZero() {
		end := now
		if !status.ClosedAt.IsZero() {
			end = status.ClosedAt
		}
		view.Elapsed = elapsedLabel(end.Sub(status.StartedAt))
	}
	if status.StepLimitSeconds > 0 {
		view.StepLimit = elapsedLabel(time.Duration(status.StepLimitSeconds) * time.Second)
	}
	for _, activity := range status.Activities {
		step := executionStepView{Attempt: activity.Attempt}
		switch activity.Type {
		case "SearchLeads":
			step.Label = messages.Text("request.details.step.search")
		case "DraftEmails":
			step.Label = messages.Text("request.details.step.draft")
		default:
			step.Label = messages.Text("request.details.step.transition")
		}
		switch activity.State {
		case "Started":
			step.State = messages.Text("request.details.state.started")
		case "Scheduled":
			step.State = messages.Text("request.details.state.scheduled")
			if activity.Attempt > 1 {
				step.State = messages.Text("request.details.state.retry")
			}
		case "CancelRequested":
			step.State = messages.Text("request.details.state.cancelled")
		case "Paused", "PauseRequested":
			step.State = messages.Text("request.details.state.paused")
		default:
			step.State = messages.Text("request.details.unknown")
		}
		if activity.State == "Started" && !activity.StartedAt.IsZero() {
			step.RunningFor = elapsedLabel(now.Sub(activity.StartedAt))
		}
		if !activity.HeartbeatAt.IsZero() {
			step.HeartbeatAge = elapsedLabel(now.Sub(activity.HeartbeatAt))
		}
		if activity.NextAttemptAt.After(now) {
			step.RetryIn = elapsedLabel(activity.NextAttemptAt.Sub(now))
		}
		switch activity.FailureKind {
		case "timeout":
			step.Failure = messages.Text("request.details.failure.timeout")
		case "cancelled":
			step.Failure = messages.Text("request.details.failure.cancelled")
		case "error":
			step.Failure = messages.Text("request.details.failure.error")
		}
		view.Steps = append(view.Steps, step)
	}
	return view
}

func elapsedLabel(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	seconds := int(duration.Seconds())
	return fmt.Sprintf(consoleMessages().Text("request.details.duration"), seconds/60, seconds%60)
}
