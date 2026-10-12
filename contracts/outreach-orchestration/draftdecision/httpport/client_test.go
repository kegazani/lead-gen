package httpport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lead-gen/contracts/outreach-orchestration/approvedaction"
	"lead-gen/contracts/outreach-orchestration/draftdecision"
)

func testInstant() time.Time {
	return time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
}

func approvedActionFixture() approvedaction.ApprovedAction {
	action := approvedaction.NewApprovedAction(approvedaction.Envelope{
		EventID:       "evt-1",
		OccurredAt:    testInstant(),
		TenantID:      "tenant-1",
		CorrelationID: "campaign-1",
		CausationID:   "decision-1",
	})
	action.ApprovedActionID = "company-1@v1"
	action.CampaignID = "campaign-1"
	action.DraftID = "company-1@v1"
	action.DraftVersion = 1
	action.DecisionID = "decision-1"
	action.Channel = approvedaction.ChannelEmailName
	action.RecipientRef = "company-1@v1"
	action.DecidedAt = testInstant()
	return action
}

func TestNewRejectsInvalidBaseURL(t *testing.T) {
	for _, base := range []string{"", "example.com", "ftp://example.com"} {
		if _, err := New(base); err == nil {
			t.Fatalf("базовый адрес %q не отвергнут", base)
		}
	}
}

func TestApprovedActionRoundTrip(t *testing.T) {
	action := approvedActionFixture()
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != approvedActionPath {
			t.Errorf("неожиданный запрос: %s %s", r.Method, r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(draftdecision.Decision{Outcome: draftdecision.OutcomeApproved, ApprovedAction: &action})
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := client.ApprovedAction(context.Background(), draftdecision.Lookup{TenantID: "tenant-1", DraftRef: "company-1@v1", DraftVersion: 1})
	if err != nil {
		t.Fatalf("ApprovedAction: %v", err)
	}
	if got != action {
		t.Fatalf("действие изменено: %+v", got)
	}
	for _, want := range []string{"tenant_id=tenant-1", "draft_ref=company-1%40v1", "draft_version=1"} {
		if !strings.Contains(gotQuery, want) {
			t.Fatalf("запрос %q не содержит %q", gotQuery, want)
		}
	}
}

func TestApprovedActionMapsOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		outcome draftdecision.Outcome
		want    error
	}{
		{"решение не принято", draftdecision.OutcomePending, draftdecision.ErrPending},
		{"оператор отклонил", draftdecision.OutcomeRejected, draftdecision.ErrRejected},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(draftdecision.Decision{Outcome: testCase.outcome})
			}))
			defer server.Close()

			client, _ := New(server.URL)
			_, err := client.ApprovedAction(context.Background(), draftdecision.Lookup{TenantID: "t", DraftRef: "d", DraftVersion: 1})
			if !errors.Is(err, testCase.want) {
				t.Fatalf("ожидалась %v, получено %v", testCase.want, err)
			}
		})
	}
}

func TestApprovedActionNotFoundAndUnavailable(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   error
	}{
		{"черновик неизвестен", http.StatusNotFound, draftdecision.ErrNotFound},
		{"источник недоступен", http.StatusServiceUnavailable, draftdecision.ErrUnavailable},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
			}))
			defer server.Close()

			client, _ := New(server.URL)
			_, err := client.ApprovedAction(context.Background(), draftdecision.Lookup{TenantID: "t", DraftRef: "d", DraftVersion: 1})
			if !errors.Is(err, testCase.want) {
				t.Fatalf("ожидалась %v, получено %v", testCase.want, err)
			}
		})
	}
}

func TestApprovedActionRejectsIncompleteLookup(t *testing.T) {
	client, _ := New("http://127.0.0.1:1")
	if _, err := client.ApprovedAction(context.Background(), draftdecision.Lookup{}); !errors.Is(err, draftdecision.ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено %v", err)
	}
}
