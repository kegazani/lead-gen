// Package httpapi реализует HTTP-адаптер среза утверждения контекста
// outreach-orchestration.
//
// Адаптер только разбирает запрос, вызывает прикладной сценарий и переводит
// доменную ошибку в код ответа. Решений о допустимости он не принимает:
// инварианты обеспечивает aggregate root campaign, а транзакцию и проверку
// доступа — прикладной слой (design.md, Решения 1 и 6). Обработчики зависят от
// узких интерфейсов сценариев и хранилища ключей и не ссылаются на конкретные
// реализации.
//
// Точка входа POST /campaigns обслуживает границу контракта campaign-command в
// объёме среза: поле command_id — ключ идемпотентности (tenant_id, command_id),
// поэтому повтор команды не создаёт вторую кампанию (задача 6.2).
package httpapi

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/idempotency"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	appeditorial "lead-gen/backend/contexts/outreach-orchestration/internal/app/editorialreview"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/query"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/readiness"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
	"lead-gen/contracts/outreach-orchestration/editorialreview"
	"lead-gen/frontend/components"
)

const (
	// maxBodyBytes ограничивает размер тела запроса, чтобы разбор не мог
	// исчерпать память процесса.
	maxBodyBytes = 1 << 20

	contentTypeJSON = "application/json; charset=utf-8"
	contentTypeHTML = "text/html; charset=utf-8"

	// contentSecurityPolicy — политика безопасности консольных HTML-экранов
	// рассылок. Повторяет политику консолей lead-management и request-fulfillment:
	// без неё разметка остаётся без ограничений на источники стилей, скриптов и
	// форм.
	contentSecurityPolicy = "default-src 'none'; style-src 'self'; script-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
)

// templateFS — шаблоны операторских экранов очереди утверждения рассылок. Они
// встроены в бинарник, поэтому разметка не может разойтись с кодом обработчиков.
//
//go:embed templates/*.html
var templateFS embed.FS

// CreateCampaignUseCase — узкий вход сценария создания кампании.
type CreateCampaignUseCase interface {
	Handle(context.Context, command.CreateCampaign) (command.CreateCampaignResult, error)
}

// DraftActionUseCase — узкий вход сценария постановки черновика.
type DraftActionUseCase interface {
	Handle(context.Context, command.DraftAction) (command.DraftActionResult, error)
}

// ApproveDraftUseCase — узкий вход сценария утверждения черновика.
type ApproveDraftUseCase interface {
	Handle(context.Context, command.ApproveDraft) (command.DraftDecisionResult, error)
}

// RejectDraftUseCase — узкий вход сценария отклонения черновика.
type RejectDraftUseCase interface {
	Handle(context.Context, command.RejectDraft) (command.DraftDecisionResult, error)
}

// ReviseDraftUseCase — узкий вход сценария доработки черновика.
type ReviseDraftUseCase interface {
	Handle(context.Context, command.ReviseDraft) (command.ReviseDraftResult, error)
}

// ApprovalQueueUseCase — узкий вход запроса очереди утверждения.
type ApprovalQueueUseCase interface {
	Handle(context.Context, query.ApprovalQueue) ([]query.ApprovalQueueItem, error)
}

// DraftDecisionUseCase — узкий вход запроса журнала решений по черновику.
type DraftDecisionUseCase interface {
	Handle(context.Context, query.DraftDecisionJournal) ([]query.DraftDecisionEntry, error)
}

// BatchQueueUseCase — узкий вход запроса пакетной проекции очереди. Проекция уже
// посчитана агрегатом: адаптер её только сериализует.
type BatchQueueUseCase interface {
	Handle(context.Context, query.BatchQueue) (query.BatchQueueProjection, error)
}

// BuildBatchUseCase — узкий вход сценария показа состава пакета: digest,
// образец содержимого, перечень отличий и исключённые с причинами.
type BuildBatchUseCase interface {
	Handle(context.Context, command.BuildBatch) (command.BuildBatchResult, error)
}

// ApproveBatchUseCase — узкий вход сценария пакетного решения по digest состава.
type ApproveBatchUseCase interface {
	Handle(context.Context, command.ApproveBatch) (command.ApproveBatchResult, error)
}

// PostponeDraftUseCase — узкий вход сценария отложения решения по черновику.
type PostponeDraftUseCase interface {
	Handle(context.Context, command.PostponeDraft) (command.PostponeDraftResult, error)
}

// DraftQueueUseCase — узкий вход запроса очереди черновиков с проверками риска.
type DraftQueueUseCase interface {
	Handle(context.Context, query.DraftQueue) ([]query.DraftQueueItem, error)
}

// StalledDraftsUseCase — узкий вход запроса среза зависших черновиков.
type StalledDraftsUseCase interface {
	Handle(context.Context, query.StalledDrafts) (query.StalledDraftsResult, error)
}

// DefineSequenceUseCase — узкий вход сценария создания цепочки.
type DefineSequenceUseCase interface {
	Handle(context.Context, command.DefineSequence) (command.DefineSequenceResult, error)
}

// EnrollLeadUseCase — узкий вход сценария добавления лида в цепочку.
type EnrollLeadUseCase interface {
	Handle(context.Context, command.EnrollLead) (command.EnrollLeadResult, error)
}

// RemoveLeadUseCase — узкий вход сценария удаления лида из цепочки.
type RemoveLeadUseCase interface {
	Handle(context.Context, command.RemoveLead) (command.RemoveLeadResult, error)
}

// RecordOutcomeUseCase — узкий вход сценария записи реакции лида.
type RecordOutcomeUseCase interface {
	Handle(context.Context, command.RecordOutcome) (command.RecordOutcomeResult, error)
}

// ResumeExecutionUseCase — узкий вход сценария возобновления цепочки.
type ResumeExecutionUseCase interface {
	Handle(context.Context, command.ResumeExecution) (command.ResumeExecutionResult, error)
}

// SequenceHistoryUseCase — узкий вход запроса истории шагов по лиду.
type SequenceHistoryUseCase interface {
	Handle(context.Context, query.SequenceHistory) ([]query.SequenceHistoryEntry, error)
}

// SequenceStateUseCase — узкий вход запроса состояния цепочек.
type SequenceStateUseCase interface {
	Handle(context.Context, query.SequenceState) ([]query.SequenceStateItem, error)
}

// HandoffUseCase — узкий вход приёма передачи писем заявки (контракт
// outreach-handoff). nil означает, что приём не подключён: маршрут честно
// отвечает 503, а не 200 с пустым результатом.
type HandoffUseCase interface {
	Handle(context.Context, command.SubmitHandoff) (command.SubmitHandoffResult, error)
}

type EditorialHandoffUseCase interface {
	Submit(context.Context, appeditorial.Handoff) (appeditorial.Campaign, bool, error)
}

type EditorialReviewPort interface{ editorialreview.Port }

// CancelRequestUseCase — durable cancellation fence for a request owner.
type CancelRequestUseCase interface {
	Handle(context.Context, command.CancelRequest) error
}

// SubmitApprovalUseCase — узкий вход подачи черновика агента на утверждение
// (контракт campaign-command в части подачи). nil означает, что подача не
// подключена: маршрут честно отвечает 503, а не 200 с пустым результатом.
type SubmitApprovalUseCase interface {
	Handle(context.Context, command.SubmitForApproval) (command.SubmitForApprovalResult, error)
}

// ApprovedActionUseCase — узкий вход чтения утверждённого действия по черновику
// (read-контракт draftdecision). nil означает, что источник решения не подключён:
// маршрут отвечает 503, а не выдуманным решением.
type ApprovedActionUseCase interface {
	Handle(context.Context, query.ApprovedActionQuery) (approvedaction.ApprovedAction, error)
}

// CommandKeyStore — узкий интерфейс хранилища ключей идемпотентности команд.
// Его реализует adapters/idempotency.Store; HTTP-адаптер работает с ключами
// только через этот интерфейс, а к пакету idempotency обращается лишь за
// sentinel-ошибкой ErrReservationAbandoned, чтобы классифицировать ответ.
//
// Reserve резервирует ключ до выполнения эффекта: ровно один вызывающий
// получает owner=true, остальные ждут результата. Complete публикует результат,
// Release освобождает ключ при ошибке эффекта. Такой порядок закрывает TOCTOU
// между проверкой ключа и записью эффекта.
type CommandKeyStore interface {
	Reserve(ctx context.Context, tenant domain.TenantID, command domain.CommandID, candidate domain.CampaignID) (domain.CampaignID, bool, error)
	Complete(tenant domain.TenantID, command domain.CommandID, campaign domain.CampaignID)
	Release(tenant domain.TenantID, command domain.CommandID)
}

// Dependencies — внешние зависимости HTTP-адаптера. CommandKeys допускается
// равным nil: тогда идемпотентность границы не проверяется.
type Dependencies struct {
	Readiness      func(context.Context) readiness.Snapshot
	CreateCampaign CreateCampaignUseCase
	DraftAction    DraftActionUseCase
	ApproveDraft   ApproveDraftUseCase
	RejectDraft    RejectDraftUseCase
	ReviseDraft    ReviseDraftUseCase
	ApprovalQueue  ApprovalQueueUseCase
	DraftDecision  DraftDecisionUseCase
	BatchQueue     BatchQueueUseCase
	BuildBatch     BuildBatchUseCase
	ApproveBatch   ApproveBatchUseCase
	CommandKeys    CommandKeyStore

	DefineSequence  DefineSequenceUseCase
	EnrollLead      EnrollLeadUseCase
	RemoveLead      RemoveLeadUseCase
	RecordOutcome   RecordOutcomeUseCase
	ResumeExecution ResumeExecutionUseCase
	SequenceHistory SequenceHistoryUseCase
	SequenceState   SequenceStateUseCase

	// Handoff — приём передачи писем заявки из request-fulfillment (контракт
	// outreach-handoff). Межсервисная граница: сессия консоли и CSRF не
	// требуются. nil — приём не подключён, маршрут отвечает 503.
	Handoff          HandoffUseCase
	EditorialHandoff EditorialHandoffUseCase
	EditorialReview  EditorialReviewPort
	// CancelRequest durably fences request handoffs and approvals; nil is
	// unavailable rather than an implicit successful cancellation.
	CancelRequest CancelRequestUseCase

	// SubmitApproval — подача черновика агента на утверждение (контракт
	// campaign-command в части подачи). Межсервисная граница: сессия консоли и
	// CSRF не требуются. nil — подача не подключена, маршрут отвечает 503.
	SubmitApproval SubmitApprovalUseCase

	// ApprovedAction — чтение утверждённого действия по черновику (read-контракт
	// draftdecision) для шага отправки. nil — источник решения не подключён,
	// маршрут отвечает 503.
	ApprovedAction ApprovedActionUseCase

	// BatchLimits — лимиты пакета, передаваемые сценариям параметром. Это
	// конфигурация композиции, а не доменное правило: адаптер лишь прокидывает
	// её в команду и ничего по ней не решает (design.md, Решение 7).
	BatchLimits domain.BatchLimits

	// --- Операторская консоль очереди утверждения рассылок (web-frontend-
	// architecture, срез 4). Экраны — эксплуатационная проекция роли api; они
	// повторяют приёмы консоли lead-management и экранов request-fulfillment.

	// PostponeDraft — отложение решения по черновику (индивидуальное решение).
	PostponeDraft PostponeDraftUseCase

	// DraftQueue — очередь черновиков с фильтрами и результатом автоматических
	// проверок риска. nil трактуется как «очередь недоступна».
	DraftQueue DraftQueueUseCase

	// StalledDrafts — срез зависших черновиков без автоутверждения.
	StalledDrafts StalledDraftsUseCase

	// Auth — учётные данные оператора консоли рассылок и стор сессий. Пустое
	// CookieName означает общее имя cookie "session": вход в консоль рассылок
	// действителен и в других консолях, когда они делят стор сессий.
	Auth consoleauth.Config

	// Console — базовые адреса сервисов консоли для общей навигации по разделам.
	Console components.ConsoleBases

	// BasePath — внешний префикс пути консоли под единым origin (admin-proxy).
	// Пусто — консоль смонтирована в корень origin.
	BasePath string

	// ExternalPrefixes — префиксы других консолей единого origin: ссылки на них
	// в навигации не переписываются под собственный префикс.
	ExternalPrefixes []string
}

// Server — HTTP-сервер среза. Нулевое значение непригодно: используйте
// NewServer.
type Server struct {
	// deps — полный набор зависимостей: консольные обработчики обращаются к
	// сценариям по имени, а узкие поля ниже сохранены для существующих путей.
	deps Dependencies

	createCampaign CreateCampaignUseCase
	draftAction    DraftActionUseCase
	approveDraft   ApproveDraftUseCase
	rejectDraft    RejectDraftUseCase
	reviseDraft    ReviseDraftUseCase
	approvalQueue  ApprovalQueueUseCase
	draftDecision  DraftDecisionUseCase
	batchQueue     BatchQueueUseCase
	buildBatch     BuildBatchUseCase
	approveBatch   ApproveBatchUseCase
	commandKeys    CommandKeyStore
	batchLimits    domain.BatchLimits

	postponeDraft PostponeDraftUseCase
	draftQueue    DraftQueueUseCase
	stalledDrafts StalledDraftsUseCase

	// Консольные экраны: общие шаблоны компонентов, серверные сессии/CSRF и
	// базовые адреса сервисов для навигации.
	pages             *components.Set
	sessions          *consoleauth.Manager
	console           components.ConsoleBases
	consoleConfigured bool

	defineSequence  DefineSequenceUseCase
	enrollLead      EnrollLeadUseCase
	removeLead      RemoveLeadUseCase
	recordOutcome   RecordOutcomeUseCase
	resumeExecution ResumeExecutionUseCase
	sequenceHistory SequenceHistoryUseCase
	sequenceState   SequenceStateUseCase

	handoff          HandoffUseCase
	editorialHandoff EditorialHandoffUseCase
	editorialReview  EditorialReviewPort
	cancelRequest    CancelRequestUseCase

	submitApproval SubmitApprovalUseCase
	approvedAction ApprovedActionUseCase

	handler http.Handler
}

// SetReadiness подключает оценку владельца без вызова бизнес-сценариев.
func (s *Server) SetReadiness(check func(context.Context) readiness.Snapshot) {
	s.deps.Readiness = check
	s.handler = components.PrefixMiddleware(s.deps.BasePath, s.routes(), s.deps.ExternalPrefixes...)
}

// NewServer собирает маршруты среза из переданных сценариев.
func NewServer(deps Dependencies) *Server {
	server := &Server{
		deps:           deps,
		createCampaign: deps.CreateCampaign,
		draftAction:    deps.DraftAction,
		approveDraft:   deps.ApproveDraft,
		rejectDraft:    deps.RejectDraft,
		reviseDraft:    deps.ReviseDraft,
		approvalQueue:  deps.ApprovalQueue,
		draftDecision:  deps.DraftDecision,
		batchQueue:     deps.BatchQueue,
		buildBatch:     deps.BuildBatch,
		approveBatch:   deps.ApproveBatch,
		commandKeys:    deps.CommandKeys,
		batchLimits:    deps.BatchLimits,

		postponeDraft: deps.PostponeDraft,
		draftQueue:    deps.DraftQueue,
		stalledDrafts: deps.StalledDrafts,

		sessions:          consoleauth.New(deps.Auth),
		console:           deps.Console,
		consoleConfigured: strings.TrimSpace(deps.Auth.Username) != "",

		defineSequence:  deps.DefineSequence,
		enrollLead:      deps.EnrollLead,
		removeLead:      deps.RemoveLead,
		recordOutcome:   deps.RecordOutcome,
		resumeExecution: deps.ResumeExecution,
		sequenceHistory: deps.SequenceHistory,
		sequenceState:   deps.SequenceState,

		handoff:          deps.Handoff,
		editorialHandoff: deps.EditorialHandoff,
		editorialReview:  deps.EditorialReview,
		cancelRequest:    deps.CancelRequest,

		submitApproval: deps.SubmitApproval,
		approvedAction: deps.ApprovedAction,
	}
	// Шаблоны консольных экранов разбираются сразу, чтобы ошибка разметки была
	// видна при сборке сервиса. Если набор не собрался, JSON-часть продолжает
	// работать, а экран честно отвечает 500.
	server.pages, _ = newPages()
	server.handler = components.PrefixMiddleware(deps.BasePath, server.routes(), deps.ExternalPrefixes...)
	return server
}

// ServeHTTP реализует http.Handler, поэтому Server можно передать в
// http.Server или httptest.NewServer без дополнительной обвязки.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// routes объявляет маршруты среза. Методы и шаблоны путей разбирает ServeMux
// стандартной библиотеки.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /readyz", s.handleReady)

	mux.HandleFunc("POST /campaigns", s.handleCreateCampaign)
	mux.HandleFunc("GET /campaigns", s.handleApprovalQueue)
	mux.HandleFunc("POST /campaigns/{campaignID}/drafts", s.handleDraftAction)
	mux.HandleFunc("POST /campaigns/{campaignID}/drafts/{draftID}/approve", s.handleApproveDraft)
	mux.HandleFunc("POST /campaigns/{campaignID}/drafts/{draftID}/reject", s.handleRejectDraft)
	mux.HandleFunc("POST /campaigns/{campaignID}/drafts/{draftID}/revise", s.handleReviseDraft)
	mux.HandleFunc("GET /campaigns/{campaignID}/drafts/{draftID}/decisions", s.handleDraftDecision)
	mux.HandleFunc("GET /campaigns/{campaignID}/batches", s.handleBatchQueue)
	mux.HandleFunc("GET /campaigns/{campaignID}/batches/composition", s.handleBatchComposition)
	mux.HandleFunc("POST /campaigns/{campaignID}/batches/approve", s.handleApproveBatch)

	mux.HandleFunc("POST /campaigns/{campaignID}/sequences", s.handleDefineSequence)
	mux.HandleFunc("GET /campaigns/{campaignID}/sequences", s.handleSequenceState)
	mux.HandleFunc("GET /campaigns/{campaignID}/sequences/history", s.handleSequenceHistory)
	mux.HandleFunc("POST /campaigns/{campaignID}/enrollments", s.handleEnrollLead)
	mux.HandleFunc("POST /campaigns/{campaignID}/enrollments/{executionID}/remove", s.handleRemoveLead)
	mux.HandleFunc("POST /campaigns/{campaignID}/enrollments/{executionID}/outcomes", s.handleRecordOutcome)
	mux.HandleFunc("POST /campaigns/{campaignID}/enrollments/{executionID}/resume", s.handleResumeExecution)

	// Приём передачи писем заявки (контракт outreach-handoff). Межсервисная
	// граница: команда приходит от request-fulfillment, сессия консоли и CSRF не
	// требуются — как у контрактных эндпоинтов lead-search/email-generation/
	// request-intent.
	mux.HandleFunc("POST /outreach-handoff", s.handleHandoff)
	mux.HandleFunc("POST /outreach-handoff/cancellation", s.handleHandoffCancellation)

	// Подача черновика агентом в очередь утверждения и чтение решения оператора
	// (контракты campaign-command и draftdecision). Межсервисные границы: команда
	// приходит от agent-orchestration, сессия консоли и CSRF не требуются.
	mux.HandleFunc("POST /campaign-command", s.handleCampaignCommand)
	mux.HandleFunc("GET /approved-action", s.handleApprovedAction)
	mux.Handle("GET /outreach/editorial/{campaignID}/{leadID}", components.NoStore(s.consoleHandler(s.handleEditorialRead)))
	mux.Handle("POST /outreach/editorial/{campaignID}/{leadID}/decision", s.consoleHandler(s.handleEditorialDecide))

	// Операторская консоль очереди утверждения рассылок (web-frontend-
	// architecture, срез 4): HTMX + серверный рендеринг поверх тех же
	// прикладных сценариев. Действия — POST/redirect/GET; изменяющие запросы
	// требуют сессию оператора и CSRF-токен.
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(components.StaticFS())))

	mux.Handle("GET /login", s.publicHTML(s.handleLoginForm))
	mux.Handle("POST /login", s.publicHTML(s.rateLimit("/login", s.handleLogin)))
	mux.Handle("GET /logout", s.publicHTML(s.handleLogout))

	// Экраны несут персональные данные получателей, поэтому их ответы запрещено
	// кэшировать; решения по черновикам и пакетам ограничены по частоте (7.5).
	mux.Handle("GET /outreach/drafts", components.NoStore(s.consoleHandler(s.handleDraftQueue)))
	mux.Handle("GET /outreach/campaigns/{campaignID}/batches", components.NoStore(s.consoleHandler(s.handleBatchScreen)))
	mux.Handle("POST /outreach/campaigns/{campaignID}/batches/approve", s.consoleHandler(s.rateLimit("/outreach/batches/approve", s.handleBatchApprove)))
	mux.Handle("GET /outreach/campaigns/{campaignID}/drafts/{draftID}", components.NoStore(s.consoleHandler(s.handleDraftScreen)))
	mux.Handle("POST /outreach/campaigns/{campaignID}/drafts/{draftID}/approve", s.consoleHandler(s.rateLimit("/outreach/drafts/approve", s.handleDraftApprove)))
	mux.Handle("POST /outreach/campaigns/{campaignID}/drafts/{draftID}/edit-approve", s.consoleHandler(s.rateLimit("/outreach/drafts/edit-approve", s.handleDraftEditApprove)))
	mux.Handle("POST /outreach/campaigns/{campaignID}/drafts/{draftID}/reject", s.consoleHandler(s.rateLimit("/outreach/drafts/reject", s.handleDraftReject)))
	mux.Handle("POST /outreach/campaigns/{campaignID}/drafts/{draftID}/postpone", s.consoleHandler(s.rateLimit("/outreach/drafts/postpone", s.handleDraftPostpone)))
	mux.Handle("GET /outreach/stalled", components.NoStore(s.consoleHandler(s.handleStalledQueue)))

	return mux
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if s.deps.Readiness == nil {
		writeJSON(w, http.StatusServiceUnavailable, readiness.Snapshot{Status: readiness.StatusUnknown, Ready: false})
		return
	}
	report := s.deps.Readiness(r.Context())
	status := http.StatusOK
	if !report.Ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, report)
}

// publicHTML навешивает заголовки безопасности на публичную HTML-страницу
// консоли (вход, выход): сессия на них не требуется.
func (s *Server) publicHTML(next http.HandlerFunc) http.Handler {
	return components.SecurityHeaders(contentSecurityPolicy)(next)
}

// consoleHandler оборачивает консольный экран заголовками безопасности и
// требованием сессии: без валидной cookie запрос редиректится на вход, а
// изменяющий — без верного CSRF — отклоняется. Статика публична.
//
// Если консоль не настроена (точка входа не задала учётные данные), экран честно
// отвечает 503, а не уводит в недоступный вход: неподключённая консоль не должна
// выглядеть работающей.
func (s *Server) consoleHandler(next http.HandlerFunc) http.Handler {
	headers := components.SecurityHeaders(contentSecurityPolicy)
	return headers(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.consoleConfigured {
			s.renderPlainError(w, http.StatusServiceUnavailable, "консоль рассылок не настроена: не заданы учётные данные оператора")
			return
		}
		s.authMiddleware(next).ServeHTTP(w, r)
	}))
}

// authMiddleware требует сессию для обёрнутых консольных маршрутов и проверяет
// CSRF-токен на изменяющих запросах. Правила задаёт общий пакет consoleauth.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return s.sessions.Middleware(consoleauth.Policy{
		LoginPath:      "/login",
		PublicPrefixes: []string{"/static/"},
	})(next)
}

// errorResponse — единая форма ошибки HTTP-слоя.
type errorResponse struct {
	Error string `json:"error"`
}

// decodeJSON читает тело запроса в dst. Ошибка разбора — ошибка транспорта,
// а не решение о допустимости, поэтому её возвращает вызывающий.
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
	// Ошибку кодирования сообщить в ответ уже нельзя: заголовки отправлены.
	_ = json.NewEncoder(w).Encode(payload)
}

// writeErrorMessage отправляет ошибку транспорта без разбора по домену.
func writeErrorMessage(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

// writeDomainError переводит ошибку сценария в код ответа. Доменную ошибку
// адаптер только классифицирует; он не решает, допустим ли переход.
func writeDomainError(w http.ResponseWriter, err error) {
	status := statusForError(err)
	if status >= http.StatusInternalServerError {
		writeJSON(w, status, errorResponse{Error: "внутренняя ошибка"})
		return
	}
	writeJSON(w, status, errorResponse{Error: err.Error()})
}

// statusForError классифицирует ошибку прикладного и доменного слоя. Ошибки
// не найдены приходят как 404, конфликты версий и состояний — как 409,
// нарушение доступа — как 403, недопустимые значения — как 400.
//
// ErrReservationAbandoned маппится в 409 Conflict: ключ идемпотентности был
// зарезервирован, но владелец отменил резервирование, не записав результат.
// Состояние ключа осталось незавершённым, поэтому повтор той же команды —
// конфликт с текущим состоянием, который клиент вправе повторить позже.
func statusForError(err error) int {
	switch {
	case errors.Is(err, command.ErrAccessDenied), errors.Is(err, query.ErrAccessDenied):
		return http.StatusForbidden
	case errors.Is(err, ports.ErrCampaignNotFound),
		errors.Is(err, domain.ErrDraftNotFound),
		errors.Is(err, domain.ErrSequenceNotFound),
		errors.Is(err, domain.ErrExecutionNotFound),
		errors.Is(err, command.ErrCampaignTenantMismatch):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrEditorialDeliveryForbidden),
		errors.Is(err, domain.ErrDraftVersionMismatch),
		errors.Is(err, domain.ErrDecisionConflict),
		errors.Is(err, domain.ErrInvalidDraftTransition),
		errors.Is(err, domain.ErrApprovalRequired),
		errors.Is(err, domain.ErrDuplicateDraft),
		errors.Is(err, domain.ErrDuplicateSequence),
		errors.Is(err, domain.ErrCampaignCompleted),
		errors.Is(err, domain.ErrExecutionTerminal),
		errors.Is(err, domain.ErrResumeNotSuspended),
		errors.Is(err, domain.ErrBatchDigestMismatch),
		errors.Is(err, domain.ErrBatchCompositionChanged),
		errors.Is(err, domain.ErrBatchNotHomogeneous),
		errors.Is(err, domain.ErrBatchDraftNotEligible),
		errors.Is(err, idempotency.ErrReservationAbandoned):
		return http.StatusConflict
	case errors.Is(err, domain.ErrRiskFactsUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, query.ErrTenantRequired),
		errors.Is(err, query.ErrActorRequired),
		errors.Is(err, ports.ErrInvalidDraftWindowQuery),
		errors.Is(err, ports.ErrInvalidDraftWindowLimit),
		errors.Is(err, domain.ErrEmptyCampaignName),
		errors.Is(err, domain.ErrInvalidCampaignWindow),
		errors.Is(err, domain.ErrUnknownActionChannel),
		errors.Is(err, domain.ErrEmptyDraftField),
		errors.Is(err, domain.ErrEmptySequenceName),
		errors.Is(err, domain.ErrSequenceStepsEmpty),
		errors.Is(err, domain.ErrInvalidStepDelay),
		errors.Is(err, domain.ErrUnknownTransitionCondition),
		errors.Is(err, domain.ErrOpenedSignalUnavailable),
		errors.Is(err, domain.ErrUnknownReaction),
		errors.Is(err, domain.ErrExecutionTenantMismatch),
		errors.Is(err, domain.ErrDecisionWithoutVersion),
		errors.Is(err, domain.ErrInvalidDecisionOutcome),
		errors.Is(err, domain.ErrDecisionTimeRequired),
		errors.Is(err, domain.ErrDecisionReasonRequired),
		errors.Is(err, domain.ErrInvalidCampaignState),
		errors.Is(err, domain.ErrInvalidDraftState),
		errors.Is(err, domain.ErrInvalidExecutionState),
		errors.Is(err, domain.ErrUnknownCampaignCommand),
		errors.Is(err, domain.ErrBatchCompositionEmpty),
		errors.Is(err, domain.ErrPostponeDeadlineRequired),
		errors.Is(err, command.ErrApprovalDeadlineRequired),
		errors.Is(err, query.ErrInvalidRiskFilter),
		errors.Is(err, domain.ErrInvalidSnapshot):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
