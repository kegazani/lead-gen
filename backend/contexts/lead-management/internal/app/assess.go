package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/contracts/integration-platform/llm"
)

// errAssessUnavailable — порт LLM не сконфигурирован: оценивать компании нечем.
// Вердикт в этом случае не выдумывается.
var errAssessUnavailable = errors.New("app: оценка недоступна: LLM не сконфигурирован")

// errEmptyAssessJobID — оценка без идентификатора задания недопустима.
var errEmptyAssessJobID = errors.New("app: идентификатор задания оценки обязателен")

// errEmptyAssessCompanyID — оценка без идентификатора компании недопустима.
var errEmptyAssessCompanyID = errors.New("app: идентификатор компании оценки обязателен")

// AssessCompany — use case оценки компании относительно задания поиска поверх
// llm.Port.Assess. Резолвит вердикт, тип сигнала и свидетельства в доменный
// агрегат LeadAssessment, привязанный к паре (job, company). Оценка существует
// только в контексте задания и не переносится между заданиями.
type AssessCompany struct {
	// LLM — порт оценки (nil — оценка недоступна).
	LLM llm.Port
	// Clock — часы фиксации момента оценки (nil — системное время).
	Clock ports.Clock
}

// AssessInput — вход оценки компании.
type AssessInput struct {
	// JobID — идентификатор задания поиска. Обязателен.
	JobID string
	// CompanyID — идентификатор оцениваемой компании. Обязателен.
	CompanyID string
	// CompanyName — название компании. Может быть пусто.
	CompanyName string
	// CompanyDescription — описание компании. Может быть пусто.
	CompanyDescription string
	// Interpretation — критерии релевантности задания.
	Interpretation llm.Interpretation
	// Material — собранный материал о компании.
	Material []llm.AssessMaterial
}

// Assess оценивает компанию относительно задания и возвращает доменную оценку
// вместе с потраченными токенами. Ошибка порта, нарушение доменных инвариантов
// ответа модели и пустые ключевые идентификаторы пробрасываются как есть: вердикт
// не подменяется, наблюдаемый факт не склеивается с интерпретацией.
func (u *AssessCompany) Assess(ctx context.Context, in AssessInput) (*domain.LeadAssessment, int, error) {
	if u.LLM == nil {
		return nil, 0, errAssessUnavailable
	}
	jobID := strings.TrimSpace(in.JobID)
	if jobID == "" {
		return nil, 0, errEmptyAssessJobID
	}
	companyID := strings.TrimSpace(in.CompanyID)
	if companyID == "" {
		return nil, 0, errEmptyAssessCompanyID
	}

	resp, usage, err := u.LLM.Assess(ctx, llm.AssessRequest{
		JobID:              jobID,
		CompanyName:        in.CompanyName,
		CompanyDescription: in.CompanyDescription,
		Interpretation:     in.Interpretation,
		Material:           in.Material,
	})
	if err != nil {
		return nil, usage.TotalTokens, err
	}

	assessment, err := domain.CreateLeadAssessment(domain.CreateLeadAssessmentParams{
		JobID:      jobID,
		CompanyID:  companyID,
		Verdict:    mapVerdict(resp.Verdict),
		SignalKind: mapSignalKind(resp.SignalKind),
		Evidence:   mapEvidence(resp.Evidence),
		CreatedAt:  u.now(),
	})
	if err != nil {
		return nil, usage.TotalTokens, err
	}
	return assessment, usage.TotalTokens, nil
}

// now возвращает текущий момент часов либо системное время, если часы не заданы.
func (u *AssessCompany) now() time.Time {
	if u.Clock != nil {
		return u.Clock.Now()
	}
	return time.Now().UTC()
}

// mapVerdict переводит вердикт контракта в доменный вердикт. Неизвестное
// значение передаётся как есть — его отвергнет инвариант домена, вердикт не
// подменяется.
func mapVerdict(verdict string) domain.AssessmentVerdict {
	switch verdict {
	case llm.VerdictFits:
		return domain.VerdictFits
	case llm.VerdictHypothesis:
		return domain.VerdictHypothesis
	case llm.VerdictUnsuitable:
		return domain.VerdictUnsuitable
	default:
		return domain.AssessmentVerdict(verdict)
	}
}

// mapSignalKind переводит тип сигнала контракта в доменный тип. Неизвестное
// значение передаётся как есть и отвергается инвариантом домена.
func mapSignalKind(kind string) domain.SignalKind {
	switch kind {
	case llm.SignalDirect:
		return domain.SignalDirect
	case llm.SignalIndirect:
		return domain.SignalIndirect
	case llm.SignalProfile:
		return domain.SignalProfile
	default:
		return domain.SignalKind(kind)
	}
}

// mapEvidence переводит свидетельства контракта в доменные value object,
// сохраняя раздельность наблюдаемого факта, интерпретации и обоснования.
func mapEvidence(evidence []llm.AssessmentEvidence) []domain.Evidence {
	if len(evidence) == 0 {
		return nil
	}
	mapped := make([]domain.Evidence, 0, len(evidence))
	for _, e := range evidence {
		mapped = append(mapped, domain.Evidence{
			Criterion:  e.Criterion,
			Fact:       e.Fact,
			SourceURL:  e.SourceURL,
			Reasoning:  e.Reasoning,
			SignalKind: mapSignalKind(e.SignalKind),
			Missing:    e.Missing,
			Mismatch:   e.Mismatch,
		})
	}
	return mapped
}
