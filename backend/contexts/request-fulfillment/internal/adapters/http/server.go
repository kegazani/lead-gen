// Package httpapi реализует HTTP-адаптер роли api контекста request-fulfillment.
//
// Адаптер только разбирает запрос, вызывает прикладной сценарий и переводит
// доменную ошибку в код ответа. Решений о допустимости он не принимает:
// инварианты обеспечивает aggregate root request, а транзакцию — прикладной
// слой. Обработчики зависят от сценариев и не ссылаются на хранилище напрямую.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/researchbridge"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/readiness"
	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
	"lead-gen/frontend/components"
)

const (
	// maxBodyBytes ограничивает размер тела запроса, чтобы разбор не мог
	// исчерпать память процесса.
	maxBodyBytes = 1 << 20

	contentTypeJSON = "application/json; charset=utf-8"
	contentTypeHTML = "text/html; charset=utf-8"

	// contentSecurityPolicy — политика безопасности консольных HTML-экранов
	// заявок. Повторяет политику консоли lead-management: без неё разметка
	// остаётся без ограничений на источники стилей, скриптов и форм.
	contentSecurityPolicy = "default-src 'none'; style-src 'self'; script-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
)

// Dependencies — внешние зависимости HTTP-адаптера: прикладные сценарии
// контекста. FulfillmentQuery допускается равным nil только вместе с запросом
// статусов артефактов.
type Dependencies struct {
	// Research — явно подключённый путь исследования; nil сохраняет прежнее demo.
	Research               *researchbridge.Service
	ExecutionProtocol      ports.ExecutionProtocol
	ExecutionConsumer      ports.ExecutionIntentConsumer
	AdmitExecution         *command.AdmitExecutionHandler
	SubmitRequest          *command.SubmitRequestHandler
	StartValidation        *command.StartValidationHandler
	ValidateRequest        *command.ValidateRequestHandler
	StartProcessing        *command.StartProcessingHandler
	FulfillRequest         *command.FulfillRequestHandler
	RejectRequest          *command.RejectRequestHandler
	CancelRequest          *command.CancelRequestHandler
	FailRequest            *command.FailRequestHandler
	RerunRequest           *command.RerunRequestHandler
	ReturnForRevision      *command.ReturnForRevisionHandler
	Resubmit               *command.ResubmitRequestHandler
	ReviseOffer            *command.ReviseOfferHandler
	RecordAudienceSnapshot *command.RecordAudienceSnapshotHandler
	RecordCampaign         *command.RecordCampaignHandler
	RecordDeal             *command.RecordDealHandler
	GetRequest             *query.GetRequestHandler
	ListRequests           *query.ListRequestsHandler
	FulfillmentStatus      *query.FulfillmentStatusHandler
	ExecutionState         *query.ExecutionStateHandler

	// WorkflowStatus — чтение состояния исполнения заявки. nil означает, что
	// рантайм workflow не подключён: обработчик честно отвечает 503.
	WorkflowStatus ports.WorkflowStatusReader

	// WorkflowStarter — запуск исполнения заявки. nil означает, что рантайм
	// workflow не подключён: обработчик честно отвечает 503.
	WorkflowStarter ports.WorkflowStarter

	// WorkflowSignaler — сигнал утверждения результата (человеческая пауза
	// workflow). nil означает, что рантайм не подключён: обработчик честно
	// отвечает 503.
	WorkflowSignaler ports.WorkflowSignaler
	// WorkflowCanceller — отмена активного Temporal исполнения после durable
	// перехода заявки в cancelled.
	WorkflowCanceller ports.WorkflowCanceller
	// OutreachCancellation fences late handoff and approval at their owner.
	OutreachCancellation outreachhandoff.Port

	// ValidationQueue — очередь заявок на валидацию (экран валидации).
	ValidationQueue *query.ValidationQueueHandler

	// ApprovalQueue — очередь заявок, ожидающих утверждения результата
	// исполнения (экран утверждения). nil трактуется как «исполнение
	// недоступно»: экран честно показывает этот исход.
	ApprovalQueue *query.ApprovalQueueHandler

	// InitiateCall — инициирование звонка-уточнения (экран валидации).
	InitiateCall *command.InitiateCallHandler

	// RecordCall — фиксация ссылки на звонок (экран валидации).
	RecordCall *command.RecordCallHandler

	// EmbedOffer — формирование эмбеддинга оффера после валидации. nil означает,
	// что векторное хранилище не подключено: шаг пропускается.
	EmbedOffer *command.EmbedOfferHandler

	// EmailDrafts — чтение черновиков писем заявки (для консоли оператора). nil
	// означает, что хранилище черновиков не подключено: обработчик честно
	// отвечает 503.
	EmailDrafts *query.EmailDraftsHandler

	// RequestWatcher — подписка на изменения заявок тенанта (SSE-поток экрана
	// заявок компании). nil означает отсутствие потока: обработчик честно
	// отвечает 503.
	RequestWatcher ports.RequestWatcher

	// Runtime — monitor-backed факт доступности Temporal. nil считается unknown.
	Runtime RuntimeProbe
	// Readiness — явный профиль readiness владельца контекста.
	Readiness readiness.Profile
	// ReadinessEvaluator ограничивает и кэширует проверки профиля.
	ReadinessEvaluator *readiness.Evaluator

	// Console — базовые адреса сервисов консоли для общей навигации по разделам
	// (Заявки, Исполнение, Лиды, Отчёты). Пустые адреса дают относительные пути.
	Console components.ConsoleBases

	// BasePath — внешний префикс пути консоли под единым origin (admin-proxy).
	// Пусто — консоль смонтирована в корень origin.
	BasePath string

	// ExternalPrefixes — префиксы других консолей единого origin: ссылки на них
	// в навигации не переписываются под собственный префикс.
	ExternalPrefixes []string

	// Auth — учётные данные оператора консоли заявок и общий стор сессий. Пустое
	// CookieName означает общее имя cookie "session": вход в консоль заявок
	// действителен и в консоли lead-management, когда обе делят стор сессий.
	Auth consoleauth.Config
}

// RuntimeProbe — признак доступности рантайма исполнения. Реализуется
// platform/workflow.Availability; HTTP-адаптер зависит от узкого интерфейса, а не
// от платформенного пакета целиком.
type RuntimeProbe interface {
	Ready() bool
}

// Server — HTTP-сервер среза. Нулевое значение непригодно: используйте
// NewServer.
type Server struct {
	deps     Dependencies
	handler  http.Handler
	pages    *components.Set
	sessions *consoleauth.Manager
	console  components.ConsoleBases
}

// NewServer собирает маршруты среза из переданных сценариев. Шаблоны экрана
// разбираются сразу: ошибка разметки видна при сборке сервиса. Если набор не
// собрался, HTTP-часть с данными продолжает работать, а экран честно отвечает 500.
func NewServer(deps Dependencies) *Server {
	server := &Server{deps: deps, sessions: consoleauth.New(deps.Auth), console: deps.Console}
	server.pages, _ = newPages()
	server.handler = components.PrefixMiddleware(deps.BasePath, server.routes(), deps.ExternalPrefixes...)
	return server
}

// consoleNav собирает общую навигацию консоли с активным разделом. Сервис
// заявок владеет разделами «Заявки» и «Исполнение»: их пункты относительны,
// остальные ведут в свои сервисы по базовым адресам. Контекст активного тенанта и
// актора переносится в ссылки, чтобы выбранный тенант не терялся при переходе в
// консоль другого сервиса.
func (s *Server) consoleNav(active, tenant, actor string) components.Nav {
	return components.ConsoleNavWithContext(s.console, active, components.ConsoleContext{TenantID: tenant, Actor: actor})
}

// ServeHTTP реализует http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// handleRoot обслуживает GET /{$}: корень сервиса ведёт на очередь валидации, а
// не отвечает 404 — это первый экран оператора в контуре заявок.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/validation", http.StatusSeeOther)
}

// routes объявляет маршруты среза.
//
// Маршруты оборачиваются поштучно, а не общим middleware по префиксу: пути
// контрактных JSON-эндпоинтов и консольных экранов переплетены (например,
// /requests/{id}/validation/*), а контрактные обязаны оставаться анонимными.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	s.registerResearchRoutes(mux)

	// Вход и выход консоли: публичные HTML-страницы под теми же заголовками
	// безопасности, что и остальные экраны. Вход ограничен по частоте (7.5).
	mux.Handle("GET /login", s.publicHTML(s.handleLoginForm))
	mux.Handle("POST /login", s.publicHTML(s.rateLimit("/login", s.handleLogin)))
	mux.Handle("GET /logout", s.publicHTML(s.handleLogout))

	// Корень сервиса ведёт на очередь валидации, а не отвечает 404.
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("POST /requests", s.handleSubmit)
	mux.HandleFunc("GET /requests", s.handleList)
	mux.HandleFunc("GET /requests/{requestID}", s.handleGet)
	mux.HandleFunc("POST /requests/{requestID}/validation/start", s.handleStartValidation)
	mux.HandleFunc("POST /requests/{requestID}/validation/complete", s.handleValidate)
	mux.HandleFunc("POST /requests/{requestID}/processing/start", s.handleStartProcessing)
	mux.HandleFunc("POST /requests/{requestID}/fulfillment", s.handleFulfill)
	mux.HandleFunc("POST /requests/{requestID}/rejection", s.handleReject)
	mux.Handle("POST /requests/{requestID}/cancellation", s.consoleAPIMutation(http.HandlerFunc(s.handleCancel)))
	mux.HandleFunc("POST /requests/{requestID}/offer", s.handleReviseOffer)
	mux.HandleFunc("POST /requests/{requestID}/artifacts/audience-snapshot", s.handleRecordAudience)
	mux.HandleFunc("POST /requests/{requestID}/artifacts/campaign", s.handleRecordCampaign)
	mux.HandleFunc("POST /requests/{requestID}/artifacts/deal", s.handleRecordDeal)
	mux.HandleFunc("GET /requests/{requestID}/fulfillment-status", s.handleFulfillmentStatus)
	mux.HandleFunc("GET /requests/{requestID}/email-drafts", s.handleEmailDrafts)
	mux.HandleFunc("GET /requests/{requestID}/workflow", s.handleWorkflowStatus)
	mux.Handle("POST /requests/{requestID}/workflow", s.cookieAuthMutation(http.HandlerFunc(s.handleStartWorkflow)))
	mux.Handle("POST /requests/{requestID}/workflow/approval-decision", s.consoleAPIMutation(http.HandlerFunc(s.handleWorkflowApprovalDecision)))
	mux.HandleFunc("GET /healthz", s.handleHealth)
	// /readyz отражает готовность обслуживать работу с учётом доступности
	// рантайма durable-исполнения; /healthz остаётся пробой живости.
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	// Общее оформление экранов раздаётся из frontend/components.
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(components.StaticFS())))

	// Экран валидации (HTMX + серверный рендеринг) — эксплуатационная проекция
	// роли api. Действия — POST/redirect/GET; частичные ответы — для HTMX.
	// Консольные маршруты требуют сессию оператора, а изменяющие — CSRF-токен.
	mux.Handle("GET /validation", components.NoStore(s.consoleHandler(s.handleValidationQueue)))
	mux.Handle("GET /requests/{requestID}/validation", components.NoStore(s.consoleHandler(s.handleValidationScreen)))
	mux.Handle("POST /requests/{requestID}/validation/take", s.consoleHandler(s.rateLimit("/validation/take", s.handleValidationTake)))
	mux.Handle("POST /requests/{requestID}/validation/offer", s.consoleHandler(s.handleValidationOffer))
	mux.Handle("POST /requests/{requestID}/validation/approve", s.consoleHandler(s.rateLimit("/validation/approve", s.handleValidationApprove)))
	mux.Handle("POST /requests/{requestID}/validation/reject", s.consoleHandler(s.rateLimit("/validation/reject", s.handleValidationReject)))
	mux.Handle("POST /requests/{requestID}/validation/call", s.consoleHandler(s.handleValidationCall))

	// Экран утверждения результата фулфилмента (HTMX + серверный рендеринг):
	// очередь ожидающих решения, экран заявки и кнопки решения. Действия —
	// POST/redirect/GET; частичные ответы — для HTMX. Решения утверждения
	// ограничены по частоте (7.5).
	mux.Handle("GET /approvals", components.NoStore(s.consoleHandler(s.handleApprovalQueue)))
	mux.Handle("GET /requests/{requestID}/approval", components.NoStore(s.consoleHandler(s.handleApprovalScreen)))
	mux.Handle("POST /requests/{requestID}/approval/approve", s.consoleHandler(s.rateLimit("/approval/approve", s.handleApprovalApprove)))
	mux.Handle("POST /requests/{requestID}/approval/reject", s.consoleHandler(s.rateLimit("/approval/reject", s.handleApprovalReject)))

	// Экран заявок компании: список заявок выбранного тенанта с историей
	// переходов и действиями «вернуть на доработку» и «отменить» (с комментарием).
	mux.Handle("GET /company", components.NoStore(s.consoleHandler(s.handleCompanyRequests)))
	mux.Handle("POST /requests/{requestID}/revision", s.consoleHandler(s.handleCompanyReturnForRevision))
	mux.Handle("POST /requests/{requestID}/cancel", s.consoleHandler(s.handleCompanyCancel))
	mux.Handle("POST /requests/{requestID}/rerun", s.consoleHandler(s.handleCompanyRerun))
	// Живое обновление статусов заявок компании (SSE): тот же контракт доступа,
	// что и у экрана /company — требуется сессия оператора.
	mux.Handle("GET /company/stream", s.authMiddleware(http.HandlerFunc(s.handleCompanyStream)))
	// Скрипт клиентской подписки на поток. CSP экранов запрещает inline-скрипты,
	// поэтому подписка — отдельный файл, раздаваемый из встроенного каталога.
	mux.Handle("GET /static/company_requests.js", s.companyStreamScript())
	mux.Handle("GET /static/request_progress.js", s.requestProgressScript())

	return mux
}

// Порог и окно ограничения частоты чувствительных действий консоли.
const (
	sensitiveLimit  = 30
	sensitiveWindow = time.Minute
)

// rateLimit оборачивает чувствительное действие ограничителем частоты общего слоя:
// вход, взятие в работу и решения (web-frontend-architecture, срез 7, 7.5).
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

// recordRateLimit фиксирует блокировку в журнале процесса.
func (s *Server) recordRateLimit(r *http.Request, path, key string) {
	log.Printf("request-fulfillment: превышен порог частоты path=%s key=%s", path, key)
}

// publicHTML навешивает заголовки безопасности на публичную HTML-страницу
// консоли (вход, выход): сессия на них не требуется.
func (s *Server) publicHTML(next http.HandlerFunc) http.Handler {
	return components.SecurityHeaders(contentSecurityPolicy)(next)
}

// consoleHandler оборачивает консольный экран заголовками безопасности и
// требованием сессии: без валидной cookie запрос редиректится на вход, а
// изменяющий — без верного CSRF — отклоняется.
func (s *Server) consoleHandler(next http.HandlerFunc) http.Handler {
	return components.SecurityHeaders(contentSecurityPolicy)(s.authMiddleware(next))
}

// authMiddleware требует сессию для обёрнутых консольных маршрутов и проверяет
// CSRF-токен на изменяющих запросах. Правила задаёт общий пакет consoleauth; здесь
// только путь входа. Отказ по CSRF — стандартный 403 пакета: разметка отказа на
// экранах заявок не отличается от остальных ошибок транспорта.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return s.sessions.Middleware(consoleauth.Policy{LoginPath: "/login"})(next)
}

// cookieAuthMutation сохраняет анонимный сетевой API контракт, но если браузер
// прислал cookie консоли, требует действующую сессию и CSRF как для остальных
// cookie-authenticated mutation routes.
func (s *Server) cookieAuthMutation(next http.Handler) http.Handler {
	protected := s.authMiddleware(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(s.sessions.CookieName()); err == nil {
			protected.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// consoleAPIMutation использует тот же серверный session store и CSRF middleware,
// что и HTML-консоль, но для API возвращает 401 вместо браузерного redirect.
func (s *Server) consoleAPIMutation(next http.Handler) http.Handler {
	protected := s.authMiddleware(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.sessions.CookieName())
		if err != nil {
			writeErrorMessage(w, http.StatusUnauthorized, "требуется сессия оператора")
			return
		}
		if _, ok := s.sessions.Authenticate(cookie.Value); !ok {
			writeErrorMessage(w, http.StatusUnauthorized, "требуется сессия оператора")
			return
		}
		if token := r.Header.Get("X-CSRF-Token"); token != "" {
			clone := r.Clone(r.Context())
			urlCopy := *r.URL
			values := urlCopy.Query()
			values.Set("csrf", token)
			urlCopy.RawQuery = values.Encode()
			clone.URL = &urlCopy
			r = clone
		}
		protected.ServeHTTP(w, r)
	})
}

// loginView — модель страницы входа.
type loginView struct {
	Live  components.Live
	Error string
}

// handleLoginForm показывает форму входа оператора.
func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, pageLogin, components.Page{
		Layout: components.Layout{Title: consoleMessages().Text(msgReqLoginTitle)},
		View:   loginView{Live: consoleLive},
	})
}

// handleLogin проверяет учётные данные и ставит cookie сессии консоли заявок.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	id, err := s.sessions.Login(r.FormValue("username"), r.FormValue("password"))
	if errors.Is(err, consoleauth.ErrInvalidCredentials) {
		s.render(w, http.StatusUnauthorized, pageLogin, components.Page{
			Layout: components.Layout{Title: consoleMessages().Text(msgReqLoginTitle)},
			View:   loginView{Live: consoleLive, Error: consoleMessages().Text(msgReqLoginError)},
		})
		return
	}
	if err != nil {
		s.renderPlainError(w, http.StatusInternalServerError, "не удалось создать сессию")
		return
	}
	s.sessions.SetSessionCookie(w, r, id)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleLogout удаляет сессию консоли заявок и ведёт на вход. Выход доступен без
// сессии и идемпотентен.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(s.sessions.CookieName()); err == nil {
		s.sessions.Logout(cookie.Value)
	}
	s.sessions.ClearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// errorResponse — единая форма ошибки HTTP-слоя.
type errorResponse struct {
	Error string `json:"error"`
}

// decodeJSON читает тело запроса в dst, отвергая неизвестные поля.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("тело запроса отсутствует")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("разбор JSON: %w", err)
	}
	return nil
}

// writeJSON отправляет полезную нагрузку с заданным статусом.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// writeErrorMessage отправляет ошибку транспорта без разбора по домену.
func writeErrorMessage(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

// writeDomainError переводит ошибку сценария в код ответа.
func writeDomainError(w http.ResponseWriter, err error) {
	if errors.Is(err, command.ErrTenantMismatch) || errors.Is(err, ports.ErrRequestNotFound) {
		// Для чужой заявки не раскрываем ни факт существования, ни отличие от
		// неизвестного идентификатора.
		writeErrorMessage(w, http.StatusNotFound, "заявка не найдена")
		return
	}
	status := statusForError(err)
	if status >= http.StatusInternalServerError {
		writeJSON(w, status, errorResponse{Error: "внутренняя ошибка"})
		return
	}
	writeJSON(w, status, errorResponse{Error: err.Error()})
}

// statusForError классифицирует ошибку прикладного и доменного слоя. Ошибки не
// найдены приходят как 404, конфликты версий и состояний — как 409, нарушение
// формы запроса — как 400, отказ чтения контракта — как 502.
func statusForError(err error) int {
	if errors.Is(err, domain.ErrExecutionModeConflict) || errors.Is(err, domain.ErrResearchSelectionConflict) {
		return http.StatusConflict
	}
	switch {
	case errors.Is(err, ports.ErrRequestNotFound):
		return http.StatusNotFound
	case errors.Is(err, ports.ErrVersionConflict),
		errors.Is(err, ports.ErrExecutionCommandConflict),
		errors.Is(err, ports.ErrExecutionAdmissionBlocked),
		errors.Is(err, ports.ErrExecutionFenceConflict),
		errors.Is(err, domain.ErrInvalidTransition),
		errors.Is(err, domain.ErrTerminalState),
		errors.Is(err, domain.ErrRequestLimitReached),
		errors.Is(err, domain.ErrActiveRequestLimitReached):
		return http.StatusConflict
	case errors.Is(err, ports.ErrArtifactUnavailable):
		return http.StatusBadGateway
	case errors.Is(err, ports.ErrWorkflowNotFound):
		return http.StatusNotFound
	case errors.Is(err, ports.ErrWorkflowUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, ports.ErrExecutionAdmissionUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, ports.ErrCallUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, ports.ErrCallRejected):
		return http.StatusConflict
	case errors.Is(err, ports.ErrVectorUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, ports.ErrDraftStoreUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, errInvalidLeadTarget):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrMissingActor),
		errors.Is(err, domain.ErrMissingTime),
		errors.Is(err, domain.ErrMissingEventID),
		errors.Is(err, domain.ErrUnknownRequestStatus),
		errors.Is(err, domain.ErrOfferRequired),
		errors.Is(err, domain.ErrEmptyOfferContent),
		errors.Is(err, domain.ErrReasonRequired),
		errors.Is(err, domain.ErrTenantImmutable),
		errors.Is(err, domain.ErrInvalidOfferRevision):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
