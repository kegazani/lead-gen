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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/idempotency"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/query"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

const (
	// maxBodyBytes ограничивает размер тела запроса, чтобы разбор не мог
	// исчерпать память процесса.
	maxBodyBytes = 1 << 20

	contentTypeJSON = "application/json; charset=utf-8"
)

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

	// BatchLimits — лимиты пакета, передаваемые сценариям параметром. Это
	// конфигурация композиции, а не доменное правило: адаптер лишь прокидывает
	// её в команду и ничего по ней не решает (design.md, Решение 7).
	BatchLimits domain.BatchLimits
}

// Server — HTTP-сервер среза. Нулевое значение непригодно: используйте
// NewServer.
type Server struct {
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

	defineSequence  DefineSequenceUseCase
	enrollLead      EnrollLeadUseCase
	removeLead      RemoveLeadUseCase
	recordOutcome   RecordOutcomeUseCase
	resumeExecution ResumeExecutionUseCase
	sequenceHistory SequenceHistoryUseCase
	sequenceState   SequenceStateUseCase

	handler http.Handler
}

// NewServer собирает маршруты среза из переданных сценариев.
func NewServer(deps Dependencies) *Server {
	server := &Server{
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

		defineSequence:  deps.DefineSequence,
		enrollLead:      deps.EnrollLead,
		removeLead:      deps.RemoveLead,
		recordOutcome:   deps.RecordOutcome,
		resumeExecution: deps.ResumeExecution,
		sequenceHistory: deps.SequenceHistory,
		sequenceState:   deps.SequenceState,
	}
	server.handler = server.routes()
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

	return mux
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
	case errors.Is(err, domain.ErrDraftVersionMismatch),
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
		errors.Is(err, domain.ErrInvalidSnapshot):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}
