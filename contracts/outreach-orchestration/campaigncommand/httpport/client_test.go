package httpport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/outreach-orchestration/campaigncommand"
)

func validSubmission() campaigncommand.Submission {
	return campaigncommand.Submission{
		TenantID:     "tenant-1",
		RunID:        "run-1",
		CompanyID:    "company-1",
		DraftRef:     "company-1@v1",
		DraftVersion: 1,
		MaterialRef:  "company-1@v1",
	}
}

func TestNewRejectsInvalidBaseURL(t *testing.T) {
	for _, base := range []string{"", "example.com", "ftp://example.com"} {
		if _, err := New(base); err == nil {
			t.Fatalf("базовый адрес %q не отвергнут", base)
		}
	}
}

func TestSubmitForApprovalRoundTrip(t *testing.T) {
	var gotSubmission campaigncommand.Submission
	var gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != submitPath {
			t.Errorf("неожиданный запрос: %s %s", r.Method, r.URL.Path)
		}
		gotKey = r.Header.Get("Idempotency-Key")
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotSubmission); err != nil {
			t.Errorf("разбор тела: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"campaign_id":   "campaign-1",
			"draft_ref":     "company-1@v1",
			"draft_version": 1,
			"repeated":      true,
		})
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	submission := validSubmission()
	result, err := client.SubmitForApproval(context.Background(), submission)
	if err != nil {
		t.Fatalf("SubmitForApproval: %v", err)
	}
	if gotSubmission != submission {
		t.Fatalf("владелец получил другую команду: %+v", gotSubmission)
	}
	if gotKey != submission.IdempotencyKey() {
		t.Fatalf("ключ идемпотентности не передан: %q", gotKey)
	}
	want := campaigncommand.Result{CampaignID: "campaign-1", DraftRef: "company-1@v1", DraftVersion: 1, Repeated: true}
	if result != want {
		t.Fatalf("результат = %+v, ожидался %+v", result, want)
	}
}

func TestSubmitForApprovalMapsDomainRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "конфликт состава"})
	}))
	defer server.Close()

	client, _ := New(server.URL)
	if _, err := client.SubmitForApproval(context.Background(), validSubmission()); !errors.Is(err, campaigncommand.ErrRejected) {
		t.Fatalf("ожидалась ErrRejected, получено %v", err)
	}
}

func TestSubmitForApprovalMapsServerFailureToUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, _ := New(server.URL)
	if _, err := client.SubmitForApproval(context.Background(), validSubmission()); !errors.Is(err, campaigncommand.ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
}

func TestSubmitForApprovalRejectsInvalidCommandWithoutNetwork(t *testing.T) {
	client, _ := New("http://127.0.0.1:1")
	if _, err := client.SubmitForApproval(context.Background(), campaigncommand.Submission{}); !errors.Is(err, campaigncommand.ErrRejected) {
		t.Fatalf("ожидалась ErrRejected, получено %v", err)
	}
}

func TestSubmitForApprovalMapsNetworkFailureToUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	client, _ := New(url)
	if _, err := client.SubmitForApproval(context.Background(), validSubmission()); !errors.Is(err, campaigncommand.ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
}
