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
	if s.CreditsUpdated != "2026-01-02 10:00:00" {
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
	fitsRows, otherRows := splitCardRows(rows)

	if len(fitsRows) != 1 || fitsRows[0].ID != "fit.example" {
		t.Fatalf("группа «Подходящие» = %+v", fitsRows)
	}
	if len(otherRows) != 2 {
		t.Fatalf("вторая группа должна хранить гипотезу и отсутствие оценки: %+v", otherRows)
	}
	if otherRows[0].VerdictLabel != "Гипотеза" || otherRows[1].VerdictLabel != "Не оценено" {
		t.Fatalf("подписи второй группы = %+v", otherRows)
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
	fitsRows, otherRows := splitCardRows(rows)

	if len(fitsRows) != 1 || fitsRows[0].ID != "fit.example" {
		t.Fatalf("группа «Подходящие» = %+v", fitsRows)
	}
	if len(otherRows) != 2 {
		t.Fatalf("вторая группа должна хранить «Не подходит» и отсутствие оценки: %+v", otherRows)
	}
	if otherRows[0].VerdictLabel != "Не подходит" || otherRows[1].VerdictLabel != "Не оценено" {
		t.Fatalf("подписи второй группы = %+v", otherRows)
	}
	if otherRows[1].VerdictClass != "unknown" {
		t.Fatalf("класс неоценённой карточки = %q, ожидался unknown", otherRows[1].VerdictClass)
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
	view := jobFrom(detail, []*domain.LeadAssessment{assessment}, map[string]app.CompanyEmail{
		"a.example": {Email: "a@a.example"},
		"b.example": {NoEmail: true},
	})
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
	view := jobFrom(detail, nil, nil)
	if view.Distribution.Total() != 0 || len(view.Fits) != 0 {
		t.Fatalf("без оценок распределение должно быть пустым: %+v", view)
	}
	if view.Companies[0].VerdictLabel != "" {
		t.Fatalf("компания подписана оценкой без контекста: %+v", view.Companies[0])
	}
}
