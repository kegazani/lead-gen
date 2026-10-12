package httpapi

import (
	"errors"
	"net/http"

	"lead-gen/backend/contexts/lead-management/internal/app"
)

// deliveryTargetBody — тело команды разрешения получателя (контракт
// delivery-target). Имена полей совпадают с wire-форматом сетевого адаптера
// deliverytarget/httpport.
type deliveryTargetBody struct {
	TenantID     string `json:"tenant_id"`
	RecipientRef string `json:"recipient_ref"`
}

// deliveryTargetResponse — ответ команды разрешения получателя (200 OK).
// Единственное поле — проверенный адрес назначения; других персональных данных
// контракт не несёт.
type deliveryTargetResponse struct {
	Address string `json:"address"`
}

// handleDeliveryTarget обслуживает POST /delivery-target — транспорт
// опубликованного контракта delivery-target. Разбирает запрос, вызывает сценарий
// и отдаёт адрес; доменных решений здесь нет. Незамонтированный сценарий честно
// отвечает 503, адрес не выдумывается.
func (s *Server) handleDeliveryTarget(w http.ResponseWriter, r *http.Request) {
	if s.resolveDeliveryTarget == nil {
		writeErrorBody(w, http.StatusServiceUnavailable, "разрешение получателя: контракт delivery-target не подключён")
		return
	}
	var body deliveryTargetBody
	if err := decodeLeadSearchBody(w, r, &body); err != nil {
		writeErrorBody(w, http.StatusBadRequest, err.Error())
		return
	}
	recipient, err := s.resolveDeliveryTarget.Run(r.Context(), app.ResolveRecipientInput{
		TenantID:     body.TenantID,
		RecipientRef: body.RecipientRef,
	})
	if err != nil {
		writeDeliveryTargetError(w, err)
		return
	}
	writeJSONBody(w, http.StatusOK, deliveryTargetResponse{Address: recipient.Address})
}

// writeDeliveryTargetError переводит исход сценария разрешения в код ответа:
// неразрешимая ссылка — 400, недоступность владельца (нет контактов, резолвер не
// привязан к тенанту, сбой чтения) — 503, прочие сбои — 500.
func writeDeliveryTargetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, app.ErrInvalidDeliveryTarget):
		writeErrorBody(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, app.ErrDeliveryTargetUnavailable):
		writeErrorBody(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeErrorBody(w, http.StatusInternalServerError, "внутренняя ошибка")
	}
}
