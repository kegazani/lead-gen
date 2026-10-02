package httpapi

import (
	"net/http"

	"lead-gen/backend/contexts/billing/internal/app/command"
	"lead-gen/backend/contexts/billing/internal/app/query"
	"lead-gen/backend/contexts/billing/internal/domain"
)

// limitBody — лимит возможности в теле активации подписки.
type limitBody struct {
	Feature string `json:"feature"`
	Limit   int64  `json:"limit"`
}

// activateBody — тело команды активации подписки.
type activateBody struct {
	SubscriptionID string      `json:"subscription_id"`
	TenantID       string      `json:"tenant_id"`
	TariffID       string      `json:"tariff_id"`
	PriceMinor     int64       `json:"price_minor"`
	Currency       string      `json:"currency"`
	PeriodStart    string      `json:"period_start"`
	PeriodEnd      string      `json:"period_end"`
	Limits         []limitBody `json:"limits"`
	CorrelationID  string      `json:"correlation_id,omitempty"`
	CausationID    string      `json:"causation_id,omitempty"`
}

// lifecycleBody — тело команд приостановки/возобновления.
type lifecycleBody struct {
	TenantID string `json:"tenant_id"`
}

// usageBody — тело команды фиксации потребления.
type usageBody struct {
	TenantID string `json:"tenant_id"`
	UsageID  string `json:"usage_id"`
	Feature  string `json:"feature"`
	Quantity int64  `json:"quantity"`
	UsageKey string `json:"usage_key"`
}

// issueBody — тело команды выставления счёта.
type issueBody struct {
	TenantID      string `json:"tenant_id"`
	PeriodStart   string `json:"period_start"`
	PeriodEnd     string `json:"period_end"`
	CorrelationID string `json:"correlation_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}

// successFeeBody — тело команды расчёта вознаграждения.
type successFeeBody struct {
	SuccessFeeID          string `json:"success_fee_id"`
	TenantID              string `json:"tenant_id"`
	SubscriptionID        string `json:"subscription_id"`
	ResultRef             string `json:"result_ref"`
	DealID                string `json:"deal_id"`
	AmountMinor           int64  `json:"amount_minor"`
	Currency              string `json:"currency"`
	Confirmed             bool   `json:"confirmed"`
	ClosedInPlatformCRM   bool   `json:"closed_in_platform_crm"`
	CommunicationRecorded bool   `json:"communication_recorded"`
	CorrelationID         string `json:"correlation_id,omitempty"`
	CausationID           string `json:"causation_id,omitempty"`
}

// subscriptionResponse — форма ответа изменения подписки.
type subscriptionResponse struct {
	SubscriptionID string `json:"subscription_id"`
	Status         string `json:"status"`
	Version        int    `json:"version"`
	InvoiceID      string `json:"invoice_id,omitempty"`
}

// handleActivate обслуживает POST /subscriptions.
func (s *Server) handleActivate(w http.ResponseWriter, r *http.Request) {
	var body activateBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	tariff, err := buildTariff(body)
	if err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	period, ok := parsePeriod(w, body.PeriodStart, body.PeriodEnd)
	if !ok {
		return
	}
	result, err := s.deps.ActivateSubscription.Handle(r.Context(), command.ActivateSubscription{
		SubscriptionID: domain.SubscriptionID(body.SubscriptionID),
		TenantID:       domain.TenantID(body.TenantID),
		Tariff:         tariff,
		Period:         period,
		CorrelationID:  domain.CorrelationID(body.CorrelationID),
		CausationID:    domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, subscriptionResponse{
		SubscriptionID: result.SubscriptionID.String(),
		Status:         result.Status.String(),
		Version:        result.Version,
	})
}

// handleGetSubscription обслуживает GET /subscriptions/{subscriptionID}.
func (s *Server) handleGetSubscription(w http.ResponseWriter, r *http.Request) {
	view, err := s.deps.GetSubscription.Handle(r.Context(), query.GetSubscription{
		SubscriptionID: domain.SubscriptionID(r.PathValue("subscriptionID")),
		TenantID:       domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleGetEntitlement обслуживает GET /subscriptions/{subscriptionID}/entitlement.
func (s *Server) handleGetEntitlement(w http.ResponseWriter, r *http.Request) {
	view, err := s.deps.GetEntitlement.Handle(r.Context(), query.GetEntitlement{
		SubscriptionID: domain.SubscriptionID(r.PathValue("subscriptionID")),
		TenantID:       domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleSuspend обслуживает POST /subscriptions/{subscriptionID}/suspend.
func (s *Server) handleSuspend(w http.ResponseWriter, r *http.Request) {
	var body lifecycleBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.SuspendSubscription.Handle(r.Context(), command.SuspendSubscription{
		SubscriptionID: domain.SubscriptionID(r.PathValue("subscriptionID")),
		TenantID:       domain.TenantID(body.TenantID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, subscriptionResponse{
		SubscriptionID: result.SubscriptionID.String(),
		Status:         result.Status.String(),
		Version:        result.Version,
	})
}

// handleResume обслуживает POST /subscriptions/{subscriptionID}/resume.
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	var body lifecycleBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.ResumeSubscription.Handle(r.Context(), command.ResumeSubscription{
		SubscriptionID: domain.SubscriptionID(r.PathValue("subscriptionID")),
		TenantID:       domain.TenantID(body.TenantID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, subscriptionResponse{
		SubscriptionID: result.SubscriptionID.String(),
		Status:         result.Status.String(),
		Version:        result.Version,
	})
}

// usageResponse — форма ответа фиксации потребления.
type usageResponse struct {
	SubscriptionID string `json:"subscription_id"`
	Recorded       bool   `json:"recorded"`
	Duplicate      bool   `json:"duplicate"`
	Feature        string `json:"feature"`
	Limit          int64  `json:"limit"`
	Remaining      int64  `json:"remaining"`
	InvoiceIssued  bool   `json:"invoice_issued"`
	InvoiceID      string `json:"invoice_id,omitempty"`
	Version        int    `json:"version"`
}

// handleRecordUsage обслуживает POST /subscriptions/{subscriptionID}/usage.
func (s *Server) handleRecordUsage(w http.ResponseWriter, r *http.Request) {
	var body usageBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.RecordUsage.Handle(r.Context(), command.RecordUsage{
		SubscriptionID: domain.SubscriptionID(r.PathValue("subscriptionID")),
		TenantID:       domain.TenantID(body.TenantID),
		UsageID:        domain.UsageID(body.UsageID),
		Feature:        domain.Feature(body.Feature),
		Quantity:       body.Quantity,
		UsageKey:       body.UsageKey,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, usageResponse{
		SubscriptionID: result.SubscriptionID.String(),
		Recorded:       result.Recorded,
		Duplicate:      result.Duplicate,
		Feature:        result.Feature.String(),
		Limit:          result.Limit,
		Remaining:      result.Remaining,
		InvoiceIssued:  result.InvoiceIssued,
		InvoiceID:      result.InvoiceID.String(),
		Version:        result.Version,
	})
}

// invoiceResponse — форма ответа выставления счёта.
type invoiceResponse struct {
	SubscriptionID string `json:"subscription_id"`
	InvoiceID      string `json:"invoice_id"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	Version        int    `json:"version"`
}

// handleIssueInvoice обслуживает POST /subscriptions/{subscriptionID}/invoices.
func (s *Server) handleIssueInvoice(w http.ResponseWriter, r *http.Request) {
	var body issueBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	period, ok := parsePeriod(w, body.PeriodStart, body.PeriodEnd)
	if !ok {
		return
	}
	result, err := s.deps.IssueInvoice.Handle(r.Context(), command.IssueInvoice{
		SubscriptionID: domain.SubscriptionID(r.PathValue("subscriptionID")),
		TenantID:       domain.TenantID(body.TenantID),
		Period:         period,
		CorrelationID:  domain.CorrelationID(body.CorrelationID),
		CausationID:    domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, invoiceResponse{
		SubscriptionID: result.SubscriptionID.String(),
		InvoiceID:      result.InvoiceID.String(),
		AmountMinor:    result.Amount.AmountMinor(),
		Currency:       result.Amount.Currency(),
		Version:        result.Version,
	})
}

// chargeResponse — форма ответа списания.
type chargeResponse struct {
	SubscriptionID string `json:"subscription_id"`
	InvoiceID      string `json:"invoice_id"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	Paid           bool   `json:"paid"`
	Reference      string `json:"reference,omitempty"`
	Version        int    `json:"version"`
}

// handleChargeInvoice обслуживает POST
// /subscriptions/{subscriptionID}/invoices/{invoiceID}/charge.
func (s *Server) handleChargeInvoice(w http.ResponseWriter, r *http.Request) {
	var body lifecycleBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.ChargeInvoice.Handle(r.Context(), command.ChargeInvoice{
		SubscriptionID: domain.SubscriptionID(r.PathValue("subscriptionID")),
		TenantID:       domain.TenantID(body.TenantID),
		InvoiceID:      domain.InvoiceID(r.PathValue("invoiceID")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, chargeResponse{
		SubscriptionID: result.SubscriptionID.String(),
		InvoiceID:      result.InvoiceID.String(),
		AmountMinor:    result.Amount.AmountMinor(),
		Currency:       result.Amount.Currency(),
		Paid:           result.Paid,
		Reference:      result.Reference,
		Version:        result.Version,
	})
}

// successFeeResponse — форма ответа расчёта вознаграждения.
type successFeeResponse struct {
	Eligible     bool   `json:"eligible"`
	SuccessFeeID string `json:"success_fee_id,omitempty"`
	AmountMinor  int64  `json:"amount_minor"`
	Currency     string `json:"currency,omitempty"`
	Calculation  string `json:"calculation_version,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Version      int    `json:"version,omitempty"`
}

// handleCalculateSuccessFee обслуживает POST /success-fees.
func (s *Server) handleCalculateSuccessFee(w http.ResponseWriter, r *http.Request) {
	var body successFeeBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	amount, err := domain.NewMoney(body.AmountMinor, body.Currency)
	if err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.CalculateSuccessFee.Handle(r.Context(), command.CalculateSuccessFee{
		SuccessFeeID:          domain.SuccessFeeID(body.SuccessFeeID),
		TenantID:              domain.TenantID(body.TenantID),
		SubscriptionID:        domain.SubscriptionID(body.SubscriptionID),
		ResultRef:             domain.ResultRef(body.ResultRef),
		DealID:                domain.DealID(body.DealID),
		Amount:                amount,
		Confirmed:             body.Confirmed,
		ClosedInPlatformCRM:   body.ClosedInPlatformCRM,
		CommunicationRecorded: body.CommunicationRecorded,
		CorrelationID:         domain.CorrelationID(body.CorrelationID),
		CausationID:           domain.CausationID(body.CausationID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, successFeeResponse{
		Eligible:     result.Eligible,
		SuccessFeeID: result.SuccessFeeID.String(),
		AmountMinor:  result.Amount.AmountMinor(),
		Currency:     result.Amount.Currency(),
		Calculation:  result.Calculation.String(),
		Reason:       result.Reason,
		Version:      result.Version,
	})
}

// handleGetSuccessFee обслуживает GET /success-fees/{successFeeID}.
func (s *Server) handleGetSuccessFee(w http.ResponseWriter, r *http.Request) {
	view, err := s.deps.GetSuccessFee.Handle(r.Context(), query.GetSuccessFee{
		SuccessFeeID: domain.SuccessFeeID(r.PathValue("successFeeID")),
		TenantID:     domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleDisputeSuccessFee обслуживает POST /success-fees/{successFeeID}/dispute.
func (s *Server) handleDisputeSuccessFee(w http.ResponseWriter, r *http.Request) {
	var body lifecycleBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.DisputeSuccessFee.Handle(r.Context(), command.DisputeSuccessFee{
		SuccessFeeID: domain.SuccessFeeID(r.PathValue("successFeeID")),
		TenantID:     domain.TenantID(body.TenantID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success_fee_id": result.SuccessFeeID.String(),
		"status":         result.Status.String(),
		"version":        result.Version,
	})
}

// handleHealth обслуживает GET /healthz: сервис запущен и принимает запросы.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// buildTariff собирает тариф из тела активации.
func buildTariff(body activateBody) (domain.Tariff, error) {
	tariffID, err := domain.NewTariffID(body.TariffID)
	if err != nil {
		return domain.Tariff{}, err
	}
	price, err := domain.NewMoney(body.PriceMinor, body.Currency)
	if err != nil {
		return domain.Tariff{}, err
	}
	limits := make([]domain.TariffLimit, 0, len(body.Limits))
	for _, item := range body.Limits {
		feature, err := domain.NewFeature(item.Feature)
		if err != nil {
			return domain.Tariff{}, err
		}
		limit, err := domain.NewTariffLimit(feature, item.Limit)
		if err != nil {
			return domain.Tariff{}, err
		}
		limits = append(limits, limit)
	}
	return domain.NewTariff(tariffID, price, limits)
}

// parsePeriod разбирает границы периода из тела запроса. Ошибка записывается в
// ответ, и обработчик прекращает работу.
func parsePeriod(w http.ResponseWriter, start, end string) (domain.BillingPeriod, bool) {
	started, err := parseTime(start)
	if err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return domain.BillingPeriod{}, false
	}
	finished, err := parseTime(end)
	if err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return domain.BillingPeriod{}, false
	}
	period, err := domain.NewBillingPeriod(started, finished)
	if err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return domain.BillingPeriod{}, false
	}
	return period, true
}
