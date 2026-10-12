package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/frontend/components"
)

// approvalQueueView — модель очереди утверждения: заявки текущего тенанта, чьё
// исполнение стоит на человеческой паузе. Unavailable различает честный исход
// «исполнение недоступно» и пустую очередь.
type approvalQueueView struct {
	Live               components.Live
	TenantID           string
	Actor              string
	Items              []approvalQueueRow
	Executions         []executionHistoryRow
	Unavailable        bool
	HistoryUnavailable bool
}

// approvalQueueRow — строка очереди утверждения: элемент очереди и раскрытый
// недобор. Недобор в элементе — указатель (nil — цель не задана); шаблон не
// разыменовывает указатели, поэтому признак и значение готовит владелец экрана.
type approvalQueueRow struct {
	query.ApprovalItem
	HasShortfall   bool
	ShortfallCount int
}

// executionHistoryRow — строка запущенного исполнения, включая workflow,
// который уже вышел из очереди ожидания операторского решения.
type executionHistoryRow struct {
	RequestID string
	Status    domain.RequestStatus
	Phase     string
	Execution string
	Found     int
	Drafted   int
}

// approvalQueueRows раскрывает элементы очереди в строки экрана.
func approvalQueueRows(items []query.ApprovalItem) []approvalQueueRow {
	rows := make([]approvalQueueRow, 0, len(items))
	for _, item := range items {
		row := approvalQueueRow{ApprovalItem: item}
		if item.Shortfall != nil {
			row.HasShortfall = true
			row.ShortfallCount = *item.Shortfall
		}
		rows = append(rows, row)
	}
	return rows
}

// approvalView — модель экрана утверждения одной заявки: заявка и наблюдаемое
// состояние исполнения. Фазу, найденное число, цель и недобор решает владелец
// экрана, а не шаблон. HasShortfall отличает «недобор рассчитан» от «цель не
// задана»: при незаданной цели недобор не показывается нулём. Diagnosis —
// наблюдаемое объяснение недобора (пусто, если объяснять нечего).
type approvalView struct {
	Live              components.Live
	TenantID          string
	Actor             string
	Request           query.RequestView
	Notice            string
	CSRF              string
	Phase             string
	Execution         string
	Found             int
	Target            int
	Shortfall         int
	HasShortfall      bool
	Diagnosis         string
	Unavailable       bool
	CanDecide         bool
	Drafts            []query.EmailDraftView
	DraftsUnavailable bool
}

// handleApprovalQueue обслуживает GET /approvals: очередь заявок, ожидающих
// утверждения результата исполнения.
func (s *Server) handleApprovalQueue(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireSelectedTenant(w, r)
	if !ok {
		return
	}
	actor := actorOf(r)
	view := approvalQueueView{Live: consoleLive, TenantID: tenant, Actor: actor}
	if s.deps.ApprovalQueue == nil {
		view.Unavailable = true
	} else {
		items, err := s.deps.ApprovalQueue.Handle(r.Context(), query.ApprovalQueue{TenantID: domain.TenantID(tenant)})
		switch {
		case err == nil:
			view.Items = approvalQueueRows(items)
		case errors.Is(err, query.ErrApprovalRuntimeUnavailable):
			// Рантайм недоступен — это не пустая очередь: показываем исход честно.
			view.Unavailable = true
		default:
			s.renderPlainError(w, http.StatusInternalServerError, "очередь утверждения недоступна")
			return
		}
	}
	if s.deps.ListRequests == nil || s.deps.WorkflowStatus == nil {
		view.HistoryUnavailable = true
	} else {
		requests, err := s.deps.ListRequests.Handle(r.Context(), query.ListRequests{TenantID: domain.TenantID(tenant)})
		if err != nil {
			s.renderPlainError(w, http.StatusInternalServerError, "история исполнений недоступна")
			return
		}
		for _, request := range requests {
			if !isExecutionHistoryStatus(request.Status) {
				continue
			}
			status, err := s.deps.WorkflowStatus.WorkflowStatus(r.Context(), request.RequestID)
			switch {
			case errors.Is(err, ports.ErrWorkflowNotFound):
				// Терминальный доменный статус остаётся в операторской истории,
				// даже если Temporal уже не хранит соответствующий workflow.
				if !isTerminalHistoryStatus(request.Status) {
					continue
				}
				view.Executions = append(view.Executions, executionHistoryRow{
					RequestID: request.RequestID,
					Status:    request.Status,
				})
				continue
			case errors.Is(err, ports.ErrWorkflowUnavailable):
				view.HistoryUnavailable = true
				continue
			case err != nil:
				s.renderPlainError(w, http.StatusInternalServerError, "чтение состояния исполнения недоступно")
				return
			case status.Phase == query.ApprovalPhaseAwaitingApproval:
				// Такие исполнения уже показаны в основной очереди утверждения.
				continue
			}
			view.Executions = append(view.Executions, executionHistoryRow{
				RequestID: request.RequestID,
				Status:    request.Status,
				Phase:     status.Phase,
				Execution: executionStatusLabel(status.Execution),
				Found:     status.Found,
				Drafted:   status.Drafted,
			})
		}
	}
	s.render(w, http.StatusOK, pageApprovalQueue, components.Page{
		Layout:  components.Layout{Title: consoleMessages().Text(msgReqApprovalQueueTitle)},
		Sidebar: s.consoleNav(components.ConsoleSectionExecution, tenant, actor),
		View:    view,
	})
}

func isExecutionHistoryStatus(status domain.RequestStatus) bool {
	switch status {
	case domain.StatusProcessing, domain.StatusFulfilled, domain.StatusRejected, domain.StatusFailed, domain.StatusCancelled:
		return true
	default:
		return false
	}
}

func isTerminalHistoryStatus(status domain.RequestStatus) bool {
	switch status {
	case domain.StatusFulfilled, domain.StatusRejected, domain.StatusFailed, domain.StatusCancelled:
		return true
	default:
		return false
	}
}

func executionStatusLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "Выполняется"
	case "completed":
		return "Завершено"
	case "failed":
		return "Сбой"
	case "canceled", "cancelled":
		return "Отменено"
	default:
		if strings.TrimSpace(status) == "" {
			return "Неизвестно"
		}
		return status
	}
}

// handleApprovalScreen обслуживает GET /requests/{requestID}/approval: экран
// утверждения одной заявки.
func (s *Server) handleApprovalScreen(w http.ResponseWriter, r *http.Request) {
	s.renderApprovalScreen(w, r, http.StatusOK, activeTenant(r), actorOf(r), "")
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
	http.Redirect(w, r, approvalScreenURL(r.PathValue("requestID"), tenant), http.StatusSeeOther)
}

// renderApprovalScreen загружает заявку и состояние исполнения и рисует экран.
func (s *Server) renderApprovalScreen(w http.ResponseWriter, r *http.Request, status int, tenant, actor, notice string) {
	model, err := s.loadApproval(r, tenant, actor, notice)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	s.render(w, status, pageApprovalScreen, components.Page{
		Layout:  components.Layout{Title: consoleMessages().Text(msgReqApprovalTitlePrefix) + " " + model.Request.RequestID},
		Sidebar: s.consoleNav(components.ConsoleSectionExecution, tenant, actor),
		View:    model,
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
	model := approvalView{Live: consoleLive, TenantID: tenant, Actor: actor, Request: view, Notice: notice, CSRF: consoleauth.CSRF(r)}
	if s.deps.EmailDrafts == nil {
		model.DraftsUnavailable = true
	} else {
		drafts, err := s.deps.EmailDrafts.Handle(r.Context(), query.EmailDrafts{
			RequestID: domain.RequestID(view.RequestID),
			TenantID:  domain.TenantID(tenant),
		})
		if errors.Is(err, ports.ErrRequestNotFound) {
			return approvalView{}, err
		}
		if err != nil {
			// Ошибка чтения не равна пустому списку: сохраняем заявку на экране,
			// но явно сообщаем, что drafts store сейчас недоступно.
			model.DraftsUnavailable = true
		} else {
			model.Drafts = drafts
		}
	}
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
	model.Target, model.Shortfall, model.HasShortfall = shortfallState(status)
	model.Diagnosis = shortfallDiagnosis(status)
	model.CanDecide = view.Status == domain.StatusProcessing && status.Phase == query.ApprovalPhaseAwaitingApproval
	return model, nil
}

// shortfallState возвращает наблюдаемые «цель / недобор» и признак того, что
// недобор рассчитан. При незаданной цели (Shortfall == nil) недобор не
// рассчитывается и не показывается: HasShortfall равно false. Цель берётся из
// состояния, а при её отсутствии в старом исполнении выводится точно из
// наблюдаемого остатка (`найдено + недобор`), а не выдумывается.
func shortfallState(status ports.WorkflowStatus) (target int, shortfall int, hasShortfall bool) {
	if status.Shortfall == nil {
		return 0, 0, false
	}
	shortfall = *status.Shortfall
	target = status.Target
	if target <= 0 && shortfall > 0 {
		target = status.Found + shortfall
	}
	return target, shortfall, true
}

// shortfallDiagnosis формулирует наблюдаемое объяснение недобора. Диагноз
// строится только из того, что действительно наблюдается (цель достигнута,
// поиск не нашёл ни одного лида, поиск остановился с остатком). Данные задания
// поиска о тонкой причине остановки (цели обхода исчерпаны, ошибки разбора или
// оценки, отсев оценкой) этому контексту отдельным контрактом не публикуются,
// поэтому такая причина не выдумывается (AGENTS.md §3; design.md, Решение 5).
func shortfallDiagnosis(status ports.WorkflowStatus) string {
	if status.Shortfall == nil {
		return ""
	}
	if *status.Shortfall == 0 {
		return consoleMessages().Text(msgReqShortfallReached)
	}
	if status.Found == 0 {
		return consoleMessages().Text(msgReqShortfallNoLeads)
	}
	return consoleMessages().Text(msgReqShortfallPartial)
}

// approvalScreenURL собирает адрес экрана утверждения заявки. Тенант пока берётся
// из query (известный пробел до срезов 5/10: тенант должен следовать из сессии);
// актора в адресе нет — он берётся из сессии.
func approvalScreenURL(requestID, tenant string) string {
	values := url.Values{}
	values.Set("tenant_id", tenant)
	return "/requests/" + url.PathEscape(requestID) + "/approval?" + values.Encode()
}
