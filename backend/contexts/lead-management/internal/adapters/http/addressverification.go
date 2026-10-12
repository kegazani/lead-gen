package httpapi

import (
	"errors"
	"net/http"
	"time"

	"lead-gen/contracts/lead-management/addressverification"
)

// addressVerificationBody — тело запроса состояния верификации адреса (контракт
// address-verification). Имена полей совпадают с wire-форматом сетевого адаптера
// addressverification/httpport.
type addressVerificationBody struct {
	TenantID     string `json:"tenant_id"`
	RecipientRef string `json:"recipient_ref"`
}

// addressVerificationResponse — ответ состояния верификации (200 OK). Адреса нет:
// только ссылка, статус и вердикт владельца.
type addressVerificationResponse struct {
	RecipientRef string `json:"recipient_ref"`
	Status       string `json:"status"`
	Verified     bool   `json:"verified"`
	Reason       string `json:"reason"`
	CheckedAt    string `json:"checked_at,omitempty"`
}

// handleAddressVerification обслуживает POST /address-verification — транспорт
// опубликованного контракта address-verification. Обработчик делегирует вызов
// порту владельца (та же реализация, что и in-proc) и отдаёт его вердикт;
// доменных правил здесь нет. Незамонтированный порт честно отвечает 503, вердикт
// не выдумывается.
func (s *Server) handleAddressVerification(w http.ResponseWriter, r *http.Request) {
	if s.addressVerification == nil {
		writeErrorBody(w, http.StatusServiceUnavailable, "верификация адреса: контракт address-verification не подключён")
		return
	}
	var body addressVerificationBody
	if err := decodeLeadSearchBody(w, r, &body); err != nil {
		writeErrorBody(w, http.StatusBadRequest, err.Error())
		return
	}
	snapshot, err := s.addressVerification.Verification(r.Context(), addressverification.Query{
		TenantID:     body.TenantID,
		RecipientRef: body.RecipientRef,
	})
	if err != nil {
		writeAddressVerificationError(w, err)
		return
	}
	response := addressVerificationResponse{
		RecipientRef: snapshot.RecipientRef,
		Status:       string(snapshot.Status),
		Verified:     snapshot.Verified,
		Reason:       snapshot.Reason,
	}
	if !snapshot.CheckedAt.IsZero() {
		response.CheckedAt = snapshot.CheckedAt.UTC().Format(time.RFC3339)
	}
	writeJSONBody(w, http.StatusOK, response)
}

// writeAddressVerificationError переводит исход контракта в код ответа: доменный
// отказ — 400, недоступность владельца или сервиса проверки — 503, прочие сбои —
// 500. Вердикт не выдумывается.
func writeAddressVerificationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, addressverification.ErrRejected):
		writeErrorBody(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, addressverification.ErrUnavailable):
		writeErrorBody(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeErrorBody(w, http.StatusInternalServerError, "внутренняя ошибка")
	}
}
