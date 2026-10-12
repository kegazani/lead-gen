package httpapi_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	httpapi "lead-gen/backend/contexts/lead-management/internal/adapters/http"
	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/contracts/lead-management/leadrelevance"
	"lead-gen/contracts/lead-management/leadrelevance/httpport"
	"lead-gen/contracts/lead-management/recommendationapply"
	applyhttpport "lead-gen/contracts/lead-management/recommendationapply/httpport"
)

// newFeedbackServer поднимает сервер роли api с заданными сценариями обратной
// связи и чтения оценки.
func newFeedbackServer(t *testing.T, deps httpapi.Dependencies) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(httpapi.NewServer(deps))
	t.Cleanup(ts.Close)
	return ts
}

// TestRecommendationApplyHTTPRoundTrip проверяет сквозной путь контракта:
// сетевой клиент → POST /recommendation-apply → сценарий → id задания, читаемый
// клиентом; повтор идемпотентен.
func TestRecommendationApplyHTTPRoundTrip(t *testing.T) {
	jobs := memory.NewSearchJobStore()
	ts := newFeedbackServer(t, httpapi.Dependencies{ApplyRecommendation: &app.ApplyRecommendation{Jobs: jobs}})

	client, err := applyhttpport.New(ts.URL, applyhttpport.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	application := recommendationapply.Application{
		TenantID: "tenant-1", ChangeID: "chg-1",
		Change: recommendationapply.Change{Kind: recommendationapply.KindNewSearch, Criteria: "агентства"},
	}
	first, err := client.ApplyRecommendation(context.Background(), application)
	if err != nil {
		t.Fatalf("ApplyRecommendation: %v", err)
	}
	if first.JobID == "" {
		t.Fatal("пустой идентификатор задания")
	}
	second, err := client.ApplyRecommendation(context.Background(), application)
	if err != nil {
		t.Fatalf("повтор ApplyRecommendation: %v", err)
	}
	if first.JobID != second.JobID {
		t.Fatalf("идентификаторы различны: %q и %q", first.JobID, second.JobID)
	}
}

// TestRecommendationApplyHTTPRejectsInvalid проверяет перевод доменного отказа в
// 400 и ErrRejected на стороне клиента.
func TestRecommendationApplyHTTPRejectsInvalid(t *testing.T) {
	ts := newFeedbackServer(t, httpapi.Dependencies{ApplyRecommendation: &app.ApplyRecommendation{Jobs: memory.NewSearchJobStore()}})
	client, err := applyhttpport.New(ts.URL, applyhttpport.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	_, err = client.ApplyRecommendation(context.Background(), recommendationapply.Application{
		TenantID: "tenant-1", ChangeID: "chg-1",
		Change: recommendationapply.Change{Kind: recommendationapply.KindNewSearch},
	})
	if !errors.Is(err, recommendationapply.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestRecommendationApplyHTTPUnavailable проверяет честную недоступность
// незамонтированного контракта.
func TestRecommendationApplyHTTPUnavailable(t *testing.T) {
	ts := newFeedbackServer(t, httpapi.Dependencies{})
	client, err := applyhttpport.New(ts.URL, applyhttpport.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	_, err = client.ApplyRecommendation(context.Background(), recommendationapply.Application{
		TenantID: "tenant-1", ChangeID: "chg-1",
		Change: recommendationapply.Change{Kind: recommendationapply.KindNewSearch, Criteria: "x"},
	})
	if !errors.Is(err, recommendationapply.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestLeadRelevanceHTTPRoundTrip проверяет сквозной путь read-контракта: сетевой
// клиент → GET /lead-relevance → сценарий → вердикт со свидетельствами.
func TestLeadRelevanceHTTPRoundTrip(t *testing.T) {
	store := memory.NewAssessmentStore()
	seedHTTPAssessment(t, store, "job-a", "fit.example")
	ts := newFeedbackServer(t, httpapi.Dependencies{LeadRelevance: &app.ReadLeadRelevance{Assessments: store}})

	client, err := httpport.New(ts.URL, httpport.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	assessment, err := client.Read(context.Background(), leadrelevance.Request{JobID: "job-a", CompanyID: "fit.example"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if assessment.Verdict != leadrelevance.VerdictFits || assessment.SignalKind != leadrelevance.SignalDirect {
		t.Fatalf("оценка = %+v", assessment)
	}
	if len(assessment.Evidence) != 1 || assessment.Evidence[0].Fact == "" {
		t.Fatalf("свидетельства = %+v", assessment.Evidence)
	}
}

// TestLeadRelevanceHTTPNotFound проверяет перевод отсутствия оценки в 404 и
// ErrNotFound на стороне клиента.
func TestLeadRelevanceHTTPNotFound(t *testing.T) {
	ts := newFeedbackServer(t, httpapi.Dependencies{LeadRelevance: &app.ReadLeadRelevance{Assessments: memory.NewAssessmentStore()}})
	client, err := httpport.New(ts.URL, httpport.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	if _, err := client.Read(context.Background(), leadrelevance.Request{JobID: "job-a", CompanyID: "missing.example"}); !errors.Is(err, leadrelevance.ErrNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrNotFound", err)
	}
}

// TestLeadRelevanceHTTPUnavailable проверяет честную недоступность
// незамонтированного контракта.
func TestLeadRelevanceHTTPUnavailable(t *testing.T) {
	ts := newFeedbackServer(t, httpapi.Dependencies{})
	client, err := httpport.New(ts.URL, httpport.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	if _, err := client.Read(context.Background(), leadrelevance.Request{JobID: "job-a", CompanyID: "c1"}); !errors.Is(err, leadrelevance.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// seedHTTPAssessment кладёт оценку компании относительно задания в хранилище
// HTTP-теста.
func seedHTTPAssessment(t *testing.T, store *memory.AssessmentStore, jobID, companyID string) {
	t.Helper()
	assessment, err := domain.CreateLeadAssessment(domain.CreateLeadAssessmentParams{
		JobID:      jobID,
		CompanyID:  companyID,
		Verdict:    domain.VerdictFits,
		SignalKind: domain.SignalDirect,
		Evidence: []domain.Evidence{{
			Criterion: "потребность", Fact: "заявили потребность", SourceURL: "https://fit.example/blog",
			Reasoning: "прямое заявление", SignalKind: domain.SignalDirect,
		}},
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("создание оценки: %v", err)
	}
	if err := store.Save(context.Background(), assessment); err != nil {
		t.Fatalf("Save: %v", err)
	}
}
