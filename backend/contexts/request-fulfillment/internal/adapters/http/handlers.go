package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/readiness"
	"lead-gen/contracts/request-fulfillment/requestintent"
)

// transitionBody — общее тело команд перехода над существующей заявкой.
type transitionBody struct {
	TenantID      string `json:"tenant_id"`
	Actor         string `json:"actor"`
	Reason        string `json:"reason,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	CausationID   string `json:"causation_id,omitempty"`
}

// submitBody — тело команды подачи заявки.
type submitBody struct {
	RequestID        string                               `json:"request_id"`
	OfferID          string                               `json:"offer_id"`
	TenantID         string                               `json:"tenant_id"`
	Actor            string                               `json:"actor"`
	Content          string                               `json:"content"`
	Target           string                               `json:"target,omitempty"`
	CorrelationID    string                               `json:"correlation_id,omitempty"`
	CausationID      string                               `json:"causation_id,omitempty"`
	ExecutionPurpose string                               `json:"execution_purpose,omitempty"`
	SenderCompany    *requestintent.SenderCompanySnapshot `json:"sender_company,omitempty"`
}

// reviseOfferBody — тело команды правки оффера.
type reviseOfferBody struct {
	TenantID string `json:"tenant_id"`
	Actor    string `json:"actor"`
	OfferID  string `json:"offer_id"`
	Content  string `json:"content"`
	Target   string `json:"target,omitempty"`
}

// artifactBody — тело команды фиксации ссылки на артефакт фулфилмента.
type artifactBody struct {
	TenantID string `json:"tenant_id"`
	ID       string `json:"id"`
}

// transitionResponse — единая форма ответа команды перехода.
type transitionResponse struct {
	RequestID     string               `json:"request_id"`
	Status        domain.RequestStatus `json:"status"`
	Version       int                  `json:"version"`
	OfferRevision int                  `json:"offer_revision,omitempty"`
}

// handleSubmit обслуживает POST /requests.
func (s *Server) handleSubmit(w http.ResponseWriter, r *http.Request) {
	var body submitBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.SubmitRequest.Handle(r.Context(), command.SubmitRequest{
		RequestID:        domain.RequestID(body.RequestID),
		OfferID:          domain.OfferID(body.OfferID),
		TenantID:         domain.TenantID(body.TenantID),
		Content:          body.Content,
		Target:           body.Target,
		Actor:            domain.ActorID(body.Actor),
		CorrelationID:    domain.CorrelationID(body.CorrelationID),
		CausationID:      domain.CausationID(body.CausationID),
		ExecutionPurpose: body.ExecutionPurpose,
		SenderCompany:    domainSenderCompany(body.SenderCompany),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, transitionResponse{
		RequestID: result.RequestID.String(),
		Status:    result.Status,
		Version:   result.Version,
	})
}

func domainSenderCompany(snapshot *requestintent.SenderCompanySnapshot) *domain.SenderCompanySnapshot {
	if snapshot == nil {
		return nil
	}
	result := &domain.SenderCompanySnapshot{
		CompanyName: snapshot.CompanyName, Website: snapshot.Website, Description: snapshot.Description,
		Services: make([]domain.SenderServiceSnapshot, len(snapshot.Services)),
	}
	for i, service := range snapshot.Services {
		result.Services[i] = domain.SenderServiceSnapshot{Name: service.Name, Short: service.Short, Full: service.Full}
	}
	return result
}

// handleGet обслуживает GET /requests/{requestID}.
func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	view, err := s.deps.GetRequest.Handle(r.Context(), query.GetRequest{
		RequestID: domain.RequestID(r.PathValue("requestID")),
		TenantID:  domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleList обслуживает GET /requests?tenant_id=: список заявок тенанта (для
// сетевого транспорта контракта request-intent). Чужие тенанты не попадают в
// выборку.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	items, err := s.deps.ListRequests.Handle(r.Context(), query.ListRequests{
		TenantID: domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

// handleStartValidation обслуживает POST /requests/{requestID}/validation/start.
func (s *Server) handleStartValidation(w http.ResponseWriter, r *http.Request) {
	body, ok := s.decodeTransition(w, r)
	if !ok {
		return
	}
	result, err := s.deps.StartValidation.Handle(r.Context(), command.StartValidation{
		LifecycleFields: lifecycleFields(w, r, body),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransitionResponse(result))
}

// handleValidate обслуживает POST /requests/{requestID}/validation/complete.
func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	body, ok := s.decodeTransition(w, r)
	if !ok {
		return
	}
	result, err := s.deps.ValidateRequest.Handle(r.Context(), command.ValidateRequest{
		LifecycleFields: lifecycleFields(w, r, body),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransitionResponse(result))
}

// handleStartProcessing обслуживает POST /requests/{requestID}/processing/start.
func (s *Server) handleStartProcessing(w http.ResponseWriter, r *http.Request) {
	body, ok := s.decodeTransition(w, r)
	if !ok {
		return
	}
	result, err := s.deps.StartProcessing.Handle(r.Context(), command.StartProcessing{
		LifecycleFields: lifecycleFields(w, r, body),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransitionResponse(result))
}

// handleFulfill обслуживает POST /requests/{requestID}/fulfillment.
func (s *Server) handleFulfill(w http.ResponseWriter, r *http.Request) {
	body, ok := s.decodeTransition(w, r)
	if !ok {
		return
	}
	result, err := s.deps.FulfillRequest.Handle(r.Context(), command.FulfillRequest{
		LifecycleFields: lifecycleFields(w, r, body),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransitionResponse(result))
}

// handleReject обслуживает POST /requests/{requestID}/rejection.
func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	body, ok := s.decodeTransition(w, r)
	if !ok {
		return
	}
	result, err := s.deps.RejectRequest.Handle(r.Context(), command.RejectRequest{
		LifecycleFields: lifecycleFields(w, r, body),
		Reason:          body.Reason,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransitionResponse(result))
}

// handleCancel обслуживает POST /requests/{requestID}/cancellation.
func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	body, ok := s.decodeTransition(w, r)
	if !ok {
		return
	}
	fields := lifecycleFields(w, r, body)
	fields.Actor = domain.ActorID(actorOf(r))
	result, err := s.deps.CancelRequest.Handle(r.Context(), command.CancelRequest{
		LifecycleFields: fields,
		Reason:          body.Reason,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if err := s.requestWorkflowCancellation(r.Context(), fields.TenantID.String(), result.RequestID.String()); err != nil {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"request_id": result.RequestID.String(), "status": result.Status,
			"execution_cancel_pending": true,
		})
		return
	}
	writeJSON(w, http.StatusOK, toTransitionResponse(result))
}

// handleReviseOffer обслуживает POST /requests/{requestID}/offer.
func (s *Server) handleReviseOffer(w http.ResponseWriter, r *http.Request) {
	var body reviseOfferBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.deps.ReviseOffer.Handle(r.Context(), command.ReviseOffer{
		RequestID: domain.RequestID(r.PathValue("requestID")),
		TenantID:  domain.TenantID(body.TenantID),
		OfferID:   domain.OfferID(body.OfferID),
		Content:   body.Content,
		Target:    body.Target,
		Actor:     domain.ActorID(body.Actor),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, transitionResponse{
		RequestID:     result.RequestID.String(),
		Status:        result.Status,
		Version:       result.Version,
		OfferRevision: result.OfferRevision,
	})
}

// handleRecordAudience обслуживает POST .../artifacts/audience-snapshot.
func (s *Server) handleRecordAudience(w http.ResponseWriter, r *http.Request) {
	body, ok := s.decodeArtifact(w, r)
	if !ok {
		return
	}
	result, err := s.deps.RecordAudienceSnapshot.Handle(r.Context(), command.RecordAudienceSnapshot{
		RequestID:          domain.RequestID(r.PathValue("requestID")),
		TenantID:           domain.TenantID(body.TenantID),
		AudienceSnapshotID: domain.AudienceSnapshotID(body.ID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransitionResponse(result))
}

// handleRecordCampaign обслуживает POST .../artifacts/campaign.
func (s *Server) handleRecordCampaign(w http.ResponseWriter, r *http.Request) {
	body, ok := s.decodeArtifact(w, r)
	if !ok {
		return
	}
	result, err := s.deps.RecordCampaign.Handle(r.Context(), command.RecordCampaign{
		RequestID:  domain.RequestID(r.PathValue("requestID")),
		TenantID:   domain.TenantID(body.TenantID),
		CampaignID: domain.CampaignID(body.ID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransitionResponse(result))
}

// handleRecordDeal обслуживает POST .../artifacts/deal.
func (s *Server) handleRecordDeal(w http.ResponseWriter, r *http.Request) {
	body, ok := s.decodeArtifact(w, r)
	if !ok {
		return
	}
	result, err := s.deps.RecordDeal.Handle(r.Context(), command.RecordDeal{
		RequestID: domain.RequestID(r.PathValue("requestID")),
		TenantID:  domain.TenantID(body.TenantID),
		DealID:    domain.DealID(body.ID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toTransitionResponse(result))
}

// handleFulfillmentStatus обслуживает GET .../fulfillment-status.
func (s *Server) handleFulfillmentStatus(w http.ResponseWriter, r *http.Request) {
	view, err := s.deps.FulfillmentStatus.Handle(r.Context(), query.FulfillmentStatus{
		RequestID: domain.RequestID(r.PathValue("requestID")),
		TenantID:  domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// emailDraftResponse — форма черновика письма для консоли оператора.
type emailDraftResponse struct {
	LeadID   string `json:"lead_id"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	Language string `json:"language,omitempty"`
}

// emailDraftsResponse — форма ответа чтения черновиков писем заявки.
type emailDraftsResponse struct {
	RequestID string               `json:"request_id"`
	Drafts    []emailDraftResponse `json:"drafts"`
}

// handleEmailDrafts обслуживает GET .../email-drafts: оператор видит письма,
// сгенерированные под найденных лидов. Текст писем остаётся в контексте и не
// поднимается в payload workflow.
func (s *Server) handleEmailDrafts(w http.ResponseWriter, r *http.Request) {
	if s.deps.EmailDrafts == nil {
		writeErrorMessage(w, http.StatusServiceUnavailable, "черновики писем: хранилище не подключено")
		return
	}
	views, err := s.deps.EmailDrafts.Handle(r.Context(), query.EmailDrafts{
		RequestID: domain.RequestID(r.PathValue("requestID")),
		TenantID:  domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	drafts := make([]emailDraftResponse, 0, len(views))
	for _, view := range views {
		drafts = append(drafts, emailDraftResponse{
			LeadID:   view.LeadID,
			Subject:  view.Subject,
			Body:     view.Body,
			Language: view.Language,
		})
	}
	writeJSON(w, http.StatusOK, emailDraftsResponse{RequestID: r.PathValue("requestID"), Drafts: drafts})
}

// workflowStatusResponse — форма наблюдаемого состояния исполнения заявки.
// Отражает только идентификаторы и фазу — никаких данных оффера.
type workflowStatusResponse struct {
	SearchRound      int                       `json:"search_round,omitempty"`
	SearchRoundLimit int                       `json:"search_round_limit,omitempty"`
	StepLimitSeconds int                       `json:"step_limit_seconds,omitempty"`
	SourcesKnown     bool                      `json:"sources_known"`
	FromPool         int                       `json:"from_pool"`
	Crawled          int                       `json:"crawled"`
	RuntimeKnown     bool                      `json:"runtime_known"`
	Activities       []ports.ExecutionActivity `json:"activities,omitempty"`

	RequestID  string `json:"request_id"`
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id,omitempty"`
	Phase      string `json:"phase"`
	Execution  string `json:"execution,omitempty"`
	Found      int    `json:"found"`
	Drafted    int    `json:"drafted"`
	// Target и Shortfall наблюдаемы вместе: Shortfall отсутствует только при
	// незаданной цели, поэтому недобор не подменяется нулём.
	Target        int    `json:"target,omitempty"`
	Shortfall     *int   `json:"shortfall,omitempty"`
	NoLeads       bool   `json:"no_leads"`
	PartialReason string `json:"partial_reason,omitempty"`
}

// handleWorkflowStatus обслуживает GET .../workflow: состояние бегущего
// исполнения заявки видно в API и, через него, в консоли.
func (s *Server) handleWorkflowStatus(w http.ResponseWriter, r *http.Request) {
	if s.deps.ExecutionProtocol == ports.ExecutionProtocolV2 {
		if s.deps.ExecutionState == nil {
			writeErrorMessage(w, http.StatusServiceUnavailable, "durable execution projection недоступна")
			return
		}
		view, err := s.deps.ExecutionState.Handle(r.Context(), query.ExecutionState{
			RequestID: domain.RequestID(r.PathValue("requestID")), TenantID: domain.TenantID(r.URL.Query().Get("tenant_id")),
		})
		if err != nil {
			writeDomainError(w, err)
			return
		}
		var target *int
		if view.Target.State == domain.LeadTargetExplicit {
			value := view.Target.Value
			target = &value
		}
		writeJSON(w, http.StatusOK, executionStateResponse{
			RequestID: string(view.RequestID), WorkflowID: view.WorkflowID, AttemptID: view.AttemptID,
			CommandID: string(view.CommandID), Generation: view.Generation, State: view.State,
			TargetState: string(view.Target.State), LeadTarget: target, ExecutionHistoryKnown: view.ExecutionHistoryKnown,
		})
		return
	}
	_, err := s.deps.GetRequest.Handle(r.Context(), query.GetRequest{
		RequestID: domain.RequestID(r.PathValue("requestID")),
		TenantID:  domain.TenantID(r.URL.Query().Get("tenant_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if s.deps.WorkflowStatus == nil {
		writeErrorMessage(w, http.StatusServiceUnavailable, "исполнение: рантайм workflow не подключён")
		return
	}
	status, err := s.deps.WorkflowStatus.WorkflowStatus(r.Context(), r.PathValue("requestID"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, workflowStatusResponse{
		RequestID:     status.RequestID,
		WorkflowID:    status.WorkflowID,
		RunID:         status.RunID,
		Phase:         status.Phase,
		Execution:     status.Execution,
		Found:         status.Found,
		Drafted:       status.Drafted,
		Target:        status.Target,
		Shortfall:     status.Shortfall,
		NoLeads:       status.NoLeads,
		PartialReason: status.PartialReason,
		SearchRound:   status.SearchRound, SearchRoundLimit: status.SearchRoundLimit, StepLimitSeconds: status.StepLimitSeconds,
		SourcesKnown: status.SourcesKnown, FromPool: status.FromPool, Crawled: status.Crawled,
		RuntimeKnown: status.RuntimeKnown, Activities: status.Activities,
	})
}

// startWorkflowBody — тело запуска исполнения заявки. Только идентификаторы и
// цель по лидам: оффер в payload не поднимается.
type startWorkflowBody struct {
	TenantID       string          `json:"tenant_id"`
	CorrelationID  string          `json:"correlation_id,omitempty"`
	LeadTarget     *int            `json:"lead_target,omitempty"`
	RequestVersion int             `json:"request_version,omitempty"`
	OfferRevision  int             `json:"offer_revision,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Confidence     *float64        `json:"confidence,omitempty"`
	FormParameters json.RawMessage `json:"form_parameters,omitempty"`
}

// handleStartWorkflow обслуживает POST .../workflow: запускает durable-исполнение
// заявки. При недоступности рантайма честно отвечает 503, и заявка остаётся в
// текущем статусе. Повторный старт уже бегущего исполнения — не 503, а явный
// идемпотентный исход «уже запущено» (200).
func (s *Server) handleStartWorkflow(w http.ResponseWriter, r *http.Request) {
	if s.deps.ExecutionProtocol == ports.ExecutionProtocolV2 {
		if !s.executionAdmissionReady() {
			writeErrorMessage(w, http.StatusServiceUnavailable, "durable admission недоступна: нет активного consumer")
			return
		}
		commandID := r.Header.Get("Idempotency-Key")
		if strings.TrimSpace(commandID) == "" {
			writeErrorMessage(w, http.StatusBadRequest, "требуется заголовок Idempotency-Key")
			return
		}
		var body startWorkflowBody
		if err := decodeJSON(w, r, &body); err != nil {
			writeErrorMessage(w, http.StatusBadRequest, err.Error())
			return
		}
		if body.LeadTarget != nil {
			if err := validateLeadTarget(*body.LeadTarget); err != nil {
				writeDomainError(w, err)
				return
			}
		}
		admission, err := s.deps.AdmitExecution.Handle(r.Context(), command.AdmitExecution{
			LifecycleFields: command.LifecycleFields{
				RequestID:     domain.RequestID(r.PathValue("requestID")),
				TenantID:      domain.TenantID(body.TenantID),
				CorrelationID: domain.CorrelationID(body.CorrelationID),
				Actor:         domain.ActorID("api"),
			},
			CommandID: domain.CommandID(commandID), Kind: command.AdmissionExecute, LeadTarget: body.LeadTarget,
			RequestVersion: body.RequestVersion, OfferRevision: body.OfferRevision, Reason: body.Reason,
			Confidence: body.Confidence, FormParameters: body.FormParameters,
		})
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, admissionResponse(admission))
		return
	}
	if s.deps.WorkflowStarter == nil {
		writeErrorMessage(w, http.StatusServiceUnavailable, "исполнение: рантайм workflow не подключён")
		return
	}
	var body startWorkflowBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.LeadTarget != nil {
		if err := validateLeadTarget(*body.LeadTarget); err != nil {
			writeDomainError(w, err)
			return
		}
	}
	requestView, err := s.deps.GetRequest.Handle(r.Context(), query.GetRequest{
		RequestID: domain.RequestID(r.PathValue("requestID")), TenantID: domain.TenantID(body.TenantID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	run, err := s.deps.WorkflowStarter.StartFulfillment(r.Context(), ports.StartFulfillment{
		RequestID:        r.PathValue("requestID"),
		TenantID:         body.TenantID,
		CorrelationID:    body.CorrelationID,
		LeadTarget:       targetValue(body.LeadTarget),
		ExecutionPurpose: requestView.ExecutionPurpose,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	response := workflowRunResponse{WorkflowID: run.WorkflowID, RunID: run.RunID, AlreadyStarted: run.AlreadyStarted}
	if run.AlreadyStarted {
		writeJSON(w, http.StatusOK, response)
		return
	}
	writeJSON(w, http.StatusAccepted, response)
}

// workflowRunResponse — форма ссылки на запущенное исполнение. AlreadyStarted
// отличает повторный старт от только что созданного исполнения.
type workflowRunResponse struct {
	WorkflowID     string `json:"workflow_id"`
	RunID          string `json:"run_id"`
	AlreadyStarted bool   `json:"already_started,omitempty"`
}

// workflowDecisionBody — прежнее JSON-представление сигнала. Actor принимается
// только для совместимости формы запроса, но никогда не используется как субъект.
type workflowDecisionBody struct {
	TenantID string `json:"tenant_id"`
	Approved bool   `json:"approved"`
	Actor    string `json:"actor,omitempty"`
}

// handleWorkflowApprovalDecision обслуживает POST .../workflow/approval-decision:
// подаёт бегущему исполнению сигнал утверждения результата.
func (s *Server) handleWorkflowApprovalDecision(w http.ResponseWriter, r *http.Request) {
	var tenant string
	var approved bool
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		var body workflowDecisionBody
		if err := decodeJSON(w, r, &body); err != nil {
			writeErrorMessage(w, http.StatusBadRequest, err.Error())
			return
		}
		tenant, approved = body.TenantID, body.Approved
	} else {
		if err := r.ParseForm(); err != nil {
			writeErrorMessage(w, http.StatusBadRequest, "не удалось разобрать форму")
			return
		}
		tenant, approved = r.FormValue("tenant_id"), r.FormValue("approved") == "true"
	}
	tenantID := domain.TenantID(tenant)
	if _, err := s.deps.GetRequest.Handle(r.Context(), query.GetRequest{
		RequestID: domain.RequestID(r.PathValue("requestID")), TenantID: tenantID,
	}); err != nil {
		writeDomainError(w, err)
		return
	}
	if s.deps.WorkflowSignaler == nil {
		writeErrorMessage(w, http.StatusServiceUnavailable, "исполнение: рантайм workflow не подключён")
		return
	}
	actor := actorOf(r)
	if actor == "" {
		writeErrorMessage(w, http.StatusUnauthorized, "требуется сессия оператора")
		return
	}
	if err := s.deps.WorkflowSignaler.SignalApprovalDecision(r.Context(), ports.WorkflowDecision{
		RequestID: r.PathValue("requestID"),
		Approved:  approved,
		Actor:     actor,
	}); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

// handleHealth обслуживает GET /healthz: сервис запущен и принимает запросы
// (проба живости, не зависит от рантайма исполнения).
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz обслуживает GET /readyz: готовность обслуживать работу с учётом
// доступности рантайма durable-исполнения. Недоступный рантайм — неготовность
// (503), тогда как /healthz продолжает отвечать как проба живости.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	evaluator := s.deps.ReadinessEvaluator
	if evaluator == nil {
		evaluator = readiness.New(readiness.Config{Timeout: 750 * time.Millisecond, TTL: 2 * time.Second})
	}
	snapshot := evaluator.Evaluate(r.Context(), s.deps.Readiness)
	status := http.StatusOK
	if !snapshot.Ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, snapshot)
}

// decodeTransition разбирает общее тело команды перехода.
func (s *Server) decodeTransition(w http.ResponseWriter, r *http.Request) (transitionBody, bool) {
	var body transitionBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return transitionBody{}, false
	}
	return body, true
}

// decodeArtifact разбирает тело команды фиксации ссылки.
func (s *Server) decodeArtifact(w http.ResponseWriter, r *http.Request) (artifactBody, bool) {
	var body artifactBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return artifactBody{}, false
	}
	return body, true
}

// lifecycleFields собирает общую часть команды перехода из тела и пути.
func lifecycleFields(_ http.ResponseWriter, r *http.Request, body transitionBody) command.LifecycleFields {
	return command.LifecycleFields{
		RequestID:     domain.RequestID(r.PathValue("requestID")),
		TenantID:      domain.TenantID(body.TenantID),
		Actor:         domain.ActorID(body.Actor),
		CorrelationID: domain.CorrelationID(body.CorrelationID),
		CausationID:   domain.CausationID(body.CausationID),
	}
}

// toTransitionResponse переводит результат перехода в форму ответа.
func toTransitionResponse(result command.TransitionResult) transitionResponse {
	return transitionResponse{
		RequestID: result.RequestID.String(),
		Status:    result.Status,
		Version:   result.Version,
	}
}
