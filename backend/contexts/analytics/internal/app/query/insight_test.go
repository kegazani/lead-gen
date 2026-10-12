package query

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

type testClock struct{ at time.Time }

func (c testClock) Now() time.Time { return c.at }

// toggleSource — источник, у которого можно включить ошибку недоступности.
type toggleSource struct {
	model  *readmodel.InsightModel
	failed bool
}

func (s *toggleSource) Insight(tenantID string, filter readmodel.InsightFilter) (readmodel.InsightData, error) {
	if s.failed {
		return readmodel.InsightData{}, ErrInsightSourceUnavailable
	}
	return s.model.Insight(tenantID, filter), nil
}

// queryStore — тестовый двойник ports.InsightStore (только в тестах).
type queryStore struct {
	snapshots    []readmodel.InsightSnapshot
	applications []readmodel.RecommendationApplication
}

func (s *queryStore) SaveSnapshot(_ context.Context, snapshot readmodel.InsightSnapshot) error {
	s.snapshots = append(s.snapshots, snapshot)
	return nil
}

func (s *queryStore) LoadSnapshots(_ context.Context) ([]readmodel.InsightSnapshot, error) {
	return s.snapshots, nil
}

func (s *queryStore) SaveApplication(_ context.Context, application readmodel.RecommendationApplication) error {
	s.applications = append(s.applications, application)
	return nil
}

func (s *queryStore) LoadApplications(_ context.Context) ([]readmodel.RecommendationApplication, error) {
	return s.applications, nil
}

var queryTime = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

// TestInsightViewUnavailableInsteadOfZero проверяет, что недоступность источника
// показывается явно и не подменяется нулём (задача 4.4).
func TestInsightViewUnavailableInsteadOfZero(t *testing.T) {
	handler := NewInsightHandler(nil, testClock{at: queryTime})
	view := handler.Handle(context.Background(), InsightQuery{TenantID: "t1"})
	if view.Available {
		t.Fatalf("недоступный источник показан доступным")
	}
	if !view.Stale || view.StaleReason == "" || view.Reason == "" {
		t.Fatalf("недоступность не сообщена явно: %+v", view)
	}
	// Метрики не отданы и не подменены нулём как текущими.
	if view.Data.Total != 0 || view.Data.Visible {
		t.Fatalf("недоступный источник отдал срез: %+v", view.Data)
	}
}

// TestInsightViewBuildsSlice проверяет сборку видимого среза.
func TestInsightViewBuildsSlice(t *testing.T) {
	model := readmodel.NewInsightModel()
	for i := 0; i < 3; i++ {
		model.Apply(readmodel.InsightOutcome{
			TenantID: "t1", JobID: "job-a", CompanyID: string(rune('a' + i)), Source: "crawl", Industry: "saas",
			Verdict: readmodel.VerdictHypothesis, Reason: readmodel.ReasonNoSignal, OccurredAt: queryTime,
		})
	}
	handler := NewInsightHandler(ModelInsightSource{Model: model}, testClock{at: queryTime})
	view := handler.Handle(context.Background(), InsightQuery{TenantID: "t1", Filters: InsightFilters{Industry: "saas"}})
	if !view.Available || !view.Visible {
		t.Fatalf("срез не показан: %+v", view)
	}
	if view.Data.Total != 3 || view.Data.Verdicts.Hypothesis != 3 {
		t.Fatalf("срез = %+v", view.Data)
	}
	if view.UpdatedAtLabel == "" {
		t.Fatalf("время обновления не показано")
	}
}

// TestInsightViewDegradesToCache проверяет деградацию к последнему срезу при
// недоступности источника.
func TestInsightViewDegradesToCache(t *testing.T) {
	model := readmodel.NewInsightModel()
	for i := 0; i < 2; i++ {
		model.Apply(readmodel.InsightOutcome{
			TenantID: "t1", JobID: "job-a", CompanyID: string(rune('a' + i)), Source: "crawl", Industry: "saas",
			Verdict: readmodel.VerdictFits, OccurredAt: queryTime,
		})
	}
	source := &toggleSource{model: model}
	handler := NewInsightHandler(source, testClock{at: queryTime})

	// Успешный срез кэшируется.
	if view := handler.Handle(context.Background(), InsightQuery{TenantID: "t1"}); !view.Available {
		t.Fatalf("первый срез недоступен")
	}
	// Источник падает: отдаётся последний срез с явным временем, а не нули.
	source.failed = true
	view := handler.Handle(context.Background(), InsightQuery{TenantID: "t1"})
	if !view.Stale || view.StaleReason == "" {
		t.Fatalf("деградация не сообщена: %+v", view)
	}
	if view.Data.Total != 2 {
		t.Fatalf("последний срез не отдан: %+v", view)
	}
}

// TestApplicationsHandlerComparesBeforeAfter проверяет сравнение «до/после» по
// применённым рекомендациям (задача 4.5).
func TestApplicationsHandlerComparesBeforeAfter(t *testing.T) {
	model := readmodel.NewInsightModel()
	model.Apply(readmodel.InsightOutcome{
		TenantID: "t1", JobID: "job-a", CompanyID: "a1", Source: "crawl", Industry: "saas",
		Verdict: readmodel.VerdictHypothesis, Reason: readmodel.ReasonNoSignal, OccurredAt: queryTime,
	})
	before := readmodel.Summarize(model.Insight("t1", readmodel.InsightFilter{Industry: "saas"}))

	store := &queryStore{applications: []readmodel.RecommendationApplication{{
		TenantID: "t1", ScopeKind: "industry", ScopeKey: "saas", Action: "добавить цели обхода",
		AppliedAt: queryTime, BeforeWatermark: model.Watermark("t1"), Before: before,
	}}}
	handler := NewApplicationsHandler(store, ModelInsightSource{Model: model})

	// После применения появился новый исход.
	model.Apply(readmodel.InsightOutcome{
		TenantID: "t1", JobID: "job-a", CompanyID: "a2", Source: "crawl", Industry: "saas",
		Verdict: readmodel.VerdictFits, OccurredAt: queryTime.Add(time.Hour),
	})
	view := handler.Handle(context.Background(), ApplicationQuery{TenantID: "t1"})
	if !view.Available || len(view.Comparisons) != 1 {
		t.Fatalf("сравнение недоступно: %+v", view)
	}
	comparison := view.Comparisons[0]
	if !comparison.Changed || comparison.NewOutcomes != 1 {
		t.Fatalf("сравнение до/после = %+v", comparison)
	}
}

// TestApplicationsHandlerUnavailable проверяет честную недоступность без
// хранилища применений.
func TestApplicationsHandlerUnavailable(t *testing.T) {
	handler := NewApplicationsHandler(nil, ModelInsightSource{Model: readmodel.NewInsightModel()})
	view := handler.Handle(context.Background(), ApplicationQuery{TenantID: "t1"})
	if view.Available {
		t.Fatalf("сравнение без хранилища показано доступным")
	}
	if view.Reason == "" {
		t.Fatalf("недоступность не сообщена")
	}
}
