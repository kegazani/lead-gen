package httpapi

import (
	"encoding/json"
	"net/http"

	"lead-gen/backend/contexts/platform-governance/internal/app/query"
	"lead-gen/contracts/platform-governance/accessdecision"
)

// maxAccessDecisionBodyBytes ограничивает размер тела запроса решения, чтобы
// разбор не мог исчерпать память процесса.
const maxAccessDecisionBodyBytes = 1 << 20

// handleAccessDecisionContract обслуживает POST /access-decision — транспорт
// опубликованного контракта access-decision. RBAC здесь не воспроизводится:
// решение выдаёт use case DecideAccess владельца. Незамонтированный use case
// отвечает 503 — честная недоступность, а не выдуманное разрешение. Пустой актор
// классифицируется writeError как некорректная команда (400), а не как «вправе».
func (s *Server) handleAccessDecisionContract(w http.ResponseWriter, r *http.Request) {
	if s.deps.DecideAccess == nil {
		s.writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "решение о доступе не подключено"})
		return
	}
	var body accessdecision.CanActRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAccessDecisionBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		s.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "некорректное тело запроса"})
		return
	}
	allowed, err := s.deps.DecideAccess.Handle(r.Context(), query.DecideAccess{Actor: body.Actor, TenantID: body.Tenant})
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, accessdecision.CanActResponse{Allowed: allowed})
}
