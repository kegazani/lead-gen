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
	"net/http"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/frontend/components"
)

const (
	// maxBodyBytes ограничивает размер тела запроса, чтобы разбор не мог
	// исчерпать память процесса.
	maxBodyBytes = 1 << 20

	contentTypeJSON = "application/json; charset=utf-8"
	contentTypeHTML = "text/html; charset=utf-8"
)

// Dependencies — внешние зависимости HTTP-адаптера: прикладные сценарии
// контекста. FulfillmentQuery допускается равным nil только вместе с запросом
// статусов артефактов.
type Dependencies struct {
	SubmitRequest          *command.SubmitRequestHandler
	StartValidation        *command.StartValidationHandler
	ValidateRequest        *command.ValidateRequestHandler
	StartProcessing        *command.StartProcessingHandler
	FulfillRequest         *command.FulfillRequestHandler
	RejectRequest          *command.RejectRequestHandler
	CancelRequest          *command.CancelRequestHandler
	ReviseOffer            *command.ReviseOfferHandler
	RecordAudienceSnapshot *command.RecordAudienceSnapshotHandler
	RecordCampaign         *command.RecordCampaignHandler
	RecordDeal             *command.RecordDealHandler
	GetRequest             *query.GetRequestHandler
	FulfillmentStatus      *query.FulfillmentStatusHandler

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
}

// Server — HTTP-сервер среза. Нулевое значение непригодно: используйте
// NewServer.
type Server struct {
	deps    Dependencies
	handler http.Handler
	pages   *components.Set
}

// NewServer собирает маршруты среза из переданных сценариев. Шаблоны экрана
// разбираются сразу: ошибка разметки видна при сборке сервиса. Если набор не
// собрался, HTTP-часть с данными продолжает работать, а экран честно отвечает 500.
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

// routes объявляет маршруты среза.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /requests", s.handleSubmit)
	mux.HandleFunc("GET /requests/{requestID}", s.handleGet)
	mux.HandleFunc("POST /requests/{requestID}/validation/start", s.handleStartValidation)
	mux.HandleFunc("POST /requests/{requestID}/validation/complete", s.handleValidate)
	mux.HandleFunc("POST /requests/{requestID}/processing/start", s.handleStartProcessing)
	mux.HandleFunc("POST /requests/{requestID}/fulfillment", s.handleFulfill)
	mux.HandleFunc("POST /requests/{requestID}/rejection", s.handleReject)
	mux.HandleFunc("POST /requests/{requestID}/cancellation", s.handleCancel)
	mux.HandleFunc("POST /requests/{requestID}/offer", s.handleReviseOffer)
	mux.HandleFunc("POST /requests/{requestID}/artifacts/audience-snapshot", s.handleRecordAudience)
	mux.HandleFunc("POST /requests/{requestID}/artifacts/campaign", s.handleRecordCampaign)
	mux.HandleFunc("POST /requests/{requestID}/artifacts/deal", s.handleRecordDeal)
	mux.HandleFunc("GET /requests/{requestID}/fulfillment-status", s.handleFulfillmentStatus)
	mux.HandleFunc("GET /requests/{requestID}/workflow", s.handleWorkflowStatus)
	mux.HandleFunc("POST /requests/{requestID}/workflow", s.handleStartWorkflow)
	mux.HandleFunc("POST /requests/{requestID}/workflow/approval-decision", s.handleWorkflowApprovalDecision)
	mux.HandleFunc("GET /healthz", s.handleHealth)

	// Экран валидации (HTMX + серверный рендеринг) — эксплуатационная проекция
	// роли api. Действия — POST/redirect/GET; частичные ответы — для HTMX.
	mux.HandleFunc("GET /validation", s.handleValidationQueue)
	mux.HandleFunc("GET /requests/{requestID}/validation", s.handleValidationScreen)
	mux.HandleFunc("POST /requests/{requestID}/validation/take", s.handleValidationTake)
	mux.HandleFunc("POST /requests/{requestID}/validation/offer", s.handleValidationOffer)
	mux.HandleFunc("POST /requests/{requestID}/validation/approve", s.handleValidationApprove)
	mux.HandleFunc("POST /requests/{requestID}/validation/reject", s.handleValidationReject)
	mux.HandleFunc("POST /requests/{requestID}/validation/call", s.handleValidationCall)

	// Экран утверждения результата фулфилмента (HTMX + серверный рендеринг):
	// очередь ожидающих решения, экран заявки и кнопки решения. Действия —
	// POST/redirect/GET; частичные ответы — для HTMX.
	mux.HandleFunc("GET /approvals", s.handleApprovalQueue)
	mux.HandleFunc("GET /requests/{requestID}/approval", s.handleApprovalScreen)
	mux.HandleFunc("POST /requests/{requestID}/approval/approve", s.handleApprovalApprove)
	mux.HandleFunc("POST /requests/{requestID}/approval/reject", s.handleApprovalReject)

	return mux
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
	switch {
	case errors.Is(err, ports.ErrRequestNotFound):
		return http.StatusNotFound
	case errors.Is(err, ports.ErrVersionConflict),
		errors.Is(err, command.ErrTenantMismatch),
		errors.Is(err, domain.ErrInvalidTransition),
		errors.Is(err, domain.ErrTerminalState):
		return http.StatusConflict
	case errors.Is(err, ports.ErrArtifactUnavailable):
		return http.StatusBadGateway
	case errors.Is(err, ports.ErrWorkflowNotFound):
		return http.StatusNotFound
	case errors.Is(err, ports.ErrWorkflowUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, ports.ErrCallUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, ports.ErrCallRejected):
		return http.StatusConflict
	case errors.Is(err, ports.ErrVectorUnavailable):
		return http.StatusServiceUnavailable
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
