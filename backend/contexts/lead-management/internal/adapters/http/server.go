// Package httpapi реализует HTTP-адаптер роли api контекста lead-management:
// экраны оператора — вход, главная (запуски поиска + последние лиды), воронка
// (канбан), карточка лида (статус, история переписки, отправка), отправка по
// сегменту и действия переноса/snooze.
//
// Адаптер только разбирает запрос, вызывает прикладной сценарий и отрисовывает
// шаблон; доменных правил здесь нет. Рендеринг — серверный `html/template` с
// формами POST/redirect/GET (канон web-frontend-architecture); разметка и
// фрагменты — из модуля frontend/components. Аутентификация — минимальный
// операционный слой: серверная сессия в cookie и CSRF-токен на изменяющих формах.
package httpapi

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/frontend/components"
)

// contentSecurityPolicy — политика консоли: только собственные стили и скрипты
// (HTMX — отдельный файл), обмен только со своим источником, формы на себя,
// никаких inline-скриптов/стилей. Директива connect-src нужна для HTMX и
// fetch к своему источнику: без неё она падает до default-src 'none' и режет
// авто-обновление журнала и перенос лида перетаскиванием (design.md, Решение 11).
const leadsLimit = 200 // предел списка лидов с email на странице /leads

const contentSecurityPolicy = "default-src 'none'; style-src 'self'; script-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

// journalLimit — единый предел числа заданий в журнале: и при начальной отрисовке
// главной, и при живом обновлении фрагмента /jobs (устраняет расхождение 10/50).
const journalLimit = 100

// ctxSessionKey — ключ контекста запроса с сессией оператора.
type ctxSessionKey struct{}

// Dependencies — зависимости HTTP-адаптера.
type Dependencies struct {
	RunSearch     *app.RunSearch
	Showcase      *app.Showcase
	ChangeStage   *app.ChangeLeadStage
	Snooze        *app.SnoozeLead
	SendToLead    *app.SendToLead
	SendToSegment *app.SendToSegment
	Templates     *app.Templates
	SuggestNiches *app.SuggestNiches
	Dialogue      *app.ConverseDialogue
	ForumSignals  *app.SearchForumSignals
	Account       budget.AccountReader // nil — остаток аккаунта не показывается
	Logger        observ.Logger
	Auth          AuthConfig
}

// Server — HTTP-сервер роли api. Нулевое значение непригодно: используйте
// NewServer.
type Server struct {
	runSearch     *app.RunSearch
	showcase      *app.Showcase
	changeStage   *app.ChangeLeadStage
	snooze        *app.SnoozeLead
	sendToLead    *app.SendToLead
	sendToSegment *app.SendToSegment
	templates     *app.Templates
	suggestNiches *app.SuggestNiches
	dialogue      *app.ConverseDialogue
	forumSignals  *app.SearchForumSignals
	account       budget.AccountReader
	sessions      *sessionManager
	pages         *components.Set
	leadsExport   *template.Template
	static        http.Handler
	handler       http.Handler
	logger        observ.Logger
	// interpreter — use case интерпретации запроса для экрана ассистента. nil —
	// интерпретация недоступна: экран показывает явное «не определена».
	interpreter *app.InterpretQuery
}

// NewServer собирает маршруты, шаблоны, статику и сессии консоли. Разметка
// встроена в бинарник, поэтому ошибка разбора — неисправимая ошибка сборки.
func NewServer(deps Dependencies) *Server {
	pages, err := parsePages()
	if err != nil {
		panic("httpapi: разбор шаблонов: " + err.Error())
	}
	leadsExport, err := parseLeadsExport()
	if err != nil {
		panic("httpapi: разбор шаблона выгрузки лидов: " + err.Error())
	}
	static, err := staticFiles()
	if err != nil {
		panic("httpapi: каталог статики: " + err.Error())
	}

	// Интерпретатор собирается из RunSearch, когда он есть: LLM и часы уже
	// впрыснуты composition root'ом, отдельного источника критериев не нужно.
	var interpreter *app.InterpretQuery
	if deps.RunSearch != nil {
		interpreter = &app.InterpretQuery{LLM: deps.RunSearch.LLM, Clock: deps.RunSearch.Clock}
	}

	server := &Server{
		runSearch:     deps.RunSearch,
		showcase:      deps.Showcase,
		changeStage:   deps.ChangeStage,
		snooze:        deps.Snooze,
		sendToLead:    deps.SendToLead,
		sendToSegment: deps.SendToSegment,
		templates:     deps.Templates,
		suggestNiches: deps.SuggestNiches,
		dialogue:      deps.Dialogue,
		forumSignals:  deps.ForumSignals,
		account:       deps.Account,
		sessions:      newSessionManager(deps.Auth),
		pages:         pages,
		leadsExport:   leadsExport,
		static:        static,
		logger:        deps.Logger,
		interpreter:   interpreter,
	}
	server.handler = components.SecurityHeaders(contentSecurityPolicy)(server.authMiddleware(server.routes()))
	return server
}

// ServeHTTP реализует http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// routes объявляет маршруты консоли.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /logout", s.handleLogout)
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /jobs", s.handleJobs)
	mux.HandleFunc("GET /jobs/{id}", s.handleJob)
	mux.HandleFunc("POST /jobs/{id}/cancel", s.handleCancelJob)
	mux.HandleFunc("POST /jobs/cancel", s.handleBulkCancelJobs)
	mux.HandleFunc("POST /search", s.handleSearch)
	mux.HandleFunc("GET /cards", s.handleCards)
	mux.HandleFunc("GET /leads", s.handleLeads)
	mux.HandleFunc("GET /leads/export", s.handleLeadsExport)
	mux.HandleFunc("GET /leads/export.csv", s.handleLeadsExportCSV)
	mux.HandleFunc("GET /cards/{id}", s.handleCard)
	mux.HandleFunc("POST /cards/{id}/hide", s.handleHide)
	mux.HandleFunc("POST /cards/{id}/delete", s.handleDelete)
	mux.HandleFunc("POST /cards/{id}/send", s.handleSendToLead)
	mux.HandleFunc("POST /cards/{id}/send/preview", s.handleCardPreview)
	mux.HandleFunc("POST /cards/{id}/snooze", s.handleSnooze)
	mux.HandleFunc("GET /funnel", s.handleFunnel)
	mux.HandleFunc("POST /funnel/{id}/stage", s.handleChangeStage)
	mux.HandleFunc("GET /send", s.handleSendForm)
	mux.HandleFunc("POST /send-segment", s.handleSendToSegment)
	mux.HandleFunc("POST /send/preview", s.handleSegmentPreview)
	mux.HandleFunc("GET /templates", s.handleTemplates)
	mux.HandleFunc("POST /templates", s.handleCreateTemplate)
	mux.HandleFunc("GET /templates/{id}/edit", s.handleTemplateEditForm)
	mux.HandleFunc("POST /templates/{id}", s.handleUpdateTemplate)
	mux.HandleFunc("POST /templates/{id}/delete", s.handleDeleteTemplate)
	mux.HandleFunc("GET /assistant", s.handleAssistant)
	mux.HandleFunc("POST /assistant", s.handleAssistantTurn)
	mux.HandleFunc("POST /assistant/stream", s.handleAssistantStream)
	mux.HandleFunc("GET /forum-signals", s.handleForumSignals)
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.Handle("GET /static/", http.StripPrefix("/static/", s.static))
	return mux
}

// authMiddleware требует сессию для всех маршрутов, кроме входа, пробы живости и
// статики, и проверяет CSRF-токен на изменяющих запросах. Сессия кладётся в
// контекст запроса, чтобы обработчики не аутентифицировали повторно.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/login" || path == "/healthz" || strings.HasPrefix(path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			s.redirectLogin(w, r)
			return
		}
		sess, ok := s.sessions.authenticate(cookie.Value)
		if !ok {
			s.redirectLogin(w, r)
			return
		}
		if r.Method == http.MethodPost && path != "/logout" {
			if r.FormValue("csrf") != sess.csrf {
				s.renderNotice(w, http.StatusForbidden, "Недопустимый запрос", "Отсутствует или неверен CSRF-токен.")
				return
			}
		}
		ctx := context.WithValue(r.Context(), ctxSessionKey{}, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// redirectLogin перенаправляет на страницу входа.
func (s *Server) redirectLogin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// currentSession возвращает сессию оператора из контекста запроса.
func currentSession(r *http.Request) *session {
	sess, _ := r.Context().Value(ctxSessionKey{}).(*session)
	return sess
}

// csrfToken возвращает CSRF-токен сессии (пусто, если сессии нет).
func csrfToken(r *http.Request) string {
	if sess := currentSession(r); sess != nil {
		return sess.csrf
	}
	return ""
}

// render отдаёт страницу целиком.
func (s *Server) render(w http.ResponseWriter, status int, name string, page components.Page) {
	var buffer bytes.Buffer
	if err := s.pages.Render(&buffer, name, page); err != nil {
		s.logger.Error("httpapi: рендеринг страницы", err, observ.Fields{}, "page", name)
		http.Error(w, "внутренняя ошибка", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buffer.WriteTo(w)
}

// renderFragment отдаёт именованный фрагмент без каркаса страницы.
func (s *Server) renderFragment(w http.ResponseWriter, pageName, fragmentName string, data any) {
	var buffer bytes.Buffer
	if err := s.pages.RenderFragment(&buffer, pageName, fragmentName, data); err != nil {
		s.logger.Error("httpapi: рендеринг фрагмента", err, observ.Fields{}, "fragment", fragmentName)
		http.Error(w, "внутренняя ошибка", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buffer.WriteTo(w)
}

// renderNotice отдаёт страницу-уведомление.
func (s *Server) renderNotice(w http.ResponseWriter, status int, heading, message string) {
	s.render(w, status, pageNotice, pageView(heading, components.Notice{Heading: heading, Message: message}))
}

// handleLoginForm показывает форму входа.
func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, pageLogin, pageView("Вход", loginView{}))
}

// handleLogin проверяет учётные данные и ставит cookie сессии.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректная форма", "Не удалось разобрать форму.")
		return
	}
	id, err := s.sessions.Login(r.FormValue("username"), r.FormValue("password"))
	if errors.Is(err, ErrInvalidCredentials) {
		s.render(w, http.StatusUnauthorized, pageLogin, pageView("Вход", loginView{Error: "Неверный логин или пароль."}))
		return
	}
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Вход недоступен", "Не удалось создать сессию.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleLogout удаляет сессию и перенаправляет на вход.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil {
		s.sessions.Logout(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecure(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// handleDashboard показывает главную: последние запуски поиска и последние лиды.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	dashboard, err := s.showcase.Dashboard(r.Context(), journalLimit, 10)
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Главная недоступна", "Не удалось загрузить сводку.")
		return
	}
	s.render(w, http.StatusOK, pageDashboard, pageViewSidebar("Главная", consoleSidebar(sidebarHome), dashboardView{
		QueryField:           components.Field{ID: "query", Name: "query", Label: "Запрос", Required: true},
		TargetCompaniesField: components.Field{ID: "targetCompanies", Name: "targetCompanies", Label: "Сколько найти компаний (необязательно)"},
		MaxResultsField:      components.Field{ID: "maxResults", Name: "maxResults", Label: "Результатов на запрос (необязательно)"},
		TargetsField:         components.Field{ID: "manualTargets", Name: "manualTargets", Label: "Свои цели (по строке — запрос или URL; необязательно)", Type: "textarea", Rows: 3},
		BudgetField:          components.Field{ID: "budget", Name: "budget", Label: "Бюджет кредитов Firecrawl (необязательно)"},
		CSRF:                 csrfToken(r),
		Journal:              journalFrom(dashboard.Jobs, csrfToken(r)),
		Leads:                cardRows(dashboard.Cards),
		Firecrawl:            firecrawlStatusFrom(s.account),
	}))
}

// handleJobs отдаёт HTMX-фрагмент журнала заданий.
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.showcase.ListJobs(r.Context(), journalLimit)
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Журнал недоступен", "Не удалось загрузить журнал заданий.")
		return
	}
	s.renderFragment(w, pageDashboard, "journal", journalFrom(jobs, csrfToken(r)))
}

// handleJob показывает экран подробностей задания: полный запрос, состояние,
// бюджет, найденные компании и ход выполнения по событиям жизненного цикла.
// Неизвестный идентификатор даёт нейтральное уведомление 404 без внутренних
// деталей (design.md, Решение 6).
func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	detail, err := s.showcase.Job(r.Context(), r.PathValue("id"))
	if errors.Is(err, app.ErrJobNotFound) {
		s.renderNotice(w, http.StatusNotFound, "Задание не найдено", "Запись не существует или была удалена.")
		return
	}
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Задание недоступно", "Не удалось загрузить задание.")
		return
	}
	companies := s.resultCompanies(r.Context(), detail)
	s.render(w, http.StatusOK, pageJob, pageViewSidebar("Задание", consoleSidebar(sidebarHome), jobFrom(detail, s.jobAssessments(r.Context(), detail.Job.ID), companies)))
}

// resultCompanies читает email найденных компаний одним пакетным запросом. Email —
// дополнительный признак: при ошибке чтения возвращается nil, и экран задания
// помечает email компаний как «недоступен», а не выдаёт их за компании без адреса.
func (s *Server) resultCompanies(ctx context.Context, detail app.JobDetail) map[string]app.CompanyEmail {
	if len(detail.Job.ResultRef) == 0 {
		return nil
	}
	emails, err := s.showcase.CompanyEmails(ctx, detail.Job.ResultRef)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("httpapi: чтение email компаний задания", err, observ.Fields{}, "job", detail.Job.ID)
		}
		return nil
	}
	return emails
}

// jobAssessments читает оценки задания. Оценка — дополнительный результат: её
// отсутствие или ошибка чтения не роняют экран задания, а дают пустой набор —
// распределение и основания тогда пусты, а не выдуманы.
func (s *Server) jobAssessments(ctx context.Context, jobID string) []*domain.LeadAssessment {
	if s.runSearch == nil || s.runSearch.Assessments == nil {
		return nil
	}
	assessments, err := s.runSearch.Assessments.ListByJob(ctx, jobID)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("httpapi: чтение оценок задания", err, observ.Fields{}, "job", jobID)
		}
		return nil
	}
	return assessments
}

// latestAssessments читает последнюю по времени оценку каждой из карточек
// витрины по всем заданиям. Оценка — дополнительный результат: её отсутствие или
// ошибка чтения дают пустую карту, поэтому компании показываются как «Не оценено»,
// а не выдаются за «Подходит».
func (s *Server) latestAssessments(ctx context.Context, cards []app.Card) map[string]*domain.LeadAssessment {
	if s.runSearch == nil || s.runSearch.Assessments == nil {
		return nil
	}
	companyIDs := make([]string, 0, len(cards))
	for _, card := range cards {
		companyIDs = append(companyIDs, card.ID)
	}
	byCompany, err := s.runSearch.Assessments.LatestByCompany(ctx, companyIDs)
	if err != nil {
		if s.logger != nil {
			s.logger.Error("httpapi: чтение последних оценок компаний", err, observ.Fields{})
		}
		return nil
	}
	return byCompany
}

// handleCancelJob отменяет задание поиска и возвращает на главную.
func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	if err := s.runSearch.Cancel(r.Context(), r.PathValue("id")); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Не удалось отменить", err.Error())
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleBulkCancelJobs отменяет выбранные в журнале задания одной операцией и
// возвращает на главную. Пустой выбор ничего не отменяет. Отказ по отдельному
// заданию не прерывает отмену остальных: оператор видит счётчики исходов,
// а исход уже терминальных заданий не меняется (отмена идемпотентна).
func (s *Server) handleBulkCancelJobs(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректная форма", "Не удалось разобрать форму.")
		return
	}
	ids := r.Form["ids"]
	if len(ids) == 0 {
		s.renderNotice(w, http.StatusBadRequest, "Ничего не выбрано", "Отметьте задания, которые нужно отменить.")
		return
	}

	cancelled, failed := 0, 0
	for _, id := range ids {
		if err := s.runSearch.Cancel(r.Context(), id); err != nil {
			failed++
			continue
		}
		cancelled++
	}
	if failed > 0 {
		s.renderNotice(w, http.StatusBadRequest, "Отмена неполная",
			fmt.Sprintf("Отменено %d, не удалось %d.", cancelled, failed))
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleSearch ставит задание поиска из формы и перенаправляет на главную.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректная форма", "Не удалось разобрать форму.")
		return
	}
	query := strings.TrimSpace(r.FormValue("query"))
	if query == "" {
		s.renderNotice(w, http.StatusBadRequest, "Введите запрос", "Запрос поиска не может быть пустым.")
		return
	}
	budget, err := parseBudget(r.FormValue("budget"))
	if err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректный бюджет", err.Error())
		return
	}
	targetCompanies, err := parseLimit(r.FormValue("targetCompanies"), "целевое число компаний")
	if err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректное число компаний", err.Error())
		return
	}
	maxResults, err := parseLimit(r.FormValue("maxResults"), "результатов на запрос")
	if err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректный лимит поиска", err.Error())
		return
	}
	// Ручные цели оператора (по строке — запрос или URL) разбираются и проверяются
	// как недоверенный ввод; пустой ввод означает фолбэк на планирование по запросу.
	planTargets, err := decodeManualTargets(r.FormValue("manualTargets"))
	if err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректные цели", err.Error())
		return
	}
	// Запуск с главной идёт тем же путём, что и из ассистента: запрос
	// интерпретируется, критерии сохраняются в задании отдельным полем. Ручные
	// цели берутся дословно; пустой ввод — фолбэк на планирование по запросу;
	// лимиты пустые (действуют лимиты конфигурации).
	if _, err := s.runSearch.EnqueueInterpreted(r.Context(), query, planTargets, ports.PlanLimits{}, budget, targetCompanies, maxResults); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Не удалось запустить поиск", err.Error())
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// parseBudget разбирает бюджет кредитов из формы: пустое значение — бюджет не
// задан (0, значение по умолчанию), нечисловое или неположительное — ошибка.
func parseBudget(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errors.New("бюджет должен быть целым числом")
	}
	if n <= 0 {
		return 0, errors.New("бюджет должен быть положительным числом")
	}
	return n, nil
}

// handleCards показывает таблицу карточек. Без параметра job это общая витрина:
// состава по оценкам у неё нет, поэтому все карточки в группе по умолчанию, а
// оценка из чужого задания не подставляется как свойство компании. С параметром
// job список соответствует заданию и несёт его оценки.
func (s *Server) handleCards(w http.ResponseWriter, r *http.Request) {
	cards, err := s.showcase.ListCards(r.Context(), 200)
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Витрина недоступна", "Не удалось загрузить карточки.")
		return
	}

	jobID := strings.TrimSpace(r.URL.Query().Get("job"))
	view := cardsView{}
	if jobID == "" {
		view.Fits, view.Other = splitCardRows(cardRowsWithLatestAssessments(cards, s.latestAssessments(r.Context(), cards)))
	} else {
		detail, err := s.showcase.Job(r.Context(), jobID)
		if errors.Is(err, app.ErrJobNotFound) {
			s.renderNotice(w, http.StatusNotFound, "Задание не найдено", "Запись не существует или была удалена.")
			return
		}
		if err != nil {
			s.renderNotice(w, http.StatusInternalServerError, "Задание недоступно", "Не удалось загрузить задание.")
			return
		}
		jobCards := filterCardsToJob(cards, detail.Job.ResultRef)
		byCompany := assessmentsByCompany(s.jobAssessments(r.Context(), jobID))
		view.Fits, view.Other = splitCardRows(cardRowsWithAssessments(jobCards, byCompany))
		view.JobID = jobID
		view.JobQuery = detail.Job.Query
		view.JobScoped = true
	}

	s.render(w, http.StatusOK, pageCards, pageViewSidebar("Карточки", consoleSidebar(sidebarCards), view))
}

// handleLeads показывает список лидов с email: по строке на лида — ссылка на
// сайт, название и адрес. Лиды без email в список не попадают.
func (s *Server) handleLeads(w http.ResponseWriter, r *http.Request) {
	cards, err := s.showcase.ListCards(r.Context(), leadsLimit)
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Лиды недоступны", "Не удалось загрузить лидов.")
		return
	}
	s.render(w, http.StatusOK, pageLeads, pageViewSidebar("Лиды", consoleSidebar(sidebarLeads), leadsView{
		Rows: leadRows(cards),
	}))
}

// handleLeadsExport отдаёт автономный HTML-файл со списком лидов с email. Это
// полный документ со встроенными стилями — его можно сохранить и передать
// человеку без доступа к консоли. Данные те же, что и на странице /leads.
func (s *Server) handleLeadsExport(w http.ResponseWriter, r *http.Request) {
	cards, err := s.showcase.ListCards(r.Context(), leadsLimit)
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Выгрузка недоступна", "Не удалось загрузить лидов.")
		return
	}
	var buffer bytes.Buffer
	if err := s.leadsExport.ExecuteTemplate(&buffer, "leads-export", leadsView{Rows: leadRows(cards)}); err != nil {
		if s.logger != nil {
			s.logger.Error("httpapi: рендеринг выгрузки лидов", err, observ.Fields{})
		}
		http.Error(w, "внутренняя ошибка", http.StatusInternalServerError)
		return
	}
	// Встроенные стили и небольшой инлайн-скрипт (поиск по таблице) требуют
	// ослабленной CSP на этот ответ: файл открывают и прямо по ссылке. Внешних
	// ресурсов в документе нет, разрешены только встроенные стили и скрипты.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+leadsFilename("html")+`"`)
	_, _ = buffer.WriteTo(w)
}

// handleLeadsExportCSV отдаёт список лидов с email в CSV (RFC 4180) с BOM — файл
// открывается в Excel с русскими заголовками без дополнительных настроек. Набор
// данных тот же, что и у /leads.
func (s *Server) handleLeadsExportCSV(w http.ResponseWriter, r *http.Request) {
	cards, err := s.showcase.ListCards(r.Context(), leadsLimit)
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Выгрузка недоступна", "Не удалось загрузить лидов.")
		return
	}

	var buffer bytes.Buffer
	buffer.WriteString("\ufeff") // BOM: Excel читает UTF-8 как UTF-8, а не как ANSI
	writer := csv.NewWriter(&buffer)
	rows := [][]string{{"Сайт", "Название", "Email"}}
	for _, lead := range leadRows(cards) {
		rows = append(rows, []string{lead.Site, lead.Name, lead.Email})
	}
	if err := writer.WriteAll(rows); err != nil {
		if s.logger != nil {
			s.logger.Error("httpapi: сборка выгрузки лидов в CSV", err, observ.Fields{})
		}
		http.Error(w, "внутренняя ошибка", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+leadsFilename("csv")+`"`)
	_, _ = buffer.WriteTo(w)
}

// leadsFilename собирает имя файла выгрузки с датой (UTC): leads-2026-09-29.html.
func leadsFilename(ext string) string {
	return "leads-" + time.Now().UTC().Format("2006-01-02") + "." + ext
}

// filterCardsToJob оставляет карточки, входящие в результаты задания (ResultRef),
// в порядке результатов задания. Витрина читается одним пакетным запросом, а
// список задания — срез этой страницы.
func filterCardsToJob(cards []app.Card, resultRef []string) []app.Card {
	byID := make(map[string]app.Card, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	filtered := make([]app.Card, 0, len(resultRef))
	for _, id := range resultRef {
		if c, ok := byID[id]; ok {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// handleCard показывает карточку со статусом, историей переписки и формой
// отправки. Параметры template и q выбирают шаблон и фильтруют список.
func (s *Server) handleCard(w http.ResponseWriter, r *http.Request) {
	card, err := s.showcase.Card(r.Context(), r.PathValue("id"))
	if errors.Is(err, app.ErrCardNotFound) {
		s.renderNotice(w, http.StatusNotFound, "Карточка не найдена", "Запись не существует или была удалена.")
		return
	}
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Карточка недоступна", "Не удалось загрузить карточку.")
		return
	}
	// jobID — контекст задания, в котором открыта карточка. Он сохраняется в
	// ссылках и действиях формы, чтобы выбор шаблона не терял контекст оценки и не
	// подставлял оценку другого задания.
	jobID := strings.TrimSpace(r.URL.Query().Get("job"))
	cardBase := "/cards/" + url.PathEscape(card.ID)
	cardAction := cardBase
	if jobID != "" {
		cardAction += "?job=" + url.QueryEscape(jobID)
	}
	list, err := s.templateList(r.Context(), r, cardAction, r.PathValue("id"), func(id string) string {
		href := cardBase + "?template=" + url.QueryEscape(id)
		if jobID != "" {
			href += "&job=" + url.QueryEscape(jobID)
		}
		return href
	})
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Библиотека недоступна", "Не удалось загрузить шаблоны.")
		return
	}
	if isHTMX(r) {
		s.renderFragment(w, pageTemplates, fragmentTemplateList, list)
		return
	}

	history, err := s.showcase.History(r.Context(), card.ID)
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "История недоступна", "Не удалось загрузить историю переписки.")
		return
	}

	selected := strings.TrimSpace(r.URL.Query().Get("template"))
	valuesHTML, previewHTML := s.composeForScreen(r.Context(), selected, card.ID)

	var assessment *assessmentExplanation
	if jobID != "" {
		assessment = s.cardAssessment(r.Context(), jobID, card.ID)
	}

	s.render(w, http.StatusOK, pageCard, pageViewSidebar(card.Name, consoleSidebar(sidebarCards), cardView{
		Card:         cardDetailFrom(card, history),
		CSRF:         csrfToken(r),
		SendKey:      newIdempotencyKey(),
		Options:      list.Items,
		ValuesHTML:   valuesHTML,
		TemplateList: s.renderPickerHTML(list),
		Preview:      previewHTML,
		Assessment:   assessment,
		JobID:        jobID,
		JobScoped:    jobID != "",
	}))
}

// cardAssessment читает оценку компании в контексте задания. Оценка никогда не
// берётся из другого задания: ключ — пара (задание, компания), поэтому открытие
// карточки не превращает последнюю оценку в свойство компании. Ошибки чтения
// дают nil: оценка дополнительна и не заменяется выдуманной.
func (s *Server) cardAssessment(ctx context.Context, jobID, companyID string) *assessmentExplanation {
	if s.runSearch == nil || s.runSearch.Assessments == nil {
		return nil
	}
	assessment, err := s.runSearch.Assessments.GetByJobAndCompany(ctx, jobID, companyID)
	if err != nil {
		return nil
	}
	return assessmentExplanationFrom(assessment)
}

// handleHide скрывает карточку и перенаправляет на таблицу.
func (s *Server) handleHide(w http.ResponseWriter, r *http.Request) {
	if err := s.showcase.HideCard(r.Context(), r.PathValue("id")); err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Не удалось скрыть", err.Error())
		return
	}
	http.Redirect(w, r, "/cards", http.StatusSeeOther)
}

// handleDelete безвозвратно удаляет карточку и перенаправляет на таблицу.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.showcase.DeleteCard(r.Context(), r.PathValue("id")); err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Не удалось удалить", err.Error())
		return
	}
	http.Redirect(w, r, "/cards", http.StatusSeeOther)
}

// handleFunnel показывает канбан-воронку.
func (s *Server) handleFunnel(w http.ResponseWriter, r *http.Request) {
	stages, err := s.showcase.Funnel(r.Context(), time.Now().UTC())
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Воронка недоступна", "Не удалось загрузить воронку.")
		return
	}
	s.render(w, http.StatusOK, pageFunnel, pageViewSidebar("Воронка", consoleSidebar(sidebarFunnel), funnelView{
		Stages: funnelStageRows(stages),
		CSRF:   csrfToken(r),
	}))
}

// handleChangeStage переносит лида между стадиями воронки.
func (s *Server) handleChangeStage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректная форма", "Не удалось разобрать форму.")
		return
	}
	stage := domain.Stage(strings.TrimSpace(r.FormValue("stage")))
	author := ""
	if sess := currentSession(r); sess != nil {
		author = sess.username
	}
	if err := s.changeStage.Change(r.Context(), r.PathValue("id"), stage, author); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Не удалось перенести", err.Error())
		return
	}
	http.Redirect(w, r, "/funnel", http.StatusSeeOther)
}

// handleSnooze откладывает лида на указанное число дней.
func (s *Server) handleSnooze(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректная форма", "Не удалось разобрать форму.")
		return
	}
	days, err := strconv.Atoi(r.FormValue("days"))
	if err != nil || days <= 0 {
		s.renderNotice(w, http.StatusBadRequest, "Некорректный срок", "Срок откладывания должен быть положительным числом дней.")
		return
	}
	until := time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
	if err := s.snooze.Snooze(r.Context(), r.PathValue("id"), until); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Не удалось отложить", err.Error())
		return
	}
	http.Redirect(w, r, "/cards/"+r.PathValue("id"), http.StatusSeeOther)
}

// handleSendToLead отправляет письмо лиду из карточки по шаблону библиотеки.
func (s *Server) handleSendToLead(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректная форма", "Не удалось разобрать форму.")
		return
	}
	leadID := r.PathValue("id")
	_, err := s.sendToLead.Send(r.Context(),
		leadID,
		strings.TrimSpace(r.FormValue("template")),
		overridesFromForm(r.Form),
		strings.TrimSpace(r.FormValue("key")),
	)
	if err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Не удалось отправить", err.Error())
		return
	}
	http.Redirect(w, r, "/cards/"+leadID, http.StatusSeeOther)
}

// handleSendForm показывает экран отправки по сегменту. Параметры sent/skipped
// передают исход последней рассылки после перенаправления; template и q —
// выбранный шаблон и фильтр списка.
func (s *Server) handleSendForm(w http.ResponseWriter, r *http.Request) {
	sent, _ := strconv.Atoi(r.URL.Query().Get("sent"))
	skipped, _ := strconv.Atoi(r.URL.Query().Get("skipped"))
	list, err := s.templateList(r.Context(), r, "/send", "", func(id string) string {
		return "/send?template=" + url.QueryEscape(id)
	})
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Библиотека недоступна", "Не удалось загрузить шаблоны.")
		return
	}
	if isHTMX(r) {
		s.renderFragment(w, pageTemplates, fragmentTemplateList, list)
		return
	}

	selected := strings.TrimSpace(r.URL.Query().Get("template"))
	valuesHTML, previewHTML := s.composeForScreen(r.Context(), selected, "")

	s.render(w, http.StatusOK, pageSend, pageViewSidebar("Отправка", consoleSidebar(sidebarSend), sendView{
		CSRF:         csrfToken(r),
		Sent:         sent,
		Skipped:      skipped,
		Options:      list.Items,
		ValuesHTML:   valuesHTML,
		TemplateList: s.renderPickerHTML(list),
		Preview:      previewHTML,
	}))
}

// handleSendToSegment рассылает письмо по активной воронке с исключением
// подавленных лидов.
func (s *Server) handleSendToSegment(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректная форма", "Не удалось разобрать форму.")
		return
	}
	stages, err := s.showcase.Funnel(r.Context(), time.Now().UTC())
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Воронка недоступна", "Не удалось собрать сегмент.")
		return
	}
	leadIDs := funnelLeadIDs(stages)
	result, err := s.sendToSegment.Send(r.Context(),
		leadIDs,
		strings.TrimSpace(r.FormValue("template")),
		newIdempotencyKey(),
	)
	if err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Не удалось отправить", err.Error())
		return
	}
	http.Redirect(w, r, "/send?sent="+strconv.Itoa(result.Sent)+"&skipped="+strconv.Itoa(result.Skipped), http.StatusSeeOther)
}

// handleSegmentPreview собирает письмо по выбранному шаблону для рассылки по
// сегменту (без конкретного лида — значения уходят в fallback) и отдаёт
// HTMX-фрагмент предпросмотра.
func (s *Server) handleSegmentPreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректная форма", "Не удалось разобрать форму.")
		return
	}
	s.renderPreviewFragment(w, r, "")
}

// handleCardPreview собирает письмо по выбранному шаблону для лида и отдаёт
// HTMX-фрагмент предпросмотра.
func (s *Server) handleCardPreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректная форма", "Не удалось разобрать форму.")
		return
	}
	s.renderPreviewFragment(w, r, r.PathValue("id"))
}

// handleTemplates показывает библиотеку шаблонов с формой создания и списком. По
// запросу HTMX отдаётся только фрагмент списка (прогрессивное улучшение поиска).
func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	list, err := s.templateList(r.Context(), r, "/templates", "", func(id string) string {
		return "/templates/" + url.PathEscape(id) + "/edit"
	})
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Библиотека недоступна", "Не удалось загрузить шаблоны.")
		return
	}
	if isHTMX(r) {
		s.renderFragment(w, pageTemplates, fragmentTemplateList, list)
		return
	}
	s.renderTemplatesPage(w, r, http.StatusOK, s.emptyTemplateForm(), list, "")
}

// handleCreateTemplate создаёт шаблон. Доменные нарушения возвращаются формой с
// сообщением об ошибке — правила проверяются на сервере.
func (s *Server) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректная форма", "Не удалось разобрать форму.")
		return
	}
	input := s.templateInputFromForm(r, newTemplateID(r.FormValue("name")))
	created, err := s.templates.CreateTemplate(r.Context(), input)
	if err != nil {
		s.renderTemplateFormError(w, r, http.StatusBadRequest, input, err)
		return
	}
	http.Redirect(w, r, "/templates/"+url.PathEscape(created.ID)+"/edit", http.StatusSeeOther)
}

// handleTemplateEditForm показывает форму правки существующего шаблона.
func (s *Server) handleTemplateEditForm(w http.ResponseWriter, r *http.Request) {
	template, err := s.templates.GetTemplate(r.Context(), r.PathValue("id"))
	if errors.Is(err, app.ErrTemplateNotFound) {
		s.renderNotice(w, http.StatusNotFound, "Шаблон не найден", "Запись не существует или была удалена.")
		return
	}
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Шаблон недоступен", "Не удалось загрузить шаблон.")
		return
	}
	list, err := s.templateList(r.Context(), r, "/templates", "", func(id string) string {
		return "/templates/" + url.PathEscape(id) + "/edit"
	})
	if err != nil {
		s.renderNotice(w, http.StatusInternalServerError, "Библиотека недоступна", "Не удалось загрузить шаблоны.")
		return
	}
	s.renderTemplatesPage(w, r, http.StatusOK, s.editTemplateForm(template), list, "")
}

// handleUpdateTemplate правит шаблон, повышая его ревизию.
func (s *Server) handleUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректная форма", "Не удалось разобрать форму.")
		return
	}
	input := s.templateInputFromForm(r, r.PathValue("id"))
	if _, err := s.templates.EditTemplate(r.Context(), input); err != nil {
		s.renderTemplateFormError(w, r, http.StatusBadRequest, input, err)
		return
	}
	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

// handleDeleteTemplate удаляет шаблон. Голосование не переписывает прошлые
// отправки: те хранят снимок тела и ссылку на редакцию.
func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	if err := s.templates.DeleteTemplate(r.Context(), r.PathValue("id")); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Не удалось удалить", err.Error())
		return
	}
	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

// renderTemplatesPage отдаёт страницу библиотеки с заданной формой и списком.
func (s *Server) renderTemplatesPage(w http.ResponseWriter, r *http.Request, status int, form templateFormView, list templateListView, errText string) {
	s.render(w, status, pageTemplates, pageViewSidebar("Библиотека шаблонов", consoleSidebar(sidebarSend), templatesView{
		CSRF:  csrfToken(r),
		Error: errText,
		Form:  form,
		List:  list,
	}))
}

// renderTemplateFormError возвращает страницу с сохранённым вводом и серверной
// ошибкой у формы (необъявленный плейсхолдер, переменная вне каталога и т. п.).
func (s *Server) renderTemplateFormError(w http.ResponseWriter, r *http.Request, status int, input app.TemplateInput, err error) {
	form := s.templateForm("create", "/templates", "", "", input.Name, input.Subject, input.Body, input.Variables)
	if r.PathValue("id") != "" {
		form = s.templateForm("edit", "/templates/"+url.PathEscape(r.PathValue("id")), "/templates/"+url.PathEscape(r.PathValue("id"))+"/delete", "", input.Name, input.Subject, input.Body, input.Variables)
	}
	list, listErr := s.templateList(r.Context(), r, "/templates", "", func(id string) string {
		return "/templates/" + url.PathEscape(id) + "/edit"
	})
	if listErr != nil {
		list = templateListView{Action: "/templates"}
	}
	s.renderTemplatesPage(w, r, status, form, list, err.Error())
}

// emptyTemplateForm собирает пустую форму создания шаблона.
func (s *Server) emptyTemplateForm() templateFormView {
	return s.templateForm("create", "/templates", "", "", "", "", "", nil)
}

// editTemplateForm собирает форму правки существующего шаблона.
func (s *Server) editTemplateForm(template app.TemplateView) templateFormView {
	return s.templateForm("edit", "/templates/"+url.PathEscape(template.ID), "/templates/"+url.PathEscape(template.ID)+"/delete", template.Reference, template.Name, template.Subject, template.Body, template.Variables)
}

// templateForm собирает модель формы шаблона из каталога переменных и
// объявленного набора.
func (s *Server) templateForm(mode, action, deleteAction, reference, name, subject, body string, declared []app.TemplateVariable) templateFormView {
	return templateFormView{
		Mode:         mode,
		Action:       action,
		DeleteAction: deleteAction,
		Reference:    reference,
		Name:         name,
		Subject:      subject,
		Body:         body,
		Options:      templateFormOptions(s.templates.Catalog(), declared),
	}
}

// templateInputFromForm разбирает форму шаблона: имя, тема, тело и объявленные
// переменные (отметки var_<ключ> и fallback_<ключ> по каталогу).
func (s *Server) templateInputFromForm(r *http.Request, id string) app.TemplateInput {
	input := app.TemplateInput{
		ID:      id,
		Name:    r.FormValue("name"),
		Subject: r.FormValue("subject"),
		Body:    r.FormValue("body"),
	}
	for _, option := range s.templates.Catalog() {
		if strings.TrimSpace(r.FormValue("var_"+option.Key)) == "" {
			continue
		}
		input.Variables = append(input.Variables, app.TemplateVariable{
			Key:      option.Key,
			Fallback: r.FormValue("fallback_" + option.Key),
		})
	}
	return input
}

// overridesFromForm собирает значения объявленных переменных, отредактированные
// оператором при отправке (поля value_<ключ> по каталогу).
func overridesFromForm(form url.Values) map[string]string {
	if form == nil {
		return nil
	}
	overrides := make(map[string]string)
	for _, option := range app.VariableCatalog() {
		if value := strings.TrimSpace(form.Get("value_" + option.Key)); value != "" {
			overrides[option.Key] = value
		}
	}
	if len(overrides) == 0 {
		return nil
	}
	return overrides
}

// templateList собирает список шаблонов по поисковому запросу экрана.
func (s *Server) templateList(ctx context.Context, r *http.Request, action, selected string, hrefFor func(string) string) (templateListView, error) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	view := templateListView{Query: query, Action: action}
	if s.templates == nil {
		return view, nil
	}
	templates, err := s.templates.ListTemplates(ctx, query, templatesListLimit)
	if err != nil {
		return templateListView{}, err
	}
	view.Items = templateItems(templates, hrefFor, selected)
	return view, nil
}

// compose собирает предпросмотр письма по шаблону и лиду. false — шаблон не
// выбран, недоступен или библиотека не собрана.
func (s *Server) compose(ctx context.Context, templateID, leadID string, overrides map[string]string) (app.TemplatePreview, bool) {
	if s.templates == nil || strings.TrimSpace(templateID) == "" {
		return app.TemplatePreview{}, false
	}
	preview, err := s.templates.Compose(ctx, templateID, leadID, overrides)
	if err != nil {
		return app.TemplatePreview{}, false
	}
	return preview, true
}

// composeForScreen собирает предпросмотр письма для экрана и размечает его
// фрагменты: поля значений и блок предпросмотра.
func (s *Server) composeForScreen(ctx context.Context, templateID, leadID string) (valuesHTML, previewHTML template.HTML) {
	preview, ok := s.compose(ctx, templateID, leadID, nil)
	if !ok {
		return "", ""
	}
	return s.renderValuesHTML(preview.Values), s.renderPreviewHTML(preview)
}

// renderPreviewFragment отдаёт HTMX-фрагмент предпросмотра собранного письма.
func (s *Server) renderPreviewFragment(w http.ResponseWriter, r *http.Request, leadID string) {
	preview, ok := s.compose(r.Context(), strings.TrimSpace(r.FormValue("template")), leadID, overridesFromForm(r.Form))
	if !ok {
		s.renderFragment(w, pageTemplates, fragmentEmailPreview, app.TemplatePreview{})
		return
	}
	s.renderFragment(w, pageTemplates, fragmentEmailPreview, preview)
}

// renderPickerHTML предразмечает общий блок-фрагмент списка шаблонов с поиском,
// чтобы встроить его в экраны без дублирования разметки.
func (s *Server) renderPickerHTML(list templateListView) template.HTML {
	return s.renderTemplateFragment(fragmentTemplatePicker, list)
}

// renderValuesHTML предразмечает поля значений объявленных переменных.
func (s *Server) renderValuesHTML(values []app.TemplateValue) template.HTML {
	if len(values) == 0 {
		return ""
	}
	return s.renderTemplateFragment(fragmentValueFields, templateValueViews(values))
}

// renderPreviewHTML предразмечает блок предпросмотра собранного письма.
func (s *Server) renderPreviewHTML(preview app.TemplatePreview) template.HTML {
	if preview.Reference == "" {
		return ""
	}
	return s.renderTemplateFragment(fragmentEmailPreview, preview)
}

// renderTemplateFragment рендерит именованный фрагмент библиотеки шаблонов.
func (s *Server) renderTemplateFragment(name string, data any) template.HTML {
	var buffer bytes.Buffer
	if err := s.pages.RenderFragment(&buffer, pageTemplates, name, data); err != nil {
		s.logger.Error("httpapi: рендеринг фрагмента библиотеки", err, observ.Fields{}, "fragment", name)
		return ""
	}
	return template.HTML(buffer.String())
}

// isHTMX сообщает, что запрос пришёл от HTMX (частичное обновление).
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// newTemplateID строит идентификатор нового шаблона из имени и короткого
// случайного суффикса, чтобы имена не конфликтовали.
func newTemplateID(name string) string {
	slug := slugify(name)
	suffix, err := randomToken()
	if err != nil || len(suffix) < 6 {
		return slug
	}
	if slug == "" {
		return "tpl-" + suffix[:8]
	}
	return slug + "-" + suffix[:6]
}

// slugify приводит имя к ASCII-идентификатору: буквы транслитерируются, прочие
// символы становятся дефисом.
func slugify(name string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if mapped, ok := translit[r]; ok {
			builder.WriteString(mapped)
			lastDash = false
			continue
		}
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		default:
			if builder.Len() > 0 && !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(builder.String(), "-")
}

// translit — таблица транслитерации кириллицы для читаемых идентификаторов.
var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch",
	'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}

// templatesListLimit — верхняя граница списка шаблонов на экране.
const templatesListLimit = 200

// handleAssistant показывает экран ассистента: пустой диалог и лимиты обхода по
// умолчанию из конфигурации.
func (s *Server) handleAssistant(w http.ResponseWriter, r *http.Request) {
	s.renderAssistant(w, r, s.defaultAssistantView(r))
}

// handleAssistantTurn разбирает действие формы экрана ассистента: «Отправить» —
// очередной ход диалога, «Запустить краулинг» — подтверждение плана.
func (s *Server) handleAssistantTurn(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderNotice(w, http.StatusBadRequest, "Некорректная форма", "Не удалось разобрать форму.")
		return
	}
	switch r.FormValue("action") {
	case "launch":
		s.handleAssistantLaunch(w, r)
		return
	case "probe":
		s.handleAssistantProbe(w, r)
		return
	}
	s.handleAssistantReply(w, r)
}

// handleAssistantReply выполняет ход диалога: история и черновик плана приходят из
// формы, ответ агента отрисовывается сразу без редиректа. Сервер историю не
// хранит — она уезжает обратно в скрытом поле.
func (s *Server) handleAssistantReply(w http.ResponseWriter, r *http.Request) {
	view := s.formAssistantView(r)
	messages, err := decodeHistory(r.FormValue("history"))
	if err != nil {
		view.Error = "История диалога повреждена, начните заново."
		s.renderAssistant(w, r, view)
		return
	}
	view.Messages = assistantMessagesToRows(messages)
	view.Targets = assistantTargetsToRows(decodeTargets(r.FormValue("plan")))
	view.History = encodeHistory(messages)
	view.PlanJSON = r.FormValue("plan")

	text := strings.TrimSpace(r.FormValue("message"))
	if text == "" {
		view.Error = "Введите ответ."
		s.renderAssistant(w, r, view)
		return
	}
	if s.dialogue == nil {
		s.renderNotice(w, http.StatusInternalServerError, "Ассистент недоступен", "Диалог не настроен.")
		return
	}

	messages = append(messages, dialogueMessage{Role: llm.RoleUser, Content: text})
	reply, err := s.dialogue.Reply(r.Context(), toLLMHistory(messages))
	if err != nil {
		view.Error = "Не удалось получить ответ ассистента: " + err.Error()
		s.renderAssistant(w, r, view)
		return
	}
	messages = append(messages, dialogueMessage{Role: llm.RoleAssistant, Content: reply.Text})

	// Черновик плана накапливается: свежие цели ответа замещают прежние, а пустой
	// черновик не стирает уже согласованное.
	targets := decodeTargets(r.FormValue("plan"))
	if draft := fromLLMTargets(reply.Targets); len(draft) > 0 {
		targets = draft
	}

	view.Messages = assistantMessagesToRows(messages)
	view.Targets = assistantTargetsToRows(targets)
	view.Ready = reply.Ready
	view.History = encodeHistory(messages)
	view.PlanJSON = encodeTargets(targets)
	view.Interpretation = s.interpretCriteria(r.Context(), criteriaFromHistory(messages))
	// Ход принят: поле ввода очищается, черновик остаётся только в ленте.
	view.Message = ""
	s.renderAssistant(w, r, view)
}

// interpretCriteria выводит критерии релевантности из ответов оператора для
// экрана ассистента. Это отдельная от технического плана сущность; при пустом
// запросе или недоступной интерпретации возвращается явное «не определена», а не
// выдуманные критерии.
func (s *Server) interpretCriteria(ctx context.Context, criteria string) interpretationView {
	if s.interpreter == nil || strings.TrimSpace(criteria) == "" {
		return interpretationFrom(nil)
	}
	interpretation, _, err := s.interpreter.Interpret(ctx, criteria)
	if err != nil {
		return interpretationFrom(nil)
	}
	return interpretationFrom(&interpretation)
}

// handleAssistantLaunch подтверждает план и ставит задание с согласованным планом
// обхода: критерии собираются из ответов оператора, цели даёт финальный Plan.
func (s *Server) handleAssistantLaunch(w http.ResponseWriter, r *http.Request) {
	view := s.formAssistantView(r)
	messages, err := decodeHistory(r.FormValue("history"))
	if err != nil {
		view.Error = "История диалога повреждена, начните заново."
		s.renderAssistant(w, r, view)
		return
	}
	view.Messages = assistantMessagesToRows(messages)
	view.Targets = assistantTargetsToRows(decodeTargets(r.FormValue("plan")))
	view.History = encodeHistory(messages)
	view.PlanJSON = r.FormValue("plan")

	// Непустое поле «Ваш ответ» — ещё один ход оператора: он участвует в сборке
	// задачи наравне с историей, даже если оператор не нажал «Отправить».
	criteria := criteriaFromHistory(messages)
	if text := strings.TrimSpace(r.FormValue("message")); text != "" {
		if criteria != "" {
			criteria += "\n"
		}
		criteria += text
	}
	view.Interpretation = s.interpretCriteria(r.Context(), criteria)
	if criteria == "" {
		view.Error = "Опишите задачу хотя бы одним сообщением."
		s.renderAssistant(w, r, view)
		return
	}
	if s.runSearch == nil {
		s.renderNotice(w, http.StatusInternalServerError, "Очередь недоступна", "Поиск не настроен.")
		return
	}
	limits, err := limitsFromForm(r)
	if err != nil {
		view.Error = err.Error()
		s.renderAssistant(w, r, view)
		return
	}
	targetCompanies, maxResults, err := quotaFromForm(r)
	if err != nil {
		view.Error = err.Error()
		s.renderAssistant(w, r, view)
		return
	}
	// Черновик плана разбирается и проверяется как недоверенный ввод: невалидная
	// цель не должна молча превращаться в перевывод из текста запроса.
	planTargets, err := decodePlanTargets(r.FormValue("plan"))
	if err != nil {
		view.Error = "План запуска некорректен: " + err.Error()
		s.renderAssistant(w, r, view)
		return
	}
	// Ручные цели оператора (панель плана) используются дословно и замещают
	// черновик плана ассистента; пустой ввод оставляет черновик.
	manualTargets, err := decodeManualTargets(r.FormValue("manualTargets"))
	if err != nil {
		view.Error = "Свои цели некорректны: " + err.Error()
		s.renderAssistant(w, r, view)
		return
	}
	if len(manualTargets) > 0 {
		planTargets = manualTargets
	}
	// Запуск интерпретирует запрос и сохраняет критерии отдельным полем задания:
	// оценка компаний опирается на те же критерии, что показаны оператору. Цели
	// подтверждённого плана используются дословно.
	if _, err := s.runSearch.EnqueueInterpreted(r.Context(), criteria, planTargets, limits, 0, targetCompanies, maxResults); err != nil {
		view.Error = "Не удалось запустить: " + err.Error()
		s.renderAssistant(w, r, view)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleAssistantProbe подтверждает короткую проверочную сессию: критерии
// собираются из ответов оператора, а обход ограничен явными глубиной, временем и
// бюджетом. Начальные значения формы — дефолты короткой сессии (design.md,
// Решение 10); сервер проверяет их как недоверенный ввод.
func (s *Server) handleAssistantProbe(w http.ResponseWriter, r *http.Request) {
	view := s.formAssistantView(r)
	messages, err := decodeHistory(r.FormValue("history"))
	if err != nil {
		view.Error = "История диалога повреждена, начните заново."
		s.renderAssistant(w, r, view)
		return
	}
	view.Messages = assistantMessagesToRows(messages)
	view.Targets = assistantTargetsToRows(decodeTargets(r.FormValue("plan")))
	view.History = encodeHistory(messages)
	view.PlanJSON = r.FormValue("plan")

	criteria := criteriaFromHistory(messages)
	if text := strings.TrimSpace(r.FormValue("message")); text != "" {
		if criteria != "" {
			criteria += "\n"
		}
		criteria += text
	}
	view.Interpretation = s.interpretCriteria(r.Context(), criteria)
	if criteria == "" {
		view.Error = "Опишите задачу хотя бы одним сообщением."
		s.renderAssistant(w, r, view)
		return
	}
	if s.runSearch == nil {
		s.renderNotice(w, http.StatusInternalServerError, "Очередь недоступна", "Поиск не настроен.")
		return
	}
	limits, credits, err := probeFromForm(r)
	if err != nil {
		view.Error = err.Error()
		s.renderAssistant(w, r, view)
		return
	}
	targetCompanies, maxResults, err := quotaFromForm(r)
	if err != nil {
		view.Error = err.Error()
		s.renderAssistant(w, r, view)
		return
	}
	// Черновик плана разбирается и проверяется как недоверенный ввод: невалидная
	// цель не должна молча превращаться в перевывод из текста запроса.
	planTargets, err := decodePlanTargets(r.FormValue("plan"))
	if err != nil {
		view.Error = "План запуска некорректен: " + err.Error()
		s.renderAssistant(w, r, view)
		return
	}
	if manualTargets, err := decodeManualTargets(r.FormValue("manualTargets")); err != nil {
		view.Error = "Свои цели некорректны: " + err.Error()
		s.renderAssistant(w, r, view)
		return
	} else if len(manualTargets) > 0 {
		planTargets = manualTargets
	}
	if _, err := s.runSearch.EnqueueShortSession(r.Context(), criteria, planTargets, limits, credits, targetCompanies, maxResults); err != nil {
		view.Error = "Не удалось запустить проверочную сессию: " + err.Error()
		s.renderAssistant(w, r, view)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleAssistantStream выполняет ход диалога потоком (design.md, Решение 4):
// кадры SSE delta/plan/done/error. Правила остаются в app/порте; адаптер только
// транслирует поток, не буферизуя полный текст реплики. Отмена по r.Context()
// (уход клиента) прерывает генерацию.
func (s *Server) handleAssistantStream(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.writeStreamError(w, "Некорректная форма", "")
		return
	}
	messages, err := decodeHistory(r.FormValue("history"))
	if err != nil {
		s.writeStreamError(w, "История диалога повреждена, начните заново.", "")
		return
	}
	text := strings.TrimSpace(r.FormValue("message"))
	if text == "" {
		s.writeStreamError(w, "Введите ответ.", encodeHistory(messages))
		return
	}
	if s.dialogue == nil {
		s.writeStreamError(w, "Диалог не настроен.", encodeHistory(messages))
		return
	}
	messages = append(messages, dialogueMessage{Role: llm.RoleUser, Content: text})
	// История с уже добавленным ходом оператора уезжает клиенту в каждом кадре
	// ошибки — иначе ход потерялся бы вместе с неудачной генерацией.
	history := encodeHistory(messages)

	// Заголовки потока: без кэша (красная зона — канон web-frontend-architecture,
	// Решение 15) и без буферизации обратным прокси.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)

	send := func(event string, payload any) bool {
		data, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	targets := decodeTargets(r.FormValue("plan"))
	// Критерии релевантности считаются один раз на запрос и уезжают в кадре плана
	// отдельным блоком — они не смешиваются с целями и лимитами обхода.
	interpretation := s.interpretCriteria(r.Context(), criteriaFromHistory(messages))
	for delta, err := range s.dialogue.StreamReply(r.Context(), toLLMHistory(messages)) {
		if err != nil {
			send("error", map[string]string{"message": err.Error(), "history": history})
			return
		}
		if delta.Text != "" {
			if !send("delta", map[string]string{"text": delta.Text}) {
				return
			}
		}
		if delta.Reply == nil {
			continue
		}
		// Черновик накапливается: свежие цели замещают прежние, пустой черновик
		// не стирает уже согласованное.
		if draft := fromLLMTargets(delta.Reply.Targets); len(draft) > 0 {
			targets = draft
		}
		if !send("plan", map[string]any{
			"targets":        assistantTargetsToRows(targets),
			"limits":         planLimitChips(delta.Reply.Limits),
			"ready":          delta.Reply.Ready,
			"interpretation": interpretation,
		}) {
			return
		}
		reply := append(messages, dialogueMessage{Role: llm.RoleAssistant, Content: delta.Reply.Text})
		if !send("done", map[string]any{
			"text":    delta.Reply.Text,
			"history": encodeHistory(reply),
			"plan":    encodeTargets(targets),
		}) {
			return
		}
	}
}

// writeStreamError отдаёт единственный кадр ошибки потока. Статус остаётся 200:
// клиент читает поток, а не HTTP-ошибку; CSRF при этом по-прежнему проверяет
// authMiddleware до обработчика. История передаётся тем же кадром, чтобы клиент
// сохранил уже сделанные ходы (формат совместим с чтением в assistant.js).
func (s *Server) writeStreamError(w http.ResponseWriter, message, history string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	data, err := json.Marshal(map[string]string{"message": message, "history": history})
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", data)
	_ = http.NewResponseController(w).Flush()
}

// renderAssistant отрисовывает экран ассистента.
func (s *Server) renderAssistant(w http.ResponseWriter, r *http.Request, view assistantView) {
	s.render(w, http.StatusOK, pageAssistant, pageViewSidebar("Ассистент", consoleSidebar(sidebarAssistant), view))
}

// defaultAssistantView собирает представление с операторскими лимитами
// конфигурации: глубина и время (страницы и пути — внутренние значения). Целевое
// число компаний и лимит поиска не заданы по умолчанию: значения не выдумываются.
func (s *Server) defaultAssistantView(r *http.Request) assistantView {
	var limits app.CrawlLimits
	if s.runSearch != nil {
		limits = s.runSearch.Limits
	}
	maxDepth := intOrEmpty(limits.MaxDepth)
	maxTime := intOrEmpty(limits.MaxTimeSeconds)
	return assistantView{
		CSRF:             csrfToken(r),
		MaxDepth:         maxDepth,
		MaxTimeSeconds:   maxTime,
		ProbeDepth:       strconv.Itoa(app.DefaultProbeDepth),
		ProbeTimeSeconds: strconv.Itoa(app.DefaultProbeTimeSeconds),
		ProbeMaxCredits:  strconv.Itoa(app.DefaultProbeMaxCredits),
		Limits:           planLimitChipsFromStrings(maxDepth, maxTime, "", ""),
	}
}

// formAssistantView собирает представление из формы, сохраняя согласованные
// лимиты и операторские параметры при повторной отрисовке.
func (s *Server) formAssistantView(r *http.Request) assistantView {
	maxDepth := strings.TrimSpace(r.FormValue("maxDepth"))
	maxTime := strings.TrimSpace(r.FormValue("maxTimeSeconds"))
	targetCompanies := strings.TrimSpace(r.FormValue("targetCompanies"))
	maxResults := strings.TrimSpace(r.FormValue("maxResults"))
	return assistantView{
		CSRF:             csrfToken(r),
		Message:          strings.TrimSpace(r.FormValue("message")),
		MaxDepth:         maxDepth,
		MaxTimeSeconds:   maxTime,
		TargetCompanies:  targetCompanies,
		MaxResults:       maxResults,
		ManualTargets:    strings.TrimSpace(r.FormValue("manualTargets")),
		ProbeDepth:       probeField(r, "probeMaxDepth", app.DefaultProbeDepth),
		ProbeTimeSeconds: probeField(r, "probeMaxTimeSeconds", app.DefaultProbeTimeSeconds),
		ProbeMaxCredits:  probeField(r, "probeMaxCredits", app.DefaultProbeMaxCredits),
		Limits:           planLimitChipsFromStrings(maxDepth, maxTime, targetCompanies, maxResults),
	}
}

// planLimitChipsFromStrings собирает чипы операторских параметров из уже
// отформатированных строк (значения конфигурации или полей формы). Пустые
// значения не показываются.
func planLimitChipsFromStrings(maxDepth, maxTimeSeconds, targetCompanies, maxResults string) []planLimitChip {
	chips := make([]planLimitChip, 0, 4)
	if maxDepth != "" {
		chips = append(chips, planLimitChip{Label: "Глубина", Value: maxDepth})
	}
	if maxTimeSeconds != "" {
		chips = append(chips, planLimitChip{Label: "Время, с", Value: maxTimeSeconds})
	}
	if targetCompanies != "" {
		chips = append(chips, planLimitChip{Label: "Сколько найти", Value: targetCompanies})
	}
	if maxResults != "" {
		chips = append(chips, planLimitChip{Label: "Результатов на запрос", Value: maxResults})
	}
	return chips
}

// quotaFromForm разбирает операторские параметры задания с целью: целевое число
// компаний и лимит результатов поиска. Оба поля необязательны (пустое значение —
// 0: цель не задана, лимит адаптера) и проверяются как недоверенный ввод.
func quotaFromForm(r *http.Request) (int, int, error) {
	targetCompanies, err := parseLimit(r.FormValue("targetCompanies"), "целевое число компаний")
	if err != nil {
		return 0, 0, err
	}
	maxResults, err := parseLimit(r.FormValue("maxResults"), "результатов на запрос")
	if err != nil {
		return 0, 0, err
	}
	return targetCompanies, maxResults, nil
}

// probeFromForm разбирает ограничения и бюджет короткой проверочной сессии.
// Пустое поле берёт начальное значение по умолчанию; значения проверяются как
// недоверенный ввод и обязаны быть неотрицательными (положительность требует
// EnqueueShortSession).
func probeFromForm(r *http.Request) (ports.PlanLimits, int, error) {
	depth, err := parseLimit(probeField(r, "probeMaxDepth", app.DefaultProbeDepth), "глубина обхода")
	if err != nil {
		return ports.PlanLimits{}, 0, err
	}
	seconds, err := parseLimit(probeField(r, "probeMaxTimeSeconds", app.DefaultProbeTimeSeconds), "время работы, с")
	if err != nil {
		return ports.PlanLimits{}, 0, err
	}
	credits, err := parseLimit(probeField(r, "probeMaxCredits", app.DefaultProbeMaxCredits), "бюджет кредитов")
	if err != nil {
		return ports.PlanLimits{}, 0, err
	}
	return ports.PlanLimits{MaxDepth: depth, MaxTimeSeconds: seconds}, credits, nil
}

// probeField читает поле формы короткой сессии, подставляя начальное значение по
// умолчанию, если поле не задано.
func probeField(r *http.Request, name string, fallback int) string {
	if value := strings.TrimSpace(r.FormValue(name)); value != "" {
		return value
	}
	return strconv.Itoa(fallback)
}

// limitsFromForm разбирает операторские лимиты обхода из формы: глубина и
// время. Число страниц и include/exclude-пути оператор больше не задаёт.
func limitsFromForm(r *http.Request) (ports.PlanLimits, error) {
	maxDepth, err := parseLimit(r.FormValue("maxDepth"), "глубина обхода")
	if err != nil {
		return ports.PlanLimits{}, err
	}
	maxTime, err := parseLimit(r.FormValue("maxTimeSeconds"), "время работы, с")
	if err != nil {
		return ports.PlanLimits{}, err
	}
	return ports.PlanLimits{
		MaxDepth:       maxDepth,
		MaxTimeSeconds: maxTime,
	}, nil
}

// parseLimit разбирает необязательный неотрицательный целочисленный лимит.
func parseLimit(raw, label string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("поле «%s» должно быть целым числом", label)
	}
	if v < 0 {
		return 0, fmt.Errorf("поле «%s» не может быть отрицательным", label)
	}
	return v, nil
}

// intOrEmpty возвращает число строкой либо пустую строку для нуля/отрицательного.
func intOrEmpty(v int) string {
	if v <= 0 {
		return ""
	}
	return strconv.Itoa(v)
}

// handleForumSignals показывает экран форумных сигналов: форму запроса и, при
// наличии параметра q, список обезличенных сигналов.
func (s *Server) handleForumSignals(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	view := forumSignalsView{
		QueryField: components.Field{ID: "q", Name: "q", Label: "Запрос", Required: true, Value: query},
		Query:      query,
	}
	if query != "" {
		if s.forumSignals == nil {
			s.renderNotice(w, http.StatusInternalServerError, "Поиск недоступен", "Форумный источник не настроен.")
			return
		}
		signals, err := s.forumSignals.Search(r.Context(), query)
		if err != nil {
			s.renderNotice(w, http.StatusInternalServerError, "Поиск недоступен", err.Error())
			return
		}
		view.Signals = forumSignalRows(signals)
	}
	s.render(w, http.StatusOK, pageForumSignals, pageViewSidebar("Форумы", consoleSidebar(sidebarForum), view))
}

// handleHealth — проба живости процесса.
func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("ok\n"))
}

// isSecure сообщает, что запрос пришёл по защищённому каналу (прямой TLS либо
// обратный прокси).
func isSecure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// newIdempotencyKey генерирует ключ идемпотентности формы отправки.
func newIdempotencyKey() string {
	key, err := randomToken()
	if err != nil {
		return ""
	}
	return key
}

// Проверка, что Server — http.Handler.
var _ http.Handler = (*Server)(nil)
