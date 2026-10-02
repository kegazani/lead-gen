// Package httpapi реализует HTTP-адаптер роли api контекста billing.
//
// Адаптер только разбирает запрос, вызывает прикладной сценарий и переводит
// доменную ошибку в код ответа. Решений о допустимости он не принимает:
// инварианты обеспечивают агрегаты, а транзакцию — прикладной слой. Обработчики
// зависят от сценариев и не ссылаются на хранилище напрямую.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"lead-gen/backend/contexts/billing/internal/app/command"
	"lead-gen/backend/contexts/billing/internal/app/query"
	"lead-gen/backend/contexts/billing/internal/domain"
	"lead-gen/backend/contexts/billing/internal/ports"
)

const (
	// maxBodyBytes ограничивает размер тела запроса, чтобы разбор не мог
	// исчерпать память процесса.
	maxBodyBytes = 1 << 20

	contentTypeJSON = "application/json; charset=utf-8"
)

// Dependencies — внешние зависимости HTTP-адаптера: прикладные сценарии контекста.
type Dependencies struct {
	ActivateSubscription *command.ActivateSubscriptionHandler
	GetSubscription      *query.GetSubscriptionHandler
	SuspendSubscription  *command.SuspendSubscriptionHandler
	ResumeSubscription   *command.ResumeSubscriptionHandler
	RecordUsage          *command.RecordUsageHandler
	IssueInvoice         *command.IssueInvoiceHandler
	ChargeInvoice        *command.ChargeInvoiceHandler
	GetEntitlement       *query.GetEntitlementHandler
	CalculateSuccessFee  *command.CalculateSuccessFeeHandler
	GetSuccessFee        *query.GetSuccessFeeHandler
	DisputeSuccessFee    *command.DisputeSuccessFeeHandler
}

// Server — HTTP-сервер среза. Нулевое значение непригодно: используйте NewServer.
type Server struct {
	deps    Dependencies
	handler http.Handler
}

// NewServer собирает маршруты среза из переданных сценариев.
func NewServer(deps Dependencies) *Server {
	server := &Server{deps: deps}
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

	mux.HandleFunc("POST /subscriptions", s.handleActivate)
	mux.HandleFunc("GET /subscriptions/{subscriptionID}", s.handleGetSubscription)
	mux.HandleFunc("GET /subscriptions/{subscriptionID}/entitlement", s.handleGetEntitlement)
	mux.HandleFunc("POST /subscriptions/{subscriptionID}/suspend", s.handleSuspend)
	mux.HandleFunc("POST /subscriptions/{subscriptionID}/resume", s.handleResume)
	mux.HandleFunc("POST /subscriptions/{subscriptionID}/usage", s.handleRecordUsage)
	mux.HandleFunc("POST /subscriptions/{subscriptionID}/invoices", s.handleIssueInvoice)
	mux.HandleFunc("POST /subscriptions/{subscriptionID}/invoices/{invoiceID}/charge", s.handleChargeInvoice)
	mux.HandleFunc("POST /success-fees", s.handleCalculateSuccessFee)
	mux.HandleFunc("GET /success-fees/{successFeeID}", s.handleGetSuccessFee)
	mux.HandleFunc("POST /success-fees/{successFeeID}/dispute", s.handleDisputeSuccessFee)
	mux.HandleFunc("GET /healthz", s.handleHealth)

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

// statusForError классифицирует ошибку прикладного и доменного слоя.
func statusForError(err error) int {
	switch {
	case errors.Is(err, ports.ErrSubscriptionNotFound),
		errors.Is(err, ports.ErrSuccessFeeNotFound),
		errors.Is(err, domain.ErrInvoiceNotFound):
		return http.StatusNotFound
	case errors.Is(err, ports.ErrVersionConflict),
		errors.Is(err, command.ErrTenantMismatch),
		errors.Is(err, domain.ErrSubscriptionNotActive),
		errors.Is(err, domain.ErrStatusUnchanged),
		errors.Is(err, domain.ErrTariffLimitExceeded),
		errors.Is(err, domain.ErrInvalidFeeTransition),
		errors.Is(err, ports.ErrPaymentRejected):
		return http.StatusConflict
	case errors.Is(err, ports.ErrPaymentUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrResultRefRequired),
		errors.Is(err, domain.ErrMissingTime),
		errors.Is(err, domain.ErrMissingEventID),
		errors.Is(err, domain.ErrUnknownSubscriptionStatus),
		errors.Is(err, domain.ErrInvalidPeriod),
		errors.Is(err, domain.ErrNegativeAmount),
		errors.Is(err, domain.ErrInvalidCurrency),
		errors.Is(err, domain.ErrCurrencyMismatch),
		errors.Is(err, domain.ErrUnknownFeature),
		errors.Is(err, domain.ErrFeatureNotInTariff),
		errors.Is(err, domain.ErrInvalidLimit),
		errors.Is(err, domain.ErrDuplicateFeature),
		errors.Is(err, domain.ErrQuantityRequired),
		errors.Is(err, domain.ErrUsageKeyRequired),
		errors.Is(err, domain.ErrUnconfirmedResult),
		errors.Is(err, domain.ErrEmptyCalculationVersion),
		errors.Is(err, domain.ErrInvalidRate),
		errors.Is(err, domain.ErrUnknownFeeStatus),
		errors.Is(err, domain.ErrUnknownBillingResultKind):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// parseTime разбирает время в формате RFC3339.
func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("время %q: %w", value, err)
	}
	return parsed, nil
}
