package app

import (
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

// Валидация ChangeVerdict не должна выдавать assessment_revision_failed при
// переходе между состояниями, которые домен разрешает создавать и сохранять.
func TestChangeVerdictAcceptsEveryValidReassessment(t *testing.T) {
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	valid := []struct {
		verdict  domain.AssessmentVerdict
		signal   domain.SignalKind
		evidence []domain.Evidence
	}{
		{domain.VerdictFits, domain.SignalDirect, []domain.Evidence{{Criterion: "need", Fact: "fact", SourceURL: "https://example.test", Reasoning: "reason", SignalKind: domain.SignalDirect}}},
		{domain.VerdictFits, domain.SignalIndirect, []domain.Evidence{{Criterion: "need", Fact: "fact", SourceURL: "https://example.test", Reasoning: "reason", SignalKind: domain.SignalIndirect}}},
		{domain.VerdictHypothesis, domain.SignalProfile, []domain.Evidence{{Missing: "need signal"}}},
		{domain.VerdictUnsuitable, domain.SignalProfile, []domain.Evidence{{Mismatch: "outside scope"}}},
	}
	for _, initial := range valid {
		t.Run(initial.verdict.String()+"/"+initial.signal.String(), func(t *testing.T) {
			a, err := domain.CreateLeadAssessment(domain.CreateLeadAssessmentParams{
				JobID: "job-1", CompanyID: "company-1", Verdict: initial.verdict,
				SignalKind: initial.signal, Evidence: initial.evidence, CreatedAt: at,
			})
			if err != nil {
				t.Fatalf("создать валидную оценку: %v", err)
			}
			for _, next := range valid {
				if err := a.ChangeVerdict(next.verdict, next.signal, next.evidence, at.Add(time.Minute)); err != nil {
					t.Errorf("переоценка %s/%s: %v", next.verdict, next.signal, err)
					continue
				}
				snapshot := a.Snapshot()
				if _, err := domain.RehydrateLeadAssessment(snapshot); err != nil {
					t.Errorf("сохранённый снимок после %s/%s невалиден: %v", next.verdict, next.signal, err)
				}
				if snapshot.JobID != "job-1" || snapshot.CompanyID != "company-1" {
					t.Errorf("переоценка изменила ключ пары: (%q,%q)", snapshot.JobID, snapshot.CompanyID)
				}
			}
		})
	}
}
