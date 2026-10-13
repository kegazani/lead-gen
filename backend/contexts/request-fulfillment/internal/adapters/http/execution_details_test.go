package httpapi

import (
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"testing"
	"time"
)

func TestExecutionDetailsExplainEightLeadsAndRetry(t *testing.T) {
	now := time.Now()
	status := ports.WorkflowStatus{Found: 8, Target: 25, Phase: "searching", SearchRound: 2, SearchRoundLimit: 3, StepLimitSeconds: 600, SourcesKnown: true, FromPool: 8,
		StartedAt: now.Add(-15 * time.Minute), Activities: []ports.ExecutionActivity{{Type: "SearchLeads", State: "Scheduled", Attempt: 2, HeartbeatAt: now.Add(-20 * time.Second), NextAttemptAt: now.Add(time.Minute), FailureKind: "timeout"}}}
	view := executionDetails(status, 3, true, now)
	if view.FromPool != 8 || view.Crawled != 0 || !view.SourcesKnown || view.SearchRound != 2 || view.SavedDrafts != 3 || view.Elapsed != "15 мин 0 с" || view.StepLimit != "10 мин 0 с" {
		t.Fatalf("детали: %+v", view)
	}
	if len(view.Steps) != 1 || view.Steps[0].State != "Ожидает повторной попытки" || view.Steps[0].Attempt != 2 || view.Steps[0].RetryIn != "1 мин 0 с" || view.Steps[0].Failure != "Превышено время ожидания" {
		t.Fatalf("ожидание: %+v", view.Steps)
	}
	status.SourcesKnown = false
	status.ClosedAt = status.StartedAt.Add(5 * time.Minute)
	old := executionDetails(status, 0, false, now)
	if old.SourcesKnown || old.DraftsKnown || old.Elapsed != "5 мин 0 с" {
		t.Fatalf("старые/закрытые данные: %+v", old)
	}
}

func TestExecutionDetailsExplainRejectedContactPages(t *testing.T) {
	status := ports.WorkflowStatus{Found: 0, Target: 25, Phase: "searching", PartialReason: "no_usable_contacts"}
	view := executionDetails(status, 0, true, time.Now())
	if view.SearchIssue != "Часть страниц отклонена: не найдены пригодные контакты компании или источник является статьёй." {
		t.Fatalf("причина недобора скрыта: %+v", view)
	}
}
