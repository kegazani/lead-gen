package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"lead-gen/backend/platform/consoleauth"
	"lead-gen/frontend/components"
)

// Вход оператора консоли платформы. Отдельный путь /console/login не
// конфликтует с клиентским /login; сессия — общая с другими консолями
// (consoleauth, cookie session).

// consoleLoginView — модель страницы входа консоли.
type consoleLoginView struct {
	Notice   string
	Username string
}

// handleConsoleLoginForm обслуживает GET /console/login.
func (s *Server) handleConsoleLoginForm(w http.ResponseWriter, r *http.Request) {
	if s.consoleAuth == nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, http.StatusOK, pageConsoleLogin, components.Page{
		Layout: components.Layout{Title: "Вход в консоль"},
		View:   consoleLoginView{},
	})
}

// handleConsoleLoginSubmit обслуживает POST /console/login: проверяет учётные
// данные, ставит сессию и ведёт в раздел регистраций.
func (s *Server) handleConsoleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if s.consoleAuth == nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	username := strings.TrimSpace(r.PostFormValue("username"))
	id, err := s.consoleAuth.Login(username, r.PostFormValue("password"))
	if err != nil {
		status := http.StatusUnauthorized
		notice := "Неверный логин или пароль"
		if !errors.Is(err, consoleauth.ErrInvalidCredentials) {
			status = http.StatusInternalServerError
			notice = "Не удалось создать сессию"
		}
		s.render(w, status, pageConsoleLogin, components.Page{
			Layout: components.Layout{Title: "Вход в консоль"},
			View:   consoleLoginView{Notice: notice, Username: username},
		})
		return
	}
	s.consoleAuth.SetSessionCookie(w, r, id)
	http.Redirect(w, r, "/console/registrations", http.StatusSeeOther)
}

// handleConsoleLogout обслуживает GET /console/logout: снимает сессию и ведёт на
// страницу входа.
func (s *Server) handleConsoleLogout(w http.ResponseWriter, r *http.Request) {
	if s.consoleAuth == nil {
		http.NotFound(w, r)
		return
	}
	if cookie, err := r.Cookie(s.consoleAuth.CookieName()); err == nil {
		s.consoleAuth.Logout(cookie.Value)
	}
	s.consoleAuth.ClearSessionCookie(w, r)
	http.Redirect(w, r, "/console/login", http.StatusSeeOther)
}
