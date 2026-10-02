package httpapi

import (
	"errors"
	"net/http"
	"net/url"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/frontend/components"
)

// approvalQueueView — модель очереди утверждения: заявки текущего тенанта, чьё
// исполнение стоит на человеческой паузе. Unavailable различает честный исход
// «исполнение недоступно» и пустую очередь.
type approvalQueueView struct {
	TenantID    string
	Actor       string
	Items       []query.ApprovalItem
	Unavailable bool
}

// approvalView — модель экрана утверждения одной заявки: заявка и наблюдаемое
// состояние исполнения. Фазу и найденное число решает владелец экрана, а не
// шаблон.
type approvalView struct {
	TenantID    string
	Actor       string
	Request     query.RequestView
	Notice      string
	Phase       string
	Execution   string
	Found       int
	Unavailable bool
	CanDecide   bool
}

// handleApprovalQueue обслуживает GET /approvals: очередь заявок, ожидающих
// утверждения результата исполнения.
func (s *Server) handleApprovalQueue(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant_id")
	actor := actorOf(r)
	view := approvalQueueView{TenantID: tenant, Actor: actor}
	if s.deps.ApprovalQueue == nil {
		view.Unavailable = true
	} else {
		items, err := s.deps.ApprovalQueue.Handle(r.Context(), query.ApprovalQueue{TenantID: domain.TenantID(tenant)})
		switch {
		case err == nil:
			view.Items = items
		case errors.Is(err, query.ErrApprovalRuntimeUnavailable):
			// Рантайм недоступен — это не пустая очередь: показываем исход честно.
			view.Unavailable = true
		default:
			s.renderPlainError(w, http.StatusInternalServerError, "очередь утверждения недоступна")
			return
		}
	}
	s.render(w, http.StatusOK, pageApprovalQueue, components.Page{
		Layout: components.Layout{Title: "Очередь утверждения"},
		View:   view,
	})
}

// handleApprovalScreen обслуживает GET /requests/{requestID}/approval: экран
// утверждения одной заявки.
func (s *Server) handleApprovalScreen(w http.ResponseWriter, r *http.Request) {
	s.renderApprovalScreen(w, r, http.StatusOK, r.URL.Query().Get("tenant_id"), actorOf(r), "")
}

// handleApprovalApprove обслуживает POST .../approval/approve: сигнал утверждения.
func (s *Server) handleApprovalApprove(w http.ResponseWriter, r *http.Request) {
	s.approvalDecision(w, r, true)
}

// handleApprovalReject обслуживает POST .../approval/reject: сигнал отклонения.
func (s *Server) handleApprovalReject(w http.ResponseWriter, r *http.Request) {
	s.approvalDecision(w, r, false)
}

// approvalDecision выполняет решение оператора: подаёт бегущему исполнению
// сигнал утверждения либо отклонения через ports.WorkflowSignaler. Действие
// возвращается на экран (POST/redirect/GET); для HTMX — частичный ответ.
// Недоступность рантайма — честный 503, состояние не выдумывается.
func (s *Server) approvalDecision(w http.ResponseWriter, r *http.Request, approved bool) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	tenant := r.FormValue("tenant_id")
	actor := actorOf(r)
	if s.deps.WorkflowSignaler == nil {
		s.renderApprovalScreen(w, r, http.StatusServiceUnavailable, tenant, actor,
			"исполнение: рантайм workflow не подключён")
		return
	}
	if err := s.deps.WorkflowSignaler.SignalApprovalDecision(r.Context(), ports.WorkflowDecision{
		RequestID: r.PathValue("requestID"),
		Approved:  approved,
		Actor:     actor,
	}); err != nil {
		s.renderApprovalScreen(w, r, statusForError(err), tenant, actor, err.Error())
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		model, err := s.loadApproval(r, tenant, actor, "")
		if err != nil {
			writeDomainError(w, err)
			return
		}
		s.renderPageFragment(w, http.StatusOK, pageApprovalScreen, fragmentApproval, model)
		return
	}
	http.Redirect(w, r, approvalScreenURL(r.PathValue("requestID"), tenant, actor), http.StatusSeeOther)
}

// renderApprovalScreen загружает заявку и состояние исполнения и рисует экран.
func (s *Server) renderApprovalScreen(w http.ResponseWriter, r *http.Request, status int, tenant, actor, notice string) {
	model, err := s.loadApproval(r, tenant, actor, notice)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	s.render(w, status, pageApprovalScreen, components.Page{
		Layout: components.Layout{Title: "Утверждение заявки " + model.Request.RequestID},
		View:   model,
	})
}

// loadApproval читает заявку и состояние исполнения и собирает модель экрана.
// Недоступность рантайма — отдельный исход (Unavailable), а не ошибка экрана:
// заявка и оффер всё равно показываются, а фаза честно помечается недоступной.
func (s *Server) loadApproval(r *http.Request, tenant, actor, notice string) (approvalView, error) {
	view, err := s.deps.GetRequest.Handle(r.Context(), query.GetRequest{
		RequestID: domain.RequestID(r.PathValue("requestID")),
		TenantID:  domain.TenantID(tenant),
	})
	if err != nil {
		return approvalView{}, err
	}
	model := approvalView{TenantID: tenant, Actor: actor, Request: view, Notice: notice}
	if s.deps.WorkflowStatus == nil {
		model.Unavailable = true
		return model, nil
	}
	status, err := s.deps.WorkflowStatus.WorkflowStatus(r.Context(), view.RequestID)
	switch {
	case err == nil:
	case errors.Is(err, ports.ErrWorkflowUnavailable):
		model.Unavailable = true
		return model, nil
	case errors.Is(err, ports.ErrWorkflowNotFound):
		// Исполнения нет: фазу не показываем, но и ошибкой это не является.
		return model, nil
	default:
		return approvalView{}, err
	}
	model.Phase = status.Phase
	model.Execution = status.Execution
	model.Found = status.Found
	model.CanDecide = view.Status == domain.StatusProcessing && status.Phase == query.ApprovalPhaseAwaitingApproval
	return model, nil
}

// approvalScreenURL собирает адрес экрана утверждения заявки.
func approvalScreenURL(requestID, tenant, actor string) string {
	values := url.Values{}
	values.Set("tenant_id", tenant)
	if actor != "" {
		values.Set("actor", actor)
	}
	return "/requests/" + url.PathEscape(requestID) + "/approval?" + values.Encode()
}
