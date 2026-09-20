package httpapi

import (
	"net/http"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/query"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// decisionRequest — тело команды решения оператора. ExpectedVersion — точная
// версия черновика: несовпадение отвергает решение, не меняя состояние
// (инвариант I2, design.md Решение 2).
type decisionRequest struct {
	TenantID        string `json:"tenant_id"`
	ActorID         string `json:"actor_id"`
	DecisionID      string `json:"decision_id"`
	ExpectedVersion int    `json:"expected_version"`
	Reason          string `json:"reason"`
}

// decisionResponse — ответ команды решения. Recorded равно false, если решение
// по этой версии черновика уже было принято: повтор идемпотентен.
type decisionResponse struct {
	DecisionID   string    `json:"decision_id"`
	CampaignID   string    `json:"campaign_id"`
	DraftID      string    `json:"draft_id"`
	DraftVersion int       `json:"draft_version"`
	ActorID      string    `json:"actor_id"`
	Outcome      string    `json:"outcome"`
	DecidedAt    time.Time `json:"decided_at"`
	Recorded     bool      `json:"recorded"`
}

// reviseDraftRequest — тело команды доработки черновика. Доработка повышает
// версию и возвращает черновик на утверждение.
type reviseDraftRequest struct {
	TenantID        string `json:"tenant_id"`
	ActorID         string `json:"actor_id"`
	ExpectedVersion int    `json:"expected_version"`
	MaterialID      string `json:"material_id"`
	SegmentID       string `json:"segment_id"`
	RecipientID     string `json:"recipient_id"`
	Channel         string `json:"channel"`
}

// handleApproveDraft обрабатывает POST .../approve. Положительное решение по
// точной версии — единственный путь к исполнению действия.
func (s *Server) handleApproveDraft(w http.ResponseWriter, r *http.Request) {
	var req decisionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.approveDraft.Handle(r.Context(), command.ApproveDraft{
		CampaignID:      domain.CampaignID(r.PathValue("campaignID")),
		TenantID:        domain.TenantID(req.TenantID),
		Actor:           domain.ActorID(req.ActorID),
		DraftID:         domain.DraftID(r.PathValue("draftID")),
		DecisionID:      domain.DecisionID(req.DecisionID),
		ExpectedVersion: req.ExpectedVersion,
		Reason:          req.Reason,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newDecisionResponse(result))
}

// handleRejectDraft обрабатывает POST .../reject. Причину требует домен, а не
// HTTP-слой; пустая причина отклоняется сценарием.
func (s *Server) handleRejectDraft(w http.ResponseWriter, r *http.Request) {
	var req decisionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.rejectDraft.Handle(r.Context(), command.RejectDraft{
		CampaignID:      domain.CampaignID(r.PathValue("campaignID")),
		TenantID:        domain.TenantID(req.TenantID),
		Actor:           domain.ActorID(req.ActorID),
		DraftID:         domain.DraftID(r.PathValue("draftID")),
		DecisionID:      domain.DecisionID(req.DecisionID),
		ExpectedVersion: req.ExpectedVersion,
		Reason:          req.Reason,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newDecisionResponse(result))
}

// handleReviseDraft обрабатывает POST .../revise.
func (s *Server) handleReviseDraft(w http.ResponseWriter, r *http.Request) {
	var req reviseDraftRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	spec, err := draftSpec(draftActionRequest{
		MaterialID:  req.MaterialID,
		SegmentID:   req.SegmentID,
		RecipientID: req.RecipientID,
		Channel:     req.Channel,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	result, err := s.reviseDraft.Handle(r.Context(), command.ReviseDraft{
		CampaignID:      domain.CampaignID(r.PathValue("campaignID")),
		TenantID:        domain.TenantID(req.TenantID),
		Actor:           domain.ActorID(req.ActorID),
		DraftID:         domain.DraftID(r.PathValue("draftID")),
		ExpectedVersion: req.ExpectedVersion,
		Spec:            spec,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, draftResponse{
		CampaignID: result.CampaignID.String(),
		DraftID:    result.DraftID.String(),
		Version:    result.Version,
		State:      string(result.State),
		Revised:    result.Revised,
	})
}

// approvalQueueResponse — ответ очереди утверждения.
type approvalQueueResponse struct {
	Items []approvalQueueItem `json:"items"`
}

// approvalQueueItem — позиция очереди ожидающих решения черновиков.
type approvalQueueItem struct {
	CampaignID   string `json:"campaign_id"`
	TenantID     string `json:"tenant_id"`
	DraftID      string `json:"draft_id"`
	DraftVersion int    `json:"draft_version"`
	Channel      string `json:"channel"`
	MaterialID   string `json:"material_id"`
	SegmentID    string `json:"segment_id"`
	RecipientID  string `json:"recipient_id"`
}

// handleApprovalQueue обрабатывает GET /campaigns. Область очереди задаёт
// вызывающий: повторяющийся параметр campaign_id перечисляет кампании, а
// tenant_id и actor_id обязательны и ограничивают выборку правом чтения.
// Очередь не содержит утверждённых и отклонённых черновиков — это гарантирует
// домен через PendingApproval.
func (s *Server) handleApprovalQueue(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	campaignIDs := make([]domain.CampaignID, 0, len(values["campaign_id"]))
	for _, raw := range values["campaign_id"] {
		if raw == "" {
			continue
		}
		campaignIDs = append(campaignIDs, domain.CampaignID(raw))
	}

	items, err := s.approvalQueue.Handle(r.Context(), query.ApprovalQueue{
		CampaignIDs: campaignIDs,
		TenantID:    domain.TenantID(values.Get("tenant_id")),
		Actor:       domain.ActorID(values.Get("actor_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	response := approvalQueueResponse{Items: make([]approvalQueueItem, 0, len(items))}
	for _, item := range items {
		response.Items = append(response.Items, approvalQueueItem{
			CampaignID:   item.CampaignID.String(),
			TenantID:     item.TenantID.String(),
			DraftID:      item.DraftID.String(),
			DraftVersion: item.DraftVersion,
			Channel:      item.Channel.String(),
			MaterialID:   item.Spec.Material.String(),
			SegmentID:    item.Spec.Segment.String(),
			RecipientID:  item.Spec.Recipient.String(),
		})
	}
	writeJSON(w, http.StatusOK, response)
}

// decisionJournalResponse — ответ журнала решений по черновику.
type decisionJournalResponse struct {
	Entries []decisionJournalEntry `json:"entries"`
}

// decisionJournalEntry — запись журнала: автор, время и результат решения.
// Batch отличает пакетное решение от одиночного; ссылки на пакет здесь нет,
// потому что собственного идентификатора у пакета не существует (design.md,
// Решение 4).
type decisionJournalEntry struct {
	DecisionID   string    `json:"decision_id"`
	CampaignID   string    `json:"campaign_id"`
	DraftID      string    `json:"draft_id"`
	DraftVersion int       `json:"draft_version"`
	ActorID      string    `json:"actor_id"`
	Outcome      string    `json:"outcome"`
	Reason       string    `json:"reason,omitempty"`
	DecidedAt    time.Time `json:"decided_at"`
	Batch        bool      `json:"batch"`
}

// handleDraftDecision обрабатывает GET .../decisions. tenant_id и actor_id
// обязательны: тенант ограничивает область данных, а актор нужен только для
// проверки права на чтение. Журнал возвращает решения всех операторов в
// пределах тенанта, включая решения других операторов.
func (s *Server) handleDraftDecision(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	entries, err := s.draftDecision.Handle(r.Context(), query.DraftDecisionJournal{
		CampaignID: domain.CampaignID(r.PathValue("campaignID")),
		TenantID:   domain.TenantID(values.Get("tenant_id")),
		DraftID:    domain.DraftID(r.PathValue("draftID")),
		Actor:      domain.ActorID(values.Get("actor_id")),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	response := decisionJournalResponse{Entries: make([]decisionJournalEntry, 0, len(entries))}
	for _, entry := range entries {
		response.Entries = append(response.Entries, decisionJournalEntry{
			DecisionID:   entry.DecisionID.String(),
			CampaignID:   entry.CampaignID.String(),
			DraftID:      entry.DraftID.String(),
			DraftVersion: entry.DraftVersion,
			ActorID:      entry.Actor.String(),
			Outcome:      string(entry.Outcome),
			Reason:       entry.Reason,
			DecidedAt:    entry.DecidedAt,
			Batch:        entry.Batch,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

// newDecisionResponse переводит результат сценария решения в форму ответа.
func newDecisionResponse(result command.DraftDecisionResult) decisionResponse {
	return decisionResponse{
		DecisionID:   result.DecisionID.String(),
		CampaignID:   result.CampaignID.String(),
		DraftID:      result.DraftID.String(),
		DraftVersion: result.DraftVersion,
		ActorID:      result.Actor.String(),
		Outcome:      string(result.Outcome),
		DecidedAt:    result.DecidedAt,
		Recorded:     result.Recorded,
	}
}
