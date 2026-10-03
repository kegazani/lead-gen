package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/platform/jobs"
)

// TestReassessJobEndpoint проверяет маршрут переоценки: терминальное задание с
// найденными компаниями переоценивается по критериям, выведенным заново из
// запроса, а браузер возвращается на экран задания. Прежняя оценка «Не подходит»
// пересматривается (фейк LLM отдаёт нейтральную гипотезу).
func TestReassessJobEndpoint(t *testing.T) {
	env := newTestEnv()
	ctx := context.Background()
	job, err := env.jobs.CreateSearchJob(ctx, "проектирование и монтаж инженерных систем", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	if err := env.jobs.UpdateStatus(ctx, job.ID, jobs.StatusQueued, jobs.StatusDone, "", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	provenance := domain.Provenance{SourceURL: "https://veervent.ru", ExtractedAt: testTime}
	company, err := domain.CreateCompany(domain.CreateCompanyParams{
		Domain:      "veervent.ru",
		Name:        domain.AttributedValue{Value: "ВеерВент", Provenance: provenance},
		Description: domain.AttributedValue{Value: "Проектирование и монтаж инженерных систем", Provenance: provenance},
		CreatedAt:   testTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}
	if err := env.profiles.Save(ctx, company); err != nil {
		t.Fatalf("Save профиля: %v", err)
	}
	unsuitable, err := domain.CreateLeadAssessment(domain.CreateLeadAssessmentParams{
		JobID:      job.ID,
		CompanyID:  "veervent.ru",
		Verdict:    domain.VerdictUnsuitable,
		SignalKind: domain.SignalProfile,
		Evidence: []domain.Evidence{{
			Criterion:  "критерий",
			Fact:       "компания оказывает услуги",
			SourceURL:  "https://veervent.ru",
			Reasoning:  "оказывает услуги, а не ищет подрядчика",
			SignalKind: domain.SignalProfile,
			Mismatch:   "оказывает аналогичные услуги",
		}},
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateLeadAssessment: %v", err)
	}
	if err := env.assessments.Save(ctx, unsuitable); err != nil {
		t.Fatalf("Save оценки: %v", err)
	}

	ts := httptest.NewServer(env.server)
	defer ts.Close()
	client := newConsoleClient(ts)
	client.login(t, "operator", "secret")
	csrf := client.csrfForPage(t, "/jobs/"+job.ID)

	resp := client.postForm(t, "/jobs/"+job.ID+"/reassess", url.Values{"csrf": {csrf}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /jobs/%s/reassess = %d, ожидался 303", job.ID, resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/jobs/"+job.ID {
		t.Fatalf("Location = %q, ожидался /jobs/%s", loc, job.ID)
	}

	got, err := env.assessments.GetByJobAndCompany(ctx, job.ID, "veervent.ru")
	if err != nil {
		t.Fatalf("GetByJobAndCompany: %v", err)
	}
	if got.Verdict() == domain.VerdictUnsuitable {
		t.Fatalf("вердикт не изменился после переоценки: %q", got.Verdict())
	}
}
