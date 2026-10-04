package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"lead-gen/backend/platform/consoleauth"
	"lead-gen/frontend/components"
)

// Имена страниц консольных экранов среза.
const (
	pageLogin        = "templates/login.html"
	pageDraftQueue   = "templates/draft_queue.html"
	pageBatchScreen  = "templates/batch_screen.html"
	pageDraftScreen  = "templates/draft_screen.html"
	pageStalledQueue = "templates/stalled_queue.html"
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
	return components.New(templateFS,
		pageLogin,
		pageDraftQueue,
		pageBatchScreen,
		pageDraftScreen,
		pageStalledQueue,
	)
}

// consoleNav собирает навигацию консоли: общие разделы консоли плюс подменю
// «Рассылки» с экранами этого сервиса. Общий компонент не знает раздела рассылок
// (его канонический список задан в frontend/components), поэтому подменю
// добавляется как группа — без правки общего слоя.
func (s *Server) consoleNav(active string) components.Nav {
	nav := components.ConsoleNav(s.console, "")
	nav.Groups = append(nav.Groups, components.NavGroup{
		Label: "Рассылки",
		Items: []components.NavItem{
			{Label: "Очередь черновиков", Href: "/outreach/drafts", Active: active == consoleNavDrafts},
			{Label: "Зависшие черновики", Href: "/outreach/stalled", Active: active == consoleNavStalled},
		},
	})
	return nav
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
	var buf bytes.Buffer
	if err := s.pages.Render(&buf, page, model); err != nil {
		s.renderPlainError(w, http.StatusInternalServerError, "ошибка рендеринга экрана")
		return
	}
	w.Header().Set("Content-Type", contentTypeHTML)
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
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
	Error string
}

// handleLoginForm показывает форму входа оператора.
func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	if !s.consoleConfigured {
		s.renderPlainError(w, http.StatusServiceUnavailable, "консоль рассылок не настроена: не заданы учётные данные оператора")
		return
	}
	s.render(w, http.StatusOK, pageLogin, components.Page{
		Layout: components.Layout{Title: "Вход"},
		View:   loginView{},
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
			Layout: components.Layout{Title: "Вход"},
			View:   loginView{Error: "Неверный логин или пароль."},
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
