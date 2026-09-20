package httpapi

import (
	"net/http"
	"strings"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// createCampaignRequest — тело команды создания кампании. CommandID —
// идентификатор команды границы campaign-command; вместе с TenantID он образует
// ключ идемпотентности границы.
type createCampaignRequest struct {
	CommandID   string    `json:"command_id"`
	CampaignID  string    `json:"campaign_id"`
	TenantID    string    `json:"tenant_id"`
	ActorID     string    `json:"actor_id"`
	Name        string    `json:"name"`
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
}

// campaignResponse — ответ создания кампании. Replayed отмечает идемпотентный
// повтор команды: состояние не менялось, кампания прежняя.
type campaignResponse struct {
	CampaignID string `json:"campaign_id"`
	State      string `json:"state,omitempty"`
	Created    bool   `json:"created"`
	Replayed   bool   `json:"replayed,omitempty"`
}

// handleCreateCampaign обрабатывает POST /campaigns — границу команды создания
// кампании. Ключ (tenant_id, command_id) резервируется до выполнения эффекта,
// поэтому повтор команды возвращает исходную кампанию, а конкурентный повтор
// не создаёт вторую.
func (s *Server) handleCreateCampaign(w http.ResponseWriter, r *http.Request) {
	var req createCampaignRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	window, err := domain.NewCampaignWindow(req.WindowStart, req.WindowEnd)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	tenantID := domain.TenantID(req.TenantID)
	commandID := domain.CommandID(req.CommandID)
	campaignID := domain.CampaignID(req.CampaignID)
	hasKey := s.commandKeys != nil && strings.TrimSpace(req.CommandID) != ""

	if hasKey {
		// Ключ резервируется до эффекта: гонку выигрывает ровно один вызов,
		// поэтому одинаковые команды не создают вторую кампанию.
		reserved, owner, err := s.commandKeys.Reserve(r.Context(), tenantID, commandID, campaignID)
		if err != nil {
			writeDomainError(w, err)
			return
		}
		if !owner {
			// Результат уже принадлежит другому вызову: отдаём его, не выполняя
			// эффект повторно.
			writeJSON(w, http.StatusOK, campaignResponse{CampaignID: reserved.String(), Replayed: true})
			return
		}
	}

	result, err := s.createCampaign.Handle(r.Context(), command.CreateCampaign{
		CommandID:  commandID,
		CampaignID: campaignID,
		TenantID:   tenantID,
		Actor:      domain.ActorID(req.ActorID),
		Name:       req.Name,
		Window:     window,
	})
	if err != nil {
		if hasKey {
			// Эффект не состоялся: освобождаем ключ, чтобы повтор мог его занять.
			s.commandKeys.Release(tenantID, commandID)
		}
		writeDomainError(w, err)
		return
	}

	if hasKey {
		s.commandKeys.Complete(tenantID, commandID, result.CampaignID)
	}

	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, campaignResponse{
		CampaignID: result.CampaignID.String(),
		State:      string(result.State),
		Created:    result.Created,
	})
}

// draftActionRequest — тело команды постановки черновика.
type draftActionRequest struct {
	TenantID    string `json:"tenant_id"`
	ActorID     string `json:"actor_id"`
	DraftID     string `json:"draft_id"`
	MaterialID  string `json:"material_id"`
	SegmentID   string `json:"segment_id"`
	RecipientID string `json:"recipient_id"`
	Channel     string `json:"channel"`
}

// draftResponse — ответ постановки или доработки черновика.
type draftResponse struct {
	CampaignID string `json:"campaign_id"`
	DraftID    string `json:"draft_id"`
	Version    int    `json:"version"`
	State      string `json:"state"`
	Created    bool   `json:"created,omitempty"`
	Revised    bool   `json:"revised,omitempty"`
}

// handleDraftAction обрабатывает POST /campaigns/{campaignID}/drafts. Черновик
// попадает в состояние «ожидает утверждения»; действие при этом не исполняется.
func (s *Server) handleDraftAction(w http.ResponseWriter, r *http.Request) {
	var req draftActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	spec, err := draftSpec(req)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	result, err := s.draftAction.Handle(r.Context(), command.DraftAction{
		CampaignID: domain.CampaignID(r.PathValue("campaignID")),
		TenantID:   domain.TenantID(req.TenantID),
		Actor:      domain.ActorID(req.ActorID),
		DraftID:    domain.DraftID(req.DraftID),
		Spec:       spec,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	writeJSON(w, status, draftResponse{
		CampaignID: result.CampaignID.String(),
		DraftID:    result.DraftID.String(),
		Version:    result.Version,
		State:      string(result.State),
		Created:    result.Created,
	})
}

// draftSpec собирает состав черновика из тела запроса. Канал разбирает доменный
// value object, поэтому неизвестное значение отвергается на границе.
func draftSpec(req draftActionRequest) (domain.DraftSpec, error) {
	channel, err := domain.ParseActionChannel(req.Channel)
	if err != nil {
		return domain.DraftSpec{}, err
	}
	return domain.DraftSpec{
		Material:  domain.MaterialID(req.MaterialID),
		Segment:   domain.SegmentID(req.SegmentID),
		Recipient: domain.RecipientID(req.RecipientID),
		Channel:   channel,
	}, nil
}
