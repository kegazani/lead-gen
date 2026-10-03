package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/llm"
)

// ErrReassessUnavailable — переоценка невозможна: не сконфигурированы заданий,
// оценок, карточек или LLM, либо задание без запроса.
var ErrReassessUnavailable = errors.New("app: переоценка недоступна")

// ReassessReport — итог переоценки компаний задания.
type ReassessReport struct {
	// Reassessed — число компаний, получивших новую оценку.
	Reassessed int
	// Fits, Hypothesis, Unsuitable — распределение новых вердиктов.
	Fits       int
	Hypothesis int
	Unsuitable int
	// Skipped — компании, которых не удалось переоценить (нет карточки или оценка
	// не прошла проверку): их прежняя оценка остаётся без изменений.
	Skipped int
}

// ReassessJob заново выводит критерии релевантности из запроса задания и
// переоценивает уже найденные компании, не выполняя повторный обход сайтов.
// Оценка опирается на сохранённые название и описание компании вместе с их
// источником, поэтому обращения к Firecrawl не требуется. Ключ пары (job,
// company) сохраняется: переоценка остаётся в контексте того же задания и не
// переносится в другое.
//
// Переоценка нужна, чтобы исправленные критерии применялись и к уже найденным
// компаниям, а не только к новым заданиям: интерпретация задания хранится в его
// payload и при повторном запуске не пересчитывается.
func (r *RunSearch) ReassessJob(ctx context.Context, jobID string) (ReassessReport, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return ReassessReport{}, fmt.Errorf("%w: пустой идентификатор задания", ErrReassessUnavailable)
	}
	if r.Jobs == nil || r.Assessments == nil || r.Profiles == nil || r.LLM == nil {
		return ReassessReport{}, fmt.Errorf("%w: не сконфигурированы Jobs, Assessments, Profiles или LLM", ErrReassessUnavailable)
	}

	job, err := r.Jobs.FindByID(ctx, jobID)
	if err != nil {
		return ReassessReport{}, fmt.Errorf("app: чтение задания %s: %w", jobID, err)
	}
	// Переоценка — операция над уже найденными компаниями: у выполняющегося
	// задания состав ещё меняется, поэтому переоценивать нечего.
	if !job.Status.Terminal() {
		return ReassessReport{}, fmt.Errorf("%w: задание %s ещё выполняется", ErrReassessUnavailable, jobID)
	}
	payload, err := ports.DecodeSearchJobPayload(job.Payload)
	if err != nil {
		return ReassessReport{}, fmt.Errorf("app: разбор payload задания %s: %w", jobID, err)
	}
	query := strings.TrimSpace(payload.Query)
	if query == "" {
		return ReassessReport{}, fmt.Errorf("%w: задание %s без запроса", ErrReassessUnavailable, jobID)
	}

	// Критерии выводятся заново из запроса актуальной интерпретацией: исправление
	// промпта должно применяться к уже найденным компаниям, а не только к новым
	// заданиям. Обновлённые критерии сохраняются отдельным полем задания.
	interpretation, _, err := (&InterpretQuery{LLM: r.LLM, Clock: r.Clock}).Interpret(ctx, query)
	if err != nil {
		return ReassessReport{}, fmt.Errorf("app: интерпретация запроса задания %s: %w", jobID, err)
	}
	if err := r.Jobs.SetInterpretation(ctx, jobID, &interpretation); err != nil {
		return ReassessReport{}, fmt.Errorf("app: запись интерпретации задания %s: %w", jobID, err)
	}

	assessments, err := r.Assessments.ListByJob(ctx, jobID)
	if err != nil {
		return ReassessReport{}, fmt.Errorf("app: чтение оценок задания %s: %w", jobID, err)
	}

	assess := &AssessCompany{LLM: r.LLM, Clock: r.Clock}
	at := r.now()
	var report ReassessReport
	for _, existing := range assessments {
		companyID := existing.CompanyID()

		company, err := r.Profiles.FindByID(ctx, domain.CompanyID(companyID))
		if err != nil {
			if !errors.Is(err, ports.ErrProfileNotFound) && r.Logger != nil {
				r.Logger.Error("переоценка: чтение карточки компании", err, observ.Fields{}, "job", jobID, "company", companyID)
			}
			report.Skipped++
			continue
		}

		assessment, _, err := assess.Assess(ctx, AssessInput{
			JobID:              jobID,
			CompanyID:          companyID,
			CompanyName:        company.Name().Value,
			CompanyDescription: company.Description().Value,
			Interpretation:     interpretation,
			Material:           reassessMaterial(company),
		})
		if err != nil {
			report.Skipped++
			if r.Logger != nil {
				r.Logger.Error("переоценка: оценка компании не удалась", err, observ.Fields{}, "job", jobID, "company", companyID)
			}
			continue
		}

		// Оценка остаётся в том же задании: существующая ревизуется событием
		// lead-assessment-revised, отсутствующая (не должна случиться) сохраняется.
		stored, getErr := r.Assessments.GetByJobAndCompany(ctx, jobID, companyID)
		switch {
		case getErr == nil:
			if err := stored.ChangeVerdict(assessment.Verdict(), assessment.SignalKind(), assessment.Evidence(), at); err != nil {
				report.Skipped++
				continue
			}
			if err := r.Assessments.Save(ctx, stored); err != nil {
				return report, fmt.Errorf("app: сохранение оценки %s/%s: %w", jobID, companyID, err)
			}
		case errors.Is(getErr, ports.ErrAssessmentNotFound):
			if err := r.Assessments.Save(ctx, assessment); err != nil {
				return report, fmt.Errorf("app: сохранение оценки %s/%s: %w", jobID, companyID, err)
			}
		default:
			return report, fmt.Errorf("app: чтение прежней оценки %s/%s: %w", jobID, companyID, getErr)
		}

		report.Reassessed++
		switch assessment.Verdict() {
		case domain.VerdictFits:
			report.Fits++
		case domain.VerdictHypothesis:
			report.Hypothesis++
		case domain.VerdictUnsuitable:
			report.Unsuitable++
		}
	}
	return report, nil
}

// reassessMaterial собирает материал переоценки из сохранённой карточки: текст
// (название и описание) и источник. Источник — собственный домен компании, а не
// отдельная страница, с которой извлечено описание: переоценка судит компанию, а
// не страницу. Иначе описание, взятое из блога или раздела новостей, выдавало бы
// реальную фирму за информационную статью. Повторный обход сайта не требуется;
// пустой текст даёт пустой материал.
func reassessMaterial(company *domain.Company) []llm.AssessMaterial {
	name := company.Name()
	description := company.Description()
	text := strings.TrimSpace(strings.TrimRight(name.Value, "\n") + "\n" + description.Value)
	if text == "" {
		return nil
	}
	return []llm.AssessMaterial{{URL: "https://" + string(company.ID()) + "/", Text: text}}
}
