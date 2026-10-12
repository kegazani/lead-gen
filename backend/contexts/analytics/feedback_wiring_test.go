package analytics_test

import (
	"context"
	"testing"

	analytics "lead-gen/backend/contexts/analytics"
	"lead-gen/backend/contexts/analytics/internal/app/command"
	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/contracts/lead-management/recommendationapply"
)

// wiringPort — тестовый двойник контракта recommendation-apply, которым
// проверяется проводка адаптера потребителя в Build.
type wiringPort struct {
	calls []recommendationapply.Application
}

func (p *wiringPort) ApplyRecommendation(_ context.Context, application recommendationapply.Application) (recommendationapply.Receipt, error) {
	p.calls = append(p.calls, application)
	return recommendationapply.Receipt{JobID: "reco-wired"}, nil
}

// TestBuildWiresRecommendationFeedback проверяет, что заданный контракт
// recommendation-apply подключается в Build адаптером ports.QualityFeedback, и
// применение рекомендации больше не упирается в безусловную недоступность:
// возвращается ссылка на созданное задание.
func TestBuildWiresRecommendationFeedback(t *testing.T) {
	port := &wiringPort{}
	slice, err := analytics.Build(analytics.Config{RecommendationApply: port})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	application, err := slice.ApplyRecommendation.Handle(context.Background(), command.ApplyRecommendation{
		TenantID: "t1", ScopeKind: "industry", ScopeKey: "saas", Reason: "no_signal", Basis: "наблюдений 3",
		Change: ports.SearchChange{Kind: "new-search", Criteria: "saas-агентства", Limit: 20},
	})
	if err != nil {
		t.Fatalf("применение рекомендации: %v", err)
	}
	if application.Reference != "reco-wired" {
		t.Fatalf("ссылка = %q", application.Reference)
	}
	if len(port.calls) != 1 {
		t.Fatalf("вызовов контракта %d, ожидался 1", len(port.calls))
	}
	if port.calls[0].ChangeID == "" || port.calls[0].Change.Criteria != "saas-агентства" {
		t.Fatalf("запрос контракта = %+v", port.calls[0])
	}
}
