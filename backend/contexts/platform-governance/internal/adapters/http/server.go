// Package httpapi реализует HTTP-адаптер роли api контекста platform-governance:
// экраны самообслуживания клиента (регистрация и портал).
//
// Это эксплуатационная проекция роли api, а не второй домен: адаптер только
// разбирает форму, вызывает прикладной сценарий и рисует представление. Решений о
// допустимости он не принимает — инварианты обеспечивает агрегат tenant, а
// создание заявки — владелец через контракт request-intent. Обработчики зависят
// от сценариев и не ссылаются на хранилище напрямую.
package httpapi

import (
	"bytes"
	"embed"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/backend/contexts/platform-governance/internal/app/query"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/frontend/components"
)

// contentTypeHTML — тип ответа экранов портала.
const contentTypeHTML = "text/html; charset=utf-8"

// templateFS — шаблоны портала, встроенные в бинарник: разметка экранов не может
// разойтись с кодом обработчиков.
//
//go:embed templates/*.html
var templateFS embed.FS

// Имена страниц набора шаблонов.
const (
	pageRegister = "templates/register.html"
	pagePortal   = "templates/portal.html"
	pageOffer    = "templates/offer.html"
)

// Dependencies — внешние зависимости HTTP-адаптера: прикладные сценарии
// контекста.
type Dependencies struct {
	// RegisterClient — регистрация тенанта клиента.
	RegisterClient *command.RegisterClientHandler

	// SubmitClientRequest — подача заявки через контракт request-intent.
	SubmitClientRequest *command.SubmitClientRequestHandler

	// PortalState — чтение состояния заявки клиента.
	PortalState *query.PortalStateHandler
}

// Server — HTTP-сервер среза. Нулевое значение непригодно: используйте
// NewServer.
type Server struct {
	deps    Dependencies
	handler http.Handler
	pages   *components.Set
}

// NewServer собирает маршруты среза из переданных сценариев. Шаблоны портала
// разбираются сразу: ошибка разметки видна при сборке сервиса. Если набор не
// собрался, экран честно отвечает 500.
func NewServer(deps Dependencies) *Server {
	server := &Server{deps: deps}
	server.pages, _ = newPages()
	server.handler = server.routes()
	return server
}

// ServeHTTP реализует http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// routes объявляет маршруты портала: регистрация, единое окно портала, форма
// заявки и проверка живости.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /register", s.handleRegisterForm)
	mux.HandleFunc("POST /register", s.handleRegisterSubmit)
	mux.HandleFunc("GET /portal", s.handlePortal)
	mux.HandleFunc("GET /portal/request", s.handleOfferForm)
	mux.HandleFunc("POST /portal/request", s.handleOfferSubmit)
	mux.HandleFunc("GET /healthz", s.handleHealth)

	return mux
}

// newPages собирает набор шаблонов портала: каркас и общие фрагменты из
// frontend/components плюс страницы экранов. Разбор идёт при сборке сервиса.
func newPages() (*components.Set, error) {
	return components.New(templateFS, pageRegister, pagePortal, pageOffer)
}

// registerView — модель экрана регистрации. Поля собираются здесь, а не в
// шаблоне: правила показа решает владелец экрана.
type registerView struct {
	Notice           string
	CompanyField     components.Field
	DescriptionField components.Field
}

// portalView — модель единого окна портала: состояние заявки клиента.
type portalView struct {
	TenantID   string
	RequestID  string
	Status     string
	HasRequest bool
	Notice     string
}

// offerView — модель формы заявки: что предлагает клиент и кому.
type offerView struct {
	TenantID     string
	Notice       string
	ContentField components.Field
	TargetField  components.Field
}

// handleHealth обслуживает GET /healthz: сервис запущен и принимает запросы.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// handleRegisterForm обслуживает GET /register: форма регистрации клиента.
func (s *Server) handleRegisterForm(w http.ResponseWriter, _ *http.Request) {
	s.render(w, http.StatusOK, pageRegister, components.Page{
		Layout:  components.Layout{Title: "Регистрация клиента"},
		Sidebar: portalNav("/register"),
		View:    registerModel("", "", "", nil),
	})
}

// handleRegisterSubmit обслуживает POST /register. Неполная форма не завершает
// регистрацию: экран возвращается с указанием недостающего поля, а не 500.
func (s *Server) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	company := strings.TrimSpace(r.FormValue("company_name"))
	description := strings.TrimSpace(r.FormValue("description"))

	fieldErrors := map[string]string{}
	if company == "" {
		fieldErrors["company_name"] = "Укажите название компании"
	}
	if description == "" {
		fieldErrors["description"] = "Укажите описание компании"
	}
	if len(fieldErrors) > 0 {
		s.renderRegisterError(w, http.StatusBadRequest, "Заполните обязательные поля", company, description, fieldErrors)
		return
	}

	result, err := s.deps.RegisterClient.Handle(r.Context(), command.RegisterClient{
		CompanyName: company,
		Description: description,
	})
	if err != nil {
		if errors.Is(err, domain.ErrCompanyNameRequired) {
			s.renderRegisterError(w, http.StatusBadRequest, "Заполните обязательные поля", company, description,
				map[string]string{"company_name": "Укажите название компании"})
			return
		}
		if errors.Is(err, domain.ErrDescriptionRequired) {
			s.renderRegisterError(w, http.StatusBadRequest, "Заполните обязательные поля", company, description,
				map[string]string{"description": "Укажите описание компании"})
			return
		}
		s.renderRegisterError(w, statusForError(err), "Регистрация не завершена", company, description, nil)
		return
	}

	http.Redirect(w, r, portalURL(result.TenantID, ""), http.StatusSeeOther)
}

// handlePortal обслуживает GET /portal: единое окно портала с текущим состоянием
// заявки клиента. Тенант берётся из запроса (dev-контур без аутентификации).
func (s *Server) handlePortal(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant_id")
	view, err := s.deps.PortalState.Handle(r.Context(), query.PortalState{
		TenantID:  tenant,
		RequestID: r.URL.Query().Get("request_id"),
	})
	if err != nil {
		status := statusForError(err)
		s.render(w, status, pagePortal, components.Page{
			Layout:  components.Layout{Title: "Портал клиента"},
			Sidebar: portalNav("/portal"),
			View:    portalModel(tenant, "", "", false, portalNotice(err)),
		})
		return
	}
	s.render(w, http.StatusOK, pagePortal, components.Page{
		Layout:  components.Layout{Title: "Портал клиента"},
		Sidebar: portalNav("/portal"),
		View:    portalModel(view.TenantID, view.RequestID, view.Status, view.HasRequest, ""),
	})
}

// handleOfferForm обслуживает GET /portal/request: форма оффера.
func (s *Server) handleOfferForm(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant_id")
	s.render(w, http.StatusOK, pageOffer, components.Page{
		Layout:  components.Layout{Title: "Заявка"},
		Sidebar: portalNav("/portal/request"),
		View:    offerModel(tenant, "", "", ""),
	})
}

// handleOfferSubmit обслуживает POST /portal/request: отправка заявки. После
// успеха — POST/redirect/GET на портал; созданная заявка в статусе submitted
// привязана к тенанту клиента.
func (s *Server) handleOfferSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	tenant := strings.TrimSpace(r.FormValue("tenant_id"))
	content := strings.TrimSpace(r.FormValue("content"))
	target := strings.TrimSpace(r.FormValue("target"))

	if content == "" {
		s.renderOfferError(w, http.StatusBadRequest, tenant, content, target, "Опишите, что вы предлагаете")
		return
	}

	result, err := s.deps.SubmitClientRequest.Handle(r.Context(), command.SubmitClientRequest{
		TenantID: tenant,
		Content:  content,
		Target:   target,
	})
	if err != nil {
		if errors.Is(err, app.ErrRejected) {
			s.renderOfferError(w, http.StatusBadRequest, tenant, content, target, err.Error())
			return
		}
		s.render(w, statusForError(err), pageOffer, components.Page{
			Layout:  components.Layout{Title: "Заявка"},
			Sidebar: portalNav("/portal/request"),
			View:    offerModel(tenant, content, target, portalNotice(err)),
		})
		return
	}

	http.Redirect(w, r, portalURL(tenant, result.RequestID), http.StatusSeeOther)
}

// renderRegisterError рисует форму регистрации с сообщением и ошибками полей.
func (s *Server) renderRegisterError(w http.ResponseWriter, status int, notice, company, description string, fieldErrors map[string]string) {
	s.render(w, status, pageRegister, components.Page{
		Layout:  components.Layout{Title: "Регистрация клиента"},
		Sidebar: portalNav("/register"),
		View:    registerModel(notice, company, description, fieldErrors),
	})
}

// renderOfferError рисует форму заявки с сообщением об ошибке.
func (s *Server) renderOfferError(w http.ResponseWriter, status int, tenant, content, target, notice string) {
	s.render(w, status, pageOffer, components.Page{
		Layout:  components.Layout{Title: "Заявка"},
		Sidebar: portalNav("/portal/request"),
		View:    offerModel(tenant, content, target, notice),
	})
}

// registerModel собирает модель экрана регистрации: поля формы с ошибками.
func registerModel(notice, company, description string, fieldErrors map[string]string) registerView {
	return registerView{
		Notice: notice,
		CompanyField: components.Field{
			ID: "company-name", Name: "company_name", Label: "Компания",
			Value: company, Required: true, Error: components.FieldError(fieldErrors, "company_name"),
		},
		DescriptionField: components.Field{
			ID: "description", Name: "description", Label: "Описание компании",
			Type: "textarea", Rows: 5, Value: description, Required: true,
			Error: components.FieldError(fieldErrors, "description"),
		},
	}
}

// offerModel собирает модель формы заявки: что предлагает клиент и кому.
func offerModel(tenant, content, target, notice string) offerView {
	return offerView{
		TenantID: tenant,
		Notice:   notice,
		ContentField: components.Field{
			ID: "offer-content", Name: "content", Label: "Что вы предлагаете",
			Type: "textarea", Rows: 6, Value: content, Required: true,
		},
		TargetField: components.Field{
			ID: "offer-target", Name: "target", Label: "Кому адресован оффер",
			Value: target,
		},
	}
}

// portalModel собирает модель единого окна портала.
func portalModel(tenant, requestID, status string, hasRequest bool, notice string) portalView {
	return portalView{
		TenantID:   tenant,
		RequestID:  requestID,
		Status:     status,
		HasRequest: hasRequest,
		Notice:     notice,
	}
}

// portalNav собирает вертикальную навигацию портала с активным пунктом.
func portalNav(active string) components.Nav {
	items := []components.NavItem{
		{Label: "Регистрация", Href: "/register"},
		{Label: "Портал", Href: "/portal"},
		{Label: "Заявка", Href: "/portal/request"},
	}
	for i := range items {
		items[i].Active = items[i].Href == active
	}
	return components.Nav{Items: items}
}

// portalURL собирает адрес портала для тенанта с необязательным идентификатором
// заявки.
func portalURL(tenant, requestID string) string {
	values := url.Values{}
	if tenant != "" {
		values.Set("tenant_id", tenant)
	}
	if requestID != "" {
		values.Set("request_id", requestID)
	}
	if encoded := values.Encode(); encoded != "" {
		return "/portal?" + encoded
	}
	return "/portal"
}

// portalNotice переводит исход сценария в сообщение экрана. Существование чужой
// заявки не раскрывается: «не найдено» и «чужая» неотличимы.
func portalNotice(err error) string {
	switch {
	case errors.Is(err, app.ErrRequestNotFound):
		return "Заявка не найдена"
	case errors.Is(err, app.ErrRejected):
		return "Заявка отклонена: " + err.Error()
	case errors.Is(err, app.ErrUnavailable):
		return "Владелец заявки недоступен"
	default:
		return "Внутренняя ошибка"
	}
}

// statusForError классифицирует ошибку сценария. Отсутствие заявки — 404, отказ
// владельца — 400, недоступность контракта — 502.
func statusForError(err error) int {
	switch {
	case errors.Is(err, app.ErrRequestNotFound):
		return http.StatusNotFound
	case errors.Is(err, app.ErrRejected):
		return http.StatusBadRequest
	case errors.Is(err, app.ErrUnavailable):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// render рисует страницу в буфер: частично отданный HTML выглядел бы сломанной
// страницей вместо честного отказа.
func (s *Server) render(w http.ResponseWriter, status int, page string, model components.Page) {
	if s.pages == nil {
		s.renderPlainError(w, http.StatusInternalServerError, "шаблоны портала не собраны")
		return
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
