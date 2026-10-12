package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"lead-gen/backend/contexts/lead-management/internal/app"
)

const (
	// maxLeadSearchBodyBytes ограничивает размер тела запроса поиска лидов, чтобы
	// разбор не мог исчерпать память процесса.
	maxLeadSearchBodyBytes = 1 << 20

	contentTypeJSON = "application/json; charset=utf-8"
)

// leadSearchBody — тело команды поиска лидов (контракт lead-search). Имена полей
// совпадают с wire-форматом сетевого адаптера leadsearch/httpport.
type leadSearchBody struct {
	RequestID string `json:"request_id"`
	TenantID  string `json:"tenant_id"`
	Criteria  string `json:"criteria"`
	Limit     int    `json:"limit"`
}

// leadRefBody — ссылка на найденный лид в ответе: только идентификатор и
// происхождение, без персональных данных.
type leadRefBody struct {
	ID     string `json:"id"`
	Origin string `json:"origin"`
}

// leadSearchResponse — ответ команды поиска лидов (200 OK).
type leadSearchResponse struct {
	Found         int           `json:"found"`
	FromPool      int           `json:"from_pool"`
	Crawled       int           `json:"crawled"`
	Leads         []leadRefBody `json:"leads"`
	PartialReason string        `json:"partial_reason,omitempty"`
}

// errorBody — единая форма ошибки HTTP-слоя.
type errorBody struct {
	Error string `json:"error"`
}

// handleLeadSearch обслуживает POST /lead-search — транспорт опубликованного
// контракта lead-search. Разбирает запрос, вызывает сценарий и отдаёт результат;
// доменных решений здесь нет. Незамонтированный сценарий честно отвечает 503.
func (s *Server) handleLeadSearch(w http.ResponseWriter, r *http.Request) {
	if s.searchLeads == nil {
		writeErrorBody(w, http.StatusServiceUnavailable, "поиск лидов: контракт lead-search не подключён")
		return
	}
	var body leadSearchBody
	if err := decodeLeadSearchBody(w, r, &body); err != nil {
		writeErrorBody(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.searchLeads.Run(r.Context(), app.SearchLeadsInput{
		RequestID: body.RequestID,
		TenantID:  body.TenantID,
		Criteria:  body.Criteria,
		Limit:     body.Limit,
	})
	if err != nil {
		writeLeadSearchError(w, err)
		return
	}
	leads := make([]leadRefBody, 0, len(result.Leads))
	for _, lead := range result.Leads {
		leads = append(leads, leadRefBody{ID: lead.CompanyID, Origin: string(lead.Origin)})
	}
	writeJSONBody(w, http.StatusOK, leadSearchResponse{
		Found:         result.Found,
		FromPool:      result.FromPool,
		Crawled:       result.Crawled,
		Leads:         leads,
		PartialReason: result.PartialReason,
	})
}

// decodeLeadSearchBody читает тело команды, отвергая неизвестные поля: опечатка
// в поле должна быть видна сразу, а не молча проигнорирована.
func decodeLeadSearchBody(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("тело запроса отсутствует")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLeadSearchBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("разбор JSON: %w", err)
	}
	return nil
}

// writeLeadSearchError переводит доменный отказ владельца в код ответа:
// некорректный критерий — 400, прочие сбои чтения — 500.
func writeLeadSearchError(w http.ResponseWriter, err error) {
	if errors.Is(err, app.ErrInvalidSearchCriteria) {
		writeErrorBody(w, http.StatusBadRequest, err.Error())
		return
	}
	writeErrorBody(w, http.StatusInternalServerError, "внутренняя ошибка")
}

// writeJSONBody отправляет полезную нагрузку с заданным статусом.
func writeJSONBody(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// writeErrorBody отправляет ошибку транспорта.
func writeErrorBody(w http.ResponseWriter, status int, message string) {
	writeJSONBody(w, status, errorBody{Error: message})
}
