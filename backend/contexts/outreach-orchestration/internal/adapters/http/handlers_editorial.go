package httpapi

import (
	"net/http"
	"net/url"
	"strings"

	"lead-gen/contracts/outreach-orchestration/editorialreview"
	"lead-gen/frontend/components"
)

type editorialReviewView struct {
	CampaignID  string
	LeadID      string
	TenantID    string
	MaterialRef string
	VersionID   string
	Subject     string
	Body        string
	CSRF        string
	Decision    *editorialreview.Decision
	Notice      string
}

func (s *Server) handleEditorialRead(w http.ResponseWriter, r *http.Request) {
	if s.editorialReview == nil {
		s.renderPlainError(w, http.StatusServiceUnavailable, "редакторское чтение недоступно")
		return
	}
	q := editorialreview.Query{TenantID: r.URL.Query().Get("tenant_id"), CampaignID: r.PathValue("campaignID"), LeadID: r.PathValue("leadID"), MaterialRef: r.URL.Query().Get("material_ref"), VersionID: r.URL.Query().Get("version_id")}
	record, err := s.editorialReview.Read(r.Context(), q)
	if err != nil {
		s.renderPlainError(w, statusForEditorialError(err), "не удалось прочитать точную версию материала")
		return
	}
	view := editorialReviewView{CampaignID: q.CampaignID, LeadID: q.LeadID, TenantID: q.TenantID, MaterialRef: q.MaterialRef, VersionID: q.VersionID, Subject: record.Subject, Body: record.Body, CSRF: csrfOf(r), Decision: record.Decision}
	s.render(w, http.StatusOK, pageEditorialReview, components.Page{Layout: components.Layout{Title: "Редакторская проверка"}, Sidebar: s.consoleNav(q.TenantID, actorOf(r)), View: view})
}

func (s *Server) handleEditorialDecide(w http.ResponseWriter, r *http.Request) {
	if s.editorialReview == nil {
		s.renderPlainError(w, http.StatusServiceUnavailable, "редакторское решение недоступно")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderPlainError(w, http.StatusBadRequest, "некорректная форма решения")
		return
	}
	q := editorialreview.Query{TenantID: r.Form.Get("tenant_id"), CampaignID: r.PathValue("campaignID"), LeadID: r.PathValue("leadID"), MaterialRef: r.Form.Get("material_ref"), VersionID: r.Form.Get("version_id")}
	if strings.TrimSpace(q.TenantID) == "" || strings.TrimSpace(q.MaterialRef) == "" || strings.TrimSpace(q.VersionID) == "" {
		s.renderPlainError(w, http.StatusBadRequest, "неполная привязка материала")
		return
	}
	_, err := s.editorialReview.Decide(r.Context(), editorialreview.Command{Query: q, ExpectedVersion: r.Form.Get("version_id"), Outcome: r.Form.Get("outcome"), Reason: r.Form.Get("reason"), Actor: actorOf(r)})
	if err != nil {
		s.renderPlainError(w, statusForEditorialError(err), "решение не сохранено: версия могла устареть")
		return
	}
	query := url.Values{"tenant_id": {q.TenantID}, "material_ref": {q.MaterialRef}, "version_id": {q.VersionID}}
	http.Redirect(w, r, "/outreach/editorial/"+url.PathEscape(q.CampaignID)+"/"+url.PathEscape(q.LeadID)+"?"+query.Encode(), http.StatusSeeOther)
}

func statusForEditorialError(err error) int {
	switch err {
	case editorialreview.ErrNotFound:
		return http.StatusNotFound
	case editorialreview.ErrConflict:
		return http.StatusConflict
	case editorialreview.ErrRejected:
		return http.StatusBadRequest
	default:
		return http.StatusServiceUnavailable
	}
}
