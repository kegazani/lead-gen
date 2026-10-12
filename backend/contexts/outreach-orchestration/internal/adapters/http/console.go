package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"lead-gen/backend/platform/consoleauth"
	"lead-gen/frontend/components"
)

// Имена страниц консольных экранов среза.
const (
	pageLogin           = "templates/login.html"
	pageDraftQueue      = "templates/draft_queue.html"
	pageBatchScreen     = "templates/batch_screen.html"
	pageDraftScreen     = "templates/draft_screen.html"
	pageStalledQueue    = "templates/stalled_queue.html"
	pageEditorialReview = "templates/editorial_review.html"
)

// Разделы подменю консоли рассылок: машинные ключи активного пункта.
const (
	consoleNavDrafts  = "drafts"
	consoleNavStalled = "stalled"
)

// newPages собирает набор шаблонов консоли рассылок: каркас и общие фрагменты из
// frontend/components плюс страницы этого сервиса. Разбор идёт при сборке
// сервиса, поэтому ошибка разметки видна сразу.
func newPages() (*components.Set, error) {
	return components.NewWithMessages(consoleMessages(), templateFS,
		pageLogin,
		pageDraftQueue,
		pageBatchScreen,
		pageDraftScreen,
		pageStalledQueue,
		pageEditorialReview,
	)
}

// consoleNav собирает общую навигацию консоли: канонические разделы общего слоя
// с активным разделом «Рассылки» (ConsoleSectionOutreach). Вторичных групп здесь
// нет: переходы между экранами раздела выводятся внутриэкранной панелью
// (outreachSubnav), поэтому общий сайдбар одинаков на всех консолях и не меняет
// состав и высоту при переходах (change console-shell-navigation). Контекст
// активного тенанта и актора переносится в ссылки разделов.
func (s *Server) consoleNav(tenant, actor string) components.Nav {
	ctx := components.ConsoleContext{TenantID: tenant, Actor: actor}
	return components.ConsoleNavWithContext(s.console, components.ConsoleSectionOutreach, ctx)
}

// outreachSubnav собирает внутриэкранную панель переходов по экранам раздела
// «Рассылки»: очередь черновиков и зависшие черновики. Панель выводится в области
// содержимого, а не в общем сайдбаре; текущий экран отмечен активным, а ссылки
// сохраняют активные tenant_id и actor, чтобы контекст не терялся при переходе
// между экранами раздела (change console-shell-navigation).
func outreachSubnav(active, tenant, actor string) *components.Tabs {
	ctx := components.ConsoleContext{TenantID: tenant, Actor: actor}
	return &components.Tabs{
		Items: []components.Tab{
			{Key: consoleNavDrafts, Label: consoleMessages().Text(msgDraftQueueTitle), Href: components.ConsoleHrefWithContext("/outreach/drafts", ctx)},
			{Key: consoleNavStalled, Label: consoleMessages().Text(msgStalledTitle), Href: components.ConsoleHrefWithContext("/outreach/stalled", ctx)},
		},
		Active: active,
	}
}

// render рисует страницу в буфер: частично отданный HTML выглядел бы сломанной
// страницей вместо честного отказа.
func (s *Server) render(w http.ResponseWriter, status int, page string, model components.Page) {
	if s.pages == nil {
		s.renderPlainError(w, http.StatusInternalServerError, "шаблоны экрана не собраны")
		return
	}
	if len(model.Layout.Stylesheets) == 0 {
		model.Layout.Stylesheets = components.ConsoleStylesheets
	}
	if len(model.Layout.BootstrapScripts) == 0 {
		model.Layout.BootstrapScripts = components.ConsoleBootstrapScripts
	}
	// Общие скрипты консоли возвращают фокус при подмене фрагмента и объявляют
	// изменения через область aria-live (web-frontend-architecture, срез 7, 7.3).
	if len(model.Layout.Scripts) == 0 {
		model.Layout.Scripts = components.ConsoleScripts
	}
	var buf bytes.Buffer
	if err := s.pages.Render(&buf, page, model); err != nil {
		s.renderPlainError(w, http.StatusInternalServerError, "ошибка рендеринга экрана")
		return
	}
	w.Header().Set("Content-Type", contentTypeHTML)
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// Порог и окно ограничения частоты чувствительных действий консоли.
const (
	sensitiveLimit  = 30
	sensitiveWindow = time.Minute
)

// rateLimit оборачивает чувствительное действие ограничителем частоты общего слоя:
// вход и решения утверждения (web-frontend-architecture, срез 7, 7.5). Факт
// блокировки фиксируется.
func (s *Server) rateLimit(path string, next http.HandlerFunc) http.HandlerFunc {
	limited := components.RateLimit(components.RateLimitConfig{
		Limit:  sensitiveLimit,
		Window: sensitiveWindow,
		OnBlocked: func(r *http.Request, key string) {
			s.recordRateLimit(r, path, key)
		},
	})(next)
	return limited.ServeHTTP
}

// recordRateLimit фиксирует блокировку в журнале процесса: без фиксации
// отклонение неотличимо от тишины.
func (s *Server) recordRateLimit(r *http.Request, path, key string) {
	log.Printf("outreach-orchestration: превышен порог частоты path=%s key=%s", path, key)
}

// renderPlainError отдаёт текстовую ошибку для случаев, когда HTML-экран
// недоступен (шаблоны не собраны и т.п.).
func (s *Server) renderPlainError(w http.ResponseWriter, status int, message string) {
	http.Error(w, message, status)
}

// actorOf возвращает логин оператора из сессии. Консольные маршруты обёрнуты
// middleware, поэтому сессия гарантированно есть; пустой актор без сессии честнее
// выдуманного значения по умолчанию. Настоящие роли — platform-governance.
func actorOf(r *http.Request) string {
	if sess := consoleauth.Current(r.Context()); sess != nil {
		return sess.Username()
	}
	return ""
}

// loginView — модель страницы входа консоли рассылок.
type loginView struct {
	Live  components.Live
	Error string
}

// handleLoginForm показывает форму входа оператора.
func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if !s.consoleConfigured {
		s.renderPlainError(w, http.StatusServiceUnavailable, "консоль рассылок не настроена: не заданы учётные данные оператора")
		return
	}
	s.render(w, http.StatusOK, pageLogin, components.Page{
		Layout: components.Layout{Title: consoleMessages().Text(msgLoginTitle)},
		View:   loginView{Live: consoleLive},
	})
}

// handleLogin проверяет учётные данные и ставит cookie сессии консоли.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.consoleConfigured {
		s.renderPlainError(w, http.StatusServiceUnavailable, "консоль рассылок не настроена: не заданы учётные данные оператора")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	id, err := s.sessions.Login(r.FormValue("username"), r.FormValue("password"))
	if errors.Is(err, consoleauth.ErrInvalidCredentials) {
		s.render(w, http.StatusUnauthorized, pageLogin, components.Page{
			Layout: components.Layout{Title: consoleMessages().Text(msgLoginTitle)},
			View:   loginView{Live: consoleLive, Error: consoleMessages().Text(msgLoginError)},
		})
		return
	}
	if err != nil {
		s.renderPlainError(w, http.StatusInternalServerError, "не удалось создать сессию")
		return
	}
	s.sessions.SetSessionCookie(w, r, id)
	http.Redirect(w, r, "/outreach/drafts", http.StatusSeeOther)
}

// handleLogout удаляет сессию и ведёт на вход. Выход доступен без сессии и
// идемпотентен.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(s.sessions.CookieName()); err == nil {
		s.sessions.Logout(cookie.Value)
	}
	s.sessions.ClearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// outreachURL собирает адрес консольного экрана с сохранением тенанта в query.
// Тенант пока берётся из query (известный пробел до срезов доступа/тенанта:
// тенант должен следовать из сессии); актора в адресе нет — он берётся из сессии.
func outreachURL(path, tenant string) string {
	if strings.TrimSpace(tenant) == "" {
		return path
	}
	values := url.Values{}
	values.Set("tenant_id", tenant)
	return path + "?" + values.Encode()
}

// draftScreenPath собирает путь экрана одного черновика.
func draftScreenPath(campaignID, draftID string) string {
	return fmt.Sprintf("/outreach/campaigns/%s/drafts/%s", url.PathEscape(campaignID), url.PathEscape(draftID))
}

// batchScreenPath собирает путь экрана состава пакетов кампании.
func batchScreenPath(campaignID string) string {
	return fmt.Sprintf("/outreach/campaigns/%s/batches", url.PathEscape(campaignID))
}
