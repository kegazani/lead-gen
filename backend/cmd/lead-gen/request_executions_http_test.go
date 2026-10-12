package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
)

func TestRequestExecutionHTTPReaderReadsOwnerEndpointsAndExactDomainStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tenant_id") != "tenant / 1" {
			t.Errorf("tenant scope = %q", r.URL.Query().Get("tenant_id"))
		}
		switch {
		case r.URL.Path == "/requests":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"RequestID":"req/42 ?","Status":"processing"}]`))
		case r.URL.Path == "/requests/req/42 ?/workflow" && r.URL.RawPath == "/requests/req%2F42%20%3F/workflow":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"request_id":"req/42 ?","workflow_id":"wf-1","run_id":"run-2","phase":"Completed","found":3,"target":5}`))
		default:
			t.Errorf("unexpected owner request: %s %s", r.Method, r.URL.RequestURI())
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	reader, err := newRequestExecutionHTTPReader(server.URL)
	if err != nil {
		t.Fatal("constructor rejected valid request service URL")
	}

	items, err := reader.ListRequests(context.Background(), "tenant / 1")
	if err != nil || len(items) != 1 || items[0] != (platformgovernance.RequestSummary{RequestID: "req/42 ?", DomainStatus: "processing"}) {
		t.Fatalf("ListRequests = %+v, %v", items, err)
	}
	execution, err := reader.Workflow(context.Background(), "tenant / 1", "req/42 ?")
	if err != nil {
		t.Fatalf("Workflow returned error: %v", err)
	}
	want := platformgovernance.RequestExecution{
		RequestID: "req/42 ?", DomainStatus: "processing", WorkflowID: "wf-1", RunID: "run-2",
		Phase: "Completed", ExecutionFound: true, Found: 3, Target: 5,
	}
	if execution != want {
		t.Fatalf("Workflow = %+v, want %+v", execution, want)
	}
}

func TestRequestExecutionHTTPReaderMapsWorkflowNotFoundWithoutInventingCounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/requests" {
			_, _ = w.Write([]byte(`[{"RequestID":"req-1","Status":"validated"}]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	reader, err := newRequestExecutionHTTPReader(server.URL)
	if err != nil {
		t.Fatal("constructor rejected valid request service URL")
	}
	got, err := reader.Workflow(context.Background(), "tenant-1", "req-1")
	want := platformgovernance.RequestExecution{RequestID: "req-1", DomainStatus: "validated", ExecutionFound: false}
	if err != nil || got != want {
		t.Fatalf("Workflow missing = %+v, %v; want %+v", got, err, want)
	}
}

func TestRequestExecutionHTTPReaderDoesNotLeakOwnerErrorBody(t *testing.T) {
	const sentinel = "private-owner-response-sentinel"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(sentinel))
	}))
	defer server.Close()
	reader, err := newRequestExecutionHTTPReader(server.URL)
	if err != nil {
		t.Fatal("constructor rejected valid request service URL")
	}
	_, err = reader.ListRequests(context.Background(), "tenant-1")
	if err == nil || strings.Contains(err.Error(), sentinel) {
		t.Fatalf("source error was absent or leaked response body: %v", err)
	}
}

func TestRequestExecutionHTTPReaderDoesNotTurnMissingCountersIntoZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/requests" {
			_, _ = w.Write([]byte(`[{"RequestID":"req-1","Status":"processing"}]`))
			return
		}
		_, _ = w.Write([]byte(`{"request_id":"req-1","workflow_id":"wf-1","state":"RUNNING"}`))
	}))
	defer server.Close()
	reader, err := newRequestExecutionHTTPReader(server.URL)
	if err != nil {
		t.Fatal("constructor rejected valid request service URL")
	}
	if _, err := reader.Workflow(context.Background(), "tenant-1", "req-1"); err == nil {
		t.Fatal("workflow response without actual found/target counters was silently converted to zeroes")
	}
}

func TestRequestExecutionHTTPReaderRejectsCredentialedOrMalformedBaseURL(t *testing.T) {
	for _, base := range []string{"not a URL", "file:///tmp", "http://user:secret@localhost", "http://localhost/path?query=1"} {
		if _, err := newRequestExecutionHTTPReader(base); err == nil {
			t.Errorf("accepted invalid request source URL %q", base)
		}
	}
}

func TestRequestExecutionHTTPReaderRespectsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	reader, err := newRequestExecutionHTTPReaderWithClient(server.URL, &http.Client{})
	if err != nil {
		t.Fatal("constructor rejected valid request service URL")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.ListRequests(ctx, "tenant-1"); err == nil {
		t.Fatal("cancelled request unexpectedly succeeded")
	}
}

func TestRequestExecutionReaderEscapesRequestPathSegment(t *testing.T) {
	base, _ := url.Parse("http://example.test/api")
	reader := requestExecutionHTTPReader{baseURL: base}
	got := reader.workflowEndpoint("id/with space")
	if got.EscapedPath() != "/api/requests/id%2Fwith%20space/workflow" {
		t.Fatalf("escaped request path = %q", got.EscapedPath())
	}
}
