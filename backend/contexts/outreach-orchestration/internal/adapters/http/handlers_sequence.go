package httpapi

import (
	"net/http"
	"strings"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/query"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// defineSequenceRequest — тело команды создания сценария цепочки.
type defineSequenceRequest struct {
	TenantID   string               `json:"tenant_id"`
	ActorID    string               `json:"actor_id"`
	SequenceID string               `json:"sequence_id"`
	Name       string               `json:"name"`
	Steps      []defineSequenceStep `json:"steps"`
}

// defineSequenceStep — шаг сценария: тип действия, материал, сегмент, задержка и
// условие перехода.
type defineSequenceStep struct {
	Action     string `json:"action"`
	MaterialID string `json:"material_id"`
	SegmentID  string `json:"segment_id"`
	DelayNanos int64  `json:"delay_nanos"`
	Transition string `json:"transition"`
}

// sequenceResponse — ответ создания сценария.
type sequenceResponse struct {
	CampaignID string `json:"campaign_id"`
	SequenceID string `json:"sequence_id"`
	Steps      int    `json:"steps"`
	Created    bool   `json:"created"`
}

// handleDefineSequence обрабатывает POST /campaigns/{campaignID}/sequences.
// Сценарий сохраняется и доступен движку; сам шаг ничего не отправляет.
func (s *Server) handleDefineSequence(w http.ResponseWriter, r *http.Request) {
	var req defineSequenceRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	steps, err := sequenceSteps(req.Steps)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	result, err := s.defineSequence.Handle(r.Context(), command.DefineSequence{
		CampaignID: domain.CampaignID(r.PathValue("campaignID")),
		TenantID:   domain.TenantID(req.TenantID),
		Actor:      domain.ActorID(req.ActorID),
		SequenceID: domain.SequenceID(req.SequenceID),
		Name:       req.Name,
		Steps:      steps,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, sequenceResponse{
		CampaignID: result.CampaignID.String(),
		SequenceID: result.SequenceID.String(),
		Steps:      result.Steps,
		Created:    result.Created,
	})
}

// sequenceSteps собирает шаги сценария из тела запроса. Неизвестный канал или
// условие перехода отвергается на границе, включая запрещённое условие открытия.
func sequenceSteps(steps []defineSequenceStep) ([]domain.SequenceStep, error) {
	out := make([]domain.SequenceStep, 0, len(steps))
	for _, step := range steps {
		action, err := domain.ParseActionChannel(step.Action)
		if err != nil {
			return nil, err
		}
		transition, err := domain.ParseTransitionCondition(step.Transition)
		if err != nil {
			return nil, err
		}
		parsed, err := domain.NewSequenceStep(domain.SequenceStepParams{
			Action:     action,
			Material:   domain.MaterialID(step.MaterialID),
			Segment:    domain.SegmentID(step.SegmentID),
			Delay:      time.Duration(step.DelayNanos),
			Transition: transition,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, parsed)
	}
	return out, nil
}

// enrollLeadRequest — тело команды добавления лида в цепочку.
type enrollLeadRequest struct {
	TenantID    string `json:"tenant_id"`
	ActorID     string `json:"actor_id"`
	SequenceID  string `json:"sequence_id"`
	ExecutionID string `json:"execution_id"`
	LeadID      string `json:"lead_id"`
	RecipientID string `json:"recipient_id"`
}

// enrollmentResponse — ответ по участию лида.
type enrollmentResponse struct {
	CampaignID  string `json:"campaign_id"`
	ExecutionID string `json:"execution_id"`
	State       string `json:"state"`
	DraftID     string `json:"draft_id,omitempty"`
	Created     bool   `json:"created,omitempty"`
	Removed     bool   `json:"removed,omitempty"`
	Resumed     bool   `json:"resumed,omitempty"`
}

// handleEnrollLead обрабатывает POST /campaigns/{campaignID}/enrollments.
func (s *Server) handleEnrollLead(w http.ResponseWriter, r *http.Request) {
	var req enrollLeadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.enrollLead.Handle(r.Context(), command.EnrollLead{
		CampaignID:  domain.CampaignID(r.PathValue("campaignID")),
		TenantID:    domain.TenantID(req.TenantID),
		Actor:       domain.ActorID(req.ActorID),
		SequenceID:  domain.SequenceID(req.SequenceID),
		ExecutionID: domain.ExecutionID(req.ExecutionID),
		Lead:        domain.LeadID(req.LeadID),
		Recipient:   domain.RecipientID(req.RecipientID),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, enrollmentResponse{
		CampaignID:  result.CampaignID.String(),
		ExecutionID: result.ExecutionID.String(),
		State:       string(result.State),
		DraftID:     result.DraftID.String(),
		Created:     result.Created,
	})
}

// removeLeadRequest — тело команды удаления лида из цепочки.
type removeLeadRequest struct {
	TenantID string `json:"tenant_id"`
	ActorID  string `json:"actor_id"`
	Reason   string `json:"reason"`
}

// handleRemoveLead обрабатывает POST /campaigns/{campaignID}/enrollments/{executionID}/remove.
func (s *Server) handleRemoveLead(w http.ResponseWriter, r *http.Request) {
	var req removeLeadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.removeLead.Handle(r.Context(), command.RemoveLead{
		CampaignID:  domain.CampaignID(r.PathValue("campaignID")),
		TenantID:    domain.TenantID(req.TenantID),
		Actor:       domain.ActorID(req.ActorID),
		ExecutionID: domain.ExecutionID(r.PathValue("executionID")),
		Reason:      req.Reason,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, enrollmentResponse{
		CampaignID:  result.CampaignID.String(),
		ExecutionID: result.ExecutionID.String(),
		State:       string(result.State),
		Removed:     result.Removed,
	})
}

// recordOutcomeRequest — тело команды записи реакции лида.
type recordOutcomeRequest struct {
	TenantID string `json:"tenant_id"`
	ActorID  string `json:"actor_id"`
	Reaction string `json:"reaction"`
	Reason   string `json:"reason"`

	// Confident — указатель, чтобы отсутствие поля означало уверенную
	// классификацию: неуверенную задают явно значением false.
	Confident *bool `json:"confident,omitempty"`
}

// handleRecordOutcome обрабатывает POST
// /campaigns/{campaignID}/enrollments/{executionID}/outcomes.
func (s *Server) handleRecordOutcome(w http.ResponseWriter, r *http.Request) {
	var req recordOutcomeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	reaction, err := domain.ParseReaction(req.Reaction)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	confident := true
	if req.Confident != nil {
		confident = *req.Confident
	}

	result, err := s.recordOutcome.Handle(r.Context(), command.RecordOutcome{
		CampaignID:  domain.CampaignID(r.PathValue("campaignID")),
		TenantID:    domain.TenantID(req.TenantID),
		Actor:       domain.ActorID(req.ActorID),
		ExecutionID: domain.ExecutionID(r.PathValue("executionID")),
		Reaction:    reaction,
		Confident:   confident,
		Reason:      req.Reason,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, enrollmentResponse{
		CampaignID:  result.CampaignID.String(),
		ExecutionID: result.ExecutionID.String(),
		State:       string(result.State),
	})
}

// resumeRequest — тело команды возобновления цепочки.
type resumeRequest struct {
	TenantID string `json:"tenant_id"`
	ActorID  string `json:"actor_id"`
}

// handleResumeExecution обрабатывает POST
// /campaigns/{campaignID}/enrollments/{executionID}/resume.
func (s *Server) handleResumeExecution(w http.ResponseWriter, r *http.Request) {
	var req resumeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.resumeExecution.Handle(r.Context(), command.ResumeExecution{
		CampaignID:  domain.CampaignID(r.PathValue("campaignID")),
		TenantID:    domain.TenantID(req.TenantID),
		Actor:       domain.ActorID(req.ActorID),
		ExecutionID: domain.ExecutionID(r.PathValue("executionID")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, enrollmentResponse{
		CampaignID:  result.CampaignID.String(),
		ExecutionID: result.ExecutionID.String(),
		State:       string(result.State),
		Resumed:     result.Resumed,
	})
}

// sequenceStateResponse — ответ состояния цепочек кампании.
type sequenceStateResponse struct {
	CampaignID    string                   `json:"campaign_id"`
	PendingDrafts int                      `json:"pending_drafts"`
	Executions    []sequenceExecutionState `json:"executions"`
}

// sequenceExecutionState — состояние участия в ответе.
type sequenceExecutionState struct {
	ExecutionID string `json:"execution_id"`
	SequenceID  string `json:"sequence_id"`
	LeadID      string `json:"lead_id"`
	State       string `json:"state"`
	Step        int    `json:"step"`
	DraftID     string `json:"draft_id,omitempty"`
}

// handleSequenceState обрабатывает GET /campaigns/{campaignID}/sequences.
// Объём неутверждённых черновиков — отдельный показатель простоя.
func (s *Server) handleSequenceState(w http.ResponseWriter, r *http.Request) {
	items, err := s.sequenceState.Handle(r.Context(), query.SequenceState{
		CampaignIDs: []domain.CampaignID{domain.CampaignID(r.PathValue("campaignID"))},
		TenantID:    domain.TenantID(r.URL.Query().Get("tenant_id")),
		Actor:       domain.ActorID(r.URL.Query().Get("actor_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if len(items) == 0 {
		writeErrorMessage(w, http.StatusNotFound, "кампания не найдена")
		return
	}
	item := items[0]
	response := sequenceStateResponse{
		CampaignID:    item.CampaignID.String(),
		PendingDrafts: item.PendingDrafts,
		Executions:    make([]sequenceExecutionState, 0, len(item.Executions)),
	}
	for _, execution := range item.Executions {
		response.Executions = append(response.Executions, sequenceExecutionState{
			ExecutionID: execution.ExecutionID.String(),
			SequenceID:  execution.SequenceID.String(),
			LeadID:      execution.Lead.String(),
			State:       string(execution.State),
			Step:        execution.Step,
			DraftID:     execution.DraftID.String(),
		})
	}
	writeJSON(w, http.StatusOK, response)
}

// sequenceHistoryResponse — ответ истории шагов по лиду.
type sequenceHistoryResponse struct {
	Entries []sequenceHistoryEntry `json:"entries"`
}

// sequenceHistoryEntry — запись журнала в ответе.
type sequenceHistoryEntry struct {
	ExecutionID string `json:"execution_id"`
	Step        int    `json:"step"`
	Action      string `json:"action,omitempty"`
	Result      string `json:"result"`
	Actor       string `json:"actor,omitempty"`
	Reason      string `json:"reason,omitempty"`
	At          string `json:"at"`
}

// handleSequenceHistory обрабатывает GET
// /campaigns/{campaignID}/sequences/history?lead_id=...
func (s *Server) handleSequenceHistory(w http.ResponseWriter, r *http.Request) {
	lead := strings.TrimSpace(r.URL.Query().Get("lead_id"))
	if lead == "" {
		writeErrorMessage(w, http.StatusBadRequest, "lead_id обязателен")
		return
	}
	entries, err := s.sequenceHistory.Handle(r.Context(), query.SequenceHistory{
		CampaignIDs: []domain.CampaignID{domain.CampaignID(r.PathValue("campaignID"))},
		TenantID:    domain.TenantID(r.URL.Query().Get("tenant_id")),
		Actor:       domain.ActorID(r.URL.Query().Get("actor_id")),
		Lead:        domain.LeadID(lead),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	response := sequenceHistoryResponse{Entries: make([]sequenceHistoryEntry, 0, len(entries))}
	for _, entry := range entries {
		response.Entries = append(response.Entries, sequenceHistoryEntry{
			ExecutionID: entry.ExecutionID.String(),
			Step:        entry.Step,
			Action:      entry.Channel.String(),
			Result:      string(entry.Result),
			Actor:       entry.Actor.String(),
			Reason:      entry.Reason,
			At:          entry.At.Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	writeJSON(w, http.StatusOK, response)
}
