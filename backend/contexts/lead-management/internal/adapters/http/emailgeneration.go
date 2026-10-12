package httpapi

import (
	"errors"
	"net/http"

	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/contracts/lead-management/emailgeneration"
)

// emailGenerationBody — тело команды генерации письма (контракт email-generation).
// Имена полей совпадают с wire-форматом сетевого адаптера
// emailgeneration/httpport.
type emailGenerationBody struct {
	RequestID     string                         `json:"request_id"`
	TenantID      string                         `json:"tenant_id"`
	LeadID        string                         `json:"lead_id"`
	Criteria      string                         `json:"criteria"`
	Language      string                         `json:"language,omitempty"`
	Tone          string                         `json:"tone,omitempty"`
	SenderCompany *emailgeneration.SenderCompany `json:"sender_company,omitempty"`
}

// emailGenerationResponse — ответ команды генерации письма (200 OK).
type emailGenerationResponse struct {
	Subject      string `json:"subject"`
	Body         string `json:"body"`
	Language     string `json:"language,omitempty"`
	RecipientRef string `json:"recipient_ref,omitempty"`
}

// handleEmailGeneration обслуживает POST /email-generation — транспорт
// опубликованного контракта email-generation. Разбирает запрос, вызывает сценарий
// и отдаёт письмо; доменных решений здесь нет. Незамонтированный сценарий честно
// отвечает 503.
func (s *Server) handleEmailGeneration(w http.ResponseWriter, r *http.Request) {
	if s.emailGeneration == nil {
		writeErrorBody(w, http.StatusServiceUnavailable, "генерация письма: контракт email-generation не подключён")
		return
	}
	var body emailGenerationBody
	if err := decodeLeadSearchBody(w, r, &body); err != nil {
		writeErrorBody(w, http.StatusBadRequest, err.Error())
		return
	}
	draft, err := s.emailGeneration.Run(r.Context(), app.DraftEmailInput{
		RequestID:     body.RequestID,
		TenantID:      body.TenantID,
		LeadID:        body.LeadID,
		Criteria:      body.Criteria,
		Language:      body.Language,
		Tone:          body.Tone,
		SenderCompany: body.SenderCompany,
	})
	if err != nil {
		writeEmailGenerationError(w, err)
		return
	}
	writeJSONBody(w, http.StatusOK, emailGenerationResponse{
		Subject:      draft.Subject,
		Body:         draft.Body,
		Language:     draft.Language,
		RecipientRef: draft.RecipientRef,
	})
}

// writeEmailGenerationError переводит исход сценария в код ответа: некорректный
// запрос — 400, недоступная генерация (LLM не подключён, сбой вызова) — 503,
// прочие сбои — 500.
func writeEmailGenerationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrInvalidEmailRequest):
		writeErrorBody(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, app.ErrEmailGenerationUnavailable):
		writeErrorBody(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeErrorBody(w, http.StatusInternalServerError, "внутренняя ошибка")
	}
}
