package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/frontend/components"
)

// companyRequestRow — строка экрана заявок компании: заявка, её история
// переходов и доступные действия. CanReturn/Cancel решает владелец экрана по
// статусной модели, а не шаблон.
type companyRequestRow struct {
	RequestID    string
	Status       domain.RequestStatus
	OfferContent string
	Transitions  []query.TransitionView
	CanReturn    bool
	CanCancel    bool
	CanRerun     bool
}

// companyRequestsView — модель экрана заявок компании: список заявок выбранного
// тенанта с историей переходов.
type companyRequestsView struct {
	Live     components.Live
	TenantID string
	Actor    string
	CSRF     string
	Notice   string
	Items    []companyRequestRow
	// StatusLabelsJSON — JSON-объект «код статуса → читаемая подпись» для
	// клиентского обновления строк без перезагрузки. Подписи берутся из каталога
	// сообщений, а не из скрипта.
	StatusLabelsJSON string
}

// handleCompanyRequests обслуживает GET /requests: список заявок компании по
// активному тенанту.
func (s *Server) handleCompanyRequests(w http.ResponseWriter, r *http.Request) {
	s.renderCompanyRequests(w, r, http.StatusOK, activeTenant(r), actorOf(r), "")
}

// handleCompanyReturnForRevision обслуживает POST .../revision: возврат заявки на
// доработку (validating | processing → needs-revision). Комментарий обязателен.
func (s *Server) handleCompanyReturnForRevision(w http.ResponseWriter, r *http.Request) {
	s.companyAction(w, r, func(req *http.Request) error {
		_, err := s.deps.ReturnForRevision.Handle(req.Context(), command.ReturnForRevision{
			LifecycleFields: lifecycleFromForm(req),
			Reason:          req.FormValue("reason"),
		})
		return err
	})
}

// handleCompanyCancel обслуживает POST .../cancel: отмена заявки
// (needs-revision | processing → cancelled). Комментарий обязателен.
func (s *Server) handleCompanyCancel(w http.ResponseWriter, r *http.Request) {
	s.companyAction(w, r, func(req *http.Request) error {
		_, err := s.deps.CancelRequest.Handle(req.Context(), command.CancelRequest{
			LifecycleFields: lifecycleFromForm(req),
			Reason:          req.FormValue("reason"),
		})
		if err != nil {
			return err
		}
		fields := lifecycleFromForm(req)
		return s.requestWorkflowCancellation(req.Context(), fields.TenantID.String(), fields.RequestID.String())
	})
}

// handleCompanyRerun обслуживает POST .../rerun: явный повторный запуск упавшей
// заявки (failed → validated) с последующим запуском исполнения. Идемпотентно:
// повтор по уже validated заявке — no-op в домене, повторный старт workflow —
// AlreadyStarted. Комментарий необязателен.
func (s *Server) handleCompanyRerun(w http.ResponseWriter, r *http.Request) {
	s.companyAction(w, r, func(req *http.Request) error {
		if s.deps.ExecutionProtocol == ports.ExecutionProtocolV2 {
			// Пока нет trusted runtime-history verifier-а, новый бизнес-rerun
			// невозможен. Не создаём CommandID и не выдаём его за принятую команду.
			return ports.ErrExecutionAdmissionBlocked
		}
		if _, err := s.deps.RerunRequest.Handle(req.Context(), command.RerunRequest{
			LifecycleFields: lifecycleFromForm(req),
			Reason:          req.FormValue("reason"),
		}); err != nil {
			return err
		}
		// Повторный запуск исполнения: workflow сам делает validated → processing,
		// поэтому цель по лидам не переуточняется (прежнее значение не сохранялось в
		// агрегате). Недоступность рантайма — честная деградация, как при одобрении.
		return s.startFulfillment(req, 0)
	})
}

// companyAction выполняет действие экрана заявок компании: разбирает форму,
// вызывает сценарий и либо переводит на список (POST/redirect/GET), либо
// показывает список с сообщением об ошибке.
func (s *Server) companyAction(w http.ResponseWriter, r *http.Request, action func(*http.Request) error) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	tenant := r.FormValue("tenant_id")
	actor := actorOf(r)
	if err := action(r); err != nil {
		if errors.Is(err, ErrWorkflowCancellationPending) {
			s.renderCompanyRequests(w, r, http.StatusAccepted, tenant, actor,
				"Заявка отменена; остановка исполнения ожидает восстановления Temporal. Повторите отмену позже.")
			return
		}
		s.renderCompanyRequests(w, r, statusForError(err), tenant, actor, err.Error())
		return
	}
	http.Redirect(w, r, companyRequestsURL(tenant), http.StatusSeeOther)
}

// renderCompanyRequests читает заявки компании и рисует список.
func (s *Server) renderCompanyRequests(w http.ResponseWriter, r *http.Request, status int, tenant, actor, notice string) {
	items, err := s.deps.ListRequests.Handle(r.Context(), query.ListRequests{TenantID: domain.TenantID(tenant)})
	if err != nil {
		s.renderPlainError(w, http.StatusInternalServerError, "список заявок недоступен")
		return
	}
	rows := make([]companyRequestRow, 0, len(items))
	for _, item := range items {
		view, err := s.deps.GetRequest.Handle(r.Context(), query.GetRequest{
			RequestID: domain.RequestID(item.RequestID),
			TenantID:  domain.TenantID(tenant),
		})
		if err != nil {
			s.renderPlainError(w, http.StatusInternalServerError, "заявка недоступна")
			return
		}
		rows = append(rows, companyRequestRow{
			RequestID:    view.RequestID,
			Status:       view.Status,
			OfferContent: view.Offer.Content,
			Transitions:  view.Transitions,
			CanReturn:    view.Status == domain.StatusValidating || view.Status == domain.StatusProcessing,
			CanCancel:    view.Status == domain.StatusNeedsRevision || view.Status == domain.StatusProcessing,
			CanRerun:     view.Status == domain.StatusFailed && s.deps.ExecutionProtocol != ports.ExecutionProtocolV2,
		})
	}
	s.render(w, status, pageCompanyRequests, components.Page{
		Layout:  components.Layout{Title: consoleMessages().Text(msgReqCompanyTitle)},
		Sidebar: s.consoleNav(components.ConsoleSectionRequests, tenant, actor),
		View: companyRequestsView{
			Live:             consoleLive,
			TenantID:         tenant,
			Actor:            actor,
			CSRF:             consoleauth.CSRF(r),
			Notice:           notice,
			Items:            rows,
			StatusLabelsJSON: statusLabelsJSON(),
		},
	})
}

// companyRequestsURL собирает адрес списка заявок компании. Тенант переносится в
// query, как и на остальных экранах заявок.
func companyRequestsURL(tenant string) string {
	values := url.Values{}
	values.Set("tenant_id", tenant)
	return "/company?" + values.Encode()
}

// companyStreamHeartbeat — интервал heartbeat-кадров потока: поддерживает
// соединение живым на прокси и позволяет клиенту заметить обрыв.
const companyStreamHeartbeat = 15 * time.Second

// companyStreamItem — строка потока статусов: идентификатор заявки и её текущий
// статус. Сырые события клиенту не транслируются — только актуальный список.
type companyStreamItem struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

// handleCompanyStream обслуживает GET /company/stream: поток серверных событий
// (SSE) со статусами заявок тенанта. Подписчик получает текущий список сразу и
// свежий при каждом изменении — без перезагрузки экрана. Доступ требует сессию
// оператора (как у /company); без неё запрос редиректится на вход.
func (s *Server) handleCompanyStream(w http.ResponseWriter, r *http.Request) {
	tenant := activeTenant(r)
	if s.deps.RequestWatcher == nil {
		http.Error(w, "поток обновлений недоступен", http.StatusServiceUnavailable)
		return
	}

	// Заголовки потока: без кэша и без буферизации обратным прокси.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(w)
	send := func(event string, payload any) bool {
		data, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	notify, cancel := s.deps.RequestWatcher.Watch(domain.TenantID(tenant))
	defer cancel()

	// Начальный кадр: подписчик сразу видит текущие статусы.
	if !send("statuses", s.companyStreamPayload(r, tenant)) {
		return
	}

	ticker := time.NewTicker(companyStreamHeartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-notify:
			if !send("statuses", s.companyStreamPayload(r, tenant)) {
				return
			}
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		}
	}
}

// companyStreamPayload собирает список заявок тенанта для кадра потока. Ошибка
// чтения даёт пустой список: поток не завершает подписчика из-за разового сбоя
// хранилища, а дожидается следующего уведомления или heartbeat.
func (s *Server) companyStreamPayload(r *http.Request, tenant string) []companyStreamItem {
	items, err := s.deps.ListRequests.Handle(r.Context(), query.ListRequests{TenantID: domain.TenantID(tenant)})
	if err != nil {
		return nil
	}
	out := make([]companyStreamItem, 0, len(items))
	for _, item := range items {
		out = append(out, companyStreamItem{RequestID: item.RequestID, Status: string(item.Status)})
	}
	return out
}

// statusLabelsJSON возвращает JSON-объект «код статуса → читаемая подпись» для
// клиентского обновления строк без перезагрузки. Подписи берутся из каталога
// сообщений — единственного источника пользовательского текста.
func statusLabelsJSON() string {
	labels := make(map[string]string, len(domain.RequestStatuses()))
	for _, status := range domain.RequestStatuses() {
		labels[string(status)] = consoleMessages().Label("request.status", string(status))
	}
	data, err := json.Marshal(labels)
	if err != nil {
		return "{}"
	}
	return string(data)
}
