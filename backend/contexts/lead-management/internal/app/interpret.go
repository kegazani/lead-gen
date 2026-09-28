package app

import (
	"context"
	"strings"

	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/llm"
)

// InterpretQuery — use case интерпретации бизнес-запроса: превращает запрос
// оператора в критерии релевантности (целевой лид, признаки потребности,
// исключения, признаки гипотезы). Это отдельная от технического плана обхода
// сущность: seed-запросы, URL и лимиты отвечают на «где и сколько искать», а
// интерпретация — на «почему найденная компания решает задачу».
//
// Use case намеренно не строит цели и URL: их формирует технический план
// (RunSearch.EnqueuePlanned), а интерпретация приходит из llm.Port.Interpret.
type InterpretQuery struct {
	// LLM — порт LLM. nil — интерпретация недоступна: возвращается явная
	// неопределённость без выдуманных критериев.
	LLM llm.Port
	// Clock — часы use case (может быть nil). Вызов интерпретации не зависит от
	// времени; поле оставлено для единообразия use case прикладного слоя.
	Clock ports.Clock
}

// Interpret возвращает критерии релевантности запроса и потраченные на вызов
// токены. Пустой запрос отклоняется. Без LLM возвращается явная неопределённость
// (Uncertainty) и нулевой расход: пустая интерпретация не подменяется выдуманной.
func (u *InterpretQuery) Interpret(ctx context.Context, query string) (llm.Interpretation, int, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return llm.Interpretation{}, 0, errEmptyQuery
	}
	if u == nil || u.LLM == nil {
		return llm.Interpretation{Uncertainty: true}, 0, nil
	}
	interpretation, usage, err := u.LLM.Interpret(ctx, llm.InterpretRequest{Query: query})
	if err != nil {
		return llm.Interpretation{}, 0, err
	}
	return interpretation, usage.TotalTokens, nil
}

// EnqueueInterpreted ставит задание с критериями релевантности, выведенными из
// запроса интерпретацией. Технический план обхода строится общим хелпером
// buildSearchPlan: непустые planTargets берутся дословно, иначе — фолбэк на
// планирование по запросу с той же интерпретацией. Интерпретация сохраняется
// отдельным полем задания через ports.SearchJobStore.SetInterpretation — она не
// смешивается с планом.
//
// Ошибка интерпретации не отменяет запуск: критерии релевантности — не
// технический план, и их отсутствие не повод терять задание. Задание в этом
// случае сохраняется с явной неопределённостью (Uncertainty = true) и причиной
// сбоя (Reason), а не с выдуманными критериями. targetCompanies — целевое число
// компаний (0 — цель не задана), maxResults — лимит результатов поиска (0 —
// лимит адаптера).
func (r *RunSearch) EnqueueInterpreted(ctx context.Context, criteria string, planTargets []ports.PlanTarget, limits ports.PlanLimits, maxCredits, targetCompanies, maxResults int) (ports.SearchJob, error) {
	criteria = strings.TrimSpace(criteria)
	if criteria == "" {
		return ports.SearchJob{}, errEmptyQuery
	}
	if err := ports.ValidateMaxCredits(maxCredits); err != nil {
		return ports.SearchJob{}, err
	}
	if err := ports.ValidateTargetCompanies(targetCompanies); err != nil {
		return ports.SearchJob{}, err
	}

	interpretation, tokens, err := (&InterpretQuery{LLM: r.LLM, Clock: r.Clock}).Interpret(ctx, criteria)
	if err != nil {
		// Сбой вывода критериев виден оператору и в журнале: причина сохраняется в
		// самой интерпретации (Reason) и логируется с запросом.
		interpretation = llm.Interpretation{Uncertainty: true, Reason: err.Error()}
		tokens = 0
		if r.Logger != nil {
			r.Logger.Error("интерпретация запроса не удалась", err, observ.Fields{}, "query", criteria)
		}
	}
	if r.Logger != nil {
		r.Logger.Info("интерпретация запроса", observ.Fields{},
			"uncertainty", interpretation.Uncertainty, "tokens", tokens)
	}

	job, err := r.enqueueWithPlan(ctx, criteria, planTargets, limits, maxCredits, targetCompanies, maxResults, interpretation)
	if err != nil {
		return ports.SearchJob{}, err
	}
	if err := r.Jobs.SetInterpretation(ctx, job.ID, &interpretation); err != nil {
		return ports.SearchJob{}, err
	}
	job.Interpretation = &interpretation
	return job, nil
}
