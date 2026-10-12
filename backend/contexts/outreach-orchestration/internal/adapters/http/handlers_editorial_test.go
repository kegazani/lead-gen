package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	appeditorial "lead-gen/backend/contexts/outreach-orchestration/internal/app/editorialreview"
	"lead-gen/contracts/outreach-orchestration/editorialreview"
)

func TestEditorialReviewSessionCSRFExactVersionAndNoDelivery(t *testing.T) {
	// Read/decision используют только отдельный editorial use case, не screening.
	fx := newFixtureWithRisk(t, nil)
	resp, body := doJSON(t, http.MethodPost, fx.server.URL+"/outreach-handoff", map[string]any{"request_id": "editorial-http-request", "tenant_id": "tenant-1", "execution_purpose": "editorial", "items": []map[string]string{{"lead_id": "lead-http", "material_ref": "asset-http@version-http"}}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("editorial handoff status=%d body=%s", resp.StatusCode, body)
	}
	var handed handoffResponse
	if err := json.Unmarshal(body, &handed); err != nil {
		t.Fatal(err)
	}
	path := "/outreach/editorial/" + handed.CampaignID + "/lead-http"
	query := url.Values{"tenant_id": {"tenant-1"}, "material_ref": {"asset-http@version-http"}, "version_id": {"version-http"}}
	anonymousClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	anonymous, err := anonymousClient.Get(fx.server.URL + path + "?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	anonymous.Body.Close()
	if anonymous.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous read status=%d", anonymous.StatusCode)
	}
	session := loginConsole(t, fx)
	code, html := session.getConsole(t, fx.server.URL+path+"?"+query.Encode())
	if code != http.StatusOK || !strings.Contains(html, "Точная тема") || !strings.Contains(html, "Точный текст письма") || !strings.Contains(html, "Одобрение текста не разрешает отправку") {
		t.Fatalf("editorial screen code=%d html=%s", code, html)
	}
	decisionPath := path + "/decision"
	form := url.Values{"tenant_id": {"tenant-1"}, "material_ref": {"asset-http@version-http"}, "version_id": {"version-http"}, "outcome": {"approved"}}
	missingCSRF, err := session.client.PostForm(fx.server.URL+decisionPath, form)
	if err != nil {
		t.Fatal(err)
	}
	missingCSRF.Body.Close()
	if missingCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF status=%d", missingCSRF.StatusCode)
	}
	wrong := url.Values{}
	for k, values := range form {
		wrong[k] = append([]string(nil), values...)
	}
	wrong.Set("csrf", "invalid")
	wrongCSRF, err := session.client.PostForm(fx.server.URL+decisionPath, wrong)
	if err != nil {
		t.Fatal(err)
	}
	wrongCSRF.Body.Close()
	if wrongCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong CSRF status=%d", wrongCSRF.StatusCode)
	}
	status, location, _ := session.postConsole(t, fx.server.URL+decisionPath, form)
	if status != http.StatusSeeOther {
		t.Fatalf("approve status=%d", status)
	}
	code, html = session.getConsole(t, fx.server.URL+location)
	if code != http.StatusOK || !strings.Contains(html, "approved") || !strings.Contains(html, "operator-1") {
		t.Fatalf("persisted decision screen code=%d html=%s", code, html)
	}
	pending, err := fx.journal.Pending(context.Background())
	if err != nil || len(pending) != 0 {
		t.Fatalf("editorial handoff/decision produced outbox entries: %v err=%v", pending, err)
	}
	approved, approvedBody := doJSON(t, http.MethodGet, fx.server.URL+"/approved-action?tenant_id=tenant-1&draft_ref=lead-http&draft_version=1", nil)
	if approved.StatusCode != http.StatusNotFound {
		t.Fatalf("approved-action bypass status=%d body=%s", approved.StatusCode, approvedBody)
	}
	q := editorialreview.Query{TenantID: "tenant-1", CampaignID: handed.CampaignID, LeadID: "lead-http", MaterialRef: "asset-http@version-http", VersionID: "version-http"}
	record, err := fx.editorial.Read(context.Background(), q)
	if err != nil || record.Decision == nil || record.Decision.EventType != appeditorial.EventTypeDecisionRecorded {
		t.Fatalf("editorial decision missing: %+v %v", record, err)
	}
	events, err := fx.editorial.Events(context.Background(), handed.CampaignID)
	if err != nil || len(events) != 1 || events[0].EventType != appeditorial.EventTypeDecisionRecorded {
		t.Fatalf("unexpected editorial events: %+v err=%v", events, err)
	}
}

func TestEditorialStaleVersionRejectedWithoutChangingDecision(t *testing.T) {
	fx := newFixture(t)
	resp, body := doJSON(t, http.MethodPost, fx.server.URL+"/outreach-handoff", map[string]any{"request_id": "editorial-stale", "tenant_id": "tenant-1", "execution_purpose": "editorial", "items": []map[string]string{{"lead_id": "lead-stale", "material_ref": "asset-http@version-http"}}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("handoff %d %s", resp.StatusCode, body)
	}
	handed := decodeBody[handoffResponse](t, body)
	q := editorialreview.Query{TenantID: "tenant-1", CampaignID: handed.CampaignID, LeadID: "lead-stale", MaterialRef: "asset-http@version-http", VersionID: "version-http"}
	_, err := fx.editorial.Decide(context.Background(), editorialreview.Command{Query: q, ExpectedVersion: "other-version", Outcome: "approved", Actor: "operator-1"})
	if err == nil {
		t.Fatal("stale expected version was accepted")
	}
	read, err := fx.editorial.Read(context.Background(), q)
	if err != nil || read.Decision != nil {
		t.Fatalf("stale decision mutated record: %+v err=%v", read, err)
	}
}

func TestEditorialRejectPersistsReasonActorAndTime(t *testing.T) {
	fx := newFixture(t)
	resp, body := doJSON(t, http.MethodPost, fx.server.URL+"/outreach-handoff", map[string]any{"request_id": "editorial-reject", "tenant_id": "tenant-1", "execution_purpose": "editorial", "items": []map[string]string{{"lead_id": "lead-reject", "material_ref": "asset-http@version-http"}}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("handoff %d %s", resp.StatusCode, body)
	}
	handed := decodeBody[handoffResponse](t, body)
	session := loginConsole(t, fx)
	path := "/outreach/editorial/" + handed.CampaignID + "/lead-reject/decision"
	form := url.Values{"tenant_id": {"tenant-1"}, "material_ref": {"asset-http@version-http"}, "version_id": {"version-http"}, "outcome": {"rejected"}, "reason": {"Тема не соответствует предложению"}}
	status, _, response := session.postConsole(t, fx.server.URL+path, form)
	if status != http.StatusSeeOther {
		t.Fatalf("reject status=%d body=%s", status, response)
	}
	item, err := fx.editorial.Read(context.Background(), editorialreview.Query{TenantID: "tenant-1", CampaignID: handed.CampaignID, LeadID: "lead-reject", MaterialRef: "asset-http@version-http", VersionID: "version-http"})
	if err != nil || item.Decision == nil {
		t.Fatalf("decision missing: %+v %v", item, err)
	}
	if item.Decision.Outcome != "rejected" || item.Decision.Reason != "Тема не соответствует предложению" || item.Decision.Actor != "operator-1" || item.Decision.DecidedAt == "" || item.Decision.VersionID != "version-http" || item.Decision.MaterialRef != "asset-http@version-http" {
		t.Fatalf("decision record incomplete: %+v", item.Decision)
	}
}
