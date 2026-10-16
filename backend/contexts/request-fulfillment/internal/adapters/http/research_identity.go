package httpapi

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/researchbridge"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/frontend/components"
)

const pageResearchIdentity = "templates/research_identity.html"

type researchIdentityView struct {
	RevokeCommands map[string]string
	researchbridge.IdentityReview
	Live                                    components.Live
	TenantID, Actor, CSRF, CommandID, Query string
}

func (s *Server) registerResearchIdentityRoutes(mux *http.ServeMux) {
	mux.Handle("GET /requests/{requestID}/research/candidates/{entityID}/identity", components.NoStore(s.consoleHandler(s.handleResearchIdentity)))
	mux.Handle("POST /requests/{requestID}/research/candidates/{entityID}/identity", s.consoleHandler(s.handleResearchIdentityDecision))
}
func (s *Server) handleResearchIdentity(w http.ResponseWriter, r *http.Request) {
	if s.deps.Research == nil {
		s.researchError(w, researchbridge.ErrUnavailable)
		return
	}
	review, err := s.deps.Research.ReviewIdentity(r.Context(), researchInput(r), r.PathValue("entityID"), r.URL.Query().Get("other"), r.URL.Query().Get("q"))
	if err != nil {
		s.researchError(w, err)
		return
	}
	view := researchIdentityView{IdentityReview: review, Live: consoleLive, TenantID: review.View.Request.TenantID, Actor: actorOf(r), CSRF: consoleauth.CSRF(r), CommandID: newHTTPCommandID(), Query: r.URL.Query().Get("q")}
	view.RevokeCommands = map[string]string{}
	for _, option := range review.RevokeOptions {
		view.RevokeCommands[option.Decision.ID] = newHTTPCommandID()
	}
	s.render(w, http.StatusOK, pageResearchIdentity, components.Page{Layout: components.Layout{Title: consoleMessages().Text("research.identity.open")}, Sidebar: s.consoleNav(components.ConsoleSectionRequests, view.TenantID, view.Actor), View: view})
}
func (s *Server) handleResearchIdentityDecision(w http.ResponseWriter, r *http.Request) {
	if s.deps.Research == nil {
		s.researchError(w, researchbridge.ErrUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseForm(); err != nil {
		s.researchError(w, researchbridge.ErrInvalid)
		return
	}
	expected := map[string]int64{}
	for key, values := range r.PostForm {
		if !strings.HasPrefix(key, "revision.") {
			continue
		}
		if len(values) != 1 {
			s.researchError(w, researchbridge.ErrInvalid)
			return
		}
		version, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || version < 1 {
			s.researchError(w, researchbridge.ErrInvalid)
			return
		}
		expected[strings.TrimPrefix(key, "revision.")] = version
	}
	_, err := s.deps.Research.DecideIdentity(r.Context(), researchbridge.IdentityInput{Input: researchInput(r), EntityID: r.PathValue("entityID"), RightEntityID: r.PostForm.Get("right_entity_id"), DecisionID: r.PostForm.Get("decision_id"), Action: r.PostForm.Get("action"), Reason: r.PostForm.Get("reason"), EvidenceAssertionIDs: r.PostForm["evidence"], ExpectedRevisions: expected, RevokeDecisionID: r.PostForm.Get("revoke_decision_id")})
	if err != nil {
		s.researchError(w, err)
		return
	}
	path := "/requests/" + url.PathEscape(r.PathValue("requestID")) + "/research/candidates/" + url.PathEscape(r.PathValue("entityID")) + "/identity?" + url.Values{"tenant_id": {activeTenant(r)}}.Encode()
	http.Redirect(w, r, path, http.StatusSeeOther)
}
