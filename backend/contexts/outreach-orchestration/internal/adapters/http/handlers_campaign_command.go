package httpapi

import (
	"errors"
	"net/http"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// HTTP-поверхность подачи черновика агентом в очередь утверждения (контракт
// campaign-command, read-пара — draftdecision). Это межсервисная граница: команда
// приходит от agent-orchestration, а не от оператора консоли, поэтому сессия и CSRF
// не требуются — как у других контрактных эндпоинтов (lead-search, content-request,
// outreach-handoff). Адаптер только разбирает провод, вызывает сценарий и переводит
// его исход в код ответа; решений о допустимости он не принимает.

// campaignCommandRequest — тело команды подачи. Имена полей совпадают с wire-форматом
// сетевого клиента campaigncommand/httpport (contracts/outreach-orchestration).
type campaignCommandRequest struct {
	TenantID     string `json:"tenant_id"`
	RunID        string `json:"run_id"`
	MissionID    string `json:"mission_id,omitempty"`
	CompanyID    string `json:"company_id"`
	DraftRef     string `json:"draft_ref"`
	DraftVersion int    `json:"draft_version"`
	MaterialRef  string `json:"material_ref"`
	RecipientRef string `json:"recipient_ref,omitempty"`
}

// campaignCommandResponse — ответ подачи (200 OK). Несёт только наблюдаемый исход:
// персональных данных и содержимого письма в ответе нет.
type campaignCommandResponse struct {
	CampaignID   string `json:"campaign_id"`
	DraftRef     string `json:"draft_ref"`
	DraftVersion int    `json:"draft_version"`
	Repeated     bool   `json:"repeated"`
}

// handleCampaignCommand обслуживает POST /campaign-command — транспорт контракта
// campaign-command в части подачи черновика на утверждение. Незамонтированный
// сценарий честно отвечает 503: приём не выдаётся за работающий. Доменный отказ —
// 400, недоступность зависимостей — 503. Сетевой клиент контракта переводит эти коды
// соответственно в ErrRejected и ErrUnavailable.
func (s *Server) handleCampaignCommand(w http.ResponseWriter, r *http.Request) {
	if s.submitApproval == nil {
		writeErrorMessage(w, http.StatusServiceUnavailable, "подача черновика на утверждение не подключена")
		return
	}

	var req campaignCommandRequest
	if err := decodeJSON(w, r, &req); err != nil {
		// Неразборчивый или чужой формат — отказ команды, а не доменный исход.
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.submitApproval.Handle(r.Context(), command.SubmitForApproval{
		TenantID:     domain.TenantID(req.TenantID),
		RunID:        req.RunID,
		MissionID:    req.MissionID,
		CompanyID:    req.CompanyID,
		DraftRef:     domain.DraftID(req.DraftRef),
		DraftVersion: req.DraftVersion,
		MaterialRef:  domain.MaterialID(req.MaterialRef),
		RecipientRef: domain.RecipientID(req.RecipientRef),
	})
	if err != nil {
		writeApprovalSubmitError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, campaignCommandResponse{
		CampaignID:   result.CampaignID.String(),
		DraftRef:     result.DraftRef.String(),
		DraftVersion: result.DraftVersion,
		Repeated:     result.Repeated,
	})
}

// writeApprovalSubmitError переводит исход сценария в код ответа, различимый сетевым
// клиентом контракта campaign-command. Доменный отказ (форма, конфликт состава) —
// 400, что клиент читает как ErrRejected; недоступность зависимостей (хранилище,
// транзакция) — 503, что клиент читает как ErrUnavailable. Недоступность не выдаётся
// за успешную подачу.
func writeApprovalSubmitError(w http.ResponseWriter, err error) {
	if errors.Is(err, command.ErrApprovalRejected) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "подача черновика на утверждение недоступна"})
}
