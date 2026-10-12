package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"lead-gen/contracts/analytics/campaignreport"
)

// HTTP-поверхность роли api для контракта campaign-report: владелец отдаёт
// обезличенные агрегаты процесса потребителю — экрану операций platform-governance.
//
// Поверхность не несёт персональных данных и содержимого красной зоны: только
// сводные счётчики. Как и у прочих контрактных поверхностей владельца (adapter-status
// integration-platform), доступ проверяет потребитель на своём экране; поверхность
// рассчитана на внутреннюю сеть и наружу не публикуется (design.md, Решение 11).

// handleOperationsNumbers обслуживает GET /operations/numbers (контракт
// campaign-report): цифры процесса по клиентам и разбивка по времени.
func (s *Server) handleOperationsNumbers(w http.ResponseWriter, r *http.Request) {
	if s.deps.Operations == nil {
		http.Error(w, "агрегаты процесса не собраны", http.StatusServiceUnavailable)
		return
	}
	request, err := operationsRequest(r)
	if err != nil {
		http.Error(w, "разбор запроса: "+err.Error(), http.StatusBadRequest)
		return
	}
	response, err := s.deps.Operations.Numbers(r.Context(), request)
	if err != nil {
		respondOperationsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// handleOperationsClients обслуживает GET /operations/clients (контракт
// campaign-report): агрегаты по клиентам для полноэкранного дашборда.
func (s *Server) handleOperationsClients(w http.ResponseWriter, r *http.Request) {
	if s.deps.Operations == nil {
		http.Error(w, "агрегаты процесса не собраны", http.StatusServiceUnavailable)
		return
	}
	request, err := operationsRequest(r)
	if err != nil {
		http.Error(w, "разбор запроса: "+err.Error(), http.StatusBadRequest)
		return
	}
	response, err := s.deps.Operations.Clients(r.Context(), request)
	if err != nil {
		respondOperationsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

// operationsRequest разбирает параметры запроса агрегатов: окно разбивки и
// актора. Окно необязательно; отрицательное значение отвергается.
func operationsRequest(r *http.Request) (campaignreport.Request, error) {
	request := campaignreport.Request{
		Actor: strings.TrimSpace(r.URL.Query().Get("actor")),
	}
	if request.Actor == "" {
		request.Actor = strings.TrimSpace(r.Header.Get("X-Actor"))
	}
	raw := strings.TrimSpace(r.URL.Query().Get("window_seconds"))
	if raw == "" {
		return request, request.Validate()
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		return campaignreport.Request{}, errors.New("некорректное окно разбивки")
	}
	request.Window = time.Duration(seconds) * time.Second
	return request, request.Validate()
}

// respondOperationsError переводит ошибку сборки агрегатов в код ответа:
// недоступность источника — 503, некорректный запрос — 400.
func respondOperationsError(w http.ResponseWriter, err error) {
	if errors.Is(err, campaignreport.ErrUnavailable) {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}
