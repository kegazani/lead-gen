package httpapi

import (
	"bytes"
	"embed"
	"net/http"
	"net/url"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/frontend/components"
)

// templateFS — шаблоны экрана валидации, встроенные в бинарник: разметка экрана
// не может разойтись с кодом обработчиков.
//
//go:embed templates/*.html
var templateFS embed.FS

// Имена страниц и фрагментов набора шаблонов.
const (
	pageValidationQueue  = "templates/validation_queue.html"
	pageValidationScreen = "templates/validation_screen.html"
	pageApprovalQueue    = "templates/approval_queue.html"
	pageApprovalScreen   = "templates/approval_screen.html"
	fragmentOffer        = "offer-fragment"
	fragmentApproval     = "approval-status"
)

// defaultActor — актор перехода экрана, если он не передан (dev-контур без auth).
const defaultActor = "operator"

// newPages собирает набор шаблонов экрана валидации: каркас и общие фрагменты из
// frontend/components плюс страницы экрана. Разбор идёт при сборке сервиса.
func newPages() (*components.Set, error) {
	return components.New(templateFS, pageValidationQueue, pageValidationScreen, pageApprovalQueue, pageApprovalScreen)
}

// queueView — модель очереди валидации: заявки submitted текущего тенанта.
type queueView struct {
	TenantID string
	Actor    string
	Items    []query.ValidationItem
}

// screenView — модель экрана валидации одной заявки. Поля форм собираются здесь, а
// не в шаблоне: правила показа решает владелец экрана.
type screenView struct {
	TenantID     string
	Actor        string
	Request      query.RequestView
	Notice       string
	CanTake      bool
	CanEditOffer bool
	CanDecide    bool
	OfferField   components.Field
	TargetField  components.Field
	ReasonField  components.Field
}

// handleValidationQueue обслуживает GET /validation: очередь заявок на валидацию.
func (s *Server) handleValidationQueue(w http.ResponseWriter, r *http.Request) {
	tenant := r.URL.Query().Get("tenant_id")
	actor := actorOf(r)
	items, err := s.deps.ValidationQueue.Handle(r.Context(), query.ValidationQueue{TenantID: domain.TenantID(tenant)})
	if err != nil {
		s.renderPlainError(w, http.StatusInternalServerError, "очередь валидации недоступна")
		return
	}
	s.render(w, http.StatusOK, pageValidationQueue, components.Page{
		Layout: components.Layout{Title: "Очередь валидации"},
		View:   queueView{TenantID: tenant, Actor: actor, Items: items},
	})
}

// handleValidationScreen обслуживает GET /requests/{requestID}/validation: экран
// валидации одной заявки.
func (s *Server) handleValidationScreen(w http.ResponseWriter, r *http.Request) {
	s.renderScreen(w, r, http.StatusOK, r.URL.Query().Get("tenant_id"), actorOf(r), "")
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
		s.startFulfillment(req)
		return nil
	})
}

// startFulfillment запускает durable-исполнение заявки после одобрения. Старт —
// деградация: заявка уже validated, и ручные переходы продолжают работать, даже
// если рантайм workflow недоступен (nil-порт или ErrWorkflowUnavailable), поэтому
// ошибка старта не срывает одобрение.
func (s *Server) startFulfillment(req *http.Request) {
	if s.deps.WorkflowStarter == nil {
		return
	}
	lifecycle := lifecycleFromForm(req)
	_, _ = s.deps.WorkflowStarter.StartFulfillment(req.Context(), ports.StartFulfillment{
		RequestID:     string(lifecycle.RequestID),
		TenantID:      string(lifecycle.TenantID),
		CorrelationID: string(lifecycle.CorrelationID),
		LeadTarget:    0,
	})
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
	http.Redirect(w, r, validationScreenURL(r.PathValue("requestID"), tenant, actor), http.StatusSeeOther)
}

// renderScreen загружает заявку и рисует экран валидации.
func (s *Server) renderScreen(w http.ResponseWriter, r *http.Request, status int, tenant, actor, notice string) {
	model, err := s.loadScreen(r, tenant, actor, notice)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	s.render(w, status, pageValidationScreen, components.Page{
		Layout: components.Layout{Title: "Валидация заявки " + model.Request.RequestID},
		View:   model,
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
	return screenModel(tenant, actor, notice, view), nil
}

// screenModel собирает модель экрана: какие действия доступны и какие поля показать.
func screenModel(tenant, actor, notice string, view query.RequestView) screenView {
	editable := view.Status == domain.StatusSubmitted || view.Status == domain.StatusValidating
	return screenView{
		TenantID:     tenant,
		Actor:        actor,
		Request:      view,
		Notice:       notice,
		CanTake:      view.Status == domain.StatusSubmitted,
		CanEditOffer: editable,
		CanDecide:    view.Status == domain.StatusValidating,
		OfferField: components.Field{
			ID: "offer-content", Name: "content", Label: "Оффер",
			Type: "textarea", Rows: 6, Value: view.Offer.Content, Required: true,
		},
		TargetField: components.Field{
			ID: "offer-target", Name: "target", Label: "Цель заявки", Value: view.Offer.Target,
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

// actorOf возвращает актора из формы или запроса, подставляя значение по
// умолчанию. Контур без аутентификации: настоящие роли — platform-governance.
func actorOf(r *http.Request) string {
	for _, value := range []string{r.FormValue("actor"), r.URL.Query().Get("actor")} {
		if value != "" {
			return value
		}
	}
	return defaultActor
}

// validationScreenURL собирает адрес экрана валидации заявки.
func validationScreenURL(requestID, tenant, actor string) string {
	values := url.Values{}
	values.Set("tenant_id", tenant)
	if actor != "" {
		values.Set("actor", actor)
	}
	return "/requests/" + url.PathEscape(requestID) + "/validation?" + values.Encode()
}
