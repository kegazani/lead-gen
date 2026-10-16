package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/researchbridge"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/contracts/agent-orchestration/researchmission"
	"lead-gen/contracts/agent-orchestration/researchplan"
	"lead-gen/contracts/lead-management/researchmemory"
	"lead-gen/frontend/components"
)

const pageResearch = "templates/research.html"
const pageResearchDossier = "templates/research_dossier.html"

type researchCountry struct {
	Code, Name, Languages string
	Selected              bool
}
type researchView struct {
	EmailReadyAvailable bool
	IdentityAvailable   bool
	Quantity            int
	Unit                string
	researchbridge.View
	Live                                  components.Live
	TenantID, Actor, CSRF, Filter, Notice string
	Countries                             []researchCountry
	CanStart, CanApprove                  bool
	Shortfall                             int
}
type researchDossierView struct {
	researchView
	Dossier         researchmemory.Dossier
	PredicateLabels map[string]string
}

func (s *Server) registerResearchRoutes(mux *http.ServeMux) {
	mux.Handle("GET /requests/{requestID}/research", components.NoStore(s.consoleHandler(s.handleResearch)))
	mux.Handle("GET /requests/{requestID}/research/progress", components.NoStore(s.consoleAPIMutation(http.HandlerFunc(s.handleResearchProgress))))
	mux.Handle("POST /requests/{requestID}/research/start", s.consoleHandler(s.handleResearchStart))
	mux.Handle("POST /requests/{requestID}/research/control", s.consoleHandler(s.handleResearchControl))
	mux.Handle("GET /requests/{requestID}/research/candidates/{entityID}", components.NoStore(s.consoleHandler(s.handleResearchDossier)))
	s.registerResearchIdentityRoutes(mux)
	mux.Handle("GET /static/research_progress.js", requestScript("static/research_progress.js"))
}
func researchInput(r *http.Request) researchbridge.Input {
	return researchbridge.Input{TenantID: activeTenant(r), RequestID: r.PathValue("requestID"), Actor: actorOf(r)}
}
func researchURL(requestID, tenant string) string {
	return "/requests/" + url.PathEscape(requestID) + "/research?" + url.Values{"tenant_id": {tenant}}.Encode()
}
func (s *Server) researchModel(r *http.Request) (researchView, error) {
	if s.deps.Research == nil {
		return researchView{}, researchbridge.ErrUnavailable
	}
	filter := r.URL.Query().Get("filter")
	switch filter {
	case "", "all", "accepted", "contact", "email":
	default:
		return researchView{}, researchbridge.ErrInvalid
	}
	data, err := s.deps.Research.Load(r.Context(), researchInput(r), filter)
	if err != nil {
		return researchView{}, err
	}
	view := researchView{View: data, Live: consoleLive, TenantID: data.Request.TenantID, Actor: actorOf(r), CSRF: consoleauth.CSRF(r), Filter: filter, EmailReadyAvailable: s.deps.Research.EmailReadyAvailable()}
	view.CanApprove = data.Snapshot == nil && data.Request.Status == domain.StatusValidating
	view.CanStart = data.Snapshot == nil && data.Request.ExecutionMode == domain.ExecutionModeResearch && (data.Request.Status == domain.StatusValidated || data.Request.Status == domain.StatusProcessing)
	for _, code := range s.deps.Research.Countries() {
		country, _ := researchplan.LookupCountry(code)
		view.Countries = append(view.Countries, researchCountry{Code: code, Name: country.Name, Languages: strings.Join(country.DefaultLanguages, ", "), Selected: code == "RU"})
	}
	if selection := data.Request.ResearchSelection; selection != nil {
		view.Quantity, view.Unit = selection.Quantity, selection.Unit
		for i := range view.Countries {
			view.Countries[i].Selected = false
			for _, market := range selection.Markets {
				if market.Country == view.Countries[i].Code {
					view.Countries[i].Selected = true
					view.Countries[i].Languages = strings.Join(market.Languages, ", ")
				}
			}
		}
	}
	if data.Snapshot == nil && data.Request.ExecutionMode == domain.ExecutionModeLegacy {
		view.Notice = "Эта заявка закреплена за прежним исполнением. Для нового исследования создайте новую заявку."
	}
	if data.Snapshot != nil {
		view.Shortfall = data.Snapshot.Goal.Quantity - data.Snapshot.Counters.Accepted
		if view.Shortfall < 0 {
			view.Shortfall = 0
		}
	}
	return view, nil
}
func (s *Server) handleResearch(w http.ResponseWriter, r *http.Request) {
	view, err := s.researchModel(r)
	if err != nil {
		s.researchError(w, err)
		return
	}
	s.renderResearch(w, r, http.StatusOK, view)
}
func (s *Server) renderResearch(w http.ResponseWriter, r *http.Request, status int, view researchView) {
	s.render(w, status, pageResearch, components.Page{Layout: components.Layout{Title: consoleMessages().Text("research.title"), Scripts: append(append([]string{}, components.ConsoleScripts...), "/static/research_progress.js")}, Sidebar: s.consoleNav(components.ConsoleSectionRequests, view.TenantID, view.Actor), View: view})
}
func (s *Server) handleResearchProgress(w http.ResponseWriter, r *http.Request) {
	view, err := s.researchModel(r)
	if err != nil {
		s.researchError(w, err)
		return
	}
	etag := "\"research-empty\""
	if view.Snapshot != nil {
		etag = fmt.Sprintf("\"research-%s-%d-%s\"", view.Snapshot.MissionID, view.Snapshot.Revision, view.Filter)
	}
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	s.renderPageFragment(w, http.StatusOK, pageResearch, "research-snapshot", view)
}
func (s *Server) handleResearchStart(w http.ResponseWriter, r *http.Request) {
	s.startResearch(w, r, false)
}
func (s *Server) handleResearchApprove(w http.ResponseWriter, r *http.Request) {
	s.startResearch(w, r, true)
}
func (s *Server) startResearch(w http.ResponseWriter, r *http.Request, approve bool) {
	if s.deps.Research == nil {
		s.researchError(w, researchbridge.ErrUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		s.researchError(w, researchbridge.ErrInvalid)
		return
	}
	quantity, err := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("research_quantity")))
	if err != nil {
		s.researchError(w, fmt.Errorf("%w: количество обязательно", researchbridge.ErrInvalid))
		return
	}
	languages := map[string][]string{}
	for _, country := range r.PostForm["research_country"] {
		languages[country] = strings.FieldsFunc(r.PostForm.Get("research_language_"+country), func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
	}
	_, err = s.deps.Research.Start(r.Context(), researchbridge.StartInput{Input: researchInput(r), Quantity: quantity, Unit: r.PostForm.Get("research_unit"), Countries: r.PostForm["research_country"], Languages: languages, Approve: approve})
	if err != nil {
		// Одобрение могло сохраниться до отказа планировщика. Показываем текущее
		// состояние и возможность повтора, не запускаем прежний workflow.
		view, loadErr := s.researchModel(r)
		if loadErr == nil {
			view.Notice = researchErrorText(err)
			s.renderResearch(w, r, researchStatus(err), view)
			return
		}
		s.researchError(w, err)
		return
	}
	http.Redirect(w, r, researchURL(r.PathValue("requestID"), activeTenant(r)), http.StatusSeeOther)
}
func (s *Server) handleResearchControl(w http.ResponseWriter, r *http.Request) {
	if s.deps.Research == nil {
		s.researchError(w, researchbridge.ErrUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		s.researchError(w, researchbridge.ErrInvalid)
		return
	}
	revision, err := strconv.ParseInt(r.PostForm.Get("revision"), 10, 64)
	if err != nil || revision < 1 {
		s.researchError(w, researchbridge.ErrInvalid)
		return
	}
	action := r.PostForm.Get("command")
	if action != "pause" && action != "resume" && action != "cancel" {
		s.researchError(w, researchbridge.ErrInvalid)
		return
	}
	if _, err := s.deps.Research.Control(r.Context(), researchInput(r), revision, action); err != nil {
		s.researchError(w, err)
		return
	}
	http.Redirect(w, r, researchURL(r.PathValue("requestID"), activeTenant(r)), http.StatusSeeOther)
}
func (s *Server) handleResearchDossier(w http.ResponseWriter, r *http.Request) {
	if s.deps.Research == nil {
		s.researchError(w, researchbridge.ErrUnavailable)
		return
	}
	data, dossier, err := s.deps.Research.Dossier(r.Context(), researchInput(r), r.PathValue("entityID"))
	if err != nil {
		s.researchError(w, err)
		return
	}
	view := researchDossierView{researchView: researchView{View: data, Live: consoleLive, TenantID: data.Request.TenantID, Actor: actorOf(r), IdentityAvailable: s.deps.Research.IdentityAvailable()}, Dossier: dossier}
	view.PredicateLabels = map[string]string{}
	for _, assertion := range dossier.Assertions {
		value := consoleMessages().Label("research.predicate", assertion.Predicate)
		if strings.HasPrefix(assertion.Predicate, "criteria-evidence:") {
			value = consoleMessages().Text("research.criteria_evidence")
		}
		view.PredicateLabels[assertion.Predicate] = value
	}
	s.render(w, http.StatusOK, pageResearchDossier, components.Page{Layout: components.Layout{Title: consoleMessages().Text("research.dossier")}, Sidebar: s.consoleNav(components.ConsoleSectionRequests, view.TenantID, view.Actor), View: view})
}
func researchStatus(err error) int {
	switch {
	case errors.Is(err, researchbridge.ErrDenied):
		return http.StatusForbidden
	case errors.Is(err, researchmemory.ErrConflict), errors.Is(err, domain.ErrExecutionModeConflict), errors.Is(err, domain.ErrResearchSelectionConflict), errors.Is(err, researchbridge.ErrApprovalRequired), errors.Is(err, researchbridge.ErrLegacyRunning), errors.Is(err, researchmission.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, researchbridge.ErrInvalid), errors.Is(err, researchmission.ErrInvalid), errors.Is(err, researchmemory.ErrInvalid):
		return http.StatusBadRequest
	case errors.Is(err, ports.ErrRequestNotFound), errors.Is(err, researchmission.ErrNotFound), errors.Is(err, researchmemory.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusServiceUnavailable
	}
}
func researchErrorText(err error) string {
	if researchStatus(err) == http.StatusServiceUnavailable {
		return "Исследование временно недоступно. Последние сохранённые сведения не потеряны; повторите запрос."
	}
	return err.Error()
}
func (s *Server) researchError(w http.ResponseWriter, err error) {
	s.renderPlainError(w, researchStatus(err), researchErrorText(err))
}

func (s *Server) researchOnly() bool { return s.deps.Research != nil && s.deps.Research.OnlyResearch() }
func (s *Server) rejectLegacyResearchMode(w http.ResponseWriter) {
	writeErrorMessage(w, http.StatusConflict, "В этом окружении включено исследование лидов. Откройте «Исследование и кандидаты» и одобрите заявку на этом экране.")
}
