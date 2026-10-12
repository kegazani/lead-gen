package httpapi

import (
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/frontend/components"
)

// viewTestTime — фиксированный момент для тестов модели представления.
var viewTestTime = time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)

// fakeAccountReader — фиксированный читатель остатка аккаунта для тестов.
type fakeAccountReader struct {
	credits budget.Balance
	tokens  budget.Balance
}

func (f fakeAccountReader) CreditBalance() budget.Balance { return f.credits }
func (f fakeAccountReader) TokenBalance() budget.Balance  { return f.tokens }

func TestFirecrawlStatusFrom(t *testing.T) {
	if s := firecrawlStatusFrom(nil); s.Configured {
		t.Fatal("nil-reader должен давать Configured=false (блок не отображается)")
	}

	at := time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)
	r := fakeAccountReader{
		credits: budget.Balance{Available: true, Value: 1234, UpdatedAt: at},
		tokens:  budget.Balance{Available: true, Value: 42},
	}
	s := firecrawlStatusFrom(r)
	if !s.Configured || !s.CreditsAvailable || s.Credits != "1234" {
		t.Fatalf("статус кредитов: %+v", s)
	}
	// Время обновления локализовано общим слоем (web-frontend-architecture, 7.1).
	if s.CreditsUpdated != "02.01.2026 10:00" {
		t.Fatalf("время обновления = %q", s.CreditsUpdated)
	}
	if !s.TokensAvailable || s.Tokens != "42" {
		t.Fatalf("статус токенов: %+v", s)
	}
}

func TestFirecrawlStatusUnavailable(t *testing.T) {
	s := firecrawlStatusFrom(fakeAccountReader{})
	if !s.Configured {
		t.Fatal("непустой reader должен давать Configured=true")
	}
	if s.CreditsAvailable || s.TokensAvailable {
		t.Fatalf("без успешных ответов остаток должен быть недоступен: %+v", s)
	}
}

func TestBudgetText(t *testing.T) {
	if got := budgetText(nil); got != "" {
		t.Fatalf("nil-бюджет должен давать пустую строку: %q", got)
	}
	b := &budget.Snapshot{ConfirmedSpentCredits: 3, ReservedCredits: 0, Available: 17}
	if got, want := budgetText(b), "расход 3 · резерв 0 · доступно 17"; got != want {
		t.Fatalf("budgetText = %q, want %q", got, want)
	}
}

func TestShortQuery(t *testing.T) {
	short := "короткий запрос"
	if got := shortQuery(short); got != short {
		t.Fatalf("короткий запрос не меняется: %q", got)
	}

	long := strings.Repeat("я", maxShortQuery+10)
	got := shortQuery(long)
	if got == long {
		t.Fatal("длинный запрос должен быть сокращён")
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("сокращённый запрос оканчивается многоточием: %q", got)
	}
	if runes := []rune(strings.TrimSuffix(got, "…")); len(runes) != maxShortQuery {
		t.Fatalf("длина сокращения = %d, ожидалось %d", len(runes), maxShortQuery)
	}
}

func TestJobRowsShortQuery(t *testing.T) {
	long := strings.Repeat("a", maxShortQuery+5)
	rows := jobRows([]ports.SearchJob{{ID: "j1", Query: long}})
	if rows[0].Query != long {
		t.Fatalf("полный запрос должен остаться в строке: %q", rows[0].Query)
	}
	if rows[0].ShortQuery == long || !strings.HasSuffix(rows[0].ShortQuery, "…") {
		t.Fatalf("строка журнала должна нести короткий запрос: %q", rows[0].ShortQuery)
	}
}

func TestShortDescription(t *testing.T) {
	short := "короткое описание"
	if got := shortDescription(short); got != short {
		t.Fatalf("короткое описание не меняется: %q", got)
	}

	long := strings.Repeat("я", maxCardDescription+10)
	got := shortDescription(long)
	if got == long {
		t.Fatal("длинное описание должно быть сокращено")
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("сокращённое описание оканчивается многоточием: %q", got)
	}
	if runes := []rune(strings.TrimSuffix(got, "…")); len(runes) != maxCardDescription {
		t.Fatalf("длина сокращения = %d, ожидалось %d", len(runes), maxCardDescription)
	}
}

func TestCardRows(t *testing.T) {
	long := strings.Repeat("a", maxCardDescription+5)
	rows := cardRows([]app.Card{
		{ID: "with.example.com", Name: "С Email", Email: "anna@with.example.com", Description: "полное описание"},
		{ID: "phone.example.com", Name: "Без Email", NoEmail: true, Description: long},
	})
	if len(rows) != 2 {
		t.Fatalf("строк %d, ожидалось 2", len(rows))
	}
	if rows[0].Email != "anna@with.example.com" || rows[0].NoEmail {
		t.Fatalf("карточка с email = %+v", rows[0])
	}
	if rows[0].ShortDescription != "полное описание" || rows[0].Description != "полное описание" {
		t.Fatalf("короткое описание карточки = %+v", rows[0])
	}
	if !rows[1].NoEmail || rows[1].Email != "" {
		t.Fatalf("карточка без email = %+v", rows[1])
	}
	if rows[1].ShortDescription == long || !strings.HasSuffix(rows[1].ShortDescription, "…") {
		t.Fatalf("строка списка должна нести краткое описание: %q", rows[1].ShortDescription)
	}
	if rows[1].Description != long {
		t.Fatalf("полное описание должно оставаться в модели: %q", rows[1].Description)
	}
}

func TestCardRowsComposition(t *testing.T) {
	fits := mustAssessment(t, "job", "fit.example", domain.VerdictFits, domain.SignalDirect, []domain.Evidence{{
		Criterion: "к", Fact: "ф", SourceURL: "https://fit.example", Reasoning: "о", SignalKind: domain.SignalDirect,
	}})
	hypo := mustAssessment(t, "job", "hypo.example", domain.VerdictHypothesis, domain.SignalProfile, []domain.Evidence{{
		Criterion: "к", Fact: "ф", SignalKind: domain.SignalProfile, Missing: "прямое заявление",
	}})
	byCompany := assessmentsByCompany([]*domain.LeadAssessment{fits, hypo})

	rows := cardRowsWithAssessments([]app.Card{
		{ID: "fit.example", Name: "Подходит"},
		{ID: "hypo.example", Name: "Гипотеза"},
		{ID: "none.example", Name: "Без оценки"},
	}, byCompany)
	fitsRows, hypoRows, unsuitableRows, unassessedRows := groupCardRows(rows)

	if len(fitsRows) != 1 || fitsRows[0].ID != "fit.example" {
		t.Fatalf("группа «Подходит» = %+v", fitsRows)
	}
	if len(hypoRows) != 1 || hypoRows[0].ID != "hypo.example" || hypoRows[0].VerdictLabel != "Гипотеза" {
		t.Fatalf("группа «Гипотеза» = %+v", hypoRows)
	}
	if len(unsuitableRows) != 0 {
		t.Fatalf("группа «Не подходит» должна быть пуста: %+v", unsuitableRows)
	}
	if len(unassessedRows) != 1 || unassessedRows[0].ID != "none.example" || unassessedRows[0].VerdictLabel != "Не оценено" {
		t.Fatalf("группа «Не оценено» = %+v", unassessedRows)
	}
}

func TestCardRowsWithLatestAssessments(t *testing.T) {
	fits := mustAssessment(t, "job-1", "fit.example", domain.VerdictFits, domain.SignalDirect, []domain.Evidence{{
		Criterion: "к", Fact: "ф", SourceURL: "https://fit.example", Reasoning: "о", SignalKind: domain.SignalDirect,
	}})
	unsuitable := mustAssessment(t, "job-2", "unsuitable.example", domain.VerdictUnsuitable, domain.SignalProfile, []domain.Evidence{{
		Criterion: "к", Fact: "ф", SourceURL: "https://unsuitable.example", Reasoning: "о", SignalKind: domain.SignalProfile, Mismatch: "не та отрасль",
	}})
	latest := assessmentsByCompany([]*domain.LeadAssessment{fits, unsuitable})

	rows := cardRowsWithLatestAssessments([]app.Card{
		{ID: "fit.example", Name: "Подходит"},
		{ID: "unsuitable.example", Name: "Не подходит"},
		{ID: "none.example", Name: "Без оценки"},
	}, latest)
	fitsRows, hypoRows, unsuitableRows, unassessedRows := groupCardRows(rows)

	if len(fitsRows) != 1 || fitsRows[0].ID != "fit.example" {
		t.Fatalf("группа «Подходит» = %+v", fitsRows)
	}
	if len(hypoRows) != 0 {
		t.Fatalf("группа «Гипотеза» должна быть пуста: %+v", hypoRows)
	}
	if len(unsuitableRows) != 1 || unsuitableRows[0].VerdictLabel != "Не подходит" {
		t.Fatalf("группа «Не подходит» = %+v", unsuitableRows)
	}
	if len(unassessedRows) != 1 || unassessedRows[0].VerdictLabel != "Не оценено" {
		t.Fatalf("группа «Не оценено» = %+v", unassessedRows)
	}
	if unassessedRows[0].VerdictClass != "unknown" {
		t.Fatalf("класс неоценённой карточки = %q, ожидался unknown", unassessedRows[0].VerdictClass)
	}
}

func mustAssessment(t *testing.T, jobID, companyID string, verdict domain.AssessmentVerdict, signal domain.SignalKind, evidence []domain.Evidence) *domain.LeadAssessment {
	t.Helper()
	a, err := domain.CreateLeadAssessment(domain.CreateLeadAssessmentParams{
		JobID: jobID, CompanyID: companyID, Verdict: verdict, SignalKind: signal, Evidence: evidence, CreatedAt: viewTestTime,
	})
	if err != nil {
		t.Fatalf("CreateLeadAssessment: %v", err)
	}
	return a
}

func TestInterpretationFrom(t *testing.T) {
	if got := interpretationFrom(nil); got.Defined {
		t.Fatalf("nil-интерпретация должна быть не определена: %+v", got)
	}
	uncertain := interpretationFrom(&llm.Interpretation{Uncertainty: true})
	if !uncertain.Defined || !uncertain.Uncertainty {
		t.Fatalf("явная неопределённость потеряна: %+v", uncertain)
	}
	if uncertain.Reason != "" {
		t.Fatalf("пустая причина не должна подставляться: %+v", uncertain)
	}
	// Сбой интерпретации несёт причину: она видна оператору вместо общего текста.
	withReason := interpretationFrom(&llm.Interpretation{Uncertainty: true, Reason: "провайдер недоступен"})
	if !withReason.Defined || !withReason.Uncertainty || withReason.Reason != "провайдер недоступен" {
		t.Fatalf("причина сбоя интерпретации потеряна: %+v", withReason)
	}
	full := interpretationFrom(&llm.Interpretation{
		TargetLead:        "B2B-агентство",
		NeedSignals:       []string{"ищет автоматизацию"},
		Exclusions:        []string{"b2c"},
		HypothesisSignals: []string{"просто профиль"},
	})
	if !full.Defined || full.Uncertainty || full.TargetLead != "B2B-агентство" {
		t.Fatalf("полная интерпретация = %+v", full)
	}
	if len(full.NeedSignals) != 1 || len(full.Exclusions) != 1 || len(full.HypothesisSignals) != 1 {
		t.Fatalf("критерии потеряны: %+v", full)
	}
}

func TestJobEventPoints(t *testing.T) {
	events := []ports.SearchJobEvent{
		{Type: ports.EventEnqueued, CreatedAt: viewTestTime},
		{Type: ports.EventTaken, CreatedAt: viewTestTime},
		{Type: ports.EventPhase, CreatedAt: viewTestTime, Progress: &ports.SearchJobProgress{Phase: "crawling", PagesCrawled: 3}},
	}
	points := jobEventPoints(events)
	if len(points) != 3 {
		t.Fatalf("точек %d, ожидалось 3", len(points))
	}
	if points[0].Label != "Поставлено в очередь" || points[0].State != "done" {
		t.Fatalf("первая точка = %+v", points[0])
	}
	if points[2].State != "current" {
		t.Fatalf("последнее незавершённое событие должно быть текущим: %+v", points[2])
	}
	if !strings.Contains(points[2].Label, "обход") || !strings.Contains(points[2].Detail, "страниц 3") {
		t.Fatalf("точка фазы = %+v", points[2])
	}

	failed := jobEventPoints([]ports.SearchJobEvent{{Type: ports.EventFailed, CreatedAt: viewTestTime}})
	if failed[0].State != "fail" {
		t.Fatalf("отказ должен помечаться сбоем: %+v", failed[0])
	}
	done := jobEventPoints([]ports.SearchJobEvent{{Type: ports.EventDone, CreatedAt: viewTestTime}})
	if done[0].State != "done" {
		t.Fatalf("завершение должно помечаться завершённым: %+v", done[0])
	}
}

func TestJobFrom(t *testing.T) {
	assessment, err := domain.CreateLeadAssessment(domain.CreateLeadAssessmentParams{
		JobID:      "j1",
		CompanyID:  "a.example",
		Verdict:    domain.VerdictFits,
		SignalKind: domain.SignalIndirect,
		Evidence: []domain.Evidence{{
			Criterion:  "автоматизация продаж",
			Fact:       "наняли руководителя отдела продаж",
			SourceURL:  "https://a.example/careers",
			Reasoning:  "рост отдела продаж указывает на потребность в автоматизации",
			SignalKind: domain.SignalIndirect,
		}},
		CreatedAt: viewTestTime,
	})
	if err != nil {
		t.Fatalf("CreateLeadAssessment: %v", err)
	}

	detail := app.JobDetail{
		Job: ports.SearchJob{
			ID:        "j1",
			Query:     "пример запроса",
			ResultRef: []string{"a.example", "b.example"},
		},
		Events: []ports.SearchJobEvent{{Type: ports.EventEnqueued, CreatedAt: viewTestTime}},
	}
	view := jobFrom(detail, []*domain.LeadAssessment{assessment}, true, map[string]app.CompanyEmail{
		"a.example": {Email: "a@a.example"},
		"b.example": {NoEmail: true},
	}, components.ConsoleContext{})
	if view.ID != "j1" || view.Query != "пример запроса" {
		t.Fatalf("jobFrom = %+v", view)
	}
	// Компания без оценки задания остаётся в списке, но без подписи вердикта.
	if len(view.Companies) != 2 || view.Companies[0].Href != "/cards/a.example?job=j1" {
		t.Fatalf("Companies = %+v", view.Companies)
	}
	if view.Companies[0].VerdictLabel != "Подходит" || view.Companies[1].VerdictLabel != "" {
		t.Fatalf("подписи оценок компаний = %+v", view.Companies)
	}
	// Email и признак «нет email» берутся из пакетной карты контактов.
	if view.Companies[0].Email != "a@a.example" || view.Companies[0].NoEmail {
		t.Fatalf("email компании a.example = %+v", view.Companies[0])
	}
	if !view.Companies[1].NoEmail || view.Companies[1].Email != "" {
		t.Fatalf("компания b.example без email = %+v", view.Companies[1])
	}
	// Распределение и основания строятся только по оценкам этого задания.
	if view.Distribution.Fits != 1 || view.Distribution.Total() != 1 {
		t.Fatalf("Distribution = %+v", view.Distribution)
	}
	if len(view.Fits) != 1 || len(view.Fits[0].Evidence) != 1 || view.Fits[0].Evidence[0].SourceURL != "https://a.example/careers" {
		t.Fatalf("основания подходящих = %+v", view.Fits)
	}
	if view.Result != "2" {
		t.Fatalf("Result = %q", view.Result)
	}
	if len(view.Points) != 1 || view.Points[0].Label != "Поставлено в очередь" {
		t.Fatalf("Points = %+v", view.Points)
	}
}

// TestJobFromWithoutAssessments проверяет, что задание без оценок показывает пустое
// распределение и не подписывает компании вердиктом: отсутствие оценки не
// заменяется выдуманной (lead-relevance).
func TestJobFromWithoutAssessments(t *testing.T) {
	detail := app.JobDetail{Job: ports.SearchJob{ID: "j1", Query: "запрос", ResultRef: []string{"a.example"}}}
	view := jobFrom(detail, nil, true, nil, components.ConsoleContext{})
	if view.Distribution.Total() != 0 || len(view.Fits) != 0 {
		t.Fatalf("без оценок распределение должно быть пустым: %+v", view)
	}
	if view.Companies[0].VerdictLabel != "" {
		t.Fatalf("компания подписана оценкой без контекста: %+v", view.Companies[0])
	}
}

// TestSelectionSummaryFrom проверяет проекцию «Сводка отбора» над наблюдаемым
// прогресом: цель/найдено/недобор, охват и воронка (design.md, Решение 1).
func TestSelectionSummaryFrom(t *testing.T) {
	job := ports.SearchJob{
		ResultRef: []string{"a.example", "b.example", "c.example"},
		Progress: &ports.SearchJobProgress{
			TargetCompanies: 5, CompaniesFound: 3, PagesCrawled: 7,
			PlannedTargets: 2, PlannedURLs: 1, ParseErrors: 1, AssessmentErrors: 1,
			CreditsSpent: 2, CreditsAvailable: 8, TokensSpent: 100, StopReason: "цели исчерпаны",
		},
	}
	s := selectionSummaryFrom(job, verdictDistribution{Fits: 1, Hypothesis: 1, Unsuitable: 1}, true, 2, true)
	if !s.ProgressKnown || !s.TargetSet || s.Target != 5 || s.Found != 3 || s.Shortfall != 2 {
		t.Fatalf("цель/найдено/недобор = %+v", s)
	}
	if s.Pages != 7 || s.PlannedTargets != 2 || s.PlannedURLs != 1 {
		t.Fatalf("охват = %+v", s)
	}
	if s.ParseErrors != 1 || s.AssessmentErrors != 1 || !s.CreditsKnown || s.StopReason != "цели исчерпаны" {
		t.Fatalf("охват ошибок/кредитов/причины = %+v", s)
	}
	if len(s.Funnel) != 9 {
		t.Fatalf("воронка = %+v", s.Funnel)
	}
	if s.Funnel[0].Label != "Найдено" || s.Funnel[0].Count != 3 || !s.Funnel[0].Known {
		t.Fatalf("первый этап воронки = %+v", s.Funnel[0])
	}
	if s.Funnel[1].Label != "Email проверен" || s.Funnel[1].Count != 2 || !s.Funnel[1].Known {
		t.Fatalf("этап email = %+v", s.Funnel[1])
	}
}

// TestSelectionSummaryNoTarget проверяет, что при незаданной цели недобор не
// показывается и не подменяется нулём (design.md, Решение 5).
func TestSelectionSummaryNoTarget(t *testing.T) {
	job := ports.SearchJob{
		ResultRef: []string{"a.example"},
		Progress:  &ports.SearchJobProgress{CompaniesFound: 1, PagesCrawled: 1},
	}
	s := selectionSummaryFrom(job, verdictDistribution{}, true, 1, true)
	if s.TargetSet || s.Shortfall != 0 || s.Target != 0 {
		t.Fatalf("при незаданной цели недобор не рассчитывается: %+v", s)
	}
}

// TestFunnelUnavailableStages проверяет, что недоступный этап воронки помечается
// недоступным, а не выдуманным нулём: этапы писем/отправки/ответа на уровне
// задания недоступны, а недоступное хранилище оценок делает этапы оценок
// недоступными.
func TestFunnelUnavailableStages(t *testing.T) {
	points := funnelPoints(2, false, 0, verdictDistribution{}, false)
	byLabel := map[string]funnelPoint{}
	for _, p := range points {
		byLabel[p.Label] = p
	}
	for _, label := range []string{"Email проверен", "Оценено", "Подходит", "Гипотеза", "Не подходит", "Писем", "Отправлено", "Ответили"} {
		if point, ok := byLabel[label]; !ok || point.Known {
			t.Fatalf("этап %q должен быть недоступен: %+v", label, point)
		}
	}
	if !byLabel["Найдено"].Known || byLabel["Найдено"].Count != 2 {
		t.Fatalf("этап «Найдено» должен быть доступен: %+v", byLabel["Найдено"])
	}
}

// TestTargetDiagnosis проверяет приоритет наблюдаемых причин недобора: причина
// остановки → счётчики ошибок → распределение вердиктов → остаток без домысла.
func TestTargetDiagnosis(t *testing.T) {
	cases := []struct {
		name       string
		progress   *ports.SearchJobProgress
		dist       verdictDistribution
		wantShowed bool
		wantReason string
	}{
		{"нет прогресса", nil, verdictDistribution{}, false, ""},
		{"цель не задана", &ports.SearchJobProgress{CompaniesFound: 3}, verdictDistribution{}, false, ""},
		{"цель достигнута", &ports.SearchJobProgress{TargetCompanies: 10, CompaniesFound: 10, TargetReached: true}, verdictDistribution{}, false, ""},
		{"причина остановки", &ports.SearchJobProgress{TargetCompanies: 10, CompaniesFound: 4, StopReason: "бюджет исчерпан"}, verdictDistribution{}, true, "бюджет исчерпан"},
		{"ошибки разбора", &ports.SearchJobProgress{TargetCompanies: 10, CompaniesFound: 4, ParseErrors: 2}, verdictDistribution{}, true, "ошибок разбора 2"},
		{"ошибки оценки", &ports.SearchJobProgress{TargetCompanies: 10, CompaniesFound: 4, AssessmentErrors: 1}, verdictDistribution{}, true, "ошибок оценки 1"},
		{"отсеяно оценкой", &ports.SearchJobProgress{TargetCompanies: 10, CompaniesFound: 4}, verdictDistribution{Hypothesis: 4}, true, "отсеяны оценкой"},
		{"причина не определена", &ports.SearchJobProgress{TargetCompanies: 10, CompaniesFound: 4}, verdictDistribution{}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := targetDiagnosisFrom(tc.progress, tc.dist)
			if d.Showed != tc.wantShowed {
				t.Fatalf("Showed = %v, ожидалось %v", d.Showed, tc.wantShowed)
			}
			if tc.wantReason != "" && !strings.Contains(d.Reason, tc.wantReason) {
				t.Fatalf("причина = %q, ожидалась с %q", d.Reason, tc.wantReason)
			}
			if tc.wantShowed && tc.wantReason == "" && d.Reason != "" {
				t.Fatalf("причина не определена, но выведена: %q", d.Reason)
			}
		})
	}
}

// TestRejectionCategories проверяет группировку причин отсева по свидетельствам
// задания: «Не подходит» — по Mismatch, «Гипотеза» — по Missing, «нет email» — по
// контактам, «ошибки оценки» — по счётчику; каждой группе дан переход к списку.
func TestRejectionCategories(t *testing.T) {
	unsuitable := mustAssessment(t, "j", "u.example", domain.VerdictUnsuitable, domain.SignalProfile, []domain.Evidence{{
		Criterion: "к", Fact: "ф", SourceURL: "https://u.example/s", Reasoning: "о", SignalKind: domain.SignalProfile, Mismatch: "нет собственного отдела продаж",
	}})
	hypothesis := mustAssessment(t, "j", "h.example", domain.VerdictHypothesis, domain.SignalProfile, []domain.Evidence{{
		Criterion: "к", Fact: "ф", SourceURL: "https://h.example/s", Reasoning: "о", SignalKind: domain.SignalProfile, Missing: "прямое заявление",
	}})
	byCompany := assessmentsByCompany([]*domain.LeadAssessment{unsuitable, hypothesis})
	job := ports.SearchJob{ID: "j", ResultRef: []string{"u.example", "h.example", "m.example"}}
	emails := map[string]app.CompanyEmail{
		"u.example": {Email: "a@u.example"},
		"h.example": {NoEmail: true},
		"m.example": {Email: "a@m.example"},
	}
	categories := rejectionCategories(job, byCompany, emails, 2, components.ConsoleContext{})
	got := map[string]rejectionCategory{}
	for _, c := range categories {
		got[c.Label] = c
	}
	if c, ok := got["Не подходит"]; !ok || c.Count != 1 || len(c.Groups) != 1 || c.Groups[0].Label != "нет собственного отдела продаж" {
		t.Fatalf("группа «Не подходит» = %+v", got["Не подходит"])
	}
	if c, ok := got["Гипотеза"]; !ok || c.Count != 1 || len(c.Groups) != 1 || c.Groups[0].Label != "прямое заявление" {
		t.Fatalf("группа «Гипотеза» = %+v", got["Гипотеза"])
	}
	if c, ok := got["нет email / email недоступен"]; !ok || c.Count != 1 {
		t.Fatalf("группа «нет email» = %+v", got["нет email / email недоступен"])
	}
	if c, ok := got["ошибки оценки"]; !ok || c.Count != 2 {
		t.Fatalf("группа «ошибки оценки» = %+v", got["ошибки оценки"])
	}
	if !strings.Contains(got["Не подходит"].Href, "reason=mismatch") {
		t.Fatalf("у группы нет перехода к отфильтрованному списку: %q", got["Не подходит"].Href)
	}
}

// TestRejectionCategoriesNoInventedReason проверяет, что компании без
// свидетельства причина не назначается: пустая группа не создаётся.
func TestRejectionCategoriesNoInventedReason(t *testing.T) {
	fits := mustAssessment(t, "j", "fit.example", domain.VerdictFits, domain.SignalDirect, []domain.Evidence{{
		Criterion: "к", Fact: "ф", SourceURL: "https://fit.example/s", Reasoning: "о", SignalKind: domain.SignalDirect,
	}})
	byCompany := assessmentsByCompany([]*domain.LeadAssessment{fits})
	job := ports.SearchJob{ID: "j", ResultRef: []string{"fit.example"}}
	categories := rejectionCategories(job, byCompany, map[string]app.CompanyEmail{"fit.example": {Email: "a@fit.example"}}, 0, components.ConsoleContext{})
	if len(categories) != 0 {
		t.Fatalf("выдуманные причины отсева: %+v", categories)
	}
}

// TestFilterCardRows проверяет фильтр по причине отсева: карточка без
// соответствующего свидетельства/счётчика в выборку не попадает, а снятие
// фильтра возвращает полный список.
func TestFilterCardRows(t *testing.T) {
	rows := []cardRow{
		{ID: "u", Group: cardGroupUnsuitable, Mismatches: []string{"нет отдела продаж"}},
		{ID: "h", Group: cardGroupHypothesis, Missings: []string{"нет заявления"}},
		{ID: "f", Group: cardGroupFits, NoEmail: true},
		{ID: "x", Group: cardGroupUnassessed},
	}
	if got := filterCardRows(rows, "", ""); len(got) != 4 {
		t.Fatalf("без фильтра список должен быть полным: %+v", got)
	}
	if got := filterCardRows(rows, filterReasonMismatch, "нет отдела продаж"); len(got) != 1 || got[0].ID != "u" {
		t.Fatalf("фильтр по несоответствию = %+v", got)
	}
	if got := filterCardRows(rows, filterReasonMismatch, "другое"); len(got) != 0 {
		t.Fatalf("чужая формулировка не должна выбирать карточку: %+v", got)
	}
	if got := filterCardRows(rows, filterReasonMissing, ""); len(got) != 1 || got[0].ID != "h" {
		t.Fatalf("фильтр по недостающему свидетельству = %+v", got)
	}
	if got := filterCardRows(rows, filterReasonNoEmail, ""); len(got) != 1 || got[0].ID != "f" {
		t.Fatalf("фильтр по отсутствию email = %+v", got)
	}
	if got := filterCardRows(rows, filterReasonUnassessed, ""); len(got) != 1 || got[0].ID != "x" {
		t.Fatalf("фильтр по неоценённым = %+v", got)
	}
}
