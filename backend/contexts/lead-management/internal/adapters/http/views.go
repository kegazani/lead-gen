package httpapi

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
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

// Ключи экранов раздела «Лиды» для внутриэкранной панели переходов.
const (
	sidebarHome   = "home"
	sidebarCards  = "cards"
	sidebarLeads  = "leads"
	sidebarFunnel = "funnel"
)

// consoleSidebar собирает ОБЩУЮ навигацию консоли: канонические разделы
// (Заявки, Исполнение, Лиды, Отчёты, Рассылки) с активным разделом «Лиды». Вторых
// групп экранов сервиса здесь нет: вторичные переходы раздела живут во
// внутриэкранной панели (consoleSubnav). Поэтому состав и порядок сайдбара
// одинаковы на всех экранах консоли лидов и не меняются при переходах.
// Активный тенант и текущий оператор переносятся в ссылки, чтобы при переходе в
// консоль другого сервиса выбранный тенант не терялся.
func (s *Server) consoleSidebar(tenant, actor string) components.Nav {
	ctx := components.ConsoleContext{TenantID: tenant, Actor: actor}
	return components.ConsoleNavWithContext(s.console, components.ConsoleSectionLeads, ctx)
}

// consoleSubnav собирает внутриэкранную панель переходов по экранам раздела
// «Лиды» (Главная, Карточки, Лиды, Воронка). Активный экран подсвечивается;
// ссылки несут активные tenant_id и actor, чтобы контекст тенанта и оператора не
// терялся при переходах между экранами раздела. Панель не входит в общий сайдбар
// и не меняет его состав.
func (s *Server) consoleSubnav(active, tenant, actor string) *components.Tabs {
	ctx := components.ConsoleContext{TenantID: tenant, Actor: actor}
	return &components.Tabs{
		Items: []components.Tab{
			{Key: sidebarHome, Label: "Главная", Href: components.ConsoleHrefWithContext("/", ctx)},
			{Key: sidebarCards, Label: "Карточки", Href: components.ConsoleHrefWithContext("/cards", ctx)},
			{Key: sidebarLeads, Label: "Лиды", Href: components.ConsoleHrefWithContext("/leads", ctx)},
			{Key: sidebarFunnel, Label: "Воронка", Href: components.ConsoleHrefWithContext("/funnel", ctx)},
		},
		Active: active,
	}
}

// pageSection собирает страницу экрана раздела «Лиды»: общая навигация консоли
// без вторичных групп и внутриэкранная панель переходов по экранам раздела с
// подсвеченным активным экраном active.
func (s *Server) pageSection(r *http.Request, title, active string, view any) components.Page {
	tenant, actor := s.activeTenant(r), consoleActor(r)
	return pageViewSidebar(title, s.consoleSidebar(tenant, actor), s.consoleSubnav(active, tenant, actor), view)
}

// pageConsole собирает страницу экрана консоли лидов без внутриэкранной панели:
// общая навигация та же, панель не показывается на экранах, не входящих в раздел
// «Лиды» (отправка, библиотека шаблонов, ассистент, форумы).
func (s *Server) pageConsole(r *http.Request, title string, view any) components.Page {
	tenant, actor := s.activeTenant(r), consoleActor(r)
	return pageViewSidebar(title, s.consoleSidebar(tenant, actor), nil, view)
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

// pageViewSidebar собирает страницу с боковой навигацией, необязательной
// внутриэкранной панелью переходов раздела и моделью экрана. nil-панель означает
// экран без вторичной навигации.
func pageViewSidebar(title string, nav components.Nav, subnav *components.Tabs, view any) components.Page {
	return components.Page{
		Layout: components.Layout{
			Title:            title,
			Stylesheets:      consoleStylesheets,
			Scripts:          consoleScripts,
			BootstrapScripts: consoleBootstrapScripts,
		},
		Sidebar: nav,
		Subnav:  subnav,
		View:    view,
	}
}

// loginView — модель страницы входа.
type loginView struct {
	Live  components.Live
	Error string
}

// dashboardView — модель главной страницы: форма поиска, журнал запусков и
// последние добавленные лиды.
type dashboardView struct {
	Live       components.Live
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
	Live    components.Live
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
	Live  components.Live
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
	Live     components.Live
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
	AutoRefresh bool
	// Companies, Distribution, Fits — оценки в контексте этого задания.
	Companies    []jobCompanyRow
	Distribution verdictDistribution
	Fits         []jobFitRow
	Points       []timelinePoint
	// Selection — сводка отбора: цель/найдено/недобор, охват поиска и воронка.
	Selection selectionSummary
	// Categories — причины отсева, сгруппированные по свидетельствам задания.
	Categories []rejectionCategory
	// Diagnosis — диагноз недостигнутой цели.
	Diagnosis targetDiagnosis
	// CSRF — токен формы переоценки. Пусто, если форма не показывается.
	CSRF string
	// CanReassess — задание терминально и у него есть оценки: доступна переоценка
	// найденных компаний по актуальным критериям без повторного обхода.
	CanReassess bool
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

// selectionSummary — проекция «Сводка отбора» экрана задания: цель по лидам,
// найдено и недобор, охват поиска и воронка отбора. Значения берутся только из
// уже наблюдаемых данных задания; недоступное значение не подменяется нулём
// (design.md, Решения 1–2).
type selectionSummary struct {
	// ProgressKnown — прогресс задания наблюдался; без него охват недоступен.
	ProgressKnown bool
	// TargetSet — цель по числу компаний задана (TargetCompanies > 0).
	TargetSet bool
	Target    int
	Found     int
	// Shortfall — недобор (цель − найдено) при заданной и недостигнутой цели;
	// при незаданной или достигнутой цели не рассчитывается (0, но TargetSet
	// показывает, задана ли цель, поэтому ноль не выдаётся за недобор).
	Shortfall int
	// Reached — цель достигнута (при заданной цели).
	Reached          bool
	Pages            int
	PlannedTargets   int
	PlannedURLs      int
	ParseErrors      int
	AssessmentErrors int
	TokensSpent      int
	CreditsSpent     int
	CreditsAvailable int
	// CreditsKnown — расход/остаток кредитов наблюдался (иначе не показывается).
	CreditsKnown bool
	StopReason   string
	Funnel       []funnelPoint
}

// funnelPoint — точка воронки отбора: подпись, число компаний и признак
// доступности. Недоступный этап показывается как недоступный, а не выдуманным
// нулём (design.md, Решение 2; AGENTS.md §3).
type funnelPoint struct {
	Label string
	Count int
	Known bool
}

// rejectionCategory — категория причин отсева задания: подпись, число компаний,
// переход к списку карточек категории и подгруппы по формулировке причины
// (установленное несоответствие — по Mismatch; недостающее свидетельство — по
// Missing).
type rejectionCategory struct {
	Label  string
	Count  int
	Href   string
	Groups []rejectionGroup
}

// rejectionGroup — подгруппа причин отсева по конкретной формулировке: подпись,
// число компаний и переход к списку карточек этой формулировки. Пустая
// формулировка группу не создаёт.
type rejectionGroup struct {
	Label string
	Count int
	Href  string
}

// targetDiagnosis — диагноз недостигнутой цели задания, собранный из наблюдаемых
// полей прогресса. Пустая причина означает, что однозначную причину выделить
// нельзя: экран покажет остаток и счётчики без домысла.
type targetDiagnosis struct {
	// Showed — цель задана и не достигнута: есть что объяснять.
	Showed bool
	// Reason — наблюдаемая причина недобора (пусто — причина не определена).
	Reason string
	// Remainder — наблюдаемый остаток (цель − найдено), не отрицательный.
	Remainder int
}

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

// cardsView — модель списка карточек: четыре группы состава результатов
// («Подходит»/«Гипотеза»/«Не подходит»/«Не оценено») со счётчиками, признак
// контекста задания и активный фильтр по причине отсева. Группа выбирается на
// клиенте (переключатель состава), но счётчики считает сервер — список остаётся
// работоспособным без JavaScript. JobScoped отмечает, что список открыт в
// контексте задания, поэтому несёт его оценки.
type cardsView struct {
	Rows       []cardRow
	TenantID   string
	Live       components.Live
	Fits       []cardRow
	Hypothesis []cardRow
	Unsuitable []cardRow
	Unassessed []cardRow
	Counts     cardsCounts
	JobID      string
	JobQuery   string
	JobScoped  bool
	// Filter — активный фильтр по причине отсева; nil — фильтр не задан.
	Filter *cardsFilter
}

// cardsCounts — число карточек в каждой группе состава. Равно числу карточек,
// показанных при открытии группы (и при активном фильтре — числу отобранных).
type cardsCounts struct {
	Fits       int
	Hypothesis int
	Unsuitable int
	Unassessed int
}

func (view cardsView) Total() int {
	if !view.JobScoped {
		return len(view.Rows)
	}
	return view.Counts.Total()
}

func (counts cardsCounts) Total() int {
	return counts.Fits + counts.Hypothesis + counts.Unsuitable + counts.Unassessed
}

// cardsFilter — активный фильтр списка карточек по причине отсева: значение
// параметра (категория), необязательная точная формулировка причины и подпись с
// ссылкой снятия фильтра. Снятие фильтра возвращает полный список.
type cardsFilter struct {
	// Reason — категория причины (mismatch/missing/no-email/unassessed).
	Reason string
	// Detail — точная формулировка причины для mismatch/missing (пусто — любая).
	Detail string
	// Label — подпись фильтра для экрана (данные представления, не шаблон).
	Label string
	// Href — ссылка снятия фильтра на полный список (с сохранением контекста).
	Href string
}

// Категории фильтра по причине отсева.
const (
	filterReasonMismatch   = "mismatch"
	filterReasonMissing    = "missing"
	filterReasonNoEmail    = "no-email"
	filterReasonUnassessed = "unassessed"
)

// Группы состава результатов списка карточек.
const (
	cardGroupFits       = "fits"
	cardGroupHypothesis = "hypothesis"
	cardGroupUnsuitable = "unsuitable"
	cardGroupUnassessed = "unassessed"
)

// cardRow — карточка списка: общая модель обоих представлений (плитки и строки).
// Description остаётся полным описанием для главной страницы, ShortDescription —
// краткое описание для витрины карточек, Email и NoEmail описывают наличие адреса.
// Group задаёт состав (группу) карточки: «Подходит»/«Гипотеза»/«Не подходит»/
// «Не оценено». VerdictLabel — подпись вердикта; без оценки — «Не оценено».
type cardRow struct {
	Phone            string
	ContactSourceURL string
	HasContacts      bool
	ID               string
	Name             string
	Email            string
	NoEmail          bool
	Description      string
	ShortDescription string
	Group            string
	VerdictLabel     string
	VerdictClass     string
	// Mismatches/Missings — формулировки несоответствий и недостающих
	// свидетельств оценки этой карточки в контексте (задания или последней).
	// Служат фильтру по причине: причина, которой нет у карточки, её не выбирает.
	Mismatches []string
	Missings   []string
}

// leadsView — модель страницы всех сохранённых лидов.
type leadsView struct {
	TenantID string
	Live     components.Live
	Rows     []leadRow
}

// leadRow — строка сохранённого лида: ссылка на сайт, домен для подписи,
// название и адрес.
type leadRow struct {
	Phone            string
	ContactSourceURL string
	HasContacts      bool
	CardHref         string
	Site             string
	Domain           string
	Name             string
	Email            string
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
	Live    components.Live
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
	ID               string
	Name             string
	Description      string
	Socials          []string
	Source           provenanceView
	StageBadge       statusBadge
	Stage            string
	StagePoints      []timelinePoint
	Snoozed          bool
	Contacts         []contactRow
	RejectedContacts []contactRow
	History          []historyRow
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

// contactRow — контакт компании с происхождением, способом извлечения и бейджем
// верификации адреса (7.2).
type contactRow struct {
	Rejection    string
	Email        string
	Phone        string
	Role         string
	Source       provenanceView
	Method       string
	Verification template.HTML
}

// provenanceView — модель происхождения для шаблона provenance: источник и уже
// отформатированная дата.
type provenanceView struct {
	Source string
	Date   string
}

// funnelView — модель канбан-воронки: колонки по стадиям и CSRF-токен.
type funnelView struct {
	Live   components.Live
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
			Badge:      statusBadge{Label: jobStatusLabel(j.Status), Class: components.StatusClass(string(j.Status)), Title: statusTitle(j)},
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
// чужого задания в эту модель не попадает. assessmentsKnown отмечает
// доступность хранилища оценок: недоступный этап воронки показывается как
// недоступный, а не нулём. links переносит контекст тенанта/оператора в переходы
// к отфильтрованным спискам.
func jobFrom(detail app.JobDetail, assessments []*domain.LeadAssessment, assessmentsKnown bool, emails map[string]app.CompanyEmail, links components.ConsoleContext) jobView {
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

	selection := selectionSummaryFrom(j, distribution, emails != nil, emailsWithAddress(j.ResultRef, emails), assessmentsKnown)
	return jobView{
		Live:         consoleLive,
		ID:           j.ID,
		Query:        j.Query,
		Badge:        statusBadge{Label: jobStatusLabel(j.Status), Class: components.StatusClass(string(j.Status)), Title: statusTitle(j)},
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
		Selection:    selection,
		Categories:   rejectionCategories(j, byCompany, emails, selection.AssessmentErrors, links),
		Diagnosis:    targetDiagnosisFrom(j.Progress, distribution),
	}
}

// selectionSummaryFrom собирает сводку отбора задания из наблюдаемых данных:
// цель/найдено/недобор, охват поиска и воронку. Найдено — число компаний
// результата; недобор рассчитывается только при заданной и недостигнутой цели и
// не подменяется нулём при незаданной (design.md, Решение 5).
func selectionSummaryFrom(j ports.SearchJob, distribution verdictDistribution, emailsKnown bool, emailCount int, assessmentsKnown bool) selectionSummary {
	found := len(j.ResultRef)
	s := selectionSummary{Found: found}
	if p := j.Progress; p != nil {
		s.ProgressKnown = true
		s.Pages = p.PagesCrawled
		s.PlannedTargets = p.PlannedTargets
		s.PlannedURLs = p.PlannedURLs
		s.ParseErrors = p.ParseErrors
		s.AssessmentErrors = p.AssessmentErrors
		s.TokensSpent = p.TokensSpent
		s.CreditsSpent = p.CreditsSpent
		s.CreditsAvailable = p.CreditsAvailable
		s.CreditsKnown = p.CreditsSpent != 0 || p.CreditsAvailable != 0
		s.StopReason = strings.TrimSpace(p.StopReason)
		s.TargetSet = p.TargetCompanies > 0
		s.Target = p.TargetCompanies
		s.Reached = p.TargetReached
		if s.TargetSet && !p.TargetReached {
			s.Shortfall = p.TargetCompanies - found
			if s.Shortfall < 0 {
				s.Shortfall = 0
			}
		}
	}
	s.Funnel = funnelPoints(found, emailsKnown, emailCount, distribution, assessmentsKnown)
	return s
}

// emailsWithAddress считает компании результата с непустым адресом. При
// недоступной карте контактов (nil) возвращает 0: значение используется только
// вместе с признаком доступности.
func emailsWithAddress(ids []string, emails map[string]app.CompanyEmail) int {
	if emails == nil {
		return 0
	}
	n := 0
	for _, id := range ids {
		if contact, ok := emails[id]; ok && contact.Email != "" {
			n++
		}
	}
	return n
}

// funnelPoints строит воронку отбора: найдено → проверено email → оценено →
// вердикты → писем → отправлено → ответили. Этап без доступных на уровне
// задания данных помечается недоступным, а не выдуманным нулём: стадии «писем»,
// «отправлено» и «ответили» принадлежат другим поверхностям и на уровне задания
// недоступны.
func funnelPoints(found int, emailsKnown bool, emailCount int, distribution verdictDistribution, assessmentsKnown bool) []funnelPoint {
	points := []funnelPoint{{Label: "Найдено", Count: found, Known: true}}
	points = append(points, funnelPoint{Label: "Email проверен", Count: emailCount, Known: emailsKnown})
	if assessmentsKnown {
		points = append(points,
			funnelPoint{Label: "Оценено", Count: distribution.Total(), Known: true},
			funnelPoint{Label: "Подходит", Count: distribution.Fits, Known: true},
			funnelPoint{Label: "Гипотеза", Count: distribution.Hypothesis, Known: true},
			funnelPoint{Label: "Не подходит", Count: distribution.Unsuitable, Known: true},
		)
	} else {
		points = append(points,
			funnelPoint{Label: "Оценено"},
			funnelPoint{Label: "Подходит"},
			funnelPoint{Label: "Гипотеза"},
			funnelPoint{Label: "Не подходит"},
		)
	}
	points = append(points,
		funnelPoint{Label: "Писем"},
		funnelPoint{Label: "Отправлено"},
		funnelPoint{Label: "Ответили"},
	)
	return points
}

// targetDiagnosisFrom собирает диагноз недостигнутой цели по приоритету
// наблюдаемых причин: причина остановки → счётчики ошибок разбора/оценки →
// распределение вердиктов («всё отсеялось оценкой»). Однозначная причина не
// выдумывается: если ни один признак её не даёт, Reason остаётся пустой, а экран
// показывает остаток и счётчики (design.md, Решение 5).
func targetDiagnosisFrom(p *ports.SearchJobProgress, distribution verdictDistribution) targetDiagnosis {
	if p == nil || p.TargetCompanies <= 0 || p.TargetReached {
		return targetDiagnosis{}
	}
	remainder := p.TargetCompanies - p.CompaniesFound
	if remainder < 0 {
		remainder = 0
	}
	d := targetDiagnosis{Showed: true, Remainder: remainder}
	switch {
	case strings.TrimSpace(p.StopReason) != "":
		d.Reason = strings.TrimSpace(p.StopReason)
	case p.ParseErrors > 0 || p.AssessmentErrors > 0:
		parts := make([]string, 0, 2)
		if p.ParseErrors > 0 {
			parts = append(parts, fmt.Sprintf("ошибок разбора %d", p.ParseErrors))
		}
		if p.AssessmentErrors > 0 {
			parts = append(parts, fmt.Sprintf("ошибок оценки %d", p.AssessmentErrors))
		}
		d.Reason = "часть страниц не разобрана или не оценена: " + strings.Join(parts, ", ")
	case distribution.Total() > 0 && distribution.Fits == 0:
		d.Reason = "найденные компании отсеяны оценкой: подходящих нет"
	}
	return d
}

// rejectionCategories группирует причины отсева компаний задания по сохранённым
// свидетельствам: «Не подходит» — по установленному несоответствию (Mismatch),
// «Гипотеза» — по недостающему свидетельству (Missing), «нет email / email
// недоступен» — по контактам, «ошибки оценки» — по счётчику прогресса. Компании
// без свидетельства причина не назначается; каждой категории и подгруппе дан
// переход к списку карточек с параметром причины (design.md, Решение 3).
func rejectionCategories(j ports.SearchJob, byCompany map[string]*domain.LeadAssessment, emails map[string]app.CompanyEmail, assessmentErrors int, links components.ConsoleContext) []rejectionCategory {
	unsuitable, hypothesis, noEmail := 0, 0, 0
	mismatchCounts := map[string]int{}
	missingCounts := map[string]int{}
	for _, id := range j.ResultRef {
		if a := byCompany[id]; a != nil {
			switch a.Verdict() {
			case domain.VerdictUnsuitable:
				unsuitable++
				for _, m := range distinctReasons(a.Evidence(), func(e domain.Evidence) string { return e.Mismatch }) {
					mismatchCounts[m]++
				}
			case domain.VerdictHypothesis:
				hypothesis++
				for _, m := range distinctReasons(a.Evidence(), func(e domain.Evidence) string { return e.Missing }) {
					missingCounts[m]++
				}
			}
		}
		if emails != nil {
			if contact, ok := emails[id]; ok && (contact.NoEmail || contact.Email == "") {
				noEmail++
			}
		}
	}

	categories := make([]rejectionCategory, 0, 4)
	if unsuitable > 0 || len(mismatchCounts) > 0 {
		categories = append(categories, rejectionCategory{
			Label:  "Не подходит",
			Count:  unsuitable,
			Href:   cardsFilterHref(j.ID, filterReasonMismatch, "", links),
			Groups: reasonGroups(mismatchCounts, filterReasonMismatch, j.ID, links),
		})
	}
	if hypothesis > 0 || len(missingCounts) > 0 {
		categories = append(categories, rejectionCategory{
			Label:  "Гипотеза",
			Count:  hypothesis,
			Href:   cardsFilterHref(j.ID, filterReasonMissing, "", links),
			Groups: reasonGroups(missingCounts, filterReasonMissing, j.ID, links),
		})
	}
	if noEmail > 0 {
		categories = append(categories, rejectionCategory{
			Label: "нет email / email недоступен",
			Count: noEmail,
			Href:  cardsFilterHref(j.ID, filterReasonNoEmail, "", links),
		})
	}
	if assessmentErrors > 0 {
		// Счётчик неудачных оценок — наблюдаемое число; переход ведёт к списку
		// компаний без оценки (это след ошибки оценки), поэтому число группы и
		// размер списка могут расходиться — одно не выдаётся за другое.
		categories = append(categories, rejectionCategory{
			Label: "ошибки оценки",
			Count: assessmentErrors,
			Href:  cardsFilterHref(j.ID, filterReasonUnassessed, "", links),
		})
	}
	return categories
}

// distinctReasons возвращает различные непустые формулировки причины из
// свидетельств (Mismatch или Missing), без повторов: одна компания добавляет в
// подгруппу каждую свою причину по одному разу.
func distinctReasons(evidence []domain.Evidence, pick func(domain.Evidence) string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(evidence))
	for _, e := range evidence {
		value := strings.TrimSpace(pick(e))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// reasonGroups сортирует подгруппы по числу компаний (по убыванию) и подписи,
// чтобы вывод был детерминирован.
func reasonGroups(counts map[string]int, reason, jobID string, links components.ConsoleContext) []rejectionGroup {
	if len(counts) == 0 {
		return nil
	}
	labels := make([]string, 0, len(counts))
	for label := range counts {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool {
		if counts[labels[i]] != counts[labels[j]] {
			return counts[labels[i]] > counts[labels[j]]
		}
		return labels[i] < labels[j]
	})
	groups := make([]rejectionGroup, 0, len(labels))
	for _, label := range labels {
		groups = append(groups, rejectionGroup{
			Label: label,
			Count: counts[label],
			Href:  cardsFilterHref(jobID, reason, label, links),
		})
	}
	return groups
}

// cardsFilterHref строит адрес списка карточек с параметром причины отсева,
// сохраняя контекст задания и выбранного тенанта/оператора.
func cardsFilterHref(jobID, reason, detail string, links components.ConsoleContext) string {
	values := url.Values{}
	if jobID != "" {
		values.Set("job", jobID)
	}
	values.Set("reason", reason)
	if detail != "" {
		values.Set("detail", detail)
	}
	return components.ConsoleHrefWithContext("/cards?"+values.Encode(), links)
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
// оценка не подставляется, состав общей витрины не делится на группы.
func cardRows(cards []app.Card) []cardRow {
	rows := make([]cardRow, 0, len(cards))
	for _, c := range cards {
		rows = append(rows, cardRow{
			Phone: c.Phone, ContactSourceURL: c.ContactSourceURL, HasContacts: len(c.Contacts) > 0,
			ID:               c.ID,
			Name:             c.Name,
			Email:            c.Email,
			NoEmail:          c.NoEmail,
			Description:      c.Description,
			ShortDescription: shortDescription(c.Description),
		})
	}
	return rows
}

// leadRows отбирает лиды с непустым email и отображает их в строки: ссылка на
// сайт (домен компании), название и адрес. Лиды без email пропускаются.
func leadRows(cards []app.Card) []leadRow {
	rows := make([]leadRow, 0, len(cards))
	for _, row := range allLeadRows(cards) {
		if row.Email != "" {
			rows = append(rows, row)
		}
	}
	return rows
}

// allLeadRows не скрывает компанию из-за отсутствующего email.
func allLeadRows(cards []app.Card) []leadRow {
	rows := make([]leadRow, 0, len(cards))
	for _, c := range cards {
		rows = append(rows, leadRow{
			Phone: c.Phone, ContactSourceURL: c.ContactSourceURL, HasContacts: len(c.Contacts) > 0,
			CardHref: cardHref(c.ID, ""),
			Site:     siteURL(c.ID),
			Domain:   siteDomain(c.ID),
			Name:     c.Name,
			Email:    c.Email,
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

// cardRowsWithAssessments накладывает на строки списка оценки задания. Группа и
// подпись карточки берутся из вердикта последней оценки в этом задании: «Подходит»,
// «Гипотеза», «Не подходит»; компания результата без оценки не выдаётся за
// «Подходит» — она попадает в группу «Не оценено». Оценки берутся только из
// переданного задания: оценка чужого задания сюда не попадает (lead-relevance,
// запрет переноса).
func cardRowsWithAssessments(cards []app.Card, byCompany map[string]*domain.LeadAssessment) []cardRow {
	return applyAssessmentGroups(cardRows(cards), byCompany)
}

// cardRowsWithLatestAssessments накладывает на строки общей витрины последнюю по
// времени оценку каждой компании по всем заданиям. Состав — по последней оценке;
// компания без оценок — в группе «Не оценено». Последняя оценка — проекция
// чтения: она не переносится в компанию как постоянное свойство и не выдаёт
// оценку одного задания за оценку другого.
func cardRowsWithLatestAssessments(cards []app.Card, latest map[string]*domain.LeadAssessment) []cardRow {
	return applyAssessmentGroups(cardRows(cards), latest)
}

// applyAssessmentGroups раскладывает готовые строки по четырём группам состава по
// вердикту оценки: «Подходит», «Гипотеза», «Не подходит», отсутствие оценки —
// «Не оценено». Подпись и класс берутся из вердикта, чтобы оба пути (задание и
// общая витрина) давали одинаковый результат. Из свидетельств собираются
// формулировки причин отсева для фильтра.
func applyAssessmentGroups(rows []cardRow, byCompany map[string]*domain.LeadAssessment) []cardRow {
	for i := range rows {
		a := byCompany[rows[i].ID]
		if a == nil {
			rows[i].Group = cardGroupUnassessed
			rows[i].VerdictLabel = "Не оценено"
			rows[i].VerdictClass = "unknown"
			continue
		}
		rows[i].VerdictLabel = verdictLabel(a.Verdict())
		rows[i].VerdictClass = verdictClass(a.Verdict())
		rows[i].Group = cardGroupForVerdict(a.Verdict())
		rows[i].Mismatches = distinctReasons(a.Evidence(), func(e domain.Evidence) string { return e.Mismatch })
		rows[i].Missings = distinctReasons(a.Evidence(), func(e domain.Evidence) string { return e.Missing })
	}
	return rows
}

// cardGroupForVerdict сопоставляет вердикт группе состава.
func cardGroupForVerdict(v domain.AssessmentVerdict) string {
	switch v {
	case domain.VerdictFits:
		return cardGroupFits
	case domain.VerdictHypothesis:
		return cardGroupHypothesis
	case domain.VerdictUnsuitable:
		return cardGroupUnsuitable
	default:
		return cardGroupUnassessed
	}
}

// groupCardRows делит строки на четыре группы состава, сохраняя порядок карточек.
// Результаты всех групп остаются в модели и не удаляются: переключение состава
// лишь меняет видимую группу.
func groupCardRows(rows []cardRow) (fits, hypothesis, unsuitable, unassessed []cardRow) {
	for _, row := range rows {
		switch row.Group {
		case cardGroupHypothesis:
			hypothesis = append(hypothesis, row)
		case cardGroupUnsuitable:
			unsuitable = append(unsuitable, row)
		case cardGroupUnassessed:
			unassessed = append(unassessed, row)
		default:
			fits = append(fits, row)
		}
	}
	return fits, hypothesis, unsuitable, unassessed
}

// filterCardRows сужает строки списка до выбранной причины отсева. Причина,
// которой у карточки нет, её не выбирает; пустой фильтр возвращает строки без
// изменений (снятие фильтра даёт полный список).
func filterCardRows(rows []cardRow, reason, detail string) []cardRow {
	if reason == "" {
		return rows
	}
	filtered := make([]cardRow, 0, len(rows))
	for _, row := range rows {
		if rowMatchesReason(row, reason, detail) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

// rowMatchesReason сообщает, соответствует ли карточка выбранной причине.
func rowMatchesReason(row cardRow, reason, detail string) bool {
	switch reason {
	case filterReasonMismatch:
		return containsReason(row.Mismatches, detail)
	case filterReasonMissing:
		return containsReason(row.Missings, detail)
	case filterReasonNoEmail:
		return row.NoEmail
	case filterReasonUnassessed:
		return row.Group == cardGroupUnassessed
	default:
		return true
	}
}

// containsReason сообщает, есть ли у карточки причина с указанной формулировкой;
// пустая формулировка означает любую причину этой категории.
func containsReason(values []string, detail string) bool {
	if len(values) == 0 {
		return false
	}
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return true
	}
	for _, value := range values {
		if value == detail {
			return true
		}
	}
	return false
}

// cardsCountsFrom считает число карточек в каждой группе состава.
func cardsCountsFrom(fits, hypothesis, unsuitable, unassessed []cardRow) cardsCounts {
	return cardsCounts{
		Fits:       len(fits),
		Hypothesis: len(hypothesis),
		Unsuitable: len(unsuitable),
		Unassessed: len(unassessed),
	}
}

// cardsFilterFrom разбирает фильтр по причине отсева из запроса. Неизвестное
// значение причины фильтром не становится (фильтр не выдумывается). Href —
// ссылка снятия фильтра на полный список с сохранением контекста задания,
// тенанта и оператора.
func (s *Server) cardsFilterFrom(r *http.Request, jobID string) *cardsFilter {
	reason := strings.TrimSpace(r.URL.Query().Get("reason"))
	if !validFilterReason(reason) {
		return nil
	}
	detail := strings.TrimSpace(r.URL.Query().Get("detail"))
	values := url.Values{}
	if jobID != "" {
		values.Set("job", jobID)
	}
	href := "/cards"
	if encoded := values.Encode(); encoded != "" {
		href += "?" + encoded
	}
	ctx := components.ConsoleContext{TenantID: s.activeTenant(r), Actor: consoleActor(r)}
	return &cardsFilter{
		Reason: reason,
		Detail: detail,
		Label:  filterLabel(reason, detail),
		Href:   components.ConsoleHrefWithContext(href, ctx),
	}
}

// validFilterReason сообщает, известна ли категория фильтра.
func validFilterReason(reason string) bool {
	switch reason {
	case filterReasonMismatch, filterReasonMissing, filterReasonNoEmail, filterReasonUnassessed:
		return true
	default:
		return false
	}
}

// filterReason возвращает категорию и формулировку активного фильтра; nil-фильтр
// даёт пустые значения (строки не сужаются).
func filterReason(filter *cardsFilter) (reason, detail string) {
	if filter == nil {
		return "", ""
	}
	return filter.Reason, filter.Detail
}

// filterLabel собирает подпись фильтра для экрана: категория и, если задана,
// точная формулировка причины.
func filterLabel(reason, detail string) string {
	base := ""
	switch reason {
	case filterReasonMismatch:
		base = "Не подходит"
	case filterReasonMissing:
		base = "Гипотеза"
	case filterReasonNoEmail:
		base = "нет email / email недоступен"
	case filterReasonUnassessed:
		base = "ошибки оценки"
	}
	if detail != "" {
		return base + ": " + detail
	}
	return base
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
			Email:        c.Email,
			Phone:        c.Phone,
			Role:         c.Role,
			Source:       provenanceView{Source: c.SourceURL, Date: formatDate(c.ExtractedAt)},
			Method:       c.Method,
			Verification: unverifiedBadge(),
		})
	}

	stage := string(card.Stage)
	detail := cardDetail{
		ID:          card.ID,
		Name:        card.Name,
		Description: card.Description,
		Socials:     card.Socials,
		Source:      provenanceView{Source: card.SourceURL, Date: formatDate(card.ExtractedAt)},
		StageBadge:  statusBadge{Label: stageLabel(card.Stage), Class: components.StatusClass(stage)},
		Stage:       stage,
		StagePoints: stageTimeline(stage),
		Contacts:    contacts,
	}
	for _, c := range card.RejectedContacts {
		detail.RejectedContacts = append(detail.RejectedContacts, contactRow{Rejection: consoleMessages().Text(components.MessageKey("lead.contacts.reject." + c.RejectionCode)), Email: c.Email, Phone: c.Phone, Role: c.Role, Source: provenanceView{Source: c.SourceURL, Date: formatDate(c.ExtractedAt)}, Method: c.Method})
	}
	if card.SnoozeUntil != nil && time.Now().UTC().Before(*card.SnoozeUntil) {
		detail.Snoozed = true
	}

	rows := make([]historyRow, 0, len(history))
	for _, m := range history {
		rows = append(rows, historyRow{
			Recipient: m.Recipient,
			Template:  m.Template,
			Status:    statusBadge{Label: messageStatusLabel(m.Status), Class: components.StatusClass(m.Status)},
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

// jobStatusLabel — читаемая подпись состояния задания поиска для бейджа. Код
// остаётся в классе бейджа (оформление), а оператор видит слово, а не код.
// Неизвестное состояние показывается как есть, чтобы новое значение не пропало.
func jobStatusLabel(status jobs.Status) string {
	switch status {
	case jobs.StatusQueued:
		return "В очереди"
	case jobs.StatusRunning:
		return "Выполняется"
	case jobs.StatusDone:
		return "Готово"
	case jobs.StatusFailed:
		return "Ошибка"
	case jobs.StatusCancelled:
		return "Отменено"
	default:
		return string(status)
	}
}

// messageStatusLabel — читаемая подпись состояния исходящего письма в истории
// переписки карточки.
func messageStatusLabel(status string) string {
	switch domain.MessageStatus(status) {
	case domain.MessageStatusQueued:
		return "В очереди"
	case domain.MessageStatusSent:
		return "Отправлено"
	case domain.MessageStatusFailed:
		return "Не отправлено"
	case domain.MessageStatusBounced:
		return "Возврат"
	case domain.MessageStatusUnsubscribed:
		return "Отписка"
	case domain.MessageStatusRejected:
		return "Отклонено"
	default:
		return status
	}
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

// formatDate форматирует дату извлечения; нулевая дата — пустая строка. Формат
// локализован общим слоём (web-frontend-architecture, срез 7, 7.1).
func formatDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return components.DefaultFormatter().Date(t)
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
// Формат локализован общим слоем (web-frontend-architecture, срез 7, 7.1).
func formatDateTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return components.DefaultFormatter().DateTime(t)
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
	Live     components.Live
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
	Live       components.Live
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
