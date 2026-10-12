package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/query"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/contracts/outreach-orchestration/draftdecision"
)

// HTTP-поверхность чтения решения оператора по черновику (read-контракт
// draftdecision). Межсервисная граница: запрос приходит от agent-orchestration для
// шага отправки, сессия и CSRF не требуются. Адаптер только разбирает запрос,
// вызывает запрос чтения и переводит исход в проводную форму решения: 200 с исходом
// approved/pending/rejected; 404 — черновик/решение не найдены; 503 — источник
// недоступен. Сетевой клиент контракта переводит это обратно в исходы.

// handleApprovedAction обслуживает GET /approved-action?tenant_id=&draft_ref=&draft_version=.
// Незамонтированный запрос честно отвечает 503.
func (s *Server) handleApprovedAction(w http.ResponseWriter, r *http.Request) {
	if s.approvedAction == nil {
		writeErrorMessage(w, http.StatusServiceUnavailable, "источник решения по черновику не подключён")
		return
	}

	values := r.URL.Query()
	version, err := strconv.Atoi(values.Get("draft_version"))
	if err != nil {
		writeErrorMessage(w, http.StatusBadRequest, "некорректная версия черновика")
		return
	}

	action, err := s.approvedAction.Handle(r.Context(), query.ApprovedActionQuery{
		TenantID:     domain.TenantID(values.Get("tenant_id")),
		DraftRef:     domain.DraftID(values.Get("draft_ref")),
		DraftVersion: version,
	})
	if err != nil {
		writeApprovedActionError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, draftdecision.Decision{
		Outcome:        draftdecision.OutcomeApproved,
		ApprovedAction: &action,
	})
}

// writeApprovedActionError переводит исход запроса чтения в проводную форму
// контракта draftdecision. Решение «не принято» и «отклонено» — валидные исходы
// чтения (200 с исходом), а не ошибки транспорта: сетевое клиент различает их по
// полю outcome. Ненайденный черновик — 404, недоступность хранилища — 503.
func writeApprovedActionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, query.ErrDraftDecisionPending):
		writeJSON(w, http.StatusOK, draftdecision.Decision{Outcome: draftdecision.OutcomePending})
	case errors.Is(err, query.ErrDraftDecisionRejected):
		writeJSON(w, http.StatusOK, draftdecision.Decision{Outcome: draftdecision.OutcomeRejected})
	case errors.Is(err, query.ErrDraftDecisionNotFound), errors.Is(err, query.ErrTenantRequired):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: err.Error()})
	default:
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "источник решения недоступен"})
	}
}
