package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// ErrInvalidLeadRelevanceRequest — запрос оценки некорректен: не задано задание
// или компания. Доменный отказ, а не сбой.
var ErrInvalidLeadRelevanceRequest = errors.New("app: некорректный запрос оценки")

// ReadLeadRelevanceInput — запрос оценки по паре (задание, компания).
type ReadLeadRelevanceInput struct {
	JobID     string
	CompanyID string
}

// LeadRelevanceEvidence — свидетельство оценки в форме чтения.
type LeadRelevanceEvidence struct {
	Criterion  string
	Fact       string
	SourceURL  string
	Reasoning  string
	SignalKind string
	Missing    string
	Mismatch   string
}

// LeadRelevanceResult — прочитанная оценка компании относительно задания.
type LeadRelevanceResult struct {
	JobID      string
	CompanyID  string
	Verdict    string
	SignalKind string
	Evidence   []LeadRelevanceEvidence
	AssessedAt time.Time
}

// ReadLeadRelevance — сценарий чтения оценки компании относительно задания из
// существующего агрегата `lead-assessment`. Новых агрегатов не заводит: только
// чтение. Оценка не переносится между заданиями: ключ — пара (задание, компания).
type ReadLeadRelevance struct {
	// Assessments — хранилище оценок.
	Assessments ports.LeadAssessmentStore
}

// Run читает оценку. Отсутствие оценки — ports.ErrAssessmentNotFound (отдельный
// исход, а не выдуманный вердикт); некорректный запрос — ErrInvalidLeadRelevanceRequest.
func (u *ReadLeadRelevance) Run(ctx context.Context, in ReadLeadRelevanceInput) (LeadRelevanceResult, error) {
	jobID := strings.TrimSpace(in.JobID)
	companyID := strings.TrimSpace(in.CompanyID)
	if jobID == "" || companyID == "" {
		return LeadRelevanceResult{}, fmt.Errorf("%w: обязательны job_id и company_id", ErrInvalidLeadRelevanceRequest)
	}
	if u == nil || u.Assessments == nil {
		return LeadRelevanceResult{}, fmt.Errorf("app: чтение оценки: хранилище оценок не подключено")
	}
	assessment, err := u.Assessments.GetByJobAndCompany(ctx, jobID, companyID)
	if err != nil {
		return LeadRelevanceResult{}, fmt.Errorf("app: чтение оценки: %w", err)
	}
	if assessment == nil {
		return LeadRelevanceResult{}, ports.ErrAssessmentNotFound
	}
	evidence := make([]LeadRelevanceEvidence, 0, len(assessment.Evidence()))
	for _, e := range assessment.Evidence() {
		evidence = append(evidence, LeadRelevanceEvidence{
			Criterion:  e.Criterion,
			Fact:       e.Fact,
			SourceURL:  e.SourceURL,
			Reasoning:  e.Reasoning,
			SignalKind: e.SignalKind.String(),
			Missing:    e.Missing,
			Mismatch:   e.Mismatch,
		})
	}
	return LeadRelevanceResult{
		JobID:      assessment.JobID(),
		CompanyID:  assessment.CompanyID(),
		Verdict:    assessment.Verdict().String(),
		SignalKind: assessment.SignalKind().String(),
		Evidence:   evidence,
		AssessedAt: assessment.CreatedAt(),
	}, nil
}
