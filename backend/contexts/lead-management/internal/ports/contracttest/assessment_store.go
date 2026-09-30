package contracttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// AssessmentStoreFactory создаёт хранилище оценок для одного прогона suite. Как и
// прочие фабрики, обязана вернуть пустое состояние и сама отвечает за изоляцию
// прогонов.
type AssessmentStoreFactory func(t *testing.T) ports.LeadAssessmentStore

// RunAssessmentStoreContract проверяет, что реализация LeadAssessmentStore
// соблюдает контракт порта: сохранение и чтение round-trip, различение
// отсутствия, upsert по паре job+company, выборку оценок только своего задания,
// распределение по вердиктам с изоляцией по jobID и независимость оценок одной
// компании в разных заданиях.
func RunAssessmentStoreContract(t *testing.T, newStore AssessmentStoreFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("сохранение и чтение", func(t *testing.T) {
		store := newStore(t)
		assessment := mustAssessment(t, "job-1", "alpha.example.com", domain.VerdictFits, domain.SignalDirect, baseTime)
		if err := store.Save(ctx, assessment); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := store.GetByJobAndCompany(ctx, "job-1", "alpha.example.com")
		if err != nil {
			t.Fatalf("GetByJobAndCompany: %v", err)
		}
		assertAssessment(t, loaded, assessment)
	})

	t.Run("отсутствующая оценка", func(t *testing.T) {
		store := newStore(t)
		loaded, err := store.GetByJobAndCompany(ctx, "job-1", "missing.example.com")
		if err == nil {
			t.Fatal("GetByJobAndCompany отсутствующей оценки вернул nil-ошибку")
		}
		if !errors.Is(err, ports.ErrAssessmentNotFound) {
			t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, ErrAssessmentNotFound)", err)
		}
		if loaded != nil {
			t.Fatalf("для отсутствующей оценки ожидался nil, получено %v", loaded)
		}
	})

	t.Run("upsert по паре job+company", func(t *testing.T) {
		store := newStore(t)
		initial := mustAssessment(t, "job-1", "alpha.example.com", domain.VerdictFits, domain.SignalDirect, baseTime)
		if err := store.Save(ctx, initial); err != nil {
			t.Fatalf("Save initial: %v", err)
		}
		revised := mustAssessment(t, "job-1", "alpha.example.com", domain.VerdictUnsuitable, domain.SignalProfile, at(1))
		if err := store.Save(ctx, revised); err != nil {
			t.Fatalf("Save revised: %v", err)
		}

		loaded, err := store.GetByJobAndCompany(ctx, "job-1", "alpha.example.com")
		if err != nil {
			t.Fatalf("GetByJobAndCompany: %v", err)
		}
		assertAssessment(t, loaded, revised)

		list, err := store.ListByJob(ctx, "job-1")
		if err != nil {
			t.Fatalf("ListByJob: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("ListByJob вернул %d оценок, ожидалась 1 (upsert по паре)", len(list))
		}
	})

	t.Run("ListByJob возвращает только своё задание", func(t *testing.T) {
		store := newStore(t)
		for _, companyID := range []string{"charlie.example.com", "alpha.example.com", "bravo.example.com"} {
			assessment := mustAssessment(t, "job-1", companyID, domain.VerdictFits, domain.SignalDirect, baseTime)
			if err := store.Save(ctx, assessment); err != nil {
				t.Fatalf("Save %s: %v", companyID, err)
			}
		}
		foreign := mustAssessment(t, "job-2", "delta.example.com", domain.VerdictFits, domain.SignalDirect, baseTime)
		if err := store.Save(ctx, foreign); err != nil {
			t.Fatalf("Save job-2: %v", err)
		}

		list, err := store.ListByJob(ctx, "job-1")
		if err != nil {
			t.Fatalf("ListByJob: %v", err)
		}
		want := []string{"alpha.example.com", "bravo.example.com", "charlie.example.com"}
		if len(list) != len(want) {
			t.Fatalf("ListByJob вернул %d оценок, ожидалось %d", len(list), len(want))
		}
		for i, assessment := range list {
			if assessment.JobID() != "job-1" {
				t.Fatalf("ListByJob[%d] принадлежит заданию %q, ожидалось job-1", i, assessment.JobID())
			}
			if assessment.CompanyID() != want[i] {
				t.Fatalf("ListByJob[%d] companyID = %q, ожидалось %q (нестабильный порядок)", i, assessment.CompanyID(), want[i])
			}
		}

		empty, err := store.ListByJob(ctx, "job-unknown")
		if err != nil {
			t.Fatalf("ListByJob неизвестного задания: %v", err)
		}
		if len(empty) != 0 {
			t.Fatalf("ListByJob неизвестного задания вернул %d оценок, ожидалось 0", len(empty))
		}
	})

	t.Run("VerdictDistribution считает вердикты и изолирована по jobID", func(t *testing.T) {
		store := newStore(t)
		entries := []struct {
			jobID     string
			companyID string
			verdict   domain.AssessmentVerdict
			signal    domain.SignalKind
		}{
			{"job-1", "a.example.com", domain.VerdictFits, domain.SignalDirect},
			{"job-1", "b.example.com", domain.VerdictFits, domain.SignalIndirect},
			{"job-1", "c.example.com", domain.VerdictHypothesis, domain.SignalProfile},
			{"job-1", "d.example.com", domain.VerdictUnsuitable, domain.SignalProfile},
			{"job-2", "e.example.com", domain.VerdictFits, domain.SignalDirect},
			{"job-2", "f.example.com", domain.VerdictFits, domain.SignalDirect},
			{"job-2", "g.example.com", domain.VerdictFits, domain.SignalDirect},
		}
		for _, entry := range entries {
			assessment := mustAssessment(t, entry.jobID, entry.companyID, entry.verdict, entry.signal, baseTime)
			if err := store.Save(ctx, assessment); err != nil {
				t.Fatalf("Save %s/%s: %v", entry.jobID, entry.companyID, err)
			}
		}

		counts, err := store.VerdictDistribution(ctx, "job-1")
		if err != nil {
			t.Fatalf("VerdictDistribution job-1: %v", err)
		}
		assertVerdictCounts(t, counts, ports.VerdictCounts{Fits: 2, Hypothesis: 1, Unsuitable: 1})

		isolated, err := store.VerdictDistribution(ctx, "job-2")
		if err != nil {
			t.Fatalf("VerdictDistribution job-2: %v", err)
		}
		assertVerdictCounts(t, isolated, ports.VerdictCounts{Fits: 3, Hypothesis: 0, Unsuitable: 0})
	})

	t.Run("оценки одной компании в разных заданиях независимы", func(t *testing.T) {
		store := newStore(t)
		first := mustAssessment(t, "job-1", "alpha.example.com", domain.VerdictFits, domain.SignalDirect, baseTime)
		second := mustAssessment(t, "job-2", "alpha.example.com", domain.VerdictUnsuitable, domain.SignalProfile, at(1))
		if err := store.Save(ctx, first); err != nil {
			t.Fatalf("Save job-1: %v", err)
		}
		if err := store.Save(ctx, second); err != nil {
			t.Fatalf("Save job-2: %v", err)
		}

		loadedFirst, err := store.GetByJobAndCompany(ctx, "job-1", "alpha.example.com")
		if err != nil {
			t.Fatalf("GetByJobAndCompany job-1: %v", err)
		}
		assertAssessment(t, loadedFirst, first)

		loadedSecond, err := store.GetByJobAndCompany(ctx, "job-2", "alpha.example.com")
		if err != nil {
			t.Fatalf("GetByJobAndCompany job-2: %v", err)
		}
		assertAssessment(t, loadedSecond, second)
	})

	t.Run("LatestByCompany выбирает последнюю оценку по компании", func(t *testing.T) {
		store := newStore(t)
		first := mustAssessment(t, "job-1", "alpha.example.com", domain.VerdictFits, domain.SignalDirect, baseTime)
		latest := mustAssessment(t, "job-2", "alpha.example.com", domain.VerdictUnsuitable, domain.SignalProfile, at(1))
		other := mustAssessment(t, "job-1", "bravo.example.com", domain.VerdictHypothesis, domain.SignalProfile, baseTime)
		for _, assessment := range []*domain.LeadAssessment{first, latest, other} {
			if err := store.Save(ctx, assessment); err != nil {
				t.Fatalf("Save %s/%s: %v", assessment.JobID(), assessment.CompanyID(), err)
			}
		}

		byCompany, err := store.LatestByCompany(ctx, []string{"alpha.example.com", "bravo.example.com", "missing.example.com"})
		if err != nil {
			t.Fatalf("LatestByCompany: %v", err)
		}
		if len(byCompany) != 2 {
			t.Fatalf("LatestByCompany вернул %d компаний, ожидалось 2", len(byCompany))
		}
		if got := byCompany["alpha.example.com"]; got == nil {
			t.Fatal("alpha.example.com отсутствует в результате")
		} else {
			assertAssessment(t, got, latest)
		}
		if got := byCompany["bravo.example.com"]; got == nil {
			t.Fatal("bravo.example.com отсутствует в результате")
		} else {
			assertAssessment(t, got, other)
		}
		if _, ok := byCompany["missing.example.com"]; ok {
			t.Fatal("компания без оценок не должна попадать в результат")
		}
	})
}

// assertVerdictCounts сверяет распределение оценок с ожидаемым.
func assertVerdictCounts(t *testing.T, got, want ports.VerdictCounts) {
	t.Helper()
	if got.Fits != want.Fits {
		t.Fatalf("Fits: получено %d, ожидалось %d", got.Fits, want.Fits)
	}
	if got.Hypothesis != want.Hypothesis {
		t.Fatalf("Hypothesis: получено %d, ожидалось %d", got.Hypothesis, want.Hypothesis)
	}
	if got.Unsuitable != want.Unsuitable {
		t.Fatalf("Unsuitable: получено %d, ожидалось %d", got.Unsuitable, want.Unsuitable)
	}
}

// mustAssessment создаёт оценку с детерминированными полями и одним
// свидетельством либо завершает тест. Тип сигнала оценки и свидетельства
// совпадает с переданным; для гипотезы заполняется недостающее свидетельство, для
// «Не подходит» — установленное несоответствие, как того требуют инварианты
// агрегата.
func mustAssessment(t *testing.T, jobID, companyID string, verdict domain.AssessmentVerdict, signalKind domain.SignalKind, createdAt time.Time) *domain.LeadAssessment {
	t.Helper()
	evidence := domain.Evidence{
		Criterion:  "нужна автоматизация отчётности",
		Fact:       "компания публикует вакансию аналитика данных",
		SourceURL:  "https://" + companyID + "/careers",
		Reasoning:  "вакансия указывает на ручной сбор отчётности",
		SignalKind: signalKind,
	}
	switch verdict {
	case domain.VerdictHypothesis:
		evidence.Missing = "нет подтверждения использования BI-платформы"
	case domain.VerdictUnsuitable:
		evidence.Mismatch = "компания входит в исключённую отрасль"
	}
	assessment, err := domain.CreateLeadAssessment(domain.CreateLeadAssessmentParams{
		JobID:      jobID,
		CompanyID:  companyID,
		Verdict:    verdict,
		SignalKind: signalKind,
		Evidence:   []domain.Evidence{evidence},
		CreatedAt:  createdAt,
	})
	if err != nil {
		t.Fatalf("создание оценки %s/%s: %v", jobID, companyID, err)
	}
	return assessment
}

// assertAssessment сравнивает загруженную оценку с ожидаемой по наблюдаемому
// состоянию.
func assertAssessment(t *testing.T, got, want *domain.LeadAssessment) {
	t.Helper()
	assertAssessmentSnapshot(t, got.Snapshot(), want.Snapshot())
}

// assertAssessmentSnapshot сверяет снимки оценок поэлементно; время сравнивается
// по моменту (Equal), а не по структуре, поэтому сравнение не зависит от
// JSON-сериализации таймстампов в адаптере PostgreSQL.
func assertAssessmentSnapshot(t *testing.T, got, want domain.LeadAssessmentSnapshot) {
	t.Helper()
	if got.JobID != want.JobID {
		t.Fatalf("JobID: получено %q, ожидалось %q", got.JobID, want.JobID)
	}
	if got.CompanyID != want.CompanyID {
		t.Fatalf("CompanyID: получено %q, ожидалось %q", got.CompanyID, want.CompanyID)
	}
	if got.Verdict != want.Verdict {
		t.Fatalf("Verdict: получено %q, ожидалось %q", got.Verdict, want.Verdict)
	}
	if got.SignalKind != want.SignalKind {
		t.Fatalf("SignalKind: получено %q, ожидалось %q", got.SignalKind, want.SignalKind)
	}
	if len(got.Evidence) != len(want.Evidence) {
		t.Fatalf("число свидетельств: получено %d, ожидалось %d", len(got.Evidence), len(want.Evidence))
	}
	for i := range want.Evidence {
		gotEvidence, wantEvidence := got.Evidence[i], want.Evidence[i]
		if gotEvidence.Criterion != wantEvidence.Criterion {
			t.Fatalf("Evidence[%d].Criterion: получено %q, ожидалось %q", i, gotEvidence.Criterion, wantEvidence.Criterion)
		}
		if gotEvidence.Fact != wantEvidence.Fact {
			t.Fatalf("Evidence[%d].Fact: получено %q, ожидалось %q", i, gotEvidence.Fact, wantEvidence.Fact)
		}
		if gotEvidence.SourceURL != wantEvidence.SourceURL {
			t.Fatalf("Evidence[%d].SourceURL: получено %q, ожидалось %q", i, gotEvidence.SourceURL, wantEvidence.SourceURL)
		}
		if gotEvidence.Reasoning != wantEvidence.Reasoning {
			t.Fatalf("Evidence[%d].Reasoning: получено %q, ожидалось %q", i, gotEvidence.Reasoning, wantEvidence.Reasoning)
		}
		if gotEvidence.SignalKind != wantEvidence.SignalKind {
			t.Fatalf("Evidence[%d].SignalKind: получено %q, ожидалось %q", i, gotEvidence.SignalKind, wantEvidence.SignalKind)
		}
		if gotEvidence.Missing != wantEvidence.Missing {
			t.Fatalf("Evidence[%d].Missing: получено %q, ожидалось %q", i, gotEvidence.Missing, wantEvidence.Missing)
		}
		if gotEvidence.Mismatch != wantEvidence.Mismatch {
			t.Fatalf("Evidence[%d].Mismatch: получено %q, ожидалось %q", i, gotEvidence.Mismatch, wantEvidence.Mismatch)
		}
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("CreatedAt: получено %s, ожидалось %s", got.CreatedAt, want.CreatedAt)
	}
	if !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("UpdatedAt: получено %s, ожидалось %s", got.UpdatedAt, want.UpdatedAt)
	}
}
