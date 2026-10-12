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
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"lead-gen/backend/contexts/platform-governance/internal/adapters/session"
	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/backend/contexts/platform-governance/internal/app/query"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/csrf"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/observability/prometheus"
	"lead-gen/backend/platform/topology"
	"lead-gen/contracts/analytics/campaignreport"
	"lead-gen/contracts/platform-governance/accessdecision"
	"lead-gen/contracts/request-fulfillment/requestintent"
	"lead-gen/frontend/components"
)

// contentTypeHTML — тип ответа экранов портала.
const contentTypeHTML = "text/html; charset=utf-8"

// Периоды потока серверных событий портала (poll-backed SSE). Владелец заявки —
// другой контекст, поэтому обновление строится на честном опросе requestintent.List:
// кадр отправляется при расхождении списка статусов, а не по событию владельца.
const (
	// defaultPortalStreamPoll — период опроса владельца заявок.
	defaultPortalStreamPoll = 5 * time.Second
	// defaultPortalStreamHeartbeat — период heartbeat, чтобы соединение не
	// закрывали промежуточные прокси на простое.
	defaultPortalStreamHeartbeat = 15 * time.Second

	// portalScriptPath — адрес клиентского скрипта портала: подписывается на
	// SSE-поток и перерисовывает дерево заявок без перезагрузки страницы.
	portalScriptPath = "/static/portal.js"
)

// templateFS — шаблоны портала, встроенные в бинарник: разметка экранов не может
// разойтись с кодом обработчиков.
//
//go:embed templates/*.html
var templateFS embed.FS

// staticFS — собственные вендоренные скрипты экрана операций (HTMX): живая лента
// и граф обновляются без ручной перезагрузки.
//
//go:embed static/*.js
var staticFS embed.FS

// Имена страниц набора шаблонов.
const (
	pageRegister = "templates/register.html"
	pageLogin    = "templates/login.html"
	pageSent     = "templates/register_sent.html"
	pageProfile  = "templates/profile.html"
	pageReset    = "templates/reset.html"
	pagePortal   = "templates/portal.html"
	pageOffer    = "templates/offer.html"

	pageConsoleUsers         = "templates/console_users.html"
	pageConsoleTenant        = "templates/console_tenant.html"
	pageConsoleIntegrations  = "templates/console_integrations.html"
	pageConsoleAudit         = "templates/console_audit.html"
	pageConsoleDenied        = "templates/console_denied.html"
	pageConsoleRegistrations = "templates/console_registrations.html"
	pageConsoleLogin         = "templates/console_login.html"

	pageTopology     = "templates/topology.html"
	pageTopologyNode = "templates/topology_node.html"

	pageCockpit          = "templates/cockpit.html"
	pageCockpitAgents    = "templates/cockpit_agents.html"
	pageCockpitAgent     = "templates/cockpit_agent.html"
	pageCockpitProcess   = "templates/cockpit_process.html"
	pageCockpitDashboard = "templates/cockpit_dashboard.html"
)

// Dependencies — внешние зависимости HTTP-адаптера: прикладные сценарии
// контекста.
type Dependencies struct {
	// RegisterClient — регистрация тенанта клиента.
	RegisterClient *command.RegisterClientHandler

	// ApproveRegistration / RejectRegistration — решение администратора по заявке
	// на регистрацию.
	ApproveRegistration *command.ApproveRegistrationHandler
	RejectRegistration  *command.RejectRegistrationHandler

	// SaveProfile / SubmitProfile / ApproveProfile / RejectProfile — профиль
	// компании и его согласование.
	SaveProfile    *command.SaveProfileHandler
	SubmitProfile  *command.SubmitProfileHandler
	ApproveProfile *command.ApproveProfileHandler
	RejectProfile  *command.RejectProfileHandler

	// AuthenticateClient — вход клиента по email и паролю.
	AuthenticateClient *command.AuthenticateClientHandler

	// ResetClientPassword — установка нового пароля администратором.
	ResetClientPassword *command.ResetClientPasswordHandler

	// TenantProfile — чтение профиля компании для портала.
	TenantProfile *query.TenantProfileHandler

	// Sessions — серверные сессии портала (cookie portal_session). nil — память
	// процесса.
	Sessions *session.Manager

	// ConsoleAuth — сессии оператора консоли платформы (cookie session). nil
	// означает, что вход консоли не включён: разделы доступны по ?actor= (dev,
	// тесты).
	ConsoleAuth *consoleauth.Manager

	// Console — базовые адреса сервисов общей консоли. Объединённая навигация
	// ведёт из платформенной консоли в операторские разделы. Пустая карта даёт
	// относительные пути.
	Console components.ConsoleBases

	// BasePath — внешний префикс пути консоли платформы под единым origin
	// (admin-proxy). Пусто — корень origin.
	BasePath string

	// ExternalPrefixes — префиксы операторских консолей единого origin: ссылки
	// на них в навигации не переписываются под префикс консоли платформы.
	ExternalPrefixes []string

	// SubmitClientRequest — подача заявки через контракт request-intent.
	SubmitClientRequest *command.SubmitClientRequestHandler

	// PortalState — чтение состояния заявки клиента.
	PortalState *query.PortalStateHandler

	// ListClientRequests — чтение списка заявок клиента (история/дерево) через
	// контракт request-intent.
	ListClientRequests *query.ListClientRequestsHandler

	// Report — порт read-контракта campaign-report (владелец analytics) для
	// метрик главного экрана портала. nil означает, что владелец не подключён:
	// портал честно показывает «—», а не нули.
	Report campaignreport.Port

	// PortalStreamPoll — период опроса владельца заявок в SSE-эндпоинте портала.
	// 0 — значение по умолчанию.
	PortalStreamPoll time.Duration

	// PortalStreamHeartbeat — период heartbeat SSE-потока портала. 0 — значение
	// по умолчанию.
	PortalStreamHeartbeat time.Duration

	// CreateTenant — создание тенанта уполномоченным актором.
	CreateTenant *command.CreateTenantHandler

	// DisableTenant — отключение тенанта.
	DisableTenant *command.DisableTenantHandler

	// AssignRole — назначение роли пользователю.
	AssignRole *command.AssignRoleHandler

	// RevokeRole — отзыв роли у пользователя.
	RevokeRole *command.RevokeRoleHandler

	// DecideAccess — решение доступа по ролям актора.
	DecideAccess *query.DecideAccessHandler

	// AuditJournal — журнал аудита тенанта.
	AuditJournal *query.AuditJournalHandler

	// TenantUsers — состав пользователей и ролей тенанта для консоли управления.
	TenantUsers *query.TenantUsersHandler

	// ListTenants — список тенантов (платформенные атрибуты) для экрана выбора
	// активного тенанта.
	ListTenants *query.ListTenantsHandler

	// Adapters — состояние адаптеров интеграций для раздела «Интеграции».
	Adapters *query.AdapterCatalogHandler

	// Topology — загрузчик модели графа сервисной топологии (пакет
	// backend/platform/topology). nil означает, что модель не подключена: экран
	// честно сообщает о недоступности, а не выдумывает граф.
	Topology *topology.Loader

	// TopologyMetrics — клиент Prometheus для серверного прокси метрик. nil
	// означает, что метрики недоступны (available:false), а не ряд с нулями.
	TopologyMetrics MetricsQuerier

	// TopologyPanels — объявленный перечень панелей метрик. nil — встроенный
	// набор DefaultTopologyPanels. Произвольный PromQL от клиента не принимается.
	TopologyPanels []TopologyPanel

	// TopologyLayout — источник сохранённых позиций раскладки. nil или ошибка
	// означает детерминированную раскладку по умолчанию.
	TopologyLayout TopologyLayoutSource

	// Operations — источник цифр процесса, ленты агентов и агрегатов по клиентам.
	// nil означает, что владелец данных не подключён: экран операций честно
	// показывает «недоступно», а не нули.
	Operations OperationsData

	// WorkflowReader — серверное чтение процессов Temporal для графа процессов.
	// nil означает, что рантайм не подключён: раздел показывает «недоступно».
	WorkflowReader WorkflowReader

	// RequestExecutions — чтение заявок по тенанту и исполнения конкретной заявки.
	// nil означает, что источник не подключён; панель показывает недоступность.
	RequestExecutions RequestExecutionReader

	// CockpitMetrics — клиент Prometheus для метрик-панелей экрана операций. nil
	// означает отсутствие метрик; если задан TopologyMetrics, он используется как
	// источник по умолчанию (тот же Prometheus контура).
	CockpitMetrics MetricsQuerier

	// CockpitMetricPanels — объявленный перечень метрик-панелей экрана операций.
	// nil — встроенный набор OperationsMetricPanels. Произвольный PromQL от клиента
	// не принимается.
	CockpitMetricPanels []prometheus.Panel

	// CockpitPanels — дополнительные панели экрана: расширяемость регистрацией,
	// без правки остальных панелей.
	CockpitPanels []CockpitPanel

	// CockpitSettings — настройки экрана операций по субъекту (скрытые панели).
	// nil — настройки не сохраняются.
	CockpitSettings ports.CockpitSettings

	// CockpitMinGroup — порог минимальной группы полноэкранного дашборда. 0 —
	// значение по умолчанию.
	CockpitMinGroup int

	// IDs — генератор идентификаторов для ключей команд экранных форм.
	IDs ports.IDGenerator

	// Logger — структурированный логгер для фиксации фактов консоли (например,
	// переключения тенанта) и предупреждений. Необязателен: nil — логирование
	// пропускается.
	Logger observ.Logger
}

// Server — HTTP-сервер среза. Нулевое значение непригодно: используйте
// NewServer.
type Server struct {
	deps        Dependencies
	handler     http.Handler
	pages       *components.Set
	messages    *components.Messages
	csrf        csrf.Guard
	sessions    *session.Manager
	consoleAuth *consoleauth.Manager
	console     components.ConsoleBases

	// streamPoll — период опроса владельца заявок в SSE-эндпоинте портала.
	streamPoll time.Duration
	// streamHeartbeat — период heartbeat SSE-потока портала.
	streamHeartbeat time.Duration
}

// NewServer собирает маршруты среза из переданных сценариев. Шаблоны портала
// разбираются сразу: ошибка разметки видна при сборке сервиса. Если набор не
// собрался, экран честно отвечает 500.
func NewServer(deps Dependencies) *Server {
	sessions := deps.Sessions
	if sessions == nil {
		sessions = session.NewManager(nil, nil)
	}
	streamPoll := deps.PortalStreamPoll
	if streamPoll <= 0 {
		streamPoll = defaultPortalStreamPoll
	}
	streamHeartbeat := deps.PortalStreamHeartbeat
	if streamHeartbeat <= 0 {
		streamHeartbeat = defaultPortalStreamHeartbeat
	}
	server := &Server{
		deps:            deps,
		messages:        consoleMessages(),
		csrf:            csrf.NewGuard(),
		sessions:        sessions,
		consoleAuth:     deps.ConsoleAuth,
		console:         deps.Console,
		streamPoll:      streamPoll,
		streamHeartbeat: streamHeartbeat,
	}
	server.pages, _ = newPages(server.messages)
	server.handler = components.PrefixMiddleware(deps.BasePath, server.routes(), deps.ExternalPrefixes...)
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
	requirePortal := s.sessions.Middleware

	// Вход оператора консоли платформы: отдельный путь /console/login, чтобы не
	// конфликтовать с клиентским /login. Маршруты включаются, когда вход
	// сконфигурирован точкой входа.
	if s.consoleAuth != nil {
		mux.Handle("GET /console/login", http.HandlerFunc(s.handleConsoleLoginForm))
		mux.Handle("POST /console/login", http.HandlerFunc(s.handleConsoleLoginSubmit))
		mux.Handle("GET /console/logout", http.HandlerFunc(s.handleConsoleLogout))
	}

	// Корень сервиса ведёт на вход, а не отвечает 404: клиент начинает работу
	// со входа в портал.
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /register", s.handleRegisterForm)
	mux.Handle("POST /register", s.sensitive(s.handleRegisterSubmit))
	// Вход и выход из портала.
	mux.HandleFunc("GET /login", s.handleLoginForm)
	mux.Handle("POST /login", s.sensitive(s.handleLoginSubmit))
	mux.Handle("POST /logout", s.sensitive(s.handleLogout))
	// Запрос на сброс пароля клиентом.
	mux.HandleFunc("GET /reset", s.handleResetForm)
	mux.Handle("POST /reset", s.sensitive(s.handleResetSubmit))
	// Защищённые экраны портала: требуют сессию (cookie portal_session).
	mux.Handle("GET /portal", requirePortal(http.HandlerFunc(s.handlePortal)))
	mux.Handle("GET /portal/stream", requirePortal(http.HandlerFunc(s.handlePortalStream)))
	mux.Handle("GET /portal/request", requirePortal(http.HandlerFunc(s.handleOfferForm)))
	mux.Handle("POST /portal/request", requirePortal(s.sensitive(s.handleOfferSubmit)))
	mux.Handle("GET /profile", requirePortal(http.HandlerFunc(s.handleProfileForm)))
	mux.Handle("POST /profile/save", requirePortal(s.sensitive(s.handleProfileSave)))
	mux.Handle("POST /profile/submit", requirePortal(s.sensitive(s.handleProfileSubmit)))
	// Внутренний JSON API управления доступом: тенанты, роли, решения, аудит.
	mux.HandleFunc("POST /api/tenants", s.handleCreateTenant)
	mux.HandleFunc("POST /api/tenants/{tenantID}/disable", s.handleDisableTenant)
	mux.HandleFunc("POST /api/tenants/{tenantID}/roles/assign", s.handleAssignRole)
	mux.HandleFunc("POST /api/tenants/{tenantID}/roles/revoke", s.handleRevokeRole)
	mux.HandleFunc("GET /api/tenants/{tenantID}/access", s.handleAccessDecision)
	mux.HandleFunc("GET /api/tenants/{tenantID}/audit", s.handleAuditJournal)
	// Синхронный транспорт опубликованного контракта access-decision: решение
	// о доступе для потребителей (outreach-orchestration, data-governance) при
	// раздельном деплое. Форма — из самого контракта.
	mux.HandleFunc("POST "+accessdecision.PathCanAct, s.handleAccessDecisionContract)
	// Консоль управления: пользователи и роли, интеграции, журнал аудита с
	// выгрузкой и переключение тенанта. Доступ решается на сервере по матрице
	// «раздел → действие → право» (handlers_console.go).
	mux.HandleFunc("GET /console", s.handleConsoleRoot)
	mux.HandleFunc("GET /console/tenant", s.handleConsoleTenant)
	mux.HandleFunc("GET /console/users", s.handleConsoleUsers)
	mux.Handle("POST /console/users/roles/assign", s.sensitive(s.handleConsoleAssignRole))
	mux.Handle("POST /console/users/roles/revoke", s.sensitive(s.handleConsoleRevokeRole))
	mux.HandleFunc("GET /console/integrations", s.handleConsoleIntegrations)
	mux.HandleFunc("GET /console/audit", s.handleConsoleAudit)
	mux.HandleFunc("GET /console/audit/export", s.handleConsoleAuditExport)
	mux.Handle("POST /console/tenant/switch", s.sensitive(s.handleConsoleTenantSwitch))
	// Раздел «Регистрации»: заявки на регистрацию и профили компании на
	// согласовании. Доступ — платформенный оператор: у заявки нет активного
	// тенанта, в котором можно было бы проверить роль.
	mux.HandleFunc("GET /console/registrations", s.handleConsoleRegistrations)
	mux.Handle("POST /console/registrations/{tenantID}/approve", s.sensitive(s.handleConsoleRegistrationApprove))
	mux.Handle("POST /console/registrations/{tenantID}/reject", s.sensitive(s.handleConsoleRegistrationReject))
	mux.Handle("POST /console/registrations/{tenantID}/profile/approve", s.sensitive(s.handleConsoleProfileApprove))
	mux.Handle("POST /console/registrations/{tenantID}/profile/reject", s.sensitive(s.handleConsoleProfileReject))
	// Консоль сервисной топологии: страница с серверным SVG и доступной таблицей,
	// JSON-модель графа, карточка узла и серверный прокси метрик Prometheus.
	// Доступ решается на сервере по снимку access-decision (раздел topology).
	mux.HandleFunc("GET /platform/topology", s.handleTopology)
	mux.HandleFunc("GET /platform/topology/graph", s.handleTopologyGraph)
	mux.HandleFunc("GET /platform/topology/node/{id}", s.handleTopologyNode)
	mux.Handle("GET /platform/topology/metrics", s.topologyMetricsLimit(s.handleTopologyMetrics))
	// Экран операций платформы (capability operations-cockpit): цифры процесса,
	// живая лента агентов, подробности, граф процессов Temporal, полноэкранный
	// дашборд. Доступ решается на сервере по снимку access-decision (раздел
	// operations); изменяющие формы защищены токеном CSRF.
	mux.HandleFunc("GET /platform/operations", s.handleOperations)
	mux.HandleFunc("GET /platform/operations/agents", s.handleAgents)
	mux.HandleFunc("GET /platform/operations/agents/{runID}", s.handleAgent)
	mux.HandleFunc("GET /platform/operations/process", s.handleProcess)
	mux.HandleFunc("GET /platform/operations/dashboard", s.handleDashboard)
	mux.Handle("POST /platform/operations/panels/toggle", s.sensitive(s.handlePanelsToggle))
	// Скрипт HTMX этого сервиса: живая лента и граф обновляются без ручной
	// перезагрузки. Раздаётся раньше общего /static/, поэтому не перекрывается.
	mux.HandleFunc("GET /static/htmx.min.js", s.handleStaticAsset("htmx.min.js"))
	// Клиентский скрипт портала: SSE-подписка и живое дерево заявок.
	mux.HandleFunc("GET /static/portal.js", s.handleStaticAsset("portal.js"))
	// Общее оформление экранов раздаётся из frontend/components.
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(components.StaticFS())))
	mux.HandleFunc("GET /healthz", s.handleHealth)

	return s.consoleAccessGuard(mux)
}

// consoleAccessGuard включает вход оператора консоли: маршруты /console/** и
// /platform/** требуют действующей сессии consoleauth. Публичны только страница
// входа и статика. При не заданном ConsoleAuth защита выключена — консоль
// работает по ?actor= (dev, тесты). Клиентский портал и JSON API доступа не
// затрагиваются: их пути под стражу не попадают.
func (s *Server) consoleAccessGuard(next http.Handler) http.Handler {
	if s.consoleAuth == nil {
		return next
	}
	protected := s.consoleAuth.Middleware(consoleauth.Policy{
		LoginPath:      "/console/login",
		Public:         []string{"/console/login"},
		PublicPrefixes: []string{"/static/"},
		// Экран операций проверяет CSRF собственным токеном формы (csrf.Guard),
		// поэтому сессионная проверка токена здесь пропускается.
		CSRFExempt: []string{"/platform/operations/panels/toggle"},
	})(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasPrefix(path, "/console") || strings.HasPrefix(path, "/platform") {
			protected.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// newPages собирает набор шаблонов портала: каркас и общие фрагменты из
// frontend/components плюс страницы экранов. Разбор идёт при сборке сервиса.
func newPages(messages *components.Messages) (*components.Set, error) {
	return components.NewWithMessages(messages, templateFS,
		pageRegister, pageLogin, pageSent, pageProfile, pageReset, pagePortal, pageOffer,
		pageConsoleUsers, pageConsoleTenant, pageConsoleIntegrations, pageConsoleAudit, pageConsoleDenied, pageConsoleRegistrations, pageConsoleLogin,
		pageTopology, pageTopologyNode,
		pageCockpit, pageCockpitAgents, pageCockpitAgent, pageCockpitProcess, pageCockpitDashboard,
	)
}

// registerForm — значения полей формы регистрации.
type registerForm struct {
	CompanyName  string
	Description  string
	Email        string
	Password     string
	INN          string
	CompanyEmail string
	Website      string
	LegalData    string
}

// registerView — модель экрана регистрации. Поля собираются здесь, а не в
// шаблоне: правила показа решает владелец экрана.
type registerView struct {
	Notice            string
	AuthTabs          components.Tabs
	CompanyField      components.Field
	DescriptionField  components.Field
	EmailField        components.Field
	PasswordField     components.Field
	INNField          components.Field
	CompanyEmailField components.Field
	WebsiteField      components.Field
	LegalDataField    components.Field
}

// registerSentView — модель экрана подтверждения подачи заявки на регистрацию.
type registerSentView struct {
	CompanyName string
}

// loginView — модель экрана входа.
type loginView struct {
	Notice        string
	AuthTabs      components.Tabs
	EmailField    components.Field
	PasswordField components.Field
}

// resetView — модель экрана запроса на сброс пароля.
type resetView struct {
	Notice string
}

// portalView — модель единого окна портала: состояние заявки, услуги компании,
// метрики и дерево истории заявок.
type portalView struct {
	TenantID      string
	CompanyName   string
	RequestID     string
	Status        string
	ErrorMessage  string
	ErrorReason   string
	HasRequest    bool
	Notice        string
	ProfileStatus string
	Services      []query.ServiceView
	// Метрики главного экрана приходят из витрины analytics через контракт
	// campaign-report; до подключения владельца — честное «—» без выдуманных цифр.
	Found        string
	Communicated string
	Replied      string
	// Requests — дерево истории заявок клиента (текущие статусы из requestintent.List).
	Requests []query.ClientRequestView
	// RequestsUnavailable — владелец заявок недоступен: дерево честно сообщает
	// об этом, а не рисует пустой список как успех.
	RequestsUnavailable bool
}

// offerView — модель формы заявки: что предлагает клиент и кому.
type offerView struct {
	Notice       string
	ContentField components.Field
	TargetField  components.Field
}

// profileServiceField — поле одной услуги в форме профиля.
type profileServiceField struct {
	Index int
	Title string
	Name  string
	Short string
	Full  string
}

// profileView — модель экрана профиля компании.
type profileView struct {
	Notice        string
	TenantID      string
	CompanyName   string
	ProfileStatus string
	IsPending     bool
	// Редактируемые поля (пока профиль не на согласовании).
	Description components.Field
	Target      components.Field
	Services    []profileServiceField
	// Данные для чтения (профиль на согласовании: правка запрещена).
	ReadOnlyDescription string
	ReadOnlyTarget      string
	ReadOnlyServices    []query.ServiceView
}

// handleRoot обслуживает GET /{$}: корень портала ведёт на вход, а не
// отвечает 404.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// handleHealth обслуживает GET /healthz: сервис запущен и принимает запросы.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// handleRegisterForm обслуживает GET /register: форма регистрации клиента.
func (s *Server) handleRegisterForm(w http.ResponseWriter, _ *http.Request) {
	view := registerModel("", registerForm{}, nil)
	view.AuthTabs = s.authTabs("register")
	s.render(w, http.StatusOK, pageRegister, components.Page{
		Layout: components.Layout{Title: "Регистрация клиента"},
		View:   view,
	})
}

// handleRegisterSubmit обслуживает POST /register. Неполная форма не завершает
// регистрацию: экран возвращается с указанием недостающего поля, а не 500. После
// успеха показывается подтверждение, а не переход в портал.
func (s *Server) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	form := registerForm{
		CompanyName:  strings.TrimSpace(r.FormValue("company_name")),
		Description:  strings.TrimSpace(r.FormValue("description")),
		Email:        strings.TrimSpace(r.FormValue("email")),
		Password:     r.FormValue("password"),
		INN:          strings.TrimSpace(r.FormValue("inn")),
		CompanyEmail: strings.TrimSpace(r.FormValue("company_email")),
		Website:      strings.TrimSpace(r.FormValue("website")),
		LegalData:    strings.TrimSpace(r.FormValue("legal_data")),
	}

	fieldErrors := map[string]string{}
	if form.CompanyName == "" {
		fieldErrors["company_name"] = "Укажите название компании"
	}
	if form.Description == "" {
		fieldErrors["description"] = "Укажите описание компании"
	}
	if form.Email == "" {
		fieldErrors["email"] = "Укажите email учётной записи"
	}
	if form.Password == "" {
		fieldErrors["password"] = "Укажите пароль"
	}
	if form.INN == "" {
		fieldErrors["inn"] = "Укажите ИНН компании"
	}
	if form.CompanyEmail == "" {
		fieldErrors["company_email"] = "Укажите email компании"
	}
	if len(fieldErrors) > 0 {
		s.renderRegisterError(w, http.StatusBadRequest, "Заполните обязательные поля", form, fieldErrors)
		return
	}

	if _, err := s.deps.RegisterClient.Handle(r.Context(), command.RegisterClient{
		CompanyName:  form.CompanyName,
		Description:  form.Description,
		Email:        form.Email,
		Password:     form.Password,
		INN:          form.INN,
		CompanyEmail: form.CompanyEmail,
		Website:      form.Website,
		LegalData:    form.LegalData,
	}); err != nil {
		switch {
		case errors.Is(err, domain.ErrCompanyNameRequired):
			s.renderRegisterError(w, http.StatusBadRequest, "Заполните обязательные поля", form, map[string]string{"company_name": "Укажите название компании"})
		case errors.Is(err, domain.ErrDescriptionRequired):
			s.renderRegisterError(w, http.StatusBadRequest, "Заполните обязательные поля", form, map[string]string{"description": "Укажите описание компании"})
		case errors.Is(err, domain.ErrUserEmailRequired):
			s.renderRegisterError(w, http.StatusBadRequest, "Заполните обязательные поля", form, map[string]string{"email": "Укажите email учётной записи"})
		case errors.Is(err, domain.ErrUserPasswordRequired):
			s.renderRegisterError(w, http.StatusBadRequest, "Заполните обязательные поля", form, map[string]string{"password": "Укажите пароль"})
		case errors.Is(err, domain.ErrINNRequired):
			s.renderRegisterError(w, http.StatusBadRequest, "Заполните обязательные поля", form, map[string]string{"inn": "Укажите ИНН компании"})
		case errors.Is(err, domain.ErrCompanyEmailRequired):
			s.renderRegisterError(w, http.StatusBadRequest, "Заполните обязательные поля", form, map[string]string{"company_email": "Укажите email компании"})
		case errors.Is(err, domain.ErrDuplicateCompany):
			s.renderRegisterError(w, http.StatusConflict, "Компания с такими данными уже зарегистрирована", form, nil)
		default:
			s.renderRegisterError(w, statusForError(err), "Регистрация не завершена", form, nil)
		}
		return
	}

	s.render(w, http.StatusOK, pageSent, components.Page{
		Layout: components.Layout{Title: "Заявка отправлена"},
		View:   registerSentView{CompanyName: form.CompanyName},
	})
}

// handleLoginForm обслуживает GET /login: форма входа клиента.
func (s *Server) handleLoginForm(w http.ResponseWriter, _ *http.Request) {
	s.renderLogin(w, http.StatusOK, "", "")
}

// renderLogin рисует экран входа в анонимной карточке с сегментированным
// выбором «Вход | Регистрация» и сообщением. Единая точка для формы и ошибок
// входа, чтобы анонимные экраны не расходились.
func (s *Server) renderLogin(w http.ResponseWriter, status int, notice, email string) {
	view := loginModel(notice, email, nil)
	view.AuthTabs = s.authTabs("login")
	s.render(w, status, pageLogin, components.Page{
		Layout: components.Layout{Title: "Вход"},
		View:   view,
	})
}

// handleLoginSubmit обслуживает POST /login: проверка учётных данных и установка
// сессии портала.
func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	if email == "" || password == "" {
		s.renderLogin(w, http.StatusBadRequest, "Укажите email и пароль", email)
		return
	}
	result, err := s.deps.AuthenticateClient.Handle(r.Context(), command.AuthenticateClient{Email: email, Password: password})
	if err != nil {
		s.renderLogin(w, http.StatusUnauthorized, "Неверный email или пароль", email)
		return
	}
	id, err := s.sessions.Create(r.Context(), result.TenantID, result.UserID)
	if err != nil {
		s.renderLogin(w, http.StatusInternalServerError, "Не удалось создать сессию", email)
		return
	}
	s.sessions.SetCookie(w, r, id)
	http.Redirect(w, r, "/portal", http.StatusSeeOther)
}

// handleLogout обслуживает POST /logout: удаление сессии и возврат на вход.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(session.CookieName); err == nil {
		s.sessions.Delete(r.Context(), cookie.Value)
	}
	s.sessions.ClearCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// handleResetForm обслуживает GET /reset: форма запроса на сброс пароля.
func (s *Server) handleResetForm(w http.ResponseWriter, _ *http.Request) {
	s.render(w, http.StatusOK, pageReset, components.Page{
		Layout: components.Layout{Title: "Сброс пароля"},
		View:   resetView{},
	})
}

// handleResetSubmit обслуживает POST /reset. Заявка на сброс попадает
// администратору; админский сброс выполняет команда ResetClientPassword. Здесь
// честно сообщаем, что запрос отправлен, не создавая fake-эффекта.
func (s *Server) handleResetSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	s.render(w, http.StatusOK, pageReset, components.Page{
		Layout: components.Layout{Title: "Сброс пароля"},
		View:   resetView{Notice: "Запрос на сброс пароля отправлен администратору"},
	})
}

// handlePortal обслуживает GET /portal: единое окно портала с текущим состоянием
// заявок, услугами компании и метриками. Тенант берётся из сессии, а не из
// запроса.
func (s *Server) handlePortal(w http.ResponseWriter, r *http.Request) {
	sess := session.FromContext(r.Context())
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	tenantID := sess.TenantID()

	view := portalView{
		TenantID:     tenantID,
		Found:        "—",
		Communicated: "—",
		Replied:      "—",
	}
	if profile, err := s.deps.TenantProfile.Handle(r.Context(), query.TenantProfile{TenantID: tenantID}); err == nil {
		view.CompanyName = profile.CompanyName
		view.ProfileStatus = profile.ProfileStatus
		view.Services = profile.Services
	}
	if state, err := s.deps.PortalState.Handle(r.Context(), query.PortalState{TenantID: tenantID}); err == nil {
		view.RequestID = state.RequestID
		view.Status = state.Status
		view.ErrorMessage = state.ErrorMessage
		view.ErrorReason = state.ErrorReason
		view.HasRequest = state.HasRequest
	} else {
		view.Notice = portalNotice(err)
	}

	// Дерево истории заявок: список текущих статусов у владельца заявки.
	if requests, err := s.deps.ListClientRequests.Handle(r.Context(), query.ListClientRequests{TenantID: tenantID}); err == nil {
		view.Requests = requests
	} else if !errors.Is(err, app.ErrRequestNotFound) {
		view.RequestsUnavailable = true
	}

	// Метрики главного экрана: цифры процесса из read-контракта campaign-report.
	// Отсутствие клиента в ответе или недоступность владельца — честное «—»,
	// а не ноль.
	if numbers, ok := s.portalNumbers(r.Context(), tenantID); ok {
		view.Found = strconv.Itoa(numbers.Found)
		view.Communicated = strconv.Itoa(numbers.Processed)
		view.Replied = strconv.Itoa(numbers.Result)
	}

	s.render(w, http.StatusOK, pagePortal, components.Page{
		Layout:  s.portalLayout(),
		Sidebar: portalNav("/portal", true),
		View:    view,
	})
}

// portalLayout собирает каркас главного экрана портала: общее оформление и
// клиентский скрипт SSE-подписки (живое дерево заявок без перезагрузки).
func (s *Server) portalLayout() components.Layout {
	scripts := append([]string(nil), components.ConsoleScripts...)
	scripts = append(scripts, portalScriptPath)
	return components.Layout{Title: "Портал клиента", Scripts: scripts}
}

// portalNumbers возвращает цифры процесса тенанта из контракта campaign-report.
// Второе значение false означает, что владелец недоступен либо клиент отсутствует
// в ответе (срез ниже порога минимальной группы): вызывающий показывает честное
// «—», а не ноль.
func (s *Server) portalNumbers(ctx context.Context, tenantID string) (campaignreport.ClientNumbers, bool) {
	if s.deps.Report == nil {
		return campaignreport.ClientNumbers{}, false
	}
	response, err := s.deps.Report.Numbers(ctx, campaignreport.Request{})
	if err != nil {
		return campaignreport.ClientNumbers{}, false
	}
	for _, client := range response.Clients {
		if client.ClientID == tenantID {
			return client, true
		}
	}
	return campaignreport.ClientNumbers{}, false
}

// portalStreamRequest — строка заявки в кадре SSE-потока портала.
type portalStreamRequest struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

// portalStreamFrame — кадр SSE-потока портала: список заявок клиента с текущими
// статусами. Unavailable — владелец заявок недоступен: клиент видит честное
// сообщение, а не пустой список как успех.
type portalStreamFrame struct {
	Requests    []portalStreamRequest `json:"requests"`
	Unavailable bool                  `json:"unavailable"`
}

// streamSnapshot читает текущий список заявок клиента и его отпечаток. Отпечаток
// меняется при расхождении состава или статусов списка, поэтому кадр отправляется
// только при реальном изменении. Недоступность владельца даёт устойчивый отпечаток
// «unavailable» и кадр с флагом Unavailable — без выдумывания заявок.
func (s *Server) streamSnapshot(ctx context.Context, tenantID string) (portalStreamFrame, string) {
	views, err := s.deps.ListClientRequests.Handle(ctx, query.ListClientRequests{TenantID: tenantID})
	if err != nil {
		if errors.Is(err, app.ErrRequestNotFound) {
			return portalStreamFrame{Requests: []portalStreamRequest{}}, "empty"
		}
		return portalStreamFrame{Requests: []portalStreamRequest{}, Unavailable: true}, "unavailable"
	}
	frame := portalStreamFrame{Requests: make([]portalStreamRequest, 0, len(views))}
	for _, view := range views {
		frame.Requests = append(frame.Requests, portalStreamRequest{RequestID: view.RequestID, Status: view.Status})
	}
	return frame, portalStreamFingerprint(frame.Requests)
}

// portalStreamFingerprint строит устойчивый отпечаток списка заявок: набор пар
// «идентификатор:статус», отсортированный по идентификатору, чтобы порядок ответа
// владельца не давал ложного изменения.
func portalStreamFingerprint(requests []portalStreamRequest) string {
	if len(requests) == 0 {
		return "empty"
	}
	items := make([]string, 0, len(requests))
	for _, request := range requests {
		items = append(items, request.RequestID+":"+request.Status)
	}
	sort.Strings(items)
	return strings.Join(items, ",")
}

// handlePortalStream обслуживает GET /portal/stream: поток серверных событий (SSE)
// с текущими статусами заявок клиента. Владелец заявок — другой контекст
// (request-fulfillment), поэтому обновление — честный опрос requestintent.List:
// первый кадр отдаётся сразу, далее кадр отправляется только при расхождении
// списка статусов; heartbeat поддерживает соединение. Это poll-backed SSE, а не
// подмена событийного механизма владельца.
func (s *Server) handlePortalStream(w http.ResponseWriter, r *http.Request) {
	sess := session.FromContext(r.Context())
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	tenantID := sess.TenantID()

	// Заголовки потока: без кэша и без буферизации обратным прокси.
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	send := func(frame portalStreamFrame) bool {
		data, err := json.Marshal(frame)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: requests\ndata: %s\n\n", data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	// Начальный кадр: подписчик сразу видит текущий список.
	frame, fingerprint := s.streamSnapshot(r.Context(), tenantID)
	if !send(frame) {
		return
	}

	poll := time.NewTicker(s.streamPoll)
	defer poll.Stop()
	heartbeat := time.NewTicker(s.streamHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-poll.C:
			next, nextFingerprint := s.streamSnapshot(r.Context(), tenantID)
			if nextFingerprint != fingerprint {
				fingerprint = nextFingerprint
				if !send(next) {
					return
				}
			}
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		}
	}
}

// handleOfferForm обслуживает GET /portal/request: форма оффера. Тенант — из
// сессии.
func (s *Server) handleOfferForm(w http.ResponseWriter, r *http.Request) {
	if session.FromContext(r.Context()) == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, pageOffer, components.Page{
		Layout:  components.Layout{Title: "Заявка"},
		Sidebar: portalNav("/portal/request", true),
		View:    offerModel("", "", ""),
	})
}

// handleOfferSubmit обслуживает POST /portal/request: отправка заявки. После
// успеха — POST/redirect/GET на портал; созданная заявка в статусе submitted
// привязана к тенанту клиента.
func (s *Server) handleOfferSubmit(w http.ResponseWriter, r *http.Request) {
	sess := session.FromContext(r.Context())
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	tenant := sess.TenantID()
	content := strings.TrimSpace(r.FormValue("content"))
	target := strings.TrimSpace(r.FormValue("target"))

	if content == "" {
		s.renderOfferError(w, http.StatusBadRequest, content, target, "Опишите, что вы предлагаете")
		return
	}
	profile, err := s.deps.TenantProfile.Handle(r.Context(), query.TenantProfile{TenantID: tenant})
	if err != nil || profile.ProfileStatus != string(domain.ProfileActive) || profile.TenantID != tenant {
		s.renderOfferError(w, http.StatusForbidden, content, target, "Для подачи заявки нужен активный одобренный профиль")
		return
	}
	services := make([]requestintent.SenderServiceSnapshot, len(profile.Services))
	for i, service := range profile.Services {
		services[i] = requestintent.SenderServiceSnapshot{Name: service.Name, Short: service.Short, Full: service.Full}
	}
	sender := &requestintent.SenderCompanySnapshot{
		CompanyName: profile.CompanyName, Website: profile.Website,
		Description: profile.Description, Services: services,
	}

	result, err := s.deps.SubmitClientRequest.Handle(r.Context(), command.SubmitClientRequest{
		TenantID:      tenant,
		Content:       content,
		Target:        target,
		Actor:         sess.UserID(),
		SenderCompany: sender,
	})
	if err != nil {
		if errors.Is(err, app.ErrRejected) {
			s.renderOfferError(w, http.StatusBadRequest, content, target, err.Error())
			return
		}
		s.render(w, statusForError(err), pageOffer, components.Page{
			Layout:  components.Layout{Title: "Заявка"},
			Sidebar: portalNav("/portal/request", true),
			View:    offerModel(content, target, portalNotice(err)),
		})
		return
	}

	http.Redirect(w, r, "/portal?request_id="+result.RequestID, http.StatusSeeOther)
}

// handleProfileForm обслуживает GET /profile: форма профиля компании. Пока
// профиль на согласовании, правка заблокирована и показывается только чтение.
func (s *Server) handleProfileForm(w http.ResponseWriter, r *http.Request) {
	sess := session.FromContext(r.Context())
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	tenantID := sess.TenantID()
	profile, err := s.deps.TenantProfile.Handle(r.Context(), query.TenantProfile{TenantID: tenantID})
	if err != nil {
		s.render(w, statusForError(err), pageProfile, components.Page{
			Layout:  components.Layout{Title: "Профиль компании"},
			Sidebar: portalNav("/profile", true),
			View:    profileModel(tenantID, "", "", portalNotice(err), false),
		})
		return
	}
	notice := profileNotice(r.URL.Query().Get("notice"))
	pending := profile.ProfileStatus == string(domain.ProfilePending)
	view := profileModel(tenantID, profile.CompanyName, profile.ProfileStatus, notice, pending)
	if pending {
		view.ReadOnlyDescription = profile.Description
		view.ReadOnlyTarget = profile.Target
		view.ReadOnlyServices = profile.Services
	} else {
		view.Description = components.Field{
			ID: "profile-description", Name: "description", Label: "Описание компании",
			Type: "textarea", Rows: 4, Value: profile.Description,
		}
		view.Target = components.Field{
			ID: "profile-target", Name: "target", Label: "Кого ищете",
			Value: profile.Target,
		}
		services := make([]profileServiceField, 0, len(profile.Services)+1)
		for i, svc := range profile.Services {
			services = append(services, profileServiceField{Index: i, Title: fmt.Sprintf("Услуга %d", i+1), Name: svc.Name, Short: svc.Short, Full: svc.Full})
		}
		services = append(services, profileServiceField{Index: len(services), Title: "Новая услуга"})
		view.Services = services
	}
	s.render(w, http.StatusOK, pageProfile, components.Page{
		Layout:  components.Layout{Title: "Профиль компании"},
		Sidebar: portalNav("/profile", true),
		View:    view,
	})
}

// handleProfileSave обслуживает POST /profile/save: сохранение черновика профиля.
func (s *Server) handleProfileSave(w http.ResponseWriter, r *http.Request) {
	sess := session.FromContext(r.Context())
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	tenantID := sess.TenantID()
	err := s.deps.SaveProfile.Handle(r.Context(), command.SaveProfile{
		TenantID:    tenantID,
		Description: strings.TrimSpace(r.FormValue("description")),
		Target:      strings.TrimSpace(r.FormValue("target")),
		Services:    parseServices(r),
	})
	if err != nil {
		if errors.Is(err, domain.ErrProfilePending) {
			http.Redirect(w, r, "/profile?notice=pending", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/profile?notice=error", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}

// handleProfileSubmit обслуживает POST /profile/submit: отправка профиля на
// согласование.
func (s *Server) handleProfileSubmit(w http.ResponseWriter, r *http.Request) {
	sess := session.FromContext(r.Context())
	if sess == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	tenantID := sess.TenantID()
	// Отправка работает по уже сохранённому профилю; повторное сохранение формы
	// на случай, если клиент отправил сразу после правки без отдельного save.
	_ = s.deps.SaveProfile.Handle(r.Context(), command.SaveProfile{
		TenantID:    tenantID,
		Description: strings.TrimSpace(r.FormValue("description")),
		Target:      strings.TrimSpace(r.FormValue("target")),
		Services:    parseServices(r),
	})
	err := s.deps.SubmitProfile.Handle(r.Context(), command.SubmitProfile{TenantID: tenantID})
	if err != nil {
		if errors.Is(err, domain.ErrProfileIncomplete) {
			http.Redirect(w, r, "/profile?notice=incomplete", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/profile?notice=error", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}

// renderRegisterError рисует форму регистрации с сообщением и ошибками полей.
func (s *Server) renderRegisterError(w http.ResponseWriter, status int, notice string, form registerForm, fieldErrors map[string]string) {
	view := registerModel(notice, form, fieldErrors)
	view.AuthTabs = s.authTabs("register")
	s.render(w, status, pageRegister, components.Page{
		Layout: components.Layout{Title: "Регистрация клиента"},
		View:   view,
	})
}

// renderOfferError рисует форму заявки с сообщением об ошибке.
func (s *Server) renderOfferError(w http.ResponseWriter, status int, content, target, notice string) {
	s.render(w, status, pageOffer, components.Page{
		Layout:  components.Layout{Title: "Заявка"},
		Sidebar: portalNav("/portal/request", true),
		View:    offerModel(content, target, notice),
	})
}

// registerModel собирает модель экрана регистрации: поля формы с ошибками.
func registerModel(notice string, form registerForm, fieldErrors map[string]string) registerView {
	return registerView{
		Notice: notice,
		CompanyField: components.Field{
			ID: "company-name", Name: "company_name", Label: "Компания",
			Value: form.CompanyName, Required: true, Class: "span-2",
			Error: components.FieldError(fieldErrors, "company_name"),
		},
		DescriptionField: components.Field{
			ID: "description", Name: "description", Label: "Описание компании",
			Type: "textarea", Rows: 5, Value: form.Description, Required: true,
			Class: "span-2",
			Error: components.FieldError(fieldErrors, "description"),
		},
		EmailField: components.Field{
			ID: "email", Name: "email", Label: "Email учётной записи",
			Type: "email", Autocomplete: "email", Value: form.Email, Required: true,
			Error: components.FieldError(fieldErrors, "email"),
		},
		PasswordField: components.Field{
			ID: "password", Name: "password", Label: "Пароль",
			Type: "password", Autocomplete: "new-password", Required: true,
			Error: components.FieldError(fieldErrors, "password"),
		},
		INNField: components.Field{
			ID: "inn", Name: "inn", Label: "ИНН компании",
			Value: form.INN, Required: true, Error: components.FieldError(fieldErrors, "inn"),
		},
		CompanyEmailField: components.Field{
			ID: "company-email", Name: "company_email", Label: "Email компании",
			Type: "email", Value: form.CompanyEmail, Required: true,
			Error: components.FieldError(fieldErrors, "company_email"),
		},
		WebsiteField: components.Field{
			ID: "website", Name: "website", Label: "Сайт компании",
			Value: form.Website, Class: "span-2",
			Error: components.FieldError(fieldErrors, "website"),
		},
		LegalDataField: components.Field{
			ID: "legal-data", Name: "legal_data", Label: "Юридические данные",
			Type: "textarea", Rows: 3, Value: form.LegalData,
			Class: "span-2",
			Error: components.FieldError(fieldErrors, "legal_data"),
		},
	}
}

// loginModel собирает модель экрана входа.
func loginModel(notice, email string, fieldErrors map[string]string) loginView {
	return loginView{
		Notice: notice,
		EmailField: components.Field{
			ID: "login-email", Name: "email", Label: "Email учётной записи",
			Type: "email", Autocomplete: "email", Value: email, Required: true,
			Error: components.FieldError(fieldErrors, "email"),
		},
		PasswordField: components.Field{
			ID: "login-password", Name: "password", Label: "Пароль",
			Type: "password", Autocomplete: "current-password", Required: true,
			Error: components.FieldError(fieldErrors, "password"),
		},
	}
}

// offerModel собирает модель формы заявки: что предлагает клиент и кому.
func offerModel(content, target, notice string) offerView {
	return offerView{
		Notice: notice,
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

// profileModel собирает модель экрана профиля компании.
func profileModel(tenantID, companyName, profileStatus, notice string, pending bool) profileView {
	return profileView{
		TenantID:      tenantID,
		CompanyName:   companyName,
		ProfileStatus: profileStatus,
		Notice:        notice,
		IsPending:     pending,
	}
}

// profileNotice переводит код уведомления профиля в сообщение экрана.
func profileNotice(code string) string {
	switch code {
	case "pending":
		return "Профиль на согласовании, правка недоступна"
	case "incomplete":
		return "Профиль заполнен не полностью"
	case "error":
		return "Не удалось сохранить профиль"
	default:
		return ""
	}
}

// parseServices разбирает услуги из проиндексированных полей формы
// service_name_i / service_short_i / service_full_i. Пустые блоки пропускаются.
func parseServices(r *http.Request) []domain.Service {
	maxIndex := -1
	for key := range r.Form {
		if !strings.HasPrefix(key, "service_name_") {
			continue
		}
		if n, err := strconv.Atoi(strings.TrimPrefix(key, "service_name_")); err == nil && n > maxIndex {
			maxIndex = n
		}
	}
	services := make([]domain.Service, 0, maxIndex+1)
	for i := 0; i <= maxIndex; i++ {
		name := strings.TrimSpace(r.FormValue(fmt.Sprintf("service_name_%d", i)))
		short := strings.TrimSpace(r.FormValue(fmt.Sprintf("service_short_%d", i)))
		full := strings.TrimSpace(r.FormValue(fmt.Sprintf("service_full_%d", i)))
		if name == "" && short == "" && full == "" {
			continue
		}
		services = append(services, domain.Service{Name: name, Short: short, Full: full})
	}
	return services
}

// portalNav собирает вертикальную навигацию портала с активным пунктом. Состав
// зависит от того, вошёл ли клиент: анонимному показываются вход и регистрация,
// вошедшему — рабочие экраны портала.
// portalNav собирает вертикальную навигацию аутентифицированного портала с
// активным пунктом. Анонимный посетитель рабочего меню не имеет: анонимные
// экраны рендерятся в auth-карточке с сегментированным выбором входа и
// регистрации (authTabs), а не в каркасе консоли.
func portalNav(active string, authenticated bool) components.Nav {
	if !authenticated {
		return components.Nav{}
	}
	items := []components.NavItem{
		{Label: "Портал", Href: "/portal"},
		{Label: "Профиль", Href: "/profile"},
		{Label: "Заявка", Href: "/portal/request"},
	}
	for i := range items {
		items[i].Active = items[i].Href == active
	}
	return components.Nav{Items: items}
}

// authTabs собирает сегментированный выбор «Вход | Регистрация» для анонимных
// экранов. Подписи берутся из каталога сообщений, шаблон только показывает их.
func (s *Server) authTabs(active string) components.Tabs {
	return components.Tabs{
		Active: active,
		Items: []components.Tab{
			{Key: "login", Label: s.messages.Text(msgAuthLogin), Href: "/login"},
			{Key: "register", Label: s.messages.Text(msgAuthRegister), Href: "/register"},
		},
	}
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
	// Экран без собственных таблиц стилей получает общее оформление компонентов:
	// внешний вид портала не расходится с другими серверными экранами.
	if len(model.Layout.Stylesheets) == 0 {
		model.Layout.Stylesheets = components.ConsoleStylesheets
	}
	if len(model.Layout.BootstrapScripts) == 0 {
		model.Layout.BootstrapScripts = components.ConsoleBootstrapScripts
	}
	if len(model.Layout.Scripts) == 0 {
		// Скрипт управления фокусом и aria-live: экраны консоли используют
		// гипермедиа-фрагменты, и подмена не должна терять фокус (срез 7, 7.3).
		model.Layout.Scripts = components.ConsoleScripts
	}
	var buf bytes.Buffer
	if err := s.pages.Render(&buf, page, model); err != nil {
		s.renderPlainError(w, http.StatusInternalServerError, "ошибка рендеринга экрана")
		return
	}
	// Экраны несут данные тенанта и оператора: ответ не кэшируется (срез 7, 7.5).
	components.SetNoStore(w)
	w.Header().Set("Content-Type", contentTypeHTML)
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// renderPlainError отдаёт текстовую ошибку для случаев, когда HTML-экран
// недоступен (шаблоны не собраны и т.п.).
func (s *Server) renderPlainError(w http.ResponseWriter, status int, message string) {
	http.Error(w, message, status)
}

// handleStaticAsset отдаёт вендоренный скрипт из собственного каталога статики
// сервиса. Ответ не кэшируется: файл раздаётся вместе с экраном.
func (s *Server) handleStaticAsset(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		data, err := staticFS.ReadFile("static/" + name)
		if err != nil {
			http.Error(w, "не найдено", http.StatusNotFound)
			return
		}
		components.SetNoStore(w)
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(data)
	}
}
