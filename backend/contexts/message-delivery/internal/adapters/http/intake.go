// Package httpapi — HTTP-адаптеры контекста message-delivery.
//
// Пакет несёт приёмную поверхность опубликованного контракта approved-action:
// владелец контракта (outreach-orchestration) публикует утверждённое действие
// сетевым клиентом (contracts/outreach-orchestration/approvedaction/httpport), а
// контекст-исполнитель (message-delivery) принимает его здесь. Слушатель
// обслуживает роль worker, отдельным от прочих ролей контекста адресом; роль api
// у контекста отсутствует (design.md, Решение 2).
//
// В co-location сетевой путь не нужен: точка сборки вызывает приёмник напрямую
// (мост approved-action). Поведение контракта при этом не меняется.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	approvedactionadapter "lead-gen/backend/contexts/message-delivery/internal/adapters/commandsource/approvedaction"
	approvedaction "lead-gen/contracts/outreach-orchestration/approvedaction"
)

const (
	// approvedActionPath — путь приёма утверждённых действий.
	approvedActionPath = "/approved-action"

	// maxIntakeBodyBytes ограничивает размер разбираемого тела запроса, чтобы
	// сбойный клиент не мог исчерпать память приёмника.
	maxIntakeBodyBytes = 1 << 20
)

// Server — HTTP-поверхность приёма утверждённого действия. Реализует
// http.Handler.
type Server struct {
	publisher approvedaction.Publisher
	handler   http.Handler
}

// NewServer собирает приёмную поверхность поверх приёмника утверждённых действий
// (обычно моста approved-action, ApprovedActionBridge.Publisher). publisher == nil
// — приём не подключён: обработчик честно отвечает 503, а не выдаёт отсутствие
// проводки за работающий приём.
func NewServer(publisher approvedaction.Publisher) *Server {
	server := &Server{publisher: publisher}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+approvedActionPath, server.handleApprovedAction)
	server.handler = mux
	return server
}

// ServeHTTP реализует http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

// handleApprovedAction обслуживает POST /approved-action. Разбирает и проверяет
// контракт, затем передаёт действие приёмнику; доменных решений здесь нет.
// Неразборчивое или невалидное тело — 400; отсутствие приёмника — 503; успех —
// 204 без тела.
func (s *Server) handleApprovedAction(w http.ResponseWriter, r *http.Request) {
	if s.publisher == nil {
		writeError(w, http.StatusServiceUnavailable, "приём утверждённых действий не подключён")
		return
	}
	action, err := decodeApprovedAction(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := action.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.publisher.Publish(r.Context(), action); err != nil {
		writeIntakeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodeApprovedAction разбирает тело команды. Неизвестные поля и превышение
// размера отвергаются: приём не принимает молча чужой формат.
func decodeApprovedAction(w http.ResponseWriter, r *http.Request) (approvedaction.ApprovedAction, error) {
	var action approvedaction.ApprovedAction
	if r.Body == nil {
		return action, errors.New("тело запроса отсутствует")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIntakeBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&action); err != nil {
		return action, fmt.Errorf("разбор JSON: %w", err)
	}
	return action, nil
}

// writeIntakeError переводит исход приёмника в код ответа: доменный отказ — 400,
// недоступность резолвера ссылок — 503, прочие сбои — 500. Исход не маскируется
// под успех.
func writeIntakeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, approvedactionadapter.ErrRejected):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, approvedactionadapter.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "внутренняя ошибка приёма утверждённого действия")
	}
}

// errorBody — тело ответа об ошибке. Имя поля совпадает с разбором ошибок
// сетевого клиента approvedaction/httpport.
type errorBody struct {
	Error string `json:"error"`
}

// writeError пишет JSON-ответ об ошибке.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorBody{Error: message})
}
