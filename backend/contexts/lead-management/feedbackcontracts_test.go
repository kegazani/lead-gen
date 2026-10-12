package leadmanagement

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/adapters/persistence/memory"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/contracts/lead-management/leadrelevance"
	"lead-gen/contracts/lead-management/recommendationapply"
)

// feedbackTestTime — фиксированный момент для детерминированных тестов обратной
// связи и чтения оценки.
var feedbackTestTime = time.Date(2026, time.October, 6, 10, 0, 0, 0, time.UTC)

// TestRecommendationApplyCreatesSearchJob проверяет применение рекомендации:
// создаётся новое задание поиска с рекомендованными критериями, ссылка — его id.
func TestRecommendationApplyCreatesSearchJob(t *testing.T) {
	jobs := memory.NewSearchJobStore()
	port := NewRecommendationApply(RecommendationApplyDeps{
		Repositories: Repositories{Jobs: jobs},
		Clock:        fixedClock{now: feedbackTestTime},
	})

	receipt, err := port.ApplyRecommendation(context.Background(), recommendationapply.Application{
		TenantID: "tenant-1", ChangeID: "chg-1", ScopeKind: "industry", ScopeKey: "saas",
		Reason: "no_signal", Basis: "наблюдений 3",
		Change: recommendationapply.Change{Kind: recommendationapply.KindNewSearch, Criteria: "saas-агентства", Targets: []string{"разработка на заказ"}, Limit: 20},
	})
	if err != nil {
		t.Fatalf("ApplyRecommendation: %v", err)
	}
	if receipt.JobID == "" {
		t.Fatal("пустой идентификатор задания")
	}
	list, err := jobs.ListSearchJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSearchJobs: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("заданий %d, ожидалось 1", len(list))
	}
	if list[0].ID != receipt.JobID || list[0].Query != "saas-агентства" {
		t.Fatalf("задание = %+v", list[0])
	}
}

// TestRecommendationApplyIdempotent проверяет идемпотентность: повтор с тем же
// ключом возвращает то же задание, а не создаёт второе.
func TestRecommendationApplyIdempotent(t *testing.T) {
	jobs := memory.NewSearchJobStore()
	port := NewRecommendationApply(RecommendationApplyDeps{Repositories: Repositories{Jobs: jobs}})
	application := recommendationapply.Application{
		TenantID: "tenant-1", ChangeID: "chg-1",
		Change: recommendationapply.Change{Kind: recommendationapply.KindNewSearch, Criteria: "агентства"},
	}

	first, err := port.ApplyRecommendation(context.Background(), application)
	if err != nil {
		t.Fatalf("первое применение: %v", err)
	}
	second, err := port.ApplyRecommendation(context.Background(), application)
	if err != nil {
		t.Fatalf("повтор применения: %v", err)
	}
	if first.JobID != second.JobID {
		t.Fatalf("идентификаторы различны: %q и %q", first.JobID, second.JobID)
	}
	if list, _ := jobs.ListSearchJobs(context.Background(), 10); len(list) != 1 {
		t.Fatalf("заданий %d, ожидалось 1 (дубликат не создаётся)", len(list))
	}

	other := application
	other.ChangeID = "chg-2"
	third, err := port.ApplyRecommendation(context.Background(), other)
	if err != nil {
		t.Fatalf("применение другого ключа: %v", err)
	}
	if third.JobID == first.JobID {
		t.Fatal("разные ключи дали один идентификатор")
	}
	if list, _ := jobs.ListSearchJobs(context.Background(), 10); len(list) != 2 {
		t.Fatalf("заданий %d, ожидалось 2", len(list))
	}
}

// TestRecommendationApplyRejectsEmptyChange проверяет доменный отказ: изменение
// без критерия и целей не превращается в пустое задание.
func TestRecommendationApplyRejectsEmptyChange(t *testing.T) {
	port := NewRecommendationApply(RecommendationApplyDeps{Repositories: Repositories{Jobs: memory.NewSearchJobStore()}})
	_, err := port.ApplyRecommendation(context.Background(), recommendationapply.Application{
		TenantID: "tenant-1", ChangeID: "chg-1",
		Change: recommendationapply.Change{Kind: recommendationapply.KindNewSearch},
	})
	if !errors.Is(err, recommendationapply.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestRecommendationApplyUnavailable проверяет честную недоступность без
// хранилища заданий: применение не выдумывается.
func TestRecommendationApplyUnavailable(t *testing.T) {
	port := NewRecommendationApply(RecommendationApplyDeps{})
	_, err := port.ApplyRecommendation(context.Background(), recommendationapply.Application{
		TenantID: "tenant-1", ChangeID: "chg-1",
		Change: recommendationapply.Change{Kind: recommendationapply.KindNewSearch, Criteria: "x"},
	})
	if !errors.Is(err, recommendationapply.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// seedAssessment кладёт оценку компании относительно задания в хранилище.
func seedAssessment(t *testing.T, store *memory.AssessmentStore, jobID, companyID string) {
	t.Helper()
	assessment, err := domain.CreateLeadAssessment(domain.CreateLeadAssessmentParams{
		JobID:      jobID,
		CompanyID:  companyID,
		Verdict:    domain.VerdictFits,
		SignalKind: domain.SignalDirect,
		Evidence: []domain.Evidence{{
			Criterion:  "потребность в автосервисе",
			Fact:       "ищут подрядчика на аутсорс",
			SourceURL:  "https://fit.example/blog",
			Reasoning:  "прямое заявление о потребности",
			SignalKind: domain.SignalDirect,
		}},
		CreatedAt: feedbackTestTime,
	})
	if err != nil {
		t.Fatalf("создание оценки: %v", err)
	}
	if err := store.Save(context.Background(), assessment); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// TestLeadRelevanceReadsAssessment проверяет read-контракт: по паре (задание,
// компания) читаются вердикт, тип сигнала и свидетельства.
func TestLeadRelevanceReadsAssessment(t *testing.T) {
	store := memory.NewAssessmentStore()
	seedAssessment(t, store, "job-a", "fit.example")

	port := NewLeadRelevance(LeadRelevanceDeps{Repositories: Repositories{Assessments: store}})
	assessment, err := port.Read(context.Background(), leadrelevance.Request{JobID: "job-a", CompanyID: "fit.example"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if assessment.Contract != leadrelevance.ContractName || assessment.Verdict != leadrelevance.VerdictFits {
		t.Fatalf("оценка = %+v", assessment)
	}
	if assessment.SignalKind != leadrelevance.SignalDirect || len(assessment.Evidence) != 1 {
		t.Fatalf("свидетельства = %+v", assessment.Evidence)
	}
	if assessment.Evidence[0].SourceURL != "https://fit.example/blog" {
		t.Fatalf("свидетельство = %+v", assessment.Evidence[0])
	}
}

// TestLeadRelevanceNotFound проверяет отдельный исход «оценки нет»: вердикт не
// выдумывается, недоступностью это не подменяется.
func TestLeadRelevanceNotFound(t *testing.T) {
	port := NewLeadRelevance(LeadRelevanceDeps{Repositories: Repositories{Assessments: memory.NewAssessmentStore()}})
	_, err := port.Read(context.Background(), leadrelevance.Request{JobID: "job-a", CompanyID: "missing.example"})
	if !errors.Is(err, leadrelevance.ErrNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrNotFound", err)
	}
}

// TestLeadRelevanceRejectsInvalidRequest проверяет доменный отказ на запрос без
// задания или компании.
func TestLeadRelevanceRejectsInvalidRequest(t *testing.T) {
	port := NewLeadRelevance(LeadRelevanceDeps{Repositories: Repositories{Assessments: memory.NewAssessmentStore()}})
	if _, err := port.Read(context.Background(), leadrelevance.Request{CompanyID: "c1"}); !errors.Is(err, leadrelevance.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestLeadRelevanceUnavailable проверяет честную недоступность без хранилища
// оценок.
func TestLeadRelevanceUnavailable(t *testing.T) {
	port := NewLeadRelevance(LeadRelevanceDeps{})
	_, err := port.Read(context.Background(), leadrelevance.Request{JobID: "job-a", CompanyID: "c1"})
	if !errors.Is(err, leadrelevance.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
