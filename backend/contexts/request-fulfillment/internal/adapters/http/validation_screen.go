package httpapi

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/frontend/components"
)

// templateFS — шаблоны экрана валидации, встроенные в бинарник: разметка экрана
// не может разойтись с кодом обработчиков.
//
//go:embed templates/*.html
var templateFS embed.FS

// Имена страниц и фрагментов набора шаблонов.
const (
	pageLogin            = "templates/login.html"
	pageValidationQueue  = "templates/validation_queue.html"
	pageValidationScreen = "templates/validation_screen.html"
	pageApprovalQueue    = "templates/approval_queue.html"
	pageApprovalScreen   = "templates/approval_screen.html"
	pageCompanyRequests  = "templates/company_requests.html"
	fragmentOffer        = "offer-fragment"
	fragmentApproval     = "approval-status"
)

// newPages собирает набор шаблонов экрана валидации: каркас и общие фрагменты из
// frontend/components плюс страницы экрана. Разбор идёт при сборке сервиса.
func newPages() (*components.Set, error) {
	return components.NewWithMessages(consoleMessages(), templateFS, pageLogin, pageValidationQueue, pageValidationScreen, pageApprovalQueue, pageApprovalScreen, pageCompanyRequests)
}

// queueView — модель списка заявок текущего тенанта на всех стадиях.
type queueView struct {
	Live     components.Live
	TenantID string
	Actor    string
	Items    []requestQueueRow
}

// requestQueueRow сохраняет заявку в списке, даже если её workflow пока
// недоступен. Счётчики показываются только при полученном состоянии исполнения.
type requestQueueRow struct {
	query.ValidationItem
	HasExecution         bool
	ExecutionKnown       bool
	ExecutionUnavailable bool
	Phase                string
	Execution            string
	Found                int
	Target               int
	Drafted              int
}

// screenView — модель экрана валидации одной заявки. Поля форм собираются здесь, а
// не в шаблоне: правила показа решает владелец экрана.
type screenView struct {
	Live                components.Live
	TenantID            string
	Actor               string
	Request             query.RequestView
	Notice              string
	CSRF                string
	CommandID           string
	ExecutionProtocolV2 bool
	CanTake             bool
	CanEditOffer        bool
	CanDecide           bool
	HasExecution        bool
	OfferField          components.Field
	TargetField         components.Field
	ReasonField         components.Field
	// LeadTargetField — необязательная числовая цель по числу лидов, задаваемая
	// при одобрении и пробрасываемая в запуск исполнения заявки.
	LeadTargetField components.Field
}

// handleValidationQueue обслуживает GET /validation: заявки и ход их исполнения.
func (s *Server) handleValidationQueue(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.requireSelectedTenant(w, r)
	if !ok {
		return
	}
	actor := actorOf(r)
	items, err := s.deps.ValidationQueue.Handle(r.Context(), query.ValidationQueue{TenantID: domain.TenantID(tenant)})
	if err != nil {
		s.renderPlainError(w, http.StatusInternalServerError, "очередь валидации недоступна")
		return
	}
	rows := make([]requestQueueRow, 0, len(items))
	for _, item := range items {
		row := requestQueueRow{ValidationItem: item}
		switch item.Status {
		case domain.StatusValidated, domain.StatusProcessing, domain.StatusFulfilled, domain.StatusFailed:
			row.HasExecution = true
		}
		if row.HasExecution {
			if s.deps.WorkflowStatus == nil {
				row.ExecutionUnavailable = true
			} else {
				state, stateErr := s.deps.WorkflowStatus.WorkflowStatus(r.Context(), item.RequestID)
				switch {
				case stateErr == nil:
					row.ExecutionKnown = true
					row.Phase, row.Execution = state.Phase, executionStatusLabel(state.Execution)
					row.Found, row.Drafted = state.Found, state.Drafted
					row.Target = state.Target
				case errors.Is(stateErr, ports.ErrWorkflowNotFound):
					// Отсутствие состояния не удаляет уже одобренную заявку.
				default:
					row.ExecutionUnavailable = true
				}
			}
		}
		rows = append(rows, row)
	}
	s.render(w, http.StatusOK, pageValidationQueue, components.Page{
		Layout: components.Layout{
			Title:   consoleMessages().Text(msgReqValidationQueueTitle),
			Scripts: append(append([]string{}, components.ConsoleScripts...), "/static/request_progress.js"),
		},
		Sidebar: s.consoleNav(components.ConsoleSectionRequests, tenant, actor),
		View:    queueView{Live: consoleLive, TenantID: tenant, Actor: actor, Items: rows},
	})
}

// handleValidationScreen обслуживает GET /requests/{requestID}/validation: экран
// валидации одной заявки.
func (s *Server) handleValidationScreen(w http.ResponseWriter, r *http.Request) {
	s.renderScreen(w, r, http.StatusOK, activeTenant(r), actorOf(r), "")
}

// handleValidationTake обслуживает POST .../validation/take: submitted → validating.
func (s *Server) handleValidationTake(w http.ResponseWriter, r *http.Request) {
	s.validationAction(w, r, func(req *http.Request) error {
		_, err := s.deps.StartValidation.Handle(req.Context(), command.StartValidation{
			LifecycleFields: lifecycleFromForm(req),
		})
		return err
	})
}

// handleValidationOffer обслуживает POST .../validation/offer: правка оффера на
// месте до валидации.
func (s *Server) handleValidationOffer(w http.ResponseWriter, r *http.Request) {
	s.validationAction(w, r, func(req *http.Request) error {
		_, err := s.deps.ReviseOffer.Handle(req.Context(), command.ReviseOffer{
			RequestID: domain.RequestID(req.PathValue("requestID")),
			TenantID:  domain.TenantID(req.FormValue("tenant_id")),
			OfferID:   domain.OfferID(req.FormValue("offer_id")),
			Content:   req.FormValue("content"),
			Target:    req.FormValue("target"),
			Actor:     domain.ActorID(actorOf(req)),
		})
		return err
	})
}

// handleValidationApprove обслуживает POST .../validation/approve: validating →
// validated. После успешной валидации формируется эмбеддинг оффера (вход recall),
// затем запускается durable-исполнение заявки. Сбой эмбеддинга виден на экране и
// не выдаётся за успех, но валидация уже применена; недоступность рантайма
// workflow — честная деградация (см. startFulfillment).
func (s *Server) handleValidationApprove(w http.ResponseWriter, r *http.Request) {
	s.validationAction(w, r, func(req *http.Request) error {
		// Цель разбирается до применения одобрения: некорректное значение не
		// должно оставить заявку validated без запущенного исполнения.
		leadTarget, err := parseLeadTarget(req.FormValue("lead_target"))
		if err != nil {
			return err
		}
		if s.deps.ExecutionProtocol == ports.ExecutionProtocolV2 {
			if !s.executionAdmissionReady() {
				return ports.ErrWorkflowUnavailable
			}
			commandID := req.FormValue("command_id")
			if strings.TrimSpace(commandID) == "" {
				return domain.ErrEmptyID
			}
			var target *int
			if strings.TrimSpace(req.FormValue("lead_target")) != "" {
				target = &leadTarget
			}
			confidence, err := parseConfidence(req.FormValue("confidence"))
			if err != nil {
				return err
			}
			_, err = s.deps.AdmitExecution.Handle(req.Context(), command.AdmitExecution{
				LifecycleFields: lifecycleFromForm(req),
				CommandID:       domain.CommandID(commandID), Kind: command.AdmissionApprove, LeadTarget: target,
				Reason: req.FormValue("reason"), RequestVersion: formInt(req, "request_version"), OfferRevision: formInt(req, "offer_revision"),
				Confidence: confidence, FormParameters: json.RawMessage(req.FormValue("form_parameters")),
			})
			return err
		}
		if _, err := s.deps.ValidateRequest.Handle(req.Context(), command.ValidateRequest{
			LifecycleFields: lifecycleFromForm(req),
		}); err != nil {
			return err
		}
		if s.deps.EmbedOffer != nil {
			if _, err := s.deps.EmbedOffer.Handle(req.Context(), command.EmbedOffer{
				RequestID: domain.RequestID(req.PathValue("requestID")),
				TenantID:  domain.TenantID(req.FormValue("tenant_id")),
			}); err != nil {
				return err
			}
		}
		return s.startFulfillment(req, leadTarget)
	})
}

// errInvalidLeadTarget — цель по числу лидов задана некорректно (отрицательное
// или нецелое значение). Отображается пользователю как 400, а не 500.
var errInvalidLeadTarget = errors.New("цель по числу лидов должна быть целым неотрицательным числом")

// parseLeadTarget разбирает необязательную цель по числу лидов из формы. Пустое
// значение означает «цель не задана» (0) — прежнее поведение. Отрицательное или
// нецелое значение отвергается: исполнение не должно получить выдуманную цель.
func parseLeadTarget(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	target, err := strconv.Atoi(raw)
	if err != nil || target < 0 {
		return 0, errInvalidLeadTarget
	}
	return target, nil
}

// validateLeadTarget проверяет цель запуска, пришедшую из API: отрицательное
// значение отвергается так же, как на экране одобрения, и не доходит до workflow.
func validateLeadTarget(target int) error {
	if target < 0 {
		return errInvalidLeadTarget
	}
	return nil
}

// startFulfillment запускает durable-исполнение заявки после одобрения. Старт —
// деградация: заявка уже validated, и ручные переходы продолжают работать, даже
// если рантайм workflow недоступен (nil-порт или ErrWorkflowUnavailable), поэтому
// ошибка старта не срывает одобрение. Числовая цель — параметр запуска: 0 означает
// «цель не задана» и сохраняет прежнее поведение (недобор не рассчитывается).
func (s *Server) startFulfillment(req *http.Request, leadTarget int) error {
	if s.deps.WorkflowStarter == nil {
		return ports.ErrWorkflowUnavailable
	}
	lifecycle := lifecycleFromForm(req)
	requestView, err := s.deps.GetRequest.Handle(req.Context(), query.GetRequest{
		RequestID: lifecycle.RequestID, TenantID: lifecycle.TenantID,
	})
	if err != nil {
		return err
	}
	_, err = s.deps.WorkflowStarter.StartFulfillment(req.Context(), ports.StartFulfillment{
		RequestID:        string(lifecycle.RequestID),
		TenantID:         string(lifecycle.TenantID),
		CorrelationID:    string(lifecycle.CorrelationID),
		LeadTarget:       leadTarget,
		ExecutionPurpose: requestView.ExecutionPurpose,
	})
	return err
}

// handleValidationReject обслуживает POST .../validation/reject: validating → rejected.
func (s *Server) handleValidationReject(w http.ResponseWriter, r *http.Request) {
	s.validationAction(w, r, func(req *http.Request) error {
		_, err := s.deps.RejectRequest.Handle(req.Context(), command.RejectRequest{
			LifecycleFields: lifecycleFromForm(req),
			Reason:          req.FormValue("reason"),
		})
		return err
	})
}

// handleValidationCall обслуживает POST .../validation/call: звонок-уточнение.
func (s *Server) handleValidationCall(w http.ResponseWriter, r *http.Request) {
	s.validationAction(w, r, func(req *http.Request) error {
		_, err := s.deps.InitiateCall.Handle(req.Context(), command.InitiateCall{
			RequestID:     domain.RequestID(req.PathValue("requestID")),
			TenantID:      domain.TenantID(req.FormValue("tenant_id")),
			CorrelationID: domain.CorrelationID(req.FormValue("correlation_id")),
		})
		return err
	})
}

// validationAction выполняет действие формы экрана: разбирает форму, вызывает
// сценарий и либо переводит на экран заявки (POST/redirect/GET), либо показывает
// экран с сообщением об ошибке. Сбои не прячутся: исходный статус и причина видны.
func (s *Server) validationAction(w http.ResponseWriter, r *http.Request, action func(*http.Request) error) {
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "разбор формы: "+err.Error())
		return
	}
	tenant := r.FormValue("tenant_id")
	actor := actorOf(r)
	if err := action(r); err != nil {
		s.renderScreen(w, r, statusForError(err), tenant, actor, err.Error())
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		model, err := s.loadScreen(r, tenant, actor, "")
		if err != nil {
			writeDomainError(w, err)
			return
		}
		s.renderFragment(w, http.StatusOK, fragmentOffer, model)
		return
	}
	http.Redirect(w, r, validationScreenURL(r.PathValue("requestID"), tenant), http.StatusSeeOther)
}

// renderScreen загружает заявку и рисует экран валидации.
func (s *Server) renderScreen(w http.ResponseWriter, r *http.Request, status int, tenant, actor, notice string) {
	model, err := s.loadScreen(r, tenant, actor, notice)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	s.render(w, status, pageValidationScreen, components.Page{
		Layout:  components.Layout{Title: consoleMessages().Text(msgReqValidationTitlePrefix) + " " + model.Request.RequestID},
		Sidebar: s.consoleNav(components.ConsoleSectionRequests, tenant, actor),
		View:    model,
	})
}

// loadScreen читает заявку и собирает модель экрана.
func (s *Server) loadScreen(r *http.Request, tenant, actor, notice string) (screenView, error) {
	view, err := s.deps.GetRequest.Handle(r.Context(), query.GetRequest{
		RequestID: domain.RequestID(r.PathValue("requestID")),
		TenantID:  domain.TenantID(tenant),
	})
	if err != nil {
		return screenView{}, err
	}
	model := screenModel(tenant, actor, notice, view)
	// CSRF-токен живёт в сессии: подставляем его в формы экрана, чтобы браузер
	// отправлял его на изменяющих запросах.
	model.CSRF = consoleauth.CSRF(r)
	model.CommandID = strings.TrimSpace(r.FormValue("command_id"))
	if model.CommandID == "" {
		model.CommandID = newHTTPCommandID()
	}
	model.ExecutionProtocolV2 = s.deps.ExecutionProtocol == ports.ExecutionProtocolV2
	return model, nil
}

func formInt(r *http.Request, key string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(r.FormValue(key)))
	return value
}

func parseConfidence(raw string) (*float64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return nil, errors.New("confidence должен быть числом от 0 до 1")
	}
	return &value, nil
}

// screenModel собирает модель экрана: какие действия доступны и какие поля показать.
func screenModel(tenant, actor, notice string, view query.RequestView) screenView {
	editable := view.Status == domain.StatusSubmitted || view.Status == domain.StatusValidating
	return screenView{
		Live:         consoleLive,
		TenantID:     tenant,
		Actor:        actor,
		Request:      view,
		Notice:       notice,
		CanTake:      view.Status == domain.StatusSubmitted,
		CanEditOffer: editable,
		CanDecide:    view.Status == domain.StatusValidating,
		HasExecution: view.Status == domain.StatusValidated || view.Status == domain.StatusProcessing || view.Status == domain.StatusFulfilled || view.Status == domain.StatusFailed,
		OfferField: components.Field{
			ID: "offer-content", Name: "content", Label: "Оффер",
			Type: "textarea", Rows: 6, Value: view.Offer.Content, Required: true,
		},
		TargetField: components.Field{
			ID: "offer-target", Name: "target", Label: "Цель заявки", Value: view.Offer.Target,
		},
		LeadTargetField: components.Field{
			ID: "lead-target", Name: "lead_target", Label: "Цель по числу лидов",
		},
		ReasonField: components.Field{
			ID: "reject-reason", Name: "reason", Label: "Причина отклонения", Required: true,
		},
	}
}

// render рисует страницу в буфер: частично отданный HTML выглядел бы сломанной
// страницей вместо честного отказа.
func (s *Server) render(w http.ResponseWriter, status int, page string, model components.Page) {
	if s.pages == nil {
		s.renderPlainError(w, http.StatusInternalServerError, "шаблоны экрана не собраны")
		return
	}
	// Экран без собственных таблиц стилей получает общее оформление компонентов:
	// внешний вид экранов заявок не расходится с другими серверными экранами.
	if len(model.Layout.Stylesheets) == 0 {
		model.Layout.Stylesheets = components.ConsoleStylesheets
	}
	if len(model.Layout.BootstrapScripts) == 0 {
		model.Layout.BootstrapScripts = components.ConsoleBootstrapScripts
	}
	// Общие скрипты консоли возвращают фокус при подмене фрагмента и объявляют
	// изменения через область aria-live (7.3).
	if len(model.Layout.Scripts) == 0 {
		model.Layout.Scripts = components.ConsoleScripts
	}
	var buf bytes.Buffer
	if err := s.pages.Render(&buf, page, model); err != nil {
		s.renderPlainError(w, http.StatusInternalServerError, "ошибка рендеринга экрана")
		return
	}
	w.Header().Set("Content-Type", contentTypeHTML)
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// renderFragment рисует гипермедиа-фрагмент экрана валидации: HTMX подставит его
// в открытую страницу.
func (s *Server) renderFragment(w http.ResponseWriter, status int, name string, model screenView) {
	s.renderPageFragment(w, status, pageValidationScreen, name, model)
}

// renderPageFragment рисует именованный фрагмент набора конкретной страницы:
// HTMX подставит его в уже открытую страницу. Общий приём для экранов среза.
func (s *Server) renderPageFragment(w http.ResponseWriter, status int, page, name string, data any) {
	if s.pages == nil {
		s.renderPlainError(w, http.StatusInternalServerError, "шаблоны экрана не собраны")
		return
	}
	var buf bytes.Buffer
	if err := s.pages.RenderFragment(&buf, page, name, data); err != nil {
		s.renderPlainError(w, http.StatusInternalServerError, "ошибка рендеринга фрагмента")
		return
	}
	w.Header().Set("Content-Type", contentTypeHTML)
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// renderPlainError отдаёт текстовую ошибку для случаев, когда HTML-экран
// недоступен (шаблоны не собраны и т.п.).
func (s *Server) renderPlainError(w http.ResponseWriter, status int, message string) {
	http.Error(w, message, status)
}

// lifecycleFromForm собирает поля перехода из формы экрана.
func lifecycleFromForm(r *http.Request) command.LifecycleFields {
	return command.LifecycleFields{
		RequestID:     domain.RequestID(r.PathValue("requestID")),
		TenantID:      domain.TenantID(r.FormValue("tenant_id")),
		Actor:         domain.ActorID(actorOf(r)),
		CorrelationID: domain.CorrelationID(r.FormValue("correlation_id")),
	}
}

// actorOf возвращает логин оператора из сессии. Консольные маршруты обёрнуты
// middleware, поэтому сессия гарантированно есть; пустой актор без сессии честнее
// выдуманного значения по умолчанию. Настоящие роли — platform-governance.
func actorOf(r *http.Request) string {
	if sess := consoleauth.Current(r.Context()); sess != nil {
		return sess.Username()
	}
	return ""
}

// validationScreenURL собирает адрес экрана валидации заявки. Тенант пока берётся
// из query (известный пробел до срезов 5/10: тенант должен следовать из сессии);
// актора в адресе нет — он берётся из сессии.
func validationScreenURL(requestID, tenant string) string {
	values := url.Values{}
	values.Set("tenant_id", tenant)
	return "/requests/" + url.PathEscape(requestID) + "/validation?" + values.Encode()
}
