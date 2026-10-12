package command

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// fixedClock — часы с фиксированным временем для детерминированных тестов.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// fakeStore — тестовый двойник ports.InsightStore (только в тестах).
type fakeStore struct {
	snapshots    []readmodel.InsightSnapshot
	applications []readmodel.RecommendationApplication
}

func (s *fakeStore) SaveSnapshot(_ context.Context, snapshot readmodel.InsightSnapshot) error {
	s.snapshots = append(s.snapshots, snapshot)
	return nil
}

func (s *fakeStore) LoadSnapshots(_ context.Context) ([]readmodel.InsightSnapshot, error) {
	return s.snapshots, nil
}

func (s *fakeStore) SaveApplication(_ context.Context, application readmodel.RecommendationApplication) error {
	s.applications = append(s.applications, application)
	return nil
}

func (s *fakeStore) LoadApplications(_ context.Context) ([]readmodel.RecommendationApplication, error) {
	return s.applications, nil
}

// fakeFeedback — тестовый двойник ports.QualityFeedback (только в тестах).
type fakeFeedback struct {
	calls []ports.RecommendationChange
	ref   string
	err   error
}

func (f *fakeFeedback) ApplyRecommendation(_ context.Context, change ports.RecommendationChange) (ports.RecommendationReceipt, error) {
	f.calls = append(f.calls, change)
	if f.err != nil {
		return ports.RecommendationReceipt{}, f.err
	}
	return ports.RecommendationReceipt{Reference: f.ref}, nil
}

var insightTime = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

// TestApplyOutcomePersistsSnapshot проверяет применение исхода и запись снимка.
func TestApplyOutcomePersistsSnapshot(t *testing.T) {
	model := readmodel.NewInsightModel()
	store := &fakeStore{}
	handler := NewApplyOutcomeHandler(model, store)

	for i, outcome := range []ApplyOutcome{
		{TenantID: "t1", JobID: "job-a", CompanyID: "c1", Source: "crawl", Industry: "saas", Verdict: "hypothesis", Reason: "no_signal", OccurredAt: insightTime},
		{TenantID: "t1", JobID: "job-a", CompanyID: "c2", Source: "crawl", Industry: "saas", Verdict: "unsuitable", Reason: "refuted", OccurredAt: insightTime},
	} {
		if err := handler.Handle(context.Background(), outcome); err != nil {
			t.Fatalf("применение исхода %d: %v", i, err)
		}
	}
	data := model.Insight("t1", readmodel.InsightFilter{})
	if data.Total != 2 || data.Verdicts.Hypothesis != 1 || data.Verdicts.Unsuitable != 1 {
		t.Fatalf("проекция не обновлена: %+v", data)
	}
	if len(store.snapshots) == 0 || store.snapshots[len(store.snapshots)-1].TenantID != "t1" {
		t.Fatalf("снимок не сохранён: %+v", store.snapshots)
	}
}

// TestApplyOutcomeUnavailable проверяет честную недоступность без проекции.
func TestApplyOutcomeUnavailable(t *testing.T) {
	handler := NewApplyOutcomeHandler(nil, nil)
	if err := handler.Handle(context.Background(), ApplyOutcome{TenantID: "t1", CompanyID: "c1"}); !errors.Is(err, ErrInsightUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrInsightUnavailable", err)
	}
}

// TestApplyRecommendationRecordsBeforeSnapshot проверяет применение рекомендации:
// изменение создаётся в контуре поиска, факт применения фиксируется со снимком «до».
func TestApplyRecommendationRecordsBeforeSnapshot(t *testing.T) {
	model := readmodel.NewInsightModel()
	for i := 0; i < 3; i++ {
		model.Apply(readmodel.InsightOutcome{
			TenantID: "t1", JobID: "job-a", CompanyID: string(rune('a' + i)), Source: "crawl", Industry: "saas",
			Verdict: readmodel.VerdictHypothesis, Reason: readmodel.ReasonNoSignal, OccurredAt: insightTime,
		})
	}
	feedback := &fakeFeedback{ref: "target-42"}
	store := &fakeStore{}
	handler := NewApplyRecommendationHandler(model, feedback, store, fixedClock{at: insightTime})

	application, err := handler.Handle(context.Background(), ApplyRecommendation{
		TenantID: "t1", ScopeKind: "industry", ScopeKey: "saas",
		Reason: "no_signal", Basis: "наблюдений 3",
		Change: ports.SearchChange{Kind: "new-search", Criteria: "saas-агентства", Targets: []string{"разработка на заказ"}, Limit: 20},
	})
	if err != nil {
		t.Fatalf("применение рекомендации: %v", err)
	}
	if len(feedback.calls) != 1 {
		t.Fatalf("контур поиска вызван %d раз, ожидался 1", len(feedback.calls))
	}
	call := feedback.calls[0]
	if call.ScopeKind != "industry" || call.ScopeKey != "saas" || call.Change.Kind != "new-search" || call.Change.Criteria != "saas-агентства" || call.Change.Limit != 20 {
		t.Fatalf("структурное изменение в контуре поиска = %+v", call)
	}
	if application.Reference != "target-42" {
		t.Fatalf("ссылка на изменение = %q", application.Reference)
	}
	if !application.AppliedAt.Equal(insightTime) {
		t.Fatalf("время применения = %v", application.AppliedAt)
	}
	if application.Before.Total != 3 {
		t.Fatalf("снимок «до» = %+v", application.Before)
	}
	if len(store.applications) != 1 {
		t.Fatalf("факт применения не зафиксирован: %+v", store.applications)
	}
}

// TestApplyRecommendationUnavailable проверяет честную недоступность контура
// поиска: применение не выдумывается.
func TestApplyRecommendationUnavailable(t *testing.T) {
	model := readmodel.NewInsightModel()
	handler := NewApplyRecommendationHandler(model, nil, nil, fixedClock{at: insightTime})
	if _, err := handler.Handle(context.Background(), ApplyRecommendation{TenantID: "t1"}); !errors.Is(err, ErrFeedbackUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrFeedbackUnavailable", err)
	}

	noModel := NewApplyRecommendationHandler(nil, &fakeFeedback{}, nil, fixedClock{at: insightTime})
	if _, err := noModel.Handle(context.Background(), ApplyRecommendation{TenantID: "t1"}); !errors.Is(err, ErrInsightUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrInsightUnavailable", err)
	}
}
