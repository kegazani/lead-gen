package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	appeditorial "lead-gen/backend/contexts/outreach-orchestration/internal/app/editorialreview"
	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
)

// HTTP-поверхность приёма передачи писем заявки (опубликованный контракт
// outreach-handoff). Это межсервисная граница: команда приходит от
// request-fulfillment, а не от оператора консоли, поэтому сессия и CSRF не
// требуются — как у других контрактных эндпоинтов (lead-search,
// email-generation, request-intent). Адаптер только разбирает провод, вызывает
// сценарий и переводит его исход в код ответа; решений о допустимости он не
// принимает.

// handoffItemRequest — одно письмо передачи. Имена полей совпадают с wire-форматом
// сетевого клиента outreachhandoff/httpport (contracts/outreach-orchestration).
type handoffItemRequest struct {
	LeadID       string `json:"lead_id"`
	RecipientRef string `json:"recipient_ref"`
	MaterialRef  string `json:"material_ref"`
}

// handoffRequest — тело команды передачи. Имена полей совпадают с wire-форматом
// сетевого клиента outreachhandoff/httpport.
type handoffRequest struct {
	RequestID        string               `json:"request_id"`
	TenantID         string               `json:"tenant_id"`
	Actor            string               `json:"actor,omitempty"`
	Name             string               `json:"name,omitempty"`
	ExecutionPurpose string               `json:"execution_purpose,omitempty"`
	Items            []handoffItemRequest `json:"items"`
}

// handoffResponse — ответ приёма передачи (200 OK). Несёт только наблюдаемый
// исход: персональных данных переданных писем в ответе нет.
type handoffResponse struct {
	CampaignID string `json:"campaign_id"`
	Accepted   int    `json:"accepted"`
	Skipped    int    `json:"skipped"`
	Repeated   bool   `json:"repeated"`
}

type handoffCancellationRequest struct {
	TenantID  string `json:"tenant_id"`
	RequestID string `json:"request_id"`
}

type handoffCancellationResponse struct {
	TenantID  string `json:"tenant_id"`
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

// handleHandoff обслуживает POST /outreach-handoff — транспорт контракта
// outreach-handoff. Незамонтированный сценарий честно отвечает 503: приём не
// выдаётся за работающий. Доменный отказ формы или состава — 400, недоступность
// зависимостей — 503. Сетевой клиент контракта переводит эти коды
// соответственно в ErrRejected и ErrUnavailable.
func (s *Server) handleHandoff(w http.ResponseWriter, r *http.Request) {
	if s.handoff == nil {
		writeErrorMessage(w, http.StatusServiceUnavailable, "приём передачи писем заявки не подключён")
		return
	}

	var req handoffRequest
	if err := decodeJSON(w, r, &req); err != nil {
		// Неразборчивый или чужой формат — отказ команды, а не доменный исход.
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ExecutionPurpose == "editorial" {
		if s.editorialHandoff == nil {
			writeErrorMessage(w, http.StatusServiceUnavailable, "редакторская передача не подключена")
			return
		}
		items := make([]appeditorial.Item, 0, len(req.Items))
		for _, item := range req.Items {
			parts := strings.SplitN(item.MaterialRef, "@", 2)
			if strings.TrimSpace(item.LeadID) == "" || len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
				writeErrorMessage(w, http.StatusBadRequest, "некорректная ссылка редакторского материала")
				return
			}
			items = append(items, appeditorial.Item{LeadID: item.LeadID, MaterialRef: item.MaterialRef, AssetID: parts[0], VersionID: parts[1]})
		}
		campaign, repeated, err := s.editorialHandoff.Submit(r.Context(), appeditorial.Handoff{CampaignID: "handoff-" + req.RequestID, TenantID: req.TenantID, RequestID: req.RequestID, Items: items})
		if err != nil {
			writeHandoffError(w, err)
			return
		}
		accepted := len(items)
		skipped := 0
		if repeated {
			accepted = 0
			skipped = len(items)
		}
		writeJSON(w, http.StatusOK, handoffResponse{CampaignID: campaign.CampaignID, Accepted: accepted, Skipped: skipped, Repeated: repeated})
		return
	}

	result, err := s.handoff.Handle(r.Context(), command.SubmitHandoff{
		RequestID:        req.RequestID,
		TenantID:         req.TenantID,
		Actor:            req.Actor,
		Name:             req.Name,
		ExecutionPurpose: req.ExecutionPurpose,
		Items:            handoffItems(req.Items),
	})
	if err != nil {
		writeHandoffError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, handoffResponse{
		CampaignID: result.CampaignID.String(),
		Accepted:   result.Accepted,
		Skipped:    result.Skipped,
		Repeated:   result.Repeated,
	})
}

// handleHandoffCancellation persists the request tombstone before acknowledging
// it. The fence is valid even when campaign creation has not reached this owner.
func (s *Server) handleHandoffCancellation(w http.ResponseWriter, r *http.Request) {
	if s.cancelRequest == nil {
		writeErrorMessage(w, http.StatusServiceUnavailable, "отмена передачи заявок не подключена")
		return
	}
	var req handoffCancellationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeErrorMessage(w, http.StatusBadRequest, err.Error())
		return
	}
	cancellation := outreachhandoff.RequestCancellation{TenantID: req.TenantID, RequestID: req.RequestID}
	if err := cancellation.Validate(); err != nil {
		writeHandoffError(w, err)
		return
	}
	if err := s.cancelRequest.Handle(r.Context(), command.CancelRequest{TenantID: req.TenantID, RequestID: req.RequestID}); err != nil {
		writeHandoffError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, handoffCancellationResponse{TenantID: req.TenantID, RequestID: req.RequestID, Status: "cancelled"})
}

// handoffItems переводит проводную форму состава в форму сценария.
func handoffItems(items []handoffItemRequest) []command.SubmitHandoffItem {
	out := make([]command.SubmitHandoffItem, 0, len(items))
	for _, item := range items {
		out = append(out, command.SubmitHandoffItem{
			LeadID:       item.LeadID,
			RecipientRef: item.RecipientRef,
			MaterialRef:  item.MaterialRef,
		})
	}
	return out
}

// writeHandoffError переводит исход сценария в код ответа, различимый сетевым
// клиентом контракта outreach-handoff. Доменный отказ (форма, состав, право) —
// 400, что клиент читает как ErrRejected; недоступность зависимостей (хранилище,
// транзакция, решение о доступе) — 503, что клиент читает как ErrUnavailable.
// Недоступность не выдаётся за успешный приём.
func writeHandoffError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, command.ErrHandoffRejected), errors.Is(err, command.ErrAccessDenied), errors.Is(err, outreachhandoff.ErrRejected):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
	default:
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "приём передачи недоступен"})
	}
}
