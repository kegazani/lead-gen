package httpapi

import (
	"fmt"
	"html/template"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/forum"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/frontend/components"
)

// Ключи разделов боковой навигации консоли.
const (
	sidebarHome      = "home"
	sidebarCards     = "cards"
	sidebarLeads     = "leads"
	sidebarFunnel    = "funnel"
	sidebarSend      = "send"
	sidebarAssistant = "assistant"
	sidebarForum     = "forum"
)

// dateFormat — дата извлечения в разметке. Совпадает с форматом компонента
// provenance (frontend/components): источник и дата — одна единица разметки.
const dateFormat = "2006-01-02"

// consoleSidebar собирает боковую навигацию консоли. Активный раздел подсвечивается
// по ключу; подписи и адреса — данные представления, не доменные термины.
func consoleSidebar(active string) components.Nav {
	return components.Nav{Items: []components.NavItem{
		{Label: "Главная", Href: "/", Active: active == sidebarHome},
		{Label: "Карточки", Href: "/cards", Active: active == sidebarCards},
		{Label: "Лиды", Href: "/leads", Active: active == sidebarLeads},
		{Label: "Воронка", Href: "/funnel", Active: active == sidebarFunnel},
		{Label: "Отправка", Href: "/send", Active: active == sidebarSend},
		{Label: "Ассистент", Href: "/assistant", Active: active == sidebarAssistant},
		{Label: "Форумы", Href: "/forum-signals", Active: active == sidebarForum},
	}}
}

// pageView собирает страницу без боковой навигации (вход, уведомление).
func pageView(title string, view any) components.Page {
	return components.Page{
		Layout: components.Layout{
			Title:            title,
			Stylesheets:      consoleStylesheets,
			Scripts:          consoleScripts,
			BootstrapScripts: consoleBootstrapScripts,
		},
		View: view,
	}
}

// pageViewSidebar собирает страницу с боковой навигацией и моделью экрана.
func pageViewSidebar(title string, nav components.Nav, view any) components.Page {
	return components.Page{
		Layout: components.Layout{
			Title:            title,
			Stylesheets:      consoleStylesheets,
			Scripts:          consoleScripts,
			BootstrapScripts: consoleBootstrapScripts,
		},
		Sidebar: nav,
		View:    view,
	}
}

// loginView — модель страницы входа.
type loginView struct {
	Error string
}

// dashboardView — модель главной страницы: форма поиска, журнал запусков и
// последние добавленные лиды.
type dashboardView struct {
	QueryField components.Field
	// TargetCompaniesField — «Сколько найти»: целевое число компаний.
	TargetCompaniesField components.Field
	// MaxResultsField — «Результатов на запрос»: лимит результатов поиска.
	MaxResultsField components.Field
	// TargetsField — «Свои цели»: ручной ввод целей (по строке — запрос или URL).
	TargetsField components.Field
	BudgetField  components.Field
	CSRF         string
	Journal      journalView
	Leads        []cardRow
	Firecrawl    firecrawlStatus
}

// sendView — модель экрана отправки по сегменту.
type sendView struct {
	CSRF    string
	Sent    int
	Skipped int
	// Options — шаблоны для выпадающего списка формы.
	Options []templateItem
	// ValuesHTML — предразмеченная подстановка объявленных переменных.
	ValuesHTML template.HTML
	// TemplateList — предразмеченный общий список шаблонов с поиском.
	TemplateList template.HTML
	// Preview — предразмеченный предпросмотр собранного письма.
	Preview template.HTML
}

// templateItem — строка списка шаблонов.
type templateItem struct {
	ID       string
	Name     string
	Subject  string
	Revision int
	Href     string
	Selected bool
}

// templateListView — модель общего списка шаблонов с поиском (экран и
// HTMX-фрагмент).
type templateListView struct {
	Query  string
	Action string
	Items  []templateItem
}

// templateVariableOption — переменная каталога в форме шаблона: подпись,
// источник, признак объявления и текущий fallback.
type templateVariableOption struct {
	Key      string
	Label    string
	Source   string
	Declared bool
	Fallback string
}

// templateFormView — модель формы создания или правки шаблона.
type templateFormView struct {
	Mode         string
	Action       string
	DeleteAction string
	Reference    string
	Name         string
	Subject      string
	Body         string
	Options      []templateVariableOption
}

// templatesView — модель страницы библиотеки шаблонов.
type templatesView struct {
	CSRF  string
	Error string
	Form  templateFormView
	List  templateListView
}

// templateValueView — объявленная переменная с редактируемым значением.
type templateValueView struct {
	Key      string
	Label    string
	Value    string
	Fallback string
}

// journalView — модель журнала заданий (полный экран и HTMX-фрагмент).
type journalView struct {
	Jobs []jobRow
	// CSRF — токен для форм отмены внутри журнала.
	CSRF string
	// AutoRefresh — включать ли HTMX-опрос фрагмента (есть выполняющееся задание).
	AutoRefresh bool
}

// jobRow — строка журнала заданий. Компактная строка показывает состояние,
// дату и короткий запрос; полный запрос, ход и ошибка доступны при раскрытии.
type jobRow struct {
	ID         string
	Query      string
	ShortQuery string
	Badge      statusBadge
	Progress   string
	Created    string
	Error      string
	Cancelable bool
}

// jobCompanyRow — найденная заданием компания на экране задания: идентификатор,
// адрес карточки и подпись оценки в контексте этого задания. Оценка чужого
// задания сюда не попадает: подпись берётся из оценок этого задания.
type jobCompanyRow struct {
	ID               string
	Href             string
	Email            string
	NoEmail          bool
	EmailUnavailable bool
	VerdictLabel     string
	VerdictClass     string
}

// jobView — модель экрана подробностей задания: полный запрос, состояние, бюджет,
// распределение оценок, основания подходящих результатов, найденные компании и ход
// выполнения, собранный по событиям жизненного цикла.
type jobView struct {
	ID       string
	Query    string
	Badge    statusBadge
	Created  string
	Updated  string
	Result   string
	Budget   string
	Progress string
	Error    string
	// AutoRefresh — задание не терминально: экран опрашивается через htmx, пока
	// результат ещё пополняется.
	AutoRefresh  bool
	Companies    []jobCompanyRow
	Distribution verdictDistribution
	Fits         []jobFitRow
	Points       []timelinePoint
}

// verdictDistribution — распределение компаний задания по оценкам. Это
// распределение оценок самого задания, а не метрика правильного направления
// обхода: число компаний и страниц остаётся мониторингом (search-strategy,
// требование короткой проверочной сессии).
type verdictDistribution struct {
	Fits       int
	Hypothesis int
	Unsuitable int
}

// Total возвращает число оценённых компаний задания.
func (d verdictDistribution) Total() int { return d.Fits + d.Hypothesis + d.Unsuitable }

// jobFitRow — подходящий результат задания с основаниями и источниками,
// подтверждающими приведённые факты.
type jobFitRow struct {
	ID           string
	Href         string
	VerdictLabel string
	VerdictClass string
	SignalLabel  string
	Evidence     []evidenceRow
}

// statusBadge — модель бейджа статуса для шаблона status-badge. Класс выводится
// из строки статуса заранее, шаблон только показывает готовые строки; Title —
// подсказка при наведении (что происходит сейчас).
type statusBadge struct {
	Label string
	Class string
	Title string
}

// timelinePoint — точка прямого таймлайна статуса: подпись, состояние
// (pending/done/current/fail) и необязательные детали при наведении. Позиция
// точки задаётся порядком в списке, форма маркера — состоянием (design.md,
// Решение 6); шаблон не содержит логики.
type timelinePoint struct {
	Label  string
	State  string
	Detail string
}

// cardsView — модель таблицы карточек с переключателем состава результатов:
// группа «Подходящие» (Fits) показывается по умолчанию, вторая группа Other —
// «Неподходящие и гипотезы» — по переключению. JobScoped отмечает, что список
// открыт в контексте задания, поэтому несёт его оценки.
type cardsView struct {
	Fits      []cardRow
	Other     []cardRow
	JobID     string
	JobQuery  string
	JobScoped bool
}

// Группы состава результатов списка карточек.
const (
	cardGroupFits  = "fits"
	cardGroupOther = "other"
)

// cardRow — карточка списка: общая модель обоих представлений (плитки и строки).
// Description остаётся полным описанием для главной страницы, ShortDescription —
// краткое описание для витрины карточек, Email и NoEmail описывают наличие адреса.
// Group задаёт состав (группу) карточки, VerdictLabel — подпись оценки в контексте
// задания («Гипотеза»/«Не подходит»); без контекста задания подпись пуста.
type cardRow struct {
	ID               string
	Name             string
	Email            string
	NoEmail          bool
	Description      string
	ShortDescription string
	Group            string
	VerdictLabel     string
	VerdictClass     string
}

// leadsView — модель страницы лидов с email.
type leadsView struct {
	Rows []leadRow
}

// leadRow — строка списка лидов с email: ссылка на сайт, домен для подписи,
// название и адрес.
type leadRow struct {
	Site   string
	Domain string
	Name   string
	Email  string
}

// evidenceRow — свидетельство оценки для раскрытия «Почему эта оценка?»:
// критерий из запроса, наблюдаемый факт, ссылка на источник, объяснение связи
// факта с потребностью и тип сигнала. Наблюдаемый факт отделён от интерпретации,
// а интерпретация — от неподтверждённого намерения купить (lead-relevance,
// требование свидетельств и объяснения решения).
type evidenceRow struct {
	Criterion   string
	Fact        string
	SourceURL   string
	Reasoning   string
	SignalLabel string
	Missing     string
	Mismatch    string
}

// assessmentExplanation — раскрытие оценки компании в контексте задания: вердикт,
// тип сигнала и свидетельства. Оценка не является постоянным свойством компании,
// поэтому раскрытие существует только в контексте задания и не подставляется из
// другого задания (lead-relevance, запрет переноса).
type assessmentExplanation struct {
	Verdict      string
	VerdictLabel string
	VerdictClass string
	SignalLabel  string
	Evidence     []evidenceRow
}

// cardView — модель карточки лида: данные, история переписки, CSRF-токен и ключ
// идемпотентности формы отправки.
type cardView struct {
	Card    cardDetail
	CSRF    string
	SendKey string
	// Options — шаблоны для выпадающего списка формы отправки.
	Options []templateItem
	// ValuesHTML — предразмеченная подстановка объявленных переменных.
	ValuesHTML template.HTML
	// TemplateList — предразмеченный общий список шаблонов с поиском.
	TemplateList template.HTML
	// Preview — предразмеченный предпросмотр собранного письма.
	Preview template.HTML
	// Assessment — раскрытие оценки в контексте задания (?job=). nil — оценки в
	// этом контексте нет; чужая оценка подставляться не должна.
	Assessment *assessmentExplanation
	// JobID — задание, в контексте которого открыта карточка (пусто — без задания).
	JobID string
	// JobScoped — карточка открыта в контексте задания.
	JobScoped bool
}

// cardDetail — карточка с полями, статусом, контактами, происхождением и историей
// переписки.
type cardDetail struct {
	ID          string
	Name        string
	Description string
	Socials     []string
	Source      provenanceView
	StageBadge  statusBadge
	Stage       string
	StagePoints []timelinePoint
	Snoozed     bool
	Contacts    []contactRow
	History     []historyRow
}

// historyRow — строка истории переписки лида.
type historyRow struct {
	Recipient string
	Template  string
	Status    statusBadge
	Reason    string
	Created   string
	Points    []timelinePoint
}

// contactRow — контакт компании с происхождением и способом извлечения.
type contactRow struct {
	Email  string
	Phone  string
	Role   string
	Source provenanceView
	Method string
}

// provenanceView — модель происхождения для шаблона provenance: источник и уже
// отформатированная дата.
type provenanceView struct {
	Source string
	Date   string
}

// funnelView — модель канбан-воронки: колонки по стадиям и CSRF-токен.
type funnelView struct {
	Stages []funnelStageRow
	CSRF   string
}

// funnelStageRow — колонка воронки: стадия и лиды на ней.
type funnelStageRow struct {
	Stage string
	Label string
	Count int
	Leads []funnelLeadRow
}

// funnelLeadRow — лид в колонке воронки.
type funnelLeadRow struct {
	ID   string
	Name string
}

// maxShortQuery — предел длины короткого запроса в строке журнала (в рунах).
const maxShortQuery = 60

// jobRows отображает задания поиска в строки журнала.
func jobRows(list []ports.SearchJob) []jobRow {
	rows := make([]jobRow, 0, len(list))
	for _, j := range list {
		rows = append(rows, jobRow{
			ID:         j.ID,
			Query:      j.Query,
			ShortQuery: shortQuery(j.Query),
			Badge:      statusBadge{Label: string(j.Status), Class: components.StatusClass(string(j.Status)), Title: statusTitle(j)},
			Progress:   progressOrQueue(j),
			Created:    formatDate(j.CreatedAt),
			Error:      errorText(j),
			Cancelable: j.Status == jobs.StatusQueued || j.Status == jobs.StatusRunning,
		})
	}
	return rows
}

// shortQuery сокращает длинный запрос до короткого представления с многоточием.
// Обрезка идёт по рунам, чтобы не разрезать многобайтовый символ.
func shortQuery(query string) string {
	runes := []rune(query)
	if len(runes) <= maxShortQuery {
		return query
	}
	return strings.TrimRight(string(runes[:maxShortQuery]), " ") + "…"
}

// jobFrom собирает модель экрана подробностей задания по заданию, его событиям и
// оценкам в контексте этого задания. Оценки приходят уже отфильтрованными по
// заданию: распределение, подписи и основания строятся только по ним, оценка
// чужого задания в эту модель не попадает.
func jobFrom(detail app.JobDetail, assessments []*domain.LeadAssessment, emails map[string]app.CompanyEmail) jobView {
	j := detail.Job
	byCompany := assessmentsByCompany(assessments)

	// CompanyEmails в случае успеха всегда возвращает непустую карту с записью на
	// каждую компанию; nil означает ошибку чтения — email помечается недоступным.
	emailsUnavailable := emails == nil
	companies := make([]jobCompanyRow, 0, len(j.ResultRef))
	for _, id := range j.ResultRef {
		contact := emails[id]
		row := jobCompanyRow{
			ID:               id,
			Href:             cardHref(id, j.ID),
			Email:            contact.Email,
			NoEmail:          contact.NoEmail,
			EmailUnavailable: emailsUnavailable,
		}
		if a := byCompany[id]; a != nil {
			row.VerdictLabel = verdictLabel(a.Verdict())
			row.VerdictClass = verdictClass(a.Verdict())
		}
		companies = append(companies, row)
	}

	fits := make([]jobFitRow, 0)
	var distribution verdictDistribution
	for _, a := range assessments {
		switch a.Verdict() {
		case domain.VerdictFits:
			distribution.Fits++
			fits = append(fits, jobFitRow{
				ID:           a.CompanyID(),
				Href:         cardHref(a.CompanyID(), j.ID),
				VerdictLabel: verdictLabel(a.Verdict()),
				VerdictClass: verdictClass(a.Verdict()),
				SignalLabel:  signalLabel(a.SignalKind()),
				Evidence:     evidenceRows(a.Evidence()),
			})
		case domain.VerdictHypothesis:
			distribution.Hypothesis++
		case domain.VerdictUnsuitable:
			distribution.Unsuitable++
		}
	}

	return jobView{
		ID:           j.ID,
		Query:        j.Query,
		Badge:        statusBadge{Label: string(j.Status), Class: components.StatusClass(string(j.Status)), Title: statusTitle(j)},
		Created:      formatDateTime(j.CreatedAt),
		Updated:      formatDateTime(j.UpdatedAt),
		Result:       resultCount(j.ResultRef),
		Budget:       budgetText(j.Budget),
		Progress:     progressOrQueue(j),
		Error:        errorText(j),
		AutoRefresh:  j.Status == jobs.StatusQueued || j.Status == jobs.StatusRunning,
		Companies:    companies,
		Distribution: distribution,
		Fits:         fits,
		Points:       jobEventPoints(detail.Events),
	}
}

// cardHref строит адрес карточки в контексте задания: оценка открывается в том
// задании, в котором получена.
func cardHref(companyID, jobID string) string {
	href := "/cards/" + url.PathEscape(companyID)
	if jobID != "" {
		href += "?job=" + url.QueryEscape(jobID)
	}
	return href
}

// assessmentsByCompany индексирует оценки по идентификатору компании. Оценка
// существует только в контексте задания, поэтому переданный срез уже ограничен
// одним заданием.
func assessmentsByCompany(assessments []*domain.LeadAssessment) map[string]*domain.LeadAssessment {
	byCompany := make(map[string]*domain.LeadAssessment, len(assessments))
	for _, a := range assessments {
		if a != nil {
			byCompany[a.CompanyID()] = a
		}
	}
	return byCompany
}

// evidenceRows отображает свидетельства оценки в строки раскрытия.
func evidenceRows(evidence []domain.Evidence) []evidenceRow {
	rows := make([]evidenceRow, 0, len(evidence))
	for _, e := range evidence {
		rows = append(rows, evidenceRow{
			Criterion:   e.Criterion,
			Fact:        e.Fact,
			SourceURL:   e.SourceURL,
			Reasoning:   e.Reasoning,
			SignalLabel: signalLabel(e.SignalKind),
			Missing:     e.Missing,
			Mismatch:    e.Mismatch,
		})
	}
	return rows
}

// verdictLabel переводит вердикт оценки в подпись консоли.
func verdictLabel(v domain.AssessmentVerdict) string {
	switch v {
	case domain.VerdictFits:
		return "Подходит"
	case domain.VerdictHypothesis:
		return "Гипотеза"
	case domain.VerdictUnsuitable:
		return "Не подходит"
	default:
		return "Не оценено"
	}
}

// verdictClass переводит вердикт в класс оформления (несёт не только цвет).
func verdictClass(v domain.AssessmentVerdict) string {
	switch v {
	case domain.VerdictFits:
		return "fits"
	case domain.VerdictHypothesis:
		return "hypothesis"
	case domain.VerdictUnsuitable:
		return "unsuitable"
	default:
		return "unknown"
	}
}

// signalLabel переводит тип наблюдаемого сигнала в подпись консоли.
func signalLabel(k domain.SignalKind) string {
	switch k {
	case domain.SignalDirect:
		return "прямое заявление"
	case domain.SignalIndirect:
		return "косвенный сигнал"
	case domain.SignalProfile:
		return "совпадение по профилю"
	default:
		return string(k)
	}
}

// assessmentExplanationFrom собирает раскрытие оценки. nil даёт nil: отсутствие
// оценки не подменяется выдуманной.
func assessmentExplanationFrom(a *domain.LeadAssessment) *assessmentExplanation {
	if a == nil {
		return nil
	}
	return &assessmentExplanation{
		Verdict:      a.Verdict().String(),
		VerdictLabel: verdictLabel(a.Verdict()),
		VerdictClass: verdictClass(a.Verdict()),
		SignalLabel:  signalLabel(a.SignalKind()),
		Evidence:     evidenceRows(a.Evidence()),
	}
}

// jobEventPoints строит ход выполнения задания по событиям жизненного цикла:
// каждая точка — событие с временем и счётчиками на момент события. Последнее
// незавершённое событие помечается текущим, отказ — сбоем (design.md, Решение 5).
func jobEventPoints(events []ports.SearchJobEvent) []timelinePoint {
	points := make([]timelinePoint, 0, len(events))
	for i, e := range events {
		state := "done"
		switch {
		case e.Type == ports.EventError || e.Type == ports.EventFailed:
			state = "fail"
		case i == len(events)-1 && !terminalEvent(e.Type):
			state = "current"
		}
		points = append(points, timelinePoint{
			Label:  jobEventLabel(e),
			State:  state,
			Detail: jobEventDetail(e),
		})
	}
	return points
}

// terminalEvent сообщает, завершает ли событие жизненный цикл задания.
func terminalEvent(t ports.SearchJobEventType) bool {
	switch t {
	case ports.EventDone, ports.EventFailed, ports.EventCancelled:
		return true
	default:
		return false
	}
}

// jobEventLabel переводит событие жизненного цикла в подпись хода выполнения.
func jobEventLabel(e ports.SearchJobEvent) string {
	switch e.Type {
	case ports.EventEnqueued:
		return "Поставлено в очередь"
	case ports.EventTaken:
		return "Взято в обработку"
	case ports.EventPhase:
		if p := e.Progress; p != nil && p.Phase != "" {
			return "Фаза: " + phaseLabel(p.Phase)
		}
		return "Смена фазы"
	case ports.EventError:
		return "Ошибка"
	case ports.EventCancelRequested:
		return "Запрошена отмена"
	case ports.EventCancelled:
		return "Отменено"
	case ports.EventDone:
		return "Завершено"
	case ports.EventFailed:
		return "Отказ"
	default:
		return string(e.Type)
	}
}

// jobEventDetail собирает детали события: время, код отказа и счётчики прогресса
// на момент события.
func jobEventDetail(e ports.SearchJobEvent) string {
	parts := make([]string, 0, 3)
	if t := formatDateTime(e.CreatedAt); t != "" {
		parts = append(parts, t)
	}
	if e.Code != "" {
		parts = append(parts, "код: "+e.Code)
	}
	if p := progressText(e.Progress); p != "" {
		parts = append(parts, p)
	}
	return strings.Join(parts, " · ")
}

// journalFrom собирает модель журнала: строки, CSRF для форм отмены и признак
// авто-обновления, пока есть незавершённое задание.
func journalFrom(list []ports.SearchJob, csrf string) journalView {
	return journalView{Jobs: jobRows(list), CSRF: csrf, AutoRefresh: hasActiveJob(list)}
}

// hasActiveJob сообщает, есть ли задание в незавершённом состоянии.
func hasActiveJob(list []ports.SearchJob) bool {
	for _, j := range list {
		if j.Status == jobs.StatusQueued || j.Status == jobs.StatusRunning {
			return true
		}
	}
	return false
}

// progressText отображает прогресс задания одной строкой. nil или пустой
// прогресс даёт пустую строку.
func progressText(p *ports.SearchJobProgress) string {
	if p == nil {
		return ""
	}
	parts := make([]string, 0, 8)
	if p.Phase != "" {
		parts = append(parts, phaseLabel(p.Phase))
	}
	if p.PlannedTargets > 0 {
		parts = append(parts, fmt.Sprintf("целей %d", p.PlannedTargets))
	}
	if p.PlannedURLs > 0 {
		parts = append(parts, fmt.Sprintf("URL %d", p.PlannedURLs))
	}
	if p.CurrentURL != "" {
		parts = append(parts, p.CurrentURL)
	}
	parts = append(parts, fmt.Sprintf("страниц %d", p.PagesCrawled))
	if p.TargetCompanies > 0 {
		parts = append(parts, fmt.Sprintf("компаний %d из %d", p.CompaniesFound, p.TargetCompanies))
	} else {
		parts = append(parts, fmt.Sprintf("компаний %d", p.CompaniesFound))
	}
	if p.StopReason != "" {
		parts = append(parts, "остановлено: "+p.StopReason)
	}
	if p.ParseErrors > 0 {
		parts = append(parts, fmt.Sprintf("ошибок разбора %d", p.ParseErrors))
	}
	if p.AssessmentErrors > 0 {
		parts = append(parts, fmt.Sprintf("ошибок оценки %d", p.AssessmentErrors))
	}
	if p.TokensSpent > 0 {
		parts = append(parts, fmt.Sprintf("токенов %d", p.TokensSpent))
	}
	if p.CreditsSpent != 0 || p.CreditsAvailable != 0 {
		parts = append(parts, fmt.Sprintf("кредитов %d/%d", p.CreditsSpent, p.CreditsSpent+p.CreditsAvailable))
	}
	return strings.Join(parts, " · ")
}

// progressOrQueue отображает ход задания: для queued — позицию в очереди, для
// прочих — прогресс одной строкой.
func progressOrQueue(j ports.SearchJob) string {
	if j.Status == jobs.StatusQueued {
		if j.QueuePosition > 0 {
			return fmt.Sprintf("в очереди · позиция %d", j.QueuePosition)
		}
		return "в очереди"
	}
	return progressText(j.Progress)
}

// errorText возвращает понятную формулировку отказа: бизнес-сообщение при
// наличии кода, нейтральную подпись иначе (сырой текст оператору не показывается).
func errorText(j ports.SearchJob) string {
	if j.Status != jobs.StatusFailed {
		return ""
	}
	if j.ErrorCode != "" {
		return j.Error
	}
	return "Ошибка задания"
}

// statusTitle собирает подсказку для бейджа статуса: что происходит сейчас.
func statusTitle(j ports.SearchJob) string {
	switch j.Status {
	case jobs.StatusQueued:
		return progressOrQueue(j)
	case jobs.StatusRunning:
		if p := progressText(j.Progress); p != "" {
			return p
		}
		return "выполняется"
	case jobs.StatusFailed:
		return errorText(j)
	case jobs.StatusDone:
		if j.Progress != nil && j.Progress.CompaniesFound > 0 {
			return fmt.Sprintf("найдено компаний %d", j.Progress.CompaniesFound)
		}
		return "завершено"
	case jobs.StatusCancelled:
		return "отменено"
	default:
		return ""
	}
}

// phaseLabel переводит фазу задания в подпись для консоли.
func phaseLabel(phase string) string {
	switch phase {
	case app.PhasePlanning:
		return "планирование"
	case app.PhaseSearching:
		return "поиск"
	case app.PhaseCrawling:
		return "обход"
	case app.PhaseExtracting:
		return "извлечение"
	case app.PhaseExpanding:
		return "добор целей"
	case app.PhaseFinalizing:
		return "завершение"
	default:
		return phase
	}
}

// maxCardDescription — предел длины краткого описания в списке карточек (в рунах).
const maxCardDescription = 160

// cardRows отображает карточки в строки списка без оценок: без контекста задания
// оценка не подставляется, поэтому все карточки попадают в группу по умолчанию.
func cardRows(cards []app.Card) []cardRow {
	rows := make([]cardRow, 0, len(cards))
	for _, c := range cards {
		rows = append(rows, cardRow{
			ID:               c.ID,
			Name:             c.Name,
			Email:            c.Email,
			NoEmail:          c.NoEmail,
			Description:      c.Description,
			ShortDescription: shortDescription(c.Description),
			Group:            cardGroupFits,
		})
	}
	return rows
}

// leadRows отбирает лиды с непустым email и отображает их в строки: ссылка на
// сайт (домен компании), название и адрес. Лиды без email пропускаются.
func leadRows(cards []app.Card) []leadRow {
	rows := make([]leadRow, 0, len(cards))
	for _, c := range cards {
		if c.Email == "" {
			continue
		}
		rows = append(rows, leadRow{
			Site:   siteURL(c.ID),
			Domain: siteDomain(c.ID),
			Name:   c.Name,
			Email:  c.Email,
		})
	}
	return rows
}

// siteURL строит адрес сайта компании из её идентификатора (нормализованного
// домена). Если схема уже указана, адрес возвращается как есть; пустой
// идентификатор даёт пустую ссылку.
func siteURL(id string) string {
	id = strings.TrimSpace(id)
	switch {
	case id == "":
		return ""
	case strings.HasPrefix(id, "http://"), strings.HasPrefix(id, "https://"):
		return id
	default:
		return "https://" + id
	}
}

// siteDomain — домен компании для подписи ссылки: без схемы и хвостового слэша.
func siteDomain(id string) string {
	domain := strings.TrimSuffix(siteURL(id), "/")
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	return domain
}

// cardRowsWithAssessments накладывает на строки списка оценки задания. Карточка с
// вердиктом «Подходит» остаётся в группе по умолчанию, «Гипотеза» и «Не подходит»
// уходят во вторую группу с подписью. Оценки берутся только из переданного
// задания: оценка чужого задания сюда не попадает (lead-relevance, запрет
// переноса). Компания результата без оценки не выдаётся за «Подходит»: она
// остаётся во второй группе с явной пометкой «Не оценено».
func cardRowsWithAssessments(cards []app.Card, byCompany map[string]*domain.LeadAssessment) []cardRow {
	return applyAssessmentGroups(cardRows(cards), byCompany)
}

// cardRowsWithLatestAssessments накладывает на строки общей витрины последнюю по
// времени оценку каждой компании по всем заданиям. «Подходит» остаётся в группе по
// умолчанию; «Гипотеза» и «Не подходит» уходят во вторую группу; компания без
// оценок — во вторую группу с пометкой «Не оценено». Последняя оценка — проекция
// чтения: она не переносится в компанию как постоянное свойство и не выдаёт
// оценку одного задания за оценку другого.
func cardRowsWithLatestAssessments(cards []app.Card, latest map[string]*domain.LeadAssessment) []cardRow {
	return applyAssessmentGroups(cardRows(cards), latest)
}

// applyAssessmentGroups раскладывает готовые строки по группам состава: вердикт
// «Подходит» остаётся в группе по умолчанию, «Гипотеза» и «Не подходит» уходят во
// вторую группу, отсутствие оценки — во вторую группу с пометкой «Не оценено».
// Подпись и класс берутся из вердикта, чтобы оба пути (задание и общая витрина)
// давали одинаковый результат.
func applyAssessmentGroups(rows []cardRow, byCompany map[string]*domain.LeadAssessment) []cardRow {
	for i := range rows {
		a := byCompany[rows[i].ID]
		if a == nil {
			rows[i].Group = cardGroupOther
			rows[i].VerdictLabel = "Не оценено"
			rows[i].VerdictClass = "unknown"
			continue
		}
		rows[i].VerdictLabel = verdictLabel(a.Verdict())
		rows[i].VerdictClass = verdictClass(a.Verdict())
		if a.Verdict() != domain.VerdictFits {
			rows[i].Group = cardGroupOther
		}
	}
	return rows
}

// splitCardRows делит строки на группу «Подходящие» и вторую группу, сохраняя
// порядок карточек. Результаты второй группы не удаляются: они остаются
// доступными при переключении состава.
func splitCardRows(rows []cardRow) (fits, other []cardRow) {
	fits = make([]cardRow, 0, len(rows))
	other = make([]cardRow, 0)
	for _, row := range rows {
		if row.Group == cardGroupOther {
			other = append(other, row)
			continue
		}
		fits = append(fits, row)
	}
	return fits, other
}

// shortDescription сокращает длинное описание до краткого представления с
// многоточием. Обрезка идёт по рунам, чтобы не разрезать многобайтовый символ.
func shortDescription(description string) string {
	runes := []rune(description)
	if len(runes) <= maxCardDescription {
		return description
	}
	return strings.TrimRight(string(runes[:maxCardDescription]), " ") + "…"
}

// cardDetailFrom отображает карточку прикладного слоя и историю переписки в модель
// экрана.
func cardDetailFrom(card app.Card, history []app.CardMessage) cardDetail {
	contacts := make([]contactRow, 0, len(card.Contacts))
	for _, c := range card.Contacts {
		contacts = append(contacts, contactRow{
			Email:  c.Email,
			Phone:  c.Phone,
			Role:   c.Role,
			Source: provenanceView{Source: c.SourceURL, Date: formatDate(c.ExtractedAt)},
			Method: c.Method,
		})
	}

	stage := string(card.Stage)
	detail := cardDetail{
		ID:          card.ID,
		Name:        card.Name,
		Description: card.Description,
		Socials:     card.Socials,
		Source:      provenanceView{Source: card.SourceURL, Date: formatDate(card.ExtractedAt)},
		StageBadge:  statusBadge{Label: stage, Class: components.StatusClass(stage)},
		Stage:       stage,
		StagePoints: stageTimeline(stage),
		Contacts:    contacts,
	}
	if card.SnoozeUntil != nil && time.Now().UTC().Before(*card.SnoozeUntil) {
		detail.Snoozed = true
	}

	rows := make([]historyRow, 0, len(history))
	for _, m := range history {
		rows = append(rows, historyRow{
			Recipient: m.Recipient,
			Template:  m.Template,
			Status:    statusBadge{Label: m.Status, Class: components.StatusClass(m.Status)},
			Reason:    m.Reason,
			Created:   formatDate(m.CreatedAt),
			Points:    messageTimeline(m.Status, m.Reason),
		})
	}
	detail.History = rows
	return detail
}

// funnelStageRows отображает стадии воронки в колонки для канбан-представления.
// Стадия без лидов тоже показывается (пустая колонка).
func funnelStageRows(stages []app.FunnelStage) []funnelStageRow {
	rows := make([]funnelStageRow, 0, len(stages))
	for _, s := range stages {
		leads := make([]funnelLeadRow, 0, len(s.Leads))
		for _, l := range s.Leads {
			leads = append(leads, funnelLeadRow{ID: l.ID, Name: l.Name})
		}
		rows = append(rows, funnelStageRow{
			Stage: string(s.Stage),
			Label: stageLabel(s.Stage),
			Count: len(leads),
			Leads: leads,
		})
	}
	return rows
}

// funnelLeadIDs собирает идентификаторы всех лидов активной воронки.
func funnelLeadIDs(stages []app.FunnelStage) []string {
	var ids []string
	for _, s := range stages {
		for _, l := range s.Leads {
			ids = append(ids, l.ID)
		}
	}
	return ids
}

// stageLabel — читаемая подпись стадии воронки для колонки.
func stageLabel(stage domain.Stage) string {
	switch stage {
	case domain.StageNotWarmed:
		return "Не прогрет"
	case domain.StageEmailSent:
		return "Письмо отправлено"
	case domain.StageWarming:
		return "Прогрев"
	case domain.StageCold:
		return "Холодный"
	default:
		return string(stage)
	}
}

// stageTimeline строит прямой таймлайн стадий воронки с отметкой текущей
// стадии. Стадии вне известного набора не подсвечиваются, но список остаётся
// полным: таймлайн показывает модель, а не подсказку.
func stageTimeline(stage string) []timelinePoint {
	order := []struct{ value, label string }{
		{string(domain.StageNotWarmed), "Не прогрет"},
		{string(domain.StageEmailSent), "Письмо отправлено"},
		{string(domain.StageWarming), "Прогрев"},
		{string(domain.StageCold), "Холодный"},
	}
	current := -1
	for i, s := range order {
		if s.value == stage {
			current = i
			break
		}
	}

	points := make([]timelinePoint, 0, len(order))
	for i, s := range order {
		state := "pending"
		switch {
		case current < 0:
			state = "pending"
		case i < current:
			state = "done"
		case i == current:
			state = "current"
		}
		points = append(points, timelinePoint{Label: s.label, State: state})
	}
	return points
}

// messageTimeline строит таймлайн доставки письма: очередь -> доставка.
// Терминальные исходы (failed/bounced/unsubscribed/rejected) помечают шаг
// доставки сбоем, а причина уходит в детали точки при наведении. Новые данные у
// домена не запрашиваются — таймлайн отражает существующие статус и причину.
func messageTimeline(status, reason string) []timelinePoint {
	queued := timelinePoint{Label: "В очереди", State: "pending"}
	delivered := timelinePoint{Label: "Доставлено", State: "pending"}

	switch domain.MessageStatus(status) {
	case domain.MessageStatusQueued:
		queued.State = "current"
	case domain.MessageStatusSent:
		queued.State = "done"
		delivered.State = "done"
	case domain.MessageStatusFailed:
		queued.State = "done"
		delivered.State, delivered.Label = "fail", "Отказ"
	case domain.MessageStatusBounced:
		queued.State = "done"
		delivered.State, delivered.Label = "fail", "Возврат"
	case domain.MessageStatusUnsubscribed:
		queued.State = "done"
		delivered.State, delivered.Label = "fail", "Отписка"
	case domain.MessageStatusRejected:
		queued.State = "done"
		delivered.State, delivered.Label = "fail", "Отвергнут"
	default:
		queued.State = "done"
		delivered.State = "done"
	}

	if strings.TrimSpace(reason) != "" {
		delivered.Detail = reason
	}
	return []timelinePoint{queued, delivered}
}

// resultCount отображает число найденных компаний либо прочерк.
func resultCount(ref []string) string {
	if len(ref) == 0 {
		return "—"
	}
	return strconv.Itoa(len(ref))
}

// formatDate форматирует дату извлечения; нулевая дата — пустая строка.
func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(dateFormat)
}

// firecrawlStatus — модель остатка аккаунта Firecrawl для главной страницы.
type firecrawlStatus struct {
	Configured       bool
	CreditsAvailable bool
	Credits          string
	CreditsUpdated   string
	TokensAvailable  bool
	Tokens           string
}

// firecrawlStatusFrom читает остаток аккаунта из reader. nil означает «бюджет не
// настроен» — возвращается пустая модель, и блок не отображается.
func firecrawlStatusFrom(a budget.AccountReader) firecrawlStatus {
	if a == nil {
		return firecrawlStatus{}
	}
	s := firecrawlStatus{Configured: true}
	if c := a.CreditBalance(); c.Available {
		s.CreditsAvailable = true
		s.Credits = strconv.Itoa(c.Value)
		s.CreditsUpdated = formatDateTime(c.UpdatedAt)
	}
	if t := a.TokenBalance(); t.Available {
		s.TokensAvailable = true
		s.Tokens = strconv.Itoa(t.Value)
	}
	return s
}

// formatDateTime форматирует момент времени с часами; нулевое время — пустая строка.
func formatDateTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

// budgetText отображает бюджет задачи: расход, резерв и доступно. nil — пустая
// строка (бюджет не применялся).
func budgetText(b *budget.Snapshot) string {
	if b == nil {
		return ""
	}
	return fmt.Sprintf("расход %d · резерв %d · доступно %d", b.ConfirmedSpentCredits, b.ReservedCredits, b.Available)
}

// assistantView — модель экрана диалога: лента разговора, черновик плана,
// согласованные в диалоге лимиты и скрытые значения для подтверждённого запуска.
// История и черновик плана приходят от клиента и уезжают обратно в скрытых полях,
// поэтому сервер остаётся stateless. Лимиты согласуются в самом диалоге, а не в
// отдельной форме-панели (design.md, Решение 6).
type assistantView struct {
	CSRF     string
	History  string
	PlanJSON string
	Messages []dialogueMessageRow
	Targets  []dialogueTargetRow
	Limits   []planLimitChip
	Ready    bool
	Error    string

	// Interpretation — критерии релевантности запроса (целевой лид, признаки
	// потребности, исключения, признаки гипотезы), ОТДЕЛЬНЫЕ от технического плана
	// обхода. Нулевое значение означает, что интерпретация не определена: критерии
	// не выдумываются (search-strategy, требование интерпретации).
	Interpretation interpretationView

	// Message — черновик поля «Ваш ответ». При отказе запуска он возвращается в
	// textarea, чтобы оператор не терял набранный ход вместе с диалогом.
	Message string

	// Скрытые значения операторских лимитов, которые уезжают на подтверждённый
	// запуск. Это недоверенный ввод: сервер проверяет его тем же limitsFromForm.
	MaxDepth       string
	MaxTimeSeconds string

	// Редактируемые операторские параметры задания с целью: целевое число
	// компаний («Сколько найти»), лимит результатов поиска («Результатов на
	// запрос») и ручные цели («Свои цели», по строке — запрос или URL). Значения
	// приходят из формы и проверяются как недоверенный ввод.
	TargetCompanies string
	MaxResults      string
	ManualTargets   string

	// Скрытые начальные значения короткой проверочной сессии (design.md,
	// Решение 10). Также недоверенный ввод: проверяются probeFromForm.
	ProbeDepth       string
	ProbeTimeSeconds string
	ProbeMaxCredits  string
}

// interpretationView — представление того, как система поняла бизнес-запрос.
// Defined отличает «интерпретация не определена» от пустых значений: пустота не
// выдаётся за понятые критерии. Uncertainty — явная неопределённость: данных для
// критериев недостаточно, и система их не выдумывает.
type interpretationView struct {
	Defined           bool
	Uncertainty       bool
	Reason            string
	TargetLead        string
	NeedSignals       []string
	Exclusions        []string
	HypothesisSignals []string
}

// interpretationFrom отображает интерпретацию запроса. nil даёт неопределённое
// представление: критерии не подставляются и не выдумываются. Reason несёт
// причину, по которой критерии не определены (пусто, если сбоя не было).
func interpretationFrom(i *llm.Interpretation) interpretationView {
	if i == nil {
		return interpretationView{}
	}
	return interpretationView{
		Defined:           true,
		Uncertainty:       i.Uncertainty,
		Reason:            i.Reason,
		TargetLead:        i.TargetLead,
		NeedSignals:       i.NeedSignals,
		Exclusions:        i.Exclusions,
		HypothesisSignals: i.HypothesisSignals,
	}
}

// planLimitChip — чип согласованного в диалоге технического параметра: подпись и
// уже отформатированное значение. Чип показывает готовые строки — шаблон ничего не
// вычисляет.
type planLimitChip struct {
	Label string
	Value string
}

// planLimitChips собирает чипы операторских лимитов из согласованного в диалоге
// ответа: пустые параметры не показываются. Число страниц и пути в план больше не
// входят (design.md, Решение 5).
func planLimitChips(l llm.PlanLimits) []planLimitChip {
	chips := make([]planLimitChip, 0, 2)
	if l.MaxDepth > 0 {
		chips = append(chips, planLimitChip{Label: "Глубина", Value: strconv.Itoa(l.MaxDepth)})
	}
	if l.MaxTimeSeconds > 0 {
		chips = append(chips, planLimitChip{Label: "Время, с", Value: strconv.Itoa(l.MaxTimeSeconds)})
	}
	return chips
}

// dialogueMessageRow — строка ленты диалога (роль agent/operator).
type dialogueMessageRow struct {
	Role string
	Text string
}

// dialogueTargetRow — строка панели плана: вид цели и её значение.
type dialogueTargetRow struct {
	Kind  string
	Value string
}

// forumSignalsView — модель экрана форумных сигналов: форма запроса и, после
// поиска, список обезличенных сигналов.
type forumSignalsView struct {
	QueryField components.Field
	Query      string
	Signals    []forumSignalRow
}

// forumSignalRow — строка списка сигналов.
type forumSignalRow struct {
	Topic   string
	Summary string
	URL     string
	Date    string
}

// forumSignalRows отображает сигналы в строки экрана с уже отформатированной
// датой.
func forumSignalRows(signals []forum.Signal) []forumSignalRow {
	rows := make([]forumSignalRow, 0, len(signals))
	for _, s := range signals {
		rows = append(rows, forumSignalRow{
			Topic:   s.Topic,
			Summary: s.Summary,
			URL:     s.SourceURL,
			Date:    formatDate(s.Date),
		})
	}
	return rows
}

// templateItems отображает шаблоны прикладного слоя в строки списка. hrefFor
// строит адрес строки (правка или выбор шаблона), selected помечает выбранный.
func templateItems(templates []app.TemplateView, hrefFor func(id string) string, selected string) []templateItem {
	items := make([]templateItem, 0, len(templates))
	for _, t := range templates {
		items = append(items, templateItem{
			ID:       t.ID,
			Name:     t.Name,
			Subject:  t.Subject,
			Revision: t.Revision,
			Href:     hrefFor(t.ID),
			Selected: t.ID == selected,
		})
	}
	return items
}

// templateFormOptions объединяет каталог переменных с объявленными у шаблона:
// каждая переменная каталога получает признак объявления и текущий fallback.
func templateFormOptions(catalog []app.VariableOption, declared []app.TemplateVariable) []templateVariableOption {
	fallbacks := make(map[string]string, len(declared))
	for _, variable := range declared {
		fallbacks[variable.Key] = variable.Fallback
	}
	options := make([]templateVariableOption, 0, len(catalog))
	for _, option := range catalog {
		fallback, ok := fallbacks[option.Key]
		options = append(options, templateVariableOption{
			Key:      option.Key,
			Label:    option.Label,
			Source:   option.Source,
			Declared: ok,
			Fallback: fallback,
		})
	}
	return options
}

// templateValueViews отображает значения объявленных переменных в модель
// редактируемых полей предпросмотра.
func templateValueViews(values []app.TemplateValue) []templateValueView {
	views := make([]templateValueView, 0, len(values))
	for _, value := range values {
		views = append(views, templateValueView{
			Key:      value.Key,
			Label:    value.Label,
			Value:    value.Value,
			Fallback: value.Fallback,
		})
	}
	return views
}
