package app

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/platform/jobs"
	"lead-gen/contracts/integration-platform/llm"
)

// seedReassessCompany сохраняет карточку компании с названием, описанием и
// источником — материал, на который опирается переоценка без обхода сайтов.
func seedReassessCompany(t *testing.T, profiles *memProfiles, id, name, description string) {
	t.Helper()
	provenance := domain.Provenance{SourceURL: "https://" + id, ExtractedAt: testTime}
	company, err := domain.CreateCompany(domain.CreateCompanyParams{
		Domain:      id,
		Name:        domain.AttributedValue{Value: name, Provenance: provenance},
		Description: domain.AttributedValue{Value: description, Provenance: provenance},
		CreatedAt:   testTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany(%s): %v", id, err)
	}
	if err := profiles.Save(context.Background(), company); err != nil {
		t.Fatalf("Save(%s): %v", id, err)
	}
}

// seedUnsuitableAssessment сохраняет прежнюю оценку «Не подходит» — состояние,
// которое переоценка обязана пересмотреть.
func seedUnsuitableAssessment(t *testing.T, store *memAssessments, jobID, companyID string) {
	t.Helper()
	assessment, err := domain.CreateLeadAssessment(domain.CreateLeadAssessmentParams{
		JobID:      jobID,
		CompanyID:  companyID,
		Verdict:    domain.VerdictUnsuitable,
		SignalKind: domain.SignalProfile,
		Evidence: []domain.Evidence{{
			Criterion:  "критерий",
			Fact:       "компания оказывает услуги",
			SourceURL:  "https://" + companyID,
			Reasoning:  "оказывает услуги, а не ищет подрядчика",
			SignalKind: domain.SignalProfile,
			Mismatch:   "оказывает аналогичные услуги",
		}},
		CreatedAt: testTime,
	})
	if err != nil {
		t.Fatalf("CreateLeadAssessment(%s): %v", companyID, err)
	}
	if err := store.Save(context.Background(), assessment); err != nil {
		t.Fatalf("Save(%s): %v", companyID, err)
	}
}

// TestReassessJobAppliesCorrectedCriteria проверяет основной сценарий: уже
// найденные компании переоцениваются по критериям, выведенным заново из запроса,
// без повторного обхода. Прежние «Не подходит» становятся «Подходит», а
// интерпретация задания обновляется.
func TestReassessJobAppliesCorrectedCriteria(t *testing.T) {
	ctx := context.Background()
	jobsStore := newMemJobs()
	job, err := jobsStore.CreateSearchJob(ctx, "проектирование и монтаж инженерных систем", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	if err := jobsStore.UpdateStatus(ctx, job.ID, jobs.StatusQueued, jobs.StatusDone, "", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	profiles := newMemProfiles()
	seedReassessCompany(t, profiles, "veervent.ru", "ВеерВент", "Проектирование и монтаж инженерных систем")
	seedReassessCompany(t, profiles, "ests.msk.ru", "ЭнергоСтройТехСервис", "Монтаж инженерных систем под ключ")

	assessments := newMemAssessments()
	seedUnsuitableAssessment(t, assessments, job.ID, "veervent.ru")
	seedUnsuitableAssessment(t, assessments, job.ID, "ests.msk.ru")

	logger, metrics, _ := testObservability()
	llmFake := &fakeLLM{
		interpretation: llm.Interpretation{
			TargetLead:  "компании, которые проектируют и монтируют инженерные системы",
			NeedSignals: []string{"на сайте заявлены услуги проектирования и монтажа инженерных систем"},
		},
		assessment: llm.Assessment{
			Verdict:    llm.VerdictFits,
			SignalKind: llm.SignalDirect,
			Evidence: []llm.AssessmentEvidence{{
				Criterion:  "услуги на сайте",
				Fact:       "сайт заявляет услуги проектирования и монтажа",
				SourceURL:  "https://veervent.ru",
				Reasoning:  "заявленная деятельность совпадает с профилем задания",
				SignalKind: llm.SignalDirect,
			}},
		},
	}
	r := &RunSearch{
		Jobs:        jobsStore,
		Profiles:    profiles,
		Assessments: assessments,
		LLM:         llmFake,
		Clock:       fakeClock{t: testTime},
		Logger:      logger,
		Metrics:     metrics,
		ServiceName: "lead-management",
	}

	report, err := r.ReassessJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("ReassessJob: %v", err)
	}
	if report.Reassessed != 2 || report.Fits != 2 || report.Skipped != 0 {
		t.Fatalf("отчёт = %+v, ожидалось 2 переоценённых/подходящих без пропусков", report)
	}

	for _, id := range []string{"veervent.ru", "ests.msk.ru"} {
		got, err := assessments.GetByJobAndCompany(ctx, job.ID, id)
		if err != nil {
			t.Fatalf("GetByJobAndCompany(%s): %v", id, err)
		}
		if got.Verdict() != domain.VerdictFits {
			t.Fatalf("%s: вердикт = %q, ожидался fits", id, got.Verdict())
		}
	}

	jobsList, err := jobsStore.ListSearchJobs(ctx, 0)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(jobsList) != 1 || jobsList[0].Interpretation == nil ||
		jobsList[0].Interpretation.TargetLead != "компании, которые проектируют и монтируют инженерные системы" {
		t.Fatalf("интерпретация задания не обновлена: %+v", jobsList)
	}
}

// TestReassessJobSkipsCompaniesWithoutCard проверяет, что компания без сохранённой
// карточки пропускается без ошибки: её прежняя оценка остаётся, а задание не падает.
func TestReassessJobSkipsCompaniesWithoutCard(t *testing.T) {
	ctx := context.Background()
	jobsStore := newMemJobs()
	job, err := jobsStore.CreateSearchJob(ctx, "запрос", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	if err := jobsStore.UpdateStatus(ctx, job.ID, jobs.StatusQueued, jobs.StatusDone, "", ""); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	profiles := newMemProfiles()
	assessments := newMemAssessments()
	seedUnsuitableAssessment(t, assessments, job.ID, "missing.example")

	r := &RunSearch{
		Jobs:        jobsStore,
		Profiles:    profiles,
		Assessments: assessments,
		LLM:         &fakeLLM{interpretation: llm.Interpretation{TargetLead: "профиль"}},
		Clock:       fakeClock{t: testTime},
	}

	report, err := r.ReassessJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("ReassessJob: %v", err)
	}
	if report.Reassessed != 0 || report.Skipped != 1 {
		t.Fatalf("отчёт = %+v, ожидался 1 пропуск", report)
	}
	got, err := assessments.GetByJobAndCompany(ctx, job.ID, "missing.example")
	if err != nil {
		t.Fatalf("GetByJobAndCompany: %v", err)
	}
	if got.Verdict() != domain.VerdictUnsuitable {
		t.Fatalf("оценка без карточки изменилась: %q", got.Verdict())
	}
}

// TestReassessJobUnavailableWithoutLLM проверяет, что без LLM переоценка честно
// отказывает, а не выдумывает вердикты.
func TestReassessJobUnavailableWithoutLLM(t *testing.T) {
	r := &RunSearch{Jobs: newMemJobs(), Profiles: newMemProfiles(), Assessments: newMemAssessments()}
	if _, err := r.ReassessJob(context.Background(), "job-1"); !errors.Is(err, ErrReassessUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrReassessUnavailable", err)
	}
}

// TestReassessJobRejectsRunningJob проверяет, что выполняющееся задание не
// переоценивается: его состав ещё меняется.
func TestReassessJobRejectsRunningJob(t *testing.T) {
	ctx := context.Background()
	jobsStore := newMemJobs()
	job, err := jobsStore.CreateSearchJob(ctx, "запрос", 0)
	if err != nil {
		t.Fatalf("CreateSearchJob: %v", err)
	}
	r := &RunSearch{
		Jobs:        jobsStore,
		Profiles:    newMemProfiles(),
		Assessments: newMemAssessments(),
		LLM:         &fakeLLM{interpretation: llm.Interpretation{TargetLead: "профиль"}},
	}
	if _, err := r.ReassessJob(ctx, job.ID); !errors.Is(err, ErrReassessUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrReassessUnavailable", err)
	}
}
